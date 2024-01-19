package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/terway/deviceplugin"
	podENITypes "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	"github.com/AliyunContainerService/terway/pkg/storage"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/pkg/version"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

const (
	podNetworkTypeVPCIP      = "VPCIP"
	podNetworkTypeVPCENI     = "VPCENI"
	podNetworkTypeENIMultiIP = "ENIMultiIP"
	dbPath                   = "/var/lib/cni/terway/pod.db"
	dbName                   = "pods"

	eventTypeNormal  = corev1.EventTypeNormal
	eventTypeWarning = corev1.EventTypeWarning

	labelDynamicConfig = "terway-config"
)

// Kubernetes operation set
type Kubernetes interface {
	GetLocalPods() ([]*types.PodInfo, error)
	GetPod(namespace, name string, cache bool) (*types.PodInfo, error)
	PodExist(namespace, name string) (bool, error)
	GetServiceCIDR() *types.IPNetSet
	GetNodeCidr() *types.IPNetSet
	SetNodeAllocatablePod(count int) error
	PatchEipInfo(info *types.PodInfo) error

	PatchNodeAnnotations(anno map[string]string) error
	PatchPodIPInfo(info *types.PodInfo, ips string) error
	PatchNodeIPResCondition(status corev1.ConditionStatus, reason, message string) error
	WaitPodENIInfo(info *types.PodInfo) (podEni *podENITypes.PodENI, err error)
	GetPodENIInfo(info *types.PodInfo) (podEni *podENITypes.PodENI, err error)
	RecordNodeEvent(eventType, reason, message string)
	RecordPodEvent(podName, podNamespace, eventType, reason, message string) error
	GetNodeDynamicConfigLabel() string
	GetDynamicConfigWithName(name string) (string, error)
	SetCustomStatefulWorkloadKinds(kinds []string) error
	WaitTrunkReady() (string, error)

	GetTrunkID() string

	GetClient() client.Client
}

type k8s struct {
	client client.Client

	storage                 storage.Storage
	broadcaster             record.EventBroadcaster
	recorder                record.EventRecorder
	mode                    string
	nodeName                string
	daemonNamespace         string
	nodeCidr                *types.IPNetSet
	node                    *corev1.Node
	svcCidr                 *types.IPNetSet
	apiConnTime             time.Time
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
			return errors.Wrap(err, "failed getting service cidr")
		}
	}

	k.svcCidr = svcCidr
	return nil
}

func (k *k8s) PatchEipInfo(info *types.PodInfo) error {
	pod, err := getPod(context.Background(), k.client, info.Namespace, info.Name, true)
	if err != nil || pod == nil {
		return err
	}

	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[podEipAddress] = info.EipInfo.PodEipIP

	annotationPatchStr := fmt.Sprintf(`{"metadata":{"annotations":{"%v":"%v"}}}`, podEipAddress, info.EipInfo.PodEipIP)
	return k.client.Patch(context.Background(), pod, client.RawPatch(k8stypes.MergePatchType, []byte(annotationPatchStr)))
}

func (k *k8s) PatchPodIPInfo(info *types.PodInfo, ips string) error {
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

func (k *k8s) WaitPodENIInfo(info *types.PodInfo) (podEni *podENITypes.PodENI, err error) {
	err = wait.ExponentialBackoff(backoff.Backoff(backoff.WaitPodENIStatus), func() (bool, error) {
		podEni, err = getPodENI(context.Background(), k.client, info.Namespace, info.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				// wait pod eni exist
				return false, nil
			}
			return false, errors.Wrapf(err, "error get pod eni info")
		}
		if podEni.Status.Phase != podENITypes.ENIPhaseBind {
			// wait pod eni bind
			return false, nil
		}
		if info.PodUID != "" {
			if podEni.Annotations[types.PodUID] != info.PodUID {
				return false, nil
			}
		}

		if !podEni.DeletionTimestamp.IsZero() {
			return false, nil
		}
		return true, nil
	})
	return podEni, err
}

func (k *k8s) GetPodENIInfo(info *types.PodInfo) (podEni *podENITypes.PodENI, err error) {
	err = wait.ExponentialBackoff(backoff.Backoff(backoff.WaitPodENIStatus), func() (bool, error) {
		podEni, err = getPodENI(context.Background(), k.client, info.Namespace, info.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return true, err
			}
			return false, fmt.Errorf("error get pod eni info, %w", err)
		}
		return true, nil
	})
	return podEni, err
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

