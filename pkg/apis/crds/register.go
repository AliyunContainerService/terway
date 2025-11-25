package crds

import (
	"context"
	_ "embed"
	"fmt"

	"golang.org/x/mod/semver"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/yaml"
)

var log = ctrl.Log.WithName("crd")

// crd names
const (
	CRDPodENI           = "podenis.network.alibabacloud.com"
	CRDPodNetworking    = "podnetworkings.network.alibabacloud.com"
	CRDNode             = "nodes.network.alibabacloud.com"
	CRDNodeRuntime      = "noderuntimes.network.alibabacloud.com"
	CRDNetworkInterface = "networkinterfaces.network.alibabacloud.com"

	crdVersionKey = "crd.network.alibabacloud.com/version"
)

var (
	//go:embed network.alibabacloud.com_podenis.yaml
	crdsPodENI []byte

	//go:embed network.alibabacloud.com_podnetworkings.yaml
	crdsPodNetworking []byte

	//go:embed network.alibabacloud.com_nodes.yaml
	crdsNode []byte

	//go:embed network.alibabacloud.com_noderuntimes.yaml
	crdsNodeRuntime []byte

	//go:embed network.alibabacloud.com_networkinterfaces.yaml
	crdsNetworkInterface []byte
)

func getCRD(name string) apiextensionsv1.CustomResourceDefinition {
	var crdBytes []byte
	var version string
	switch name {
	case CRDPodENI:
		crdBytes = crdsPodENI
		version = "v0.4.2"
	case CRDPodNetworking:
		crdBytes = crdsPodNetworking
		//nolint
		version = "v0.1.0"
	case CRDNode:
		crdBytes = crdsNode
		version = "v0.6.1"
	case CRDNodeRuntime:
		crdBytes = crdsNodeRuntime
		version = "v0.1.0"
	case CRDNetworkInterface:
		crdBytes = crdsNetworkInterface
		version = "v0.1.0"
	default:
		panic(fmt.Sprintf("crd %s name not exist", name))
	}
	ciliumCRD := apiextensionsv1.CustomResourceDefinition{}
	err := yaml.Unmarshal(crdBytes, &ciliumCRD)
	if err != nil {
		panic(fmt.Sprintf("error unmarshalling CRD %s,%s", name, err.Error()))
	}

	if ciliumCRD.Annotations == nil {
		ciliumCRD.Annotations = make(map[string]string)
	}
	ciliumCRD.Annotations[crdVersionKey] = version

	return ciliumCRD
}

func CreateOrUpdateCRD(ctx context.Context, c client.Client, name string) error {
	log.Info("syncing", "crd", name)

	expect := getCRD(name)
	exist := &apiextensionsv1.CustomResourceDefinition{}
	err := c.Get(ctx, client.ObjectKey{
		Name: expect.Name,
	}, exist)

	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		log.Info("creating", "crd", name)
		err = c.Create(ctx, &expect)
		if err == nil {
			return nil
		}
		if errors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}

	result := semver.Compare(exist.Annotations[crdVersionKey], expect.Annotations[crdVersionKey])
	if result >= 0 {
		return nil
	}
	log.Info("update crd", "exist", exist.Annotations[crdVersionKey], "expect", expect.Annotations[crdVersionKey])

	update := exist.DeepCopy()
	_, err = controllerutil.CreateOrPatch(ctx, c, update, func() error {
		update.Spec = expect.Spec
		update.Annotations = expect.Annotations
		return nil
	})

	return err
}
