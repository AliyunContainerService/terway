package k8sclient

import (
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	networkingclientset "github.com/AliyunContainerService/terway/pkg/generated/clientset/versioned"
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