// newK8S return Kubernetes service by pod spec and daemon mode
func newK8S(daemonMode string, globalConfig *daemon.Config) (Kubernetes, error) {
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
	utils.RegisterClients(restConfig)

	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		return nil, fmt.Errorf("failed to get NODE_NAME")
	}

	daemonNamespace := os.Getenv("POD_NAMESPACE")
	if len(daemonNamespace) == 0 {
		daemonNamespace = "kube-system"
		log.Warnf("POD_NAMESPACE is not set in environment variables, use kube-system as default namespace")
	}

	node, err := getNode(context.Background(), c, nodeName)
	if err != nil {
		return nil, errors.Wrap(err, "failed getting node")
	}

	var nodeCidr *types.IPNetSet
	if daemonMode == daemonModeVPC {
		// vpc mode not support ipv6
		nodeCidr, err = nodeCidrFromAPIServer(utils.K8sClient, nodeName)
		if err != nil {
			return nil, errors.Wrap(err, "failed getting node cidr")
		}
	}

	storage, err := storage.NewDiskStorage(dbName, utils.NormalizePath(dbPath), serialize, deserialize)
	if err != nil {
		return nil, errors.Wrapf(err, "failed init db storage with path %s and bucket %s", dbPath, dbName)
	}

	broadcaster := record.NewBroadcaster()
	source := corev1.EventSource{Component: "terway-daemon"}
	recorder := broadcaster.NewRecorder(scheme.Scheme, source)

	sink := &typedv1.EventSinkImpl{
		Interface: typedv1.New(utils.K8sClient.CoreV1().RESTClient()).Events(""),
	}
	broadcaster.StartRecordingToSink(sink)

	k8sObj := &k8s{
		client:          c,
		mode:            daemonMode,
		node:            node,
		nodeName:        nodeName,
		nodeCidr:        nodeCidr,
		daemonNamespace: daemonNamespace,
		storage:         storage,
		broadcaster:     broadcaster,
		recorder:        recorder,
		apiConnTime:     time.Now(),
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
				log.Errorf("error cleanup k8s pod local storage, %v", err)
			}
		}
	}()

	return k8sObj, err
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
		return nil, errors.Wrapf(err, "error get networking config from configmap")
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

const k8sSystemNamespace = "kube-system"
const k8sKubeadmConfigmap = "kubeadm-config"
const k8sKubeadmConfigmapNetworking = "MasterConfiguration"
const k8sKubeadmConfigmapClusterconfiguration = "ClusterConfiguration"

const podNeedEni = "k8s.aliyun.com/ENI"
const podIngressBandwidth = "k8s.aliyun.com/ingress-bandwidth" //deprecated
const podEgressBandwidth = "k8s.aliyun.com/egress-bandwidth"   //deprecated

const podWithEip = "k8s.aliyun.com/pod-with-eip"
const eciWithEip = "k8s.aliyun.com/eci-with-eip" // to adopt ask annotation
const podEipBandwidth = "k8s.aliyun.com/eip-bandwidth"
const podEipChargeType = "k8s.aliyun.com/eip-charge-type"
const podEipInternetChargeType = "k8s.aliyun.com/eip-internet-charge-type" // to adopt ask annotation
const podEciEipInstanceID = "k8s.aliyun.com/eci-eip-instanceid"            // to adopt ask annotation
const podPodEipInstanceID = "k8s.aliyun.com/pod-eip-instanceid"
const podEipAddress = "k8s.aliyun.com/allocated-eipAddress"
const eipBandwidthPackageID = "k8s.aliyun.com/eip-common-bandwidth-package-id"
const eipISP = "k8s.aliyun.com/eip-isp"
const eipPublicIPAddressPoolID = "k8s.aliyun.com/eip-public-ip-address-pool-id"

const defaultStickTimeForSts = 5 * time.Minute

var (
	storageCleanTimeout = 1 * time.Hour
	storageCleanPeriod  = 5 * time.Minute
)

func podNetworkType(daemonMode string, pod *corev1.Pod) string {
	switch daemonMode {
	case daemonModeENIMultiIP:
		return podNetworkTypeENIMultiIP
	case daemonModeVPC:
		podAnnotation := pod.GetAnnotations()
		useENI := false
		if needEni, ok := podAnnotation[podNeedEni]; ok && (needEni != "" && needEni != conditionFalse && needEni != "0") {
			useENI = true
		}

		for _, c := range pod.Spec.Containers {
			if _, ok := c.Resources.Requests[deviceplugin.ENIResName]; ok {
				useENI = true
				break
			}
		}

		if useENI {
			return podNetworkTypeVPCENI
		}
		return podNetworkTypeVPCIP
	case daemonModeENIOnly:
		return podNetworkTypeVPCENI
	}

	panic(fmt.Errorf("unknown daemon mode %s", daemonMode))
}

