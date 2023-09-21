package crds

import (
	"context"
	_ "embed"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/AliyunContainerService/terway/pkg/utils"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/yaml"
)

var log = ctrl.Log.WithName("crd")

// crd names
const (
	CRDPodENI        = "podenis.network.alibabacloud.com"
	CRDPodNetworking = "podnetworkings.network.alibabacloud.com"

	CRDPodEIP = "podeips.alibabacloud.com"

	crdVersionKey = "crd.network.alibabacloud.com/version"
)

var (
	//go:embed network.alibabacloud.com_podenis.yaml
	crdsPodENI []byte

	//go:embed network.alibabacloud.com_podnetworkings.yaml
	crdsPodNetworking []byte

	//go:embed alibabacloud.com_podeips.yaml
	crdsPodEIP []byte
)

func getCRD(name string) apiextensionsv1.CustomResourceDefinition {
	var crdBytes []byte
	switch name {
	case CRDPodENI:
		crdBytes = crdsPodENI
	case CRDPodNetworking:
		crdBytes = crdsPodNetworking
	case CRDPodEIP:
		crdBytes = crdsPodEIP
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
	log.Info("creating", "crd", name)
	client := cs.ApiextensionsV1().CustomResourceDefinitions()
	crd := getCRD(name)
	_, err := client.Create(context.TODO(), &crd, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if errors.IsAlreadyExists(err) {
		log.Info("already exist", "name", name)
		return nil
	}
	return err
}

func createOrUpdateCRD(cs apiextensionsclient.Interface, name string) error {
	log.Info("syncing", "crd", name)
	client := cs.ApiextensionsV1().CustomResourceDefinitions()
	crd := getCRD(name)
	exist, err := client.Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		log.Info("creating", "crd", name)
		_, err = client.Create(context.TODO(), &crd, metav1.CreateOptions{})
		if err == nil {
			return nil
		}
		if errors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}

	if exist.Annotations[crdVersionKey] == crd.Annotations[crdVersionKey] {
		return nil
	}
	crd.CreationTimestamp = exist.CreationTimestamp
	crd.ResourceVersion = exist.ResourceVersion
	crd.UID = exist.UID
	crd.Generation = exist.Generation
	_, err = client.Update(context.TODO(), &crd, metav1.UpdateOptions{})
	return err
}

// RegisterCRDs will create all crds if not present
func RegisterCRDs() error {
	crds := []string{CRDPodENI, CRDPodNetworking}
	for _, crd := range crds {
		err := createOrUpdateCRD(utils.APIExtensionsClient, crd)
		if err != nil {
			return err
		}
	}
	log.Info("syncing crd success")
	return nil
}

func RegisterCRD(crds []string) error {
	for _, crd := range crds {
		err := createCRD(utils.APIExtensionsClient, crd)
		if err != nil {
			return err
		}
	}
	log.Info("create crd success")
	return nil
}
