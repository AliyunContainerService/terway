package utils

import (
	"time"

	networkingclientset "github.com/AliyunContainerService/terway/pkg/generated/clientset/versioned"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
)

// K8sClient k8s client set
var K8sClient kubernetes.Interface

// APIExtensionsClient k8s client set
var APIExtensionsClient apiextensionsclient.Interface

// NetworkClient network client set
var NetworkClient networkingclientset.Interface

// RegisterClients create all k8s clients
func RegisterClients() {
	restConfig := ctrl.GetConfigOrDie()
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

var (
	// DefaultPatchBackoff for patch status field
	DefaultPatchBackoff = wait.Backoff{
		Duration: 1 * time.Second,
		Steps:    3,
		Factor:   2,
		Jitter:   1.1,
	}
)
