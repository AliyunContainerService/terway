package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	"github.com/AliyunContainerService/terway/pkg/storage"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/pkg/utils/k8sclient"
	"github.com/AliyunContainerService/terway/pkg/version"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

const (
	k8sSystemNamespace                      = "kube-system"
	k8sKubeadmConfigmap                     = "kubeadm-config"
	k8sKubeadmConfigmapNetworking           = "MasterConfiguration"
	k8sKubeadmConfigmapClusterconfiguration = "ClusterConfiguration"

	podNeedEni          = "k8s.aliyun.com/ENI"
	podIngressBandwidth = "k8s.aliyun.com/ingress-bandwidth" //deprecated
	podEgressBandwidth  = "k8s.aliyun.com/egress-bandwidth"  //deprecated

	defaultStickTimeForSts = 5 * time.Minute

	dbPath = "/var/lib/cni/terway/pod.db"
	dbName = "pods"

	eventTypeNormal  = corev1.EventTypeNormal
	eventTypeWarning = corev1.EventTypeWarning

	labelDynamicConfig = "terway-config"

	ConditionFalse = "false"
	conditionTrue  = "true"
)

// bandwidth limit unit
const (
	BYTE = 1 << (10 * iota)
	KILOBYTE
	MEGABYTE
	GIGABYTE
	TERABYTE
)

var (
	storageCleanTimeout = 1 * time.Hour
	storageCleanPeriod  = 5 * time.Minute
)

// Kubernetes operation set
type Kubernetes interface {
	GetLocalPods() ([]*daemon.PodInfo, error)
	GetPod(ctx context.Context, namespace, name string, cache bool) (*daemon.PodInfo, error)
	PodExist(namespace, name string) (bool, error)

	GetServiceCIDR() *types.IPNetSet
	GetNodeCidr() *types.IPNetSet
	SetNodeAllocatablePod(count int) error

	PatchNodeAnnotations(anno map[string]string) error
	PatchPodIPInfo(info *daemon.PodInfo, ips string) error
	PatchNodeIPResCondition(status corev1.ConditionStatus, reason, message string) error
	RecordNodeEvent(eventType, reason, message string)
	RecordPodEvent(podName, podNamespace, eventType, reason, message string) error
	GetNodeDynamicConfigLabel() string
	GetDynamicConfigWithName(ctx context.Context, name string) (string, error)
	SetCustomStatefulWorkloadKinds(kinds []string) error
	WaitTrunkReady() (string, error)

	GetTrunkID() string

	GetClient() client.Client
}

// NewK8S return Kubernetes service by pod spec and daemon mode
func NewK8S(daemonMode string, globalConfig *daemon.Config) (Kubernetes, error) {
	restConfig := ctrl.GetConfigOrDie()
	restConfig.QPS = globalConfig.KubeClientQPS
	restConfig.Burst = globalConfig.KubeClientBurst
	restConfig.UserAgent = version.UA

	c, err := client.New(restConfig, client.Options{
		Scheme: types.Scheme,
		Mapper: types.NewRESTMapper(),
	})
	if err != nil {
		return nil, err
	}
	k8sclient.RegisterClients(restConfig)

	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		return nil, fmt.Errorf("failed to get NODE_NAME")
	}

	daemonNamespace := os.Getenv("POD_NAMESPACE")
	if len(daemonNamespace) == 0 {
		daemonNamespace = "kube-system"
		klog.Info("POD_NAMESPACE is not set in environment variables, use kube-system as default namespace")
	}

	node, err := getNode(context.Background(), c, nodeName)
	if err != nil {
		return nil, fmt.Errorf("error retrieving node spec for '%s': %w", nodeName, err)
	}

	var nodeCidr *types.IPNetSet
	if daemonMode == daemon.ModeVPC {
		// vpc mode not support ipv6
		nodeCidr, err = nodeCidrFromAPIServer(k8sclient.K8sClient, nodeName)
		if err != nil {
			return nil, fmt.Errorf("error retrieving node cidr for '%s': %w", nodeName, err)
		}
	}

	storage, err := storage.NewDiskStorage(dbName, utils.NormalizePath(dbPath), serialize, deserialize)
	if err != nil {
		return nil, fmt.Errorf("error creating storage: %w", err)
	}

	broadcaster := record.NewBroadcaster()
	source := corev1.EventSource{Component: "terway-daemon"}
	recorder := broadcaster.NewRecorder(scheme.Scheme, source)

	sink := &typedv1.EventSinkImpl{
		Interface: typedv1.New(k8sclient.K8sClient.CoreV1().RESTClient()).Events(""),
	}
	broadcaster.StartRecordingToSink(sink)

	k8sObj := &k8s{
		client:          c,
		mode:            daemonMode,
		node:            node,
		nodeName:        nodeName,
		nodeCIDR:        nodeCidr,
		daemonNamespace: daemonNamespace,
		storage:         storage,
		broadcaster:     broadcaster,
		recorder:        recorder,
		Locker:          &sync.RWMutex{},
	}

	svcCIDR := &types.IPNetSet{}
	cidrs := strings.Split(globalConfig.ServiceCIDR, ",")

	for _, cidr := range cidrs {
		svcCIDR.SetIPNet(cidr)
	}
	err = k8sObj.setSvcCIDR(svcCIDR)
	if err != nil {
		return nil, err
	}

	go func() {
		for range time.Tick(storageCleanPeriod) {
			err := k8sObj.clean()
			if err != nil {
				klog.Errorf("error cleanup k8s pod local storage, %v", err)
			}
		}
	}()

	return k8sObj, err
}

