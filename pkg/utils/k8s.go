package utils

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/types"
)

var stsKinds = []string{"StatefulSet"}

// SetStsKinds set custom sts workload kinds
func SetStsKinds(kids []string) {
	stsKinds = append(stsKinds, kids...)
}

// IsFixedNamePod pod is sts
func IsFixedNamePod(pod *corev1.Pod) bool {
	if len(pod.GetObjectMeta().GetOwnerReferences()) == 0 {
		return true
	}
	for _, own := range pod.GetObjectMeta().GetOwnerReferences() {
		for _, kind := range stsKinds {
			if own.Kind == kind {
				return true
			}
		}
	}
	return false
}

// IsDaemonSetPod pod is create by daemonSet
func IsDaemonSetPod(pod *corev1.Pod) bool {
	for _, own := range pod.GetObjectMeta().GetOwnerReferences() {
		if own.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

// ISVKNode node is run by virtual kubelet
func ISVKNode(n *corev1.Node) bool {
	return n.Labels["type"] == "virtual-kubelet"
}

func ISLingJunNode(lb map[string]string) bool {
	return lb[types.LingJunNodeLabelKey] == "true"
}

// PodSandboxExited pod sandbox is exited
func PodSandboxExited(p *corev1.Pod) bool {
	switch p.Status.Phase {
	case corev1.PodSucceeded, corev1.PodFailed:
		return true
	default:
		return false
	}
}

func PodInfoKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

var (
	// DefaultPatchBackoff for patch status field
	DefaultPatchBackoff = wait.Backoff{
		Duration: 1 * time.Second,
		Steps:    3,
		Factor:   2,
		Jitter:   1.1,
	}
)

// RuntimeFinalStatus return the latest ts, return false if not found
func RuntimeFinalStatus(status map[v1beta1.CNIStatus]*v1beta1.CNIStatusInfo) (cniStatus v1beta1.CNIStatus, cniStatusInfo *v1beta1.CNIStatusInfo, ok bool) {
	for cni, statusInfo := range status {
		if statusInfo == nil {
			continue
		}
		if cniStatusInfo == nil {
			cniStatusInfo = statusInfo
			cniStatus = cni
			ok = true
		} else {
			// statusInfo.LastUpdateTime
			if cniStatusInfo.LastUpdateTime.Before(&statusInfo.LastUpdateTime) {
				cniStatusInfo = statusInfo
				cniStatus = cni
				ok = true
			}
		}
	}
	return
}

func EventName(name string) string {
	return fmt.Sprintf("terway-controlplane/%s", name)
}

func SlimPod(i interface{}) (interface{}, error) {
	if pod, ok := i.(*corev1.Pod); ok {
		if pod.Spec.HostNetwork {
			pod.Annotations = nil
			pod.Labels = nil
		}

		pod.Spec.Volumes = nil
		pod.Spec.EphemeralContainers = nil
		pod.Spec.SecurityContext = nil
		pod.Spec.ImagePullSecrets = nil
		pod.Spec.Tolerations = nil
		pod.Spec.ReadinessGates = nil
		pod.Spec.PreemptionPolicy = nil
		pod.Status.InitContainerStatuses = nil
		pod.Status.ContainerStatuses = nil
		pod.Status.EphemeralContainerStatuses = nil
		return pod, nil
	}
	return nil, fmt.Errorf("unexpected type %T", i)
}

func SlimNode(i interface{}) (interface{}, error) {
	if node, ok := i.(*corev1.Node); ok {
		node.Status.Images = nil
		node.Status.VolumesInUse = nil
		node.Status.VolumesAttached = nil
		return node, nil
	}
	return nil, fmt.Errorf("unexpected type %T", i)
}
