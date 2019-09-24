package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/storage"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
)

const (
	podNetworkTypeVPCIP      = "VPCIP"
	podNetworkTypeVPCENI     = "VPCENI"
	podNetworkTypeENIMultiIP = "ENIMultiIP"
	dbPath                   = "/var/lib/cni/terway/pod.db"
	dbName                   = "pods"

	apiServerTimeout = 70 * time.Second
)

type podInfo struct {
	//K8sPod *v1.Pod
	Name           string
	Namespace      string
	TcIngress      uint64
	TcEgress       uint64
	PodNetworkType string
	PodIP          string
	IPStickTime    time.Duration
}

// Kubernetes operation set
type Kubernetes interface {
	GetLocalPods() ([]*podInfo, error)
	GetPod(namespace, name string) (*podInfo, error)
	GetServiceCidr() *net.IPNet
	GetNodeCidr() *net.IPNet
	SetNodeAllocatablePod(count int) error
}

type k8s struct {
	client   kubernetes.Interface
	storage  storage.Storage
	mode     string
	nodeName string
	nodeCidr *net.IPNet
	svcCidr  *net.IPNet
}

// newK8S return Kubernetes service by pod spec and daemon mode
func newK8S(client kubernetes.Interface, svcCidr *net.IPNet, daemonMode string) (Kubernetes, error) {

	nodeName, err := getNodeName(client)
	if err != nil {
		return nil, errors.Wrap(err, "failed getting node name")
	}

	var nodeCidr *net.IPNet
	if daemonMode == daemonModeVPC {
		nodeCidr, err = nodeCidrFromAPIServer(client, nodeName)
		if err != nil {
			return nil, errors.Wrap(err, "failed getting node cidr")
		}
		if svcCidr == nil {
			svcCidr, err = serviceCidrFromAPIServer(client)
			if err != nil {
				return nil, errors.Wrap(err, "failed getting service cidr")
			}
		}
	}

	storage, err := storage.NewDiskStorage(dbName, dbPath, serialize, deserialize)
	if err != nil {
		return nil, errors.Wrapf(err, "failed init db storage with path %s and bucket %s", dbPath, dbName)
	}

	k8sObj := &k8s{
		client:   client,
		mode:     daemonMode,
		nodeName: nodeName,
		nodeCidr: nodeCidr,
		svcCidr:  svcCidr,
		storage:  storage,
	}

	go func() {
		for range time.Tick(storageCleanPeriod) {
			err := k8sObj.clean()
			if err != nil {
				log.Errorf("error cleanup k8s pod local storage, %v", err)
			}
		}
	}()

	return k8sObj, nil
}

func getNodeName(client kubernetes.Interface) (string, error) {
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {

		podName := os.Getenv("POD_NAME")
		podNamespace := os.Getenv("POD_NAMESPACE")
		if podName == "" || podNamespace == "" {
			return "", fmt.Errorf("env variables POD_NAME and POD_NAMESPACE must be set")
		}

		pod, err := client.CoreV1().Pods(podNamespace).Get(podName, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("error retrieving pod spec for '%s/%s': %v", podNamespace, podName, err)
		}
		nodeName = pod.Spec.NodeName
		if nodeName == "" {
			return "", fmt.Errorf("node name not present in pod spec '%s/%s'", podNamespace, podName)
		}
	}
	return nodeName, nil
}

func nodeCidrFromAPIServer(client kubernetes.Interface, nodeName string) (*net.IPNet, error) {
	node, err := client.CoreV1().Nodes().Get(nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("error retrieving node spec for '%s': %v", nodeName, err)
	}
	if node.Spec.PodCIDR == "" {
		return nil, fmt.Errorf("node %q pod cidr not assigned", nodeName)
	}

	return parseCidr(node.Spec.PodCIDR)
}

func parseCidr(cidrString string) (*net.IPNet, error) {
	_, cidr, err := net.ParseCIDR(cidrString)
	return cidr, err
}

func serviceCidrFromAPIServer(client kubernetes.Interface) (*net.IPNet, error) {
	kubeadmConfigMap, err := client.CoreV1().ConfigMaps(k8sSystemNamespace).Get(k8sKubeadmConfigmap, metav1.GetOptions{})
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
const podIngressBandwidth = "k8s.aliyun.com/ingress-bandwidth"
const podEgressBandwidth = "k8s.aliyun.com/egress-bandwidth"

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
			if _, ok := c.Resources.Requests[deviceplugin.DefaultResourceName]; ok {
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

func convertPod(daemonMode string, pod *corev1.Pod) *podInfo {

	pi := &podInfo{
		Name:      pod.Name,
		Namespace: pod.Namespace,
	}

	pi.PodNetworkType = podNetworkType(daemonMode, pod)

	pi.PodIP = pod.Status.PodIP

	podAnnotation := pod.GetAnnotations()
	if ingressBandwidth, ok := podAnnotation[podIngressBandwidth]; ok {
		if ingress, err := parseBandwidth(ingressBandwidth); err == nil {
			pi.TcIngress = ingress
		}
		//TODO write event on pod if parse bandwidth fail
	}
	if egressBandwidth, ok := podAnnotation[podEgressBandwidth]; ok {
		if egress, err := parseBandwidth(egressBandwidth); err == nil {
			pi.TcEgress = egress
		}
	}

	if len(pod.OwnerReferences) != 0 {
		switch strings.ToLower(pod.OwnerReferences[0].Kind) {
		case "statefulset":
			pi.IPStickTime = defaultStickTimeForSts
			break
		}
	}

	return pi
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
	Pod          *podInfo
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

func (k *k8s) GetPod(namespace, name string) (*podInfo, error) {
	pod, err := k.client.CoreV1().Pods(namespace).Get(name, metav1.GetOptions{
		ResourceVersion: "0",
	})
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
	podInfo := convertPod(k.mode, pod)
	item := &storageItem{
		Pod: podInfo,
	}

	if err := k.storage.Put(podInfoKey(podInfo.Namespace, podInfo.Name), item); err != nil {
		return nil, err
	}
	return podInfo, nil
}

func (k *k8s) GetNodeCidr() *net.IPNet {
	return k.nodeCidr
}

func (k *k8s) GetLocalPods() ([]*podInfo, error) {
	options := metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("spec.nodeName", k.nodeName).String(),
		ResourceVersion: "0",
	}
	list, err := k.client.CoreV1().Pods(corev1.NamespaceAll).List(options)
	if err != nil {
		return nil, errors.Wrapf(err, "failed listting pods on %s from apiserver", k.nodeName)
	}
	var ret []*podInfo
	for _, pod := range list.Items {
		podInfo := convertPod(k.mode, &pod)
		ret = append(ret, podInfo)
	}

	return ret, nil
}
func (k *k8s) GetServiceCidr() *net.IPNet {
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
	podsMap := make(map[string]*podInfo)

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