type k8s struct {
	client client.Client

	storage                 storage.Storage
	broadcaster             record.EventBroadcaster
	recorder                record.EventRecorder
	mode                    string
	nodeName                string
	daemonNamespace         string
	nodeCIDR                *types.IPNetSet
	node                    *corev1.Node
	svcCIDR                 *types.IPNetSet
	statefulWorkloadKindSet sets.Set[string]

	sync.Locker
}

func (k *k8s) PatchNodeIPResCondition(status corev1.ConditionStatus, reason, message string) error {
	node, err := getNode(context.Background(), k.client, k.nodeName)
	if err != nil || node == nil {
		return err
	}

	transitionTime := metav1.Now()
	for _, cond := range node.Status.Conditions {
		if cond.Type == types.SufficientIPCondition && cond.Status == status &&
			cond.Reason == reason && cond.Message == message {
			if cond.LastHeartbeatTime.Add(5 * time.Minute).After(time.Now()) {
				// refresh condition period 5min
				return nil
			}
			transitionTime = cond.LastTransitionTime
		}
	}
	now := metav1.Now()
	ipResCondition := corev1.NodeCondition{
		Type:               types.SufficientIPCondition,
		Status:             status,
		LastHeartbeatTime:  now,
		LastTransitionTime: transitionTime,
		Reason:             reason,
		Message:            message,
	}

	raw, err := json.Marshal(&[]corev1.NodeCondition{ipResCondition})
	if err != nil {
		return err
	}

	patch := []byte(fmt.Sprintf(`{"status":{"conditions":%s}}`, raw))
	return k.client.Status().Patch(context.Background(), node, client.RawPatch(k8stypes.StrategicMergePatchType, patch))
}

func (k *k8s) PatchNodeAnnotations(anno map[string]string) error {
	if len(anno) == 0 {
		return nil
	}

	node, err := getNode(context.Background(), k.client, k.nodeName)
	if err != nil || node == nil {
		return err
	}
	if err != nil || node == nil {
		return err
	}

	satisfy := true
	for key, val := range anno {
		vv, ok := node.Annotations[key]
		if !ok {
			satisfy = false
			break
		}
		if vv != val {
			satisfy = false
			break
		}
	}

	if satisfy {
		return nil
	}

	out, err := json.Marshal(anno)
	if err != nil {
		return err
	}

	annotationPatchStr := fmt.Sprintf(`{"metadata":{"annotations":%s}}`, string(out))
	return k.client.Patch(context.Background(), node, client.RawPatch(k8stypes.MergePatchType, []byte(annotationPatchStr)))
}

