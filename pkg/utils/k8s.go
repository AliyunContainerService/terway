package utils

import (
	"time"

	networkingclientset "github.com/AliyunContainerService/terway/pkg/generated/clientset/versioned"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// K8sClient k8s client set
var K8sClient kubernetes.Interface

// APIExtensionsClient k8s client set
var APIExtensionsClient apiextensionsclient.Interface

// NetworkClient network client set
var NetworkClient networkingclientset.Interface

// RegisterClients create all k8s clients
func RegisterClients(restConfig *rest.Config) {
	K8sClient = kubernetes.NewForConfigOrDie(restConfig)
	APIExtensionsClient = apiextensionsclient.NewForConfigOrDie(restConfig)
	NetworkClient = networkingclientset.NewForConfigOrDie(restConfig)
}

// IsStsPod pod is sts
func IsStsPod(pod *corev1.Pod) bool {
	for _, own := range pod.GetObjectMeta().GetOwnerReferences() {
		if own.Kind == "StatefulSet" {
			return true
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

// IsJobPod pod is create by Job
func IsJobPod(pod *corev1.Pod) bool {
	for _, own := range pod.GetObjectMeta().GetOwnerReferences() {
		if own.Kind == "Job" {
			return true
		}
	}
	return false
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

var (
	// DefaultPatchBackoff for patch status field
	DefaultPatchBackoff = wait.Backoff{
		Duration: 1 * time.Second,
		Steps:    3,
		Factor:   2,
		Jitter:   1.1,
	}
)
