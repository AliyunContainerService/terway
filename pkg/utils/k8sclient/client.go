package k8sclient

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// K8sClient k8s client set
var K8sClient kubernetes.Interface

// RegisterClients create all k8s clients
func RegisterClients(restConfig *rest.Config) {
	K8sClient = kubernetes.NewForConfigOrDie(restConfig)
}