func (k *k8s) SetCustomStatefulWorkloadKinds(kinds []string) error {
	k.Lock()
	defer k.Unlock()

	// init kubernetes built-in stateful workload kind
	if len(k.statefulWorkloadKindSet) == 0 {
		k.statefulWorkloadKindSet = sets.New[string]("statefulset")
	}

	// uniform and merge all custom stateful workload kinds
	for i := range kinds {
		k.statefulWorkloadKindSet.Insert(strings.TrimSpace(strings.ToLower(kinds[i])))
	}
	return nil
}

func (k *k8s) setSvcCIDR(svcCidr *types.IPNetSet) error {
	k.Lock()
	defer k.Unlock()

	var err error
	if svcCidr.IPv4 == nil {
		svcCidr.IPv4, err = serviceCidrFromAPIServer(k.client)
		if err != nil {
			return fmt.Errorf("error retrieving service cidr: %w", err)
		}
	}

	k.svcCIDR = svcCidr
	return nil
}

func (k *k8s) PatchPodIPInfo(info *daemon.PodInfo, ips string) error {
	pod, err := getPod(context.Background(), k.client, info.Namespace, info.Name, true)
	if err != nil || pod == nil {
		return err
	}
	if pod.GetAnnotations()[types.PodIPs] == ips {
		return nil
	}

	annotationPatchStr := fmt.Sprintf(`{"metadata":{"annotations":{"%v":"%v"}}}`, types.PodIPs, ips)
	return k.client.Patch(context.Background(), pod, client.RawPatch(k8stypes.MergePatchType, []byte(annotationPatchStr)))
}

func (k *k8s) WaitTrunkReady() (string, error) {
	id := ""
	err := wait.ExponentialBackoff(backoff.Backoff(backoff.DefaultKey), func() (bool, error) {
		node, err := getNode(context.Background(), k.client, k.nodeName)
		if err != nil {
			return false, err
		}
		if node.GetAnnotations()[types.TrunkOn] != "" {
			id = node.GetAnnotations()[types.TrunkOn]
			return true, nil
		}
		return false, nil
	})
	return id, err
}

func (k *k8s) GetTrunkID() string {
	return k.node.Annotations[types.TrunkOn]
}

func (k *k8s) GetClient() client.Client {
	return k.client
}

func (k *k8s) GetPod(ctx context.Context, namespace, name string, cache bool) (*daemon.PodInfo, error) {
	pod, err := getPod(ctx, k.client, namespace, name, cache)
	key := utils.PodInfoKey(namespace, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			obj, innerErr := k.storage.Get(key)
			if innerErr == nil {
				item := obj.(*storageItem)
				return item.Pod, nil
			}

			if !errors.Is(innerErr, storage.ErrNotFound) {
				return nil, innerErr
			}
		}
		return nil, err
	}
	podInfo := convertPod(k.mode, k.statefulWorkloadKindSet, pod)
	item := &storageItem{
		Pod: podInfo,
	}
	err = k.storage.Put(key, item)
	if err != nil {
		return nil, err
	}
	return podInfo, nil
}

func (k *k8s) PodExist(namespace, name string) (bool, error) {
	pod, err := getPod(context.Background(), k.client, namespace, name, false)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if pod.Spec.NodeName != k.nodeName {
		return false, nil
	}

	return true, nil
}

func (k *k8s) GetNodeCidr() *types.IPNetSet {
	return k.nodeCIDR
}

func (k *k8s) GetLocalPods() ([]*daemon.PodInfo, error) {
	options := metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("spec.nodeName", k.nodeName).String(),
		ResourceVersion: "0",
	}
	podList := &corev1.PodList{}
	err := k.client.List(context.Background(), podList, &client.ListOptions{Raw: &options})

	if err != nil {
		return nil, fmt.Errorf("error retrieving pod list for '%s': %w", k.nodeName, err)
	}
	var ret []*daemon.PodInfo
	for _, pod := range podList.Items {
		if types.IgnoredByTerway(pod.Labels) {
			continue
		}

		podInfo := convertPod(k.mode, k.statefulWorkloadKindSet, &pod)
		ret = append(ret, podInfo)
	}

	return ret, nil
}