func convertPod(daemonMode string, statefulWorkloadKindSet sets.Set[string], pod *corev1.Pod) *types.PodInfo {
	pi := &types.PodInfo{
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

	if eipAnnotation, ok := podAnnotation[podWithEip]; ok && eipAnnotation == conditionTrue {
		pi.EipInfo.PodEip = true
		pi.EipInfo.PodEipBandWidth = 5
		pi.EipInfo.PodEipChargeType = types.PayByTraffic
	}
	if eipAnnotation, ok := podAnnotation[eciWithEip]; ok && eipAnnotation == conditionTrue {
		pi.EipInfo.PodEip = true
		pi.EipInfo.PodEipBandWidth = 5
		pi.EipInfo.PodEipChargeType = types.PayByTraffic
	}

	if eipAnnotation, ok := podAnnotation[podEipBandwidth]; ok {
		eipBandwidth, err := strconv.Atoi(eipAnnotation)
		if err != nil {
			log.Errorf("error convert eip bandwidth: %v", eipBandwidth)
		} else {
			pi.EipInfo.PodEipBandWidth = eipBandwidth
		}
	}

	if eipAnnotation, ok := podAnnotation[podEipChargeType]; ok {
		pi.EipInfo.PodEipChargeType = types.InternetChargeType(eipAnnotation)
	}
	if eipAnnotation, ok := podAnnotation[podEipInternetChargeType]; ok {
		pi.EipInfo.PodEipChargeType = types.InternetChargeType(eipAnnotation)
	}

	if eipAnnotation, ok := podAnnotation[podEciEipInstanceID]; ok && eipAnnotation != "" {
		pi.EipInfo.PodEip = true
		pi.EipInfo.PodEipID = eipAnnotation
	}

	if eipAnnotation, ok := podAnnotation[podPodEipInstanceID]; ok && eipAnnotation != "" {
		pi.EipInfo.PodEip = true
		pi.EipInfo.PodEipID = eipAnnotation
	}

	if eipAnnotation, ok := podAnnotation[eipISP]; ok && eipAnnotation != "" {
		pi.EipInfo.PodEipISP = eipAnnotation
	}

	if eipAnnotation, ok := podAnnotation[eipBandwidthPackageID]; ok && eipAnnotation != "" {
		pi.EipInfo.PodEipBandwidthPackageID = eipAnnotation
	}
	if eipAnnotation, ok := podAnnotation[eipPublicIPAddressPoolID]; ok && eipAnnotation != "" {
		pi.EipInfo.PodEipPoolID = eipAnnotation
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

// bandwidth limit unit
const (
	BYTE = 1 << (10 * iota)
	KILOBYTE
	MEGABYTE
	GIGABYTE
	TERABYTE
)

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

type storageItem struct {
	Pod          *types.PodInfo
	deletionTime *time.Time
}

func podInfoKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
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

func (k *k8s) GetPod(namespace, name string, cache bool) (*types.PodInfo, error) {
	pod, err := getPod(context.Background(), k.client, namespace, name, cache)
	if err != nil {
		if apierrors.IsNotFound(err) {
			key := podInfoKey(namespace, name)
			obj, err := k.storage.Get(key)
			if err == nil {
				item := obj.(*storageItem)
				return item.Pod, nil
			}

			if err != storage.ErrNotFound {
				return nil, err
			}
		}
		return nil, err
	}
	podInfo := convertPod(k.mode, k.statefulWorkloadKindSet, pod)
	item := &storageItem{
		Pod: podInfo,
	}

	if err := k.storage.Put(podInfoKey(podInfo.Namespace, podInfo.Name), item); err != nil {
		return nil, err
	}
	return podInfo, nil
}

func (k *k8s) GetNodeCidr() *types.IPNetSet {
	return k.nodeCidr
}

func (k *k8s) GetLocalPods() ([]*types.PodInfo, error) {
	options := metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("spec.nodeName", k.nodeName).String(),
		ResourceVersion: "0",
	}
	podList := &corev1.PodList{}
	err := k.client.List(context.Background(), podList, &client.ListOptions{Raw: &options})

	if err != nil {
		return nil, errors.Wrapf(err, "failed listting pods on %s from apiserver", k.nodeName)
	}
	var ret []*types.PodInfo
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
	return k.svcCidr
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
		return errors.Wrap(err, "error get local pods")
	}
	podsMap := make(map[string]*types.PodInfo)

	for _, pod := range localPods {
		key := podInfoKey(pod.Namespace, pod.Name)
		podsMap[key] = pod
	}

	for _, obj := range list {
		item := obj.(*storageItem)
		key := podInfoKey(item.Pod.Namespace, item.Pod.Name)

		if _, exists := podsMap[key]; exists {
			if item.deletionTime != nil {
				item.deletionTime = nil
				if err := k.storage.Put(key, item); err != nil {
					return errors.Wrapf(err, "error save storage")
				}
			}
			continue
		}

		if item.deletionTime == nil {
			now := time.Now()
			item.deletionTime = &now
			if err := k.storage.Put(key, item); err != nil {
				return errors.Wrap(err, "error save storage")
			}
			continue
		}

		if time.Since(*item.deletionTime) > storageCleanTimeout {
			if err := k.storage.Delete(key); err != nil {
				return errors.Wrap(err, "error delete storage")
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
func (k *k8s) GetDynamicConfigWithName(name string) (string, error) {
	cfgMap, err := getCM(context.Background(), k.client, k.daemonNamespace, name)
	if err != nil {
		return "", err
	}

	content, ok := cfgMap.Data["eni_conf"]
	if ok {
		return content, nil
	}

	return "", errors.New("configmap not included eni_conf")
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

func getPodENI(ctx context.Context, c client.Client, namespace, name string) (*podENITypes.PodENI, error) {
	obj := &podENITypes.PodENI{}
	err := c.Get(ctx, k8stypes.NamespacedName{Namespace: namespace, Name: name}, obj, &client.GetOptions{Raw: &metav1.GetOptions{
		ResourceVersion: "0",
	}})
	return obj, err
}
