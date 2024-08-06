package utils

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
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

func ISLinJunNode(n *corev1.Node) bool {
	return n.Labels["alibabacloud.com/lingjun-worker"] == "true"
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