func (k *k8s) GetServiceCIDR() *types.IPNetSet {
	return k.svcCIDR
}

func (k *k8s) SetNodeAllocatablePod(count int) error {
	return nil
}

// clean up storage
// tag the object as deletion when found pod not exist
// the tagged object will be deleted on secondary scan
func (k *k8s) clean() error {

	list, err := k.storage.List()
	if err != nil {
		return err
	}

	localPods, err := k.GetLocalPods()
	if err != nil {
		return fmt.Errorf("error get local pods: %w", err)
	}
	podsMap := make(map[string]*daemon.PodInfo)

	for _, pod := range localPods {
		key := utils.PodInfoKey(pod.Namespace, pod.Name)
		podsMap[key] = pod
	}

	for _, obj := range list {
		item := obj.(*storageItem)
		key := utils.PodInfoKey(item.Pod.Namespace, item.Pod.Name)

		if _, exists := podsMap[key]; exists {
			if item.deletionTime != nil {
				item.deletionTime = nil
				if err := k.storage.Put(key, item); err != nil {
					return fmt.Errorf("error save storage: %w", err)
				}
			}
			continue
		}

		if item.deletionTime == nil {
			now := time.Now()
			item.deletionTime = &now
			if err := k.storage.Put(key, item); err != nil {
				return fmt.Errorf("error save storage: %w", err)
			}
			continue
		}

		if time.Since(*item.deletionTime) > storageCleanTimeout {
			if err := k.storage.Delete(key); err != nil {
				return fmt.Errorf("error delete storage: %w", err)
			}
		}
	}
	return nil
}

func (k *k8s) RecordNodeEvent(eventType, reason, message string) {
	ref := &corev1.ObjectReference{
		Kind:      "Node",
		Name:      k.node.Name,
		UID:       k.node.UID,
		Namespace: "",
	}

	k.recorder.Event(ref, eventType, reason, message)
}

func (k *k8s) RecordPodEvent(podName, podNamespace, eventType, reason, message string) error {
	pod, err := getPod(context.Background(), k.client, podNamespace, podName, true)
	if err != nil {
		return err
	}

	ref := &corev1.ObjectReference{
		Kind:      "Pod",
		Name:      pod.Name,
		UID:       pod.UID,
		Namespace: pod.Namespace,
	}

	k.recorder.Event(ref, eventType, reason, message)
	return nil
}

// GetNodeDynamicConfigLabel returns value with label config
func (k *k8s) GetNodeDynamicConfigLabel() string {
	// use node cached in newK8s()
	cfgName, ok := k.node.Labels[labelDynamicConfig]
	if !ok {
		return ""
	}

	return cfgName
}

// GetDynamicConfigWithName gets the Dynamic Config's content with its ConfigMap name
func (k *k8s) GetDynamicConfigWithName(ctx context.Context, name string) (string, error) {
	cfgMap, err := getCM(ctx, k.client, k.daemonNamespace, name)
	if err != nil {
		return "", err
	}

	content, ok := cfgMap.Data["eni_conf"]
	if ok {
		return content, nil
	}

	return "", errors.New("configmap not included eni_conf")
}

func podNetworkType(daemonMode string, pod *corev1.Pod) string {
	switch daemonMode {
	case daemon.ModeENIMultiIP:
		return daemon.PodNetworkTypeENIMultiIP
	case daemon.ModeVPC:
		podAnnotation := pod.GetAnnotations()
		useENI := false
		if needEni, ok := podAnnotation[podNeedEni]; ok && (needEni != "" && needEni != ConditionFalse && needEni != "0") {
			useENI = true
		}

		for _, c := range pod.Spec.Containers {
			if _, ok := c.Resources.Requests[deviceplugin.ENIResName]; ok {
				useENI = true
				break
			}
		}

		if useENI {
			return daemon.PodNetworkTypeVPCENI
		}
		return daemon.PodNetworkTypeVPCIP
	case daemon.ModeENIOnly:
		return daemon.PodNetworkTypeVPCENI
	}

	panic(fmt.Errorf("unknown daemon mode %s", daemonMode))
}

