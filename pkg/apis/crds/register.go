package crds

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/AliyunContainerService/terway/pkg/utils"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/yaml"
)

var log = ctrl.Log.WithName("crd")

// crd names
const (
	CRDPodENI        = "podenis.network.alibabacloud.com"
	CRDPodNetworking = "podnetworkings.network.alibabacloud.com"
)

var (
	//go:embed network.alibabacloud.com_podenis.yaml
	crdsPodENI []byte

	//go:embed network.alibabacloud.com_podnetworkings.yaml
	crdsPodNetworking []byte
)

func getCRD(name string) apiextensionsv1.CustomResourceDefinition {
	var crdBytes []byte
	switch name {
	case CRDPodENI:
		crdBytes = crdsPodENI
	case CRDPodNetworking:
		crdBytes = crdsPodNetworking
	default:
		panic(fmt.Sprintf("crd %s name not exist", name))
	}
	ciliumCRD := apiextensionsv1.CustomResourceDefinition{}
	err := yaml.Unmarshal(crdBytes, &ciliumCRD)
	if err != nil {
		panic(fmt.Sprintf("error unmarshalling CRD %s,%s", name, err.Error()))
	}

	return ciliumCRD
}

func createCRD(cs apiextensionsclient.Interface, name string) error {
	log.Info("syncing", "crd", name)
	client := cs.ApiextensionsV1().CustomResourceDefinitions()
	crd := getCRD(name)
	_, err := client.Get(context.TODO(), name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		log.Info("creating", "crd", name)
		_, err = client.Create(context.TODO(), &crd, metav1.CreateOptions{})
	}
	if err != nil {
		if errors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}

	return nil
}

// RegisterCRDs will create all crds if not present
func RegisterCRDs() error {
	crds := []string{CRDPodENI, CRDPodNetworking}
	for _, crd := range crds {
		err := createCRD(utils.APIExtensionsClient, crd)
		if err != nil {
			return err
		}
	}
	log.Info("syncing crd success")
	return nil
}
