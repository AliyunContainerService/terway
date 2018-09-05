package daemon

import (
	"fmt"
	"github.com/AliyunContainerService/terway/deviceplugin"
	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net"
	"os"
	"strings"
)

const K8S_POD_NAME_ARGS = "K8S_POD_NAME"
const K8S_POD_NAMESPACE_ARGS = "K8S_POD_NAMESPACE"
const K8S_SYSTEM_NAMESPACE = "kube-system"
const K8S_KUBEADM_CONFIGMAP = "kubeadm-config"
const K8S_KUBEADM_CONFIGMAP_NETWORKING = "MasterConfiguration"

const POD_NEED_ENI = "k8s.aliyun.com/eni"
const POD_INGRESS_BANDWIDTH = "k8s.aliyun.com/ingress-bandwidth"
const POD_EGRESS_BANDWIDTH = "k8s.aliyun.com/egress-bandwidth"

func (eni *ENIService) podCIDR() (*net.IPNet, error) {
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {

		podName := os.Getenv("POD_NAME")
		podNamespace := os.Getenv("POD_NAMESPACE")
		if podName == "" || podNamespace == "" {
			return nil, fmt.Errorf("env variables POD_NAME and POD_NAMESPACE must be set")
		}

		pod, err := eni.client.CoreV1().Pods(podNamespace).Get(podName, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("error retrieving pod spec for '%s/%s': %v", podNamespace, podName, err)
		}
		nodeName = pod.Spec.NodeName
		if nodeName == "" {
			return nil, fmt.Errorf("node name not present in pod spec '%s/%s'", podNamespace, podName)
		}
	}
	node, err := eni.client.CoreV1().Nodes().Get(nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("error retrieving node spec for '%s': %v", nodeName, err)
	}
	if node.Spec.PodCIDR == "" {
		return nil, fmt.Errorf("node %q pod cidr not assigned", nodeName)
	}

	_, cidr, err := net.ParseCIDR(node.Spec.PodCIDR)
	if err != nil {
		return nil, err
	}
	return cidr, nil
}

func getPodArgsFromArgs(cniArgs string) map[string]string {
	argsMap := make(map[string]string)
	for _, args := range strings.Split(cniArgs, ";") {
		argsKV := strings.SplitN(args, "=", 2)
		if len(argsKV) == 2 {
			argsMap[argsKV[0]] = argsKV[1]
		}
	}
	return argsMap
}

func (eni *ENIService) getPodInfo(namespace, id string) (*podInfo, error) {
	pod, err := eni.client.CoreV1().Pods(namespace).Get(id, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	pInfo := &podInfo{}

	podAnnotation := pod.GetAnnotations()
	if needEni, ok := podAnnotation[POD_NEED_ENI]; ok && (needEni != "" && needEni != "false" && needEni != "0") {
		pInfo.useENI = true
	}

	for _, c := range pod.Spec.Containers {
		if _, ok := c.Resources.Requests[deviceplugin.DefaultResourceName]; ok {
			pInfo.useENI = true
			break
		}
	}

	if ingressBandWidth, ok := podAnnotation[POD_INGRESS_BANDWIDTH]; ok {
		pInfo.tcIngress = ingressBandWidth
	}

	if egressBandWidth, ok := podAnnotation[POD_EGRESS_BANDWIDTH]; ok {
		pInfo.tcEgress = egressBandWidth
	}

	return pInfo, nil
}

func (eni *ENIService) getSvcCidr() (string, error) {
	kubeadmConfigMap, err := eni.client.CoreV1().ConfigMaps(K8S_SYSTEM_NAMESPACE).Get(K8S_KUBEADM_CONFIGMAP, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	kubeNetworkingConfig, ok := kubeadmConfigMap.Data[K8S_KUBEADM_CONFIGMAP_NETWORKING]
	if !ok {
		return "", fmt.Errorf("cannot found kubeproxy config for svc cidr")
	}

	configMap := make(map[interface{}]interface{})

	err = yaml.Unmarshal([]byte(kubeNetworkingConfig), &configMap)

	if networkingObj, ok := configMap["networking"]; ok {
		if networkingMap, ok := networkingObj.(map[interface{}]interface{}); ok {
			if svcObj, ok := networkingMap["serviceSubnet"]; ok {
				if svcStr, ok := svcObj.(string); ok {
					return svcStr, nil
				}
			}
		}
	}
	return "", fmt.Errorf("cannot found kubeproxy config for svc cidr")
}