func convertPod(daemonMode string, statefulWorkloadKindSet sets.Set[string], pod *corev1.Pod) *daemon.PodInfo {
	pi := &daemon.PodInfo{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		PodIPs:    types.IPSet{},
		PodUID:    string(pod.UID),
	}

	pi.PodNetworkType = podNetworkType(daemonMode, pod)

	for _, str := range pod.Status.PodIPs {
		pi.PodIPs.SetIP(str.IP)
	}
	pi.PodIPs.SetIP(pod.Status.PodIP)

	podAnnotation := pod.GetAnnotations()
	if ingressBandwidth, ok := podAnnotation[podIngressBandwidth]; ok {
		if ingress, err := parseBandwidth(ingressBandwidth); err == nil {
			pi.TcIngress = ingress
		} else {
			_ = tracing.RecordPodEvent(pod.Name, pod.Namespace, eventTypeWarning,
				"ParseFailed", fmt.Sprintf("Parse ingress bandwidth %s failed.", ingressBandwidth))
		}
	}
	if egressBandwidth, ok := podAnnotation[podEgressBandwidth]; ok {
		if egress, err := parseBandwidth(egressBandwidth); err == nil {
			pi.TcEgress = egress
		} else {
			_ = tracing.RecordPodEvent(pod.Name, pod.Namespace, eventTypeWarning,
				"ParseFailed", fmt.Sprintf("Parse egress bandwidth %s failed.", egressBandwidth))
		}
	}

	pi.SandboxExited = pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded

	if podENI, ok := podAnnotation[types.PodENI]; ok {
		var err error
		pi.PodENI, err = strconv.ParseBool(podENI)
		if err != nil {
			_ = tracing.RecordPodEvent(pod.Name, pod.Namespace, eventTypeWarning,
				"ParseFailed", fmt.Sprintf("Parse pod eni %s failed.", podENI))
		}
	}

	if prio, ok := podAnnotation[types.NetworkPriority]; ok {
		switch types.NetworkPrio(prio) {
		case types.NetworkPrioBestEffort, types.NetworkPrioBurstable, types.NetworkPrioGuaranteed:
			pi.NetworkPriority = prio
		default:
			_ = tracing.RecordPodEvent(pod.Name, pod.Namespace, eventTypeWarning,
				"ParseFailed", fmt.Sprintf("Parse pod annotation %s failed.", types.NetworkPriority))
		}
	}

	pi.ERdma = isERDMA(pod)

	// determine whether pod's IP will stick 5 minutes for a reuse, priorities as below,
	// 1. pod has a positive pod-ip-reservation annotation
	// 2. pod is owned by a known stateful workload
	switch {
	case parseBool(pod.Annotations[types.PodIPReservation]):
		pi.IPStickTime = defaultStickTimeForSts
	case len(pod.OwnerReferences) > 0:
		for i := range pod.OwnerReferences {
			if statefulWorkloadKindSet.Has(strings.ToLower(pod.OwnerReferences[i].Kind)) {
				pi.IPStickTime = defaultStickTimeForSts
				break
			}
		}
	}

	return pi
}

func parseBool(s string) bool {
	b, _ := strconv.ParseBool(s)
	return b
}

func getNode(ctx context.Context, c client.Client, nodeName string) (*corev1.Node, error) {
	obj := &corev1.Node{}
	err := c.Get(ctx, k8stypes.NamespacedName{Name: nodeName}, obj, &client.GetOptions{Raw: &metav1.GetOptions{
		ResourceVersion: "0",
	}})
	return obj, err
}

func getPod(ctx context.Context, c client.Client, namespace, name string, cache bool) (*corev1.Pod, error) {
	opt := &metav1.GetOptions{}
	if cache {
		opt.ResourceVersion = "0"
	}
	obj := &corev1.Pod{}
	err := c.Get(ctx, k8stypes.NamespacedName{Namespace: namespace, Name: name}, obj, &client.GetOptions{Raw: opt})
	return obj, err
}

func getCM(ctx context.Context, c client.Client, namespace, name string) (*corev1.ConfigMap, error) {
	obj := &corev1.ConfigMap{}
	err := c.Get(ctx, k8stypes.NamespacedName{Namespace: namespace, Name: name}, obj, &client.GetOptions{Raw: &metav1.GetOptions{
		ResourceVersion: "0",
	}})
	return obj, err
}

func serialize(item interface{}) ([]byte, error) {
	return json.Marshal(item)
}

func deserialize(data []byte) (interface{}, error) {
	item := &storageItem{}
	if err := json.Unmarshal(data, item); err != nil {
		return nil, err
	}
	return item, nil
}

func nodeCidrFromAPIServer(client kubernetes.Interface, nodeName string) (*types.IPNetSet, error) {
	node, err := client.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("error retrieving node spec for '%s': %v", nodeName, err)
	}
	if node.Spec.PodCIDR == "" {
		return nil, fmt.Errorf("node %q pod cidr not assigned", nodeName)
	}
	podCIDR := &types.IPNetSet{}
	for _, cidr := range node.Spec.PodCIDRs {
		podCIDR.SetIPNet(cidr)
	}
	podCIDR.SetIPNet(node.Spec.PodCIDR)

	return podCIDR, nil
}

func parseCidr(cidrString string) (*net.IPNet, error) {
	_, cidr, err := net.ParseCIDR(cidrString)
	return cidr, err
}

func serviceCidrFromAPIServer(c client.Client) (*net.IPNet, error) {
	kubeadmConfigMap, err := getCM(context.Background(), c, k8sSystemNamespace, k8sKubeadmConfigmap)
	if err != nil {
		return nil, err
	}

	kubeNetworkingConfig, ok := kubeadmConfigMap.Data[k8sKubeadmConfigmapNetworking]
	if !ok {
		kubeNetworkingConfig, ok = kubeadmConfigMap.Data[k8sKubeadmConfigmapClusterconfiguration]
		if !ok {
			return nil, fmt.Errorf("cannot found kubeproxy config for svc cidr")
		}
	}

	configMap := make(map[interface{}]interface{})

	err = yaml.Unmarshal([]byte(kubeNetworkingConfig), &configMap)
	if err != nil {
		return nil, fmt.Errorf("configmap unmarshal failed, %w", err)
	}

	if networkingObj, ok := configMap["networking"]; ok {
		if networkingMap, ok := networkingObj.(map[interface{}]interface{}); ok {
			if svcObj, ok := networkingMap["serviceSubnet"]; ok {
				if svcStr, ok := svcObj.(string); ok {
					return parseCidr(svcStr)
				}
			}
		}
	}
	return nil, fmt.Errorf("cannot found kubeproxy config for svc cidr")
}

func parseBandwidth(s string) (uint64, error) {
	// when bandwidth is "", return
	if len(s) == 0 {
		return 0, fmt.Errorf("invalid bandwidth %s", s)
	}

	s = strings.TrimSpace(s)
	s = strings.ToUpper(s)

	i := strings.IndexFunc(s, unicode.IsLetter)

	bytesString, multiple := s[:i], s[i:]
	bytes, err := strconv.ParseFloat(bytesString, 64)
	if err != nil || bytes <= 0 {
		return 0, fmt.Errorf("invalid bandwidth %s", s)
	}

	switch multiple {
	case "T", "TB", "TIB":
		return uint64(bytes * TERABYTE), nil
	case "G", "GB", "GIB":
		return uint64(bytes * GIGABYTE), nil
	case "M", "MB", "MIB":
		return uint64(bytes * MEGABYTE), nil
	case "K", "KB", "KIB":
		return uint64(bytes * KILOBYTE), nil
	case "B", "":
		return uint64(bytes), nil
	default:
		return 0, fmt.Errorf("invalid bandwidth %s", s)
	}
}

func isERDMA(p *corev1.Pod) bool {
	for _, c := range append(p.Spec.InitContainers, p.Spec.Containers...) {
		if res, ok := c.Resources.Limits[deviceplugin.ERDMAResName]; ok && !res.IsZero() {
			return true
		}
	}
	return false
}

type storageItem struct {
	Pod          *daemon.PodInfo
	deletionTime *time.Time
}
