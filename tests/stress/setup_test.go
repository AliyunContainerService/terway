//go:build e2e

package stress

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/client-go/kubernetes/scheme"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"

	"github.com/AliyunContainerService/terway/tests/utils"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

var (
	testenv env.Environment

	containerNetworkPods int
)

func TestMain(m *testing.M) {
	home, err := os.UserHomeDir()
	if err != nil {
		panic("error get home path")
	}

	restConfig, err := clientcmd.BuildConfigFromFlags("", filepath.Join(home, ".kube", "config"))
	if err != nil {
		panic(err)
	}

	restConfig.QPS = 3000
	restConfig.Burst = 5000

	client, err := klient.New(restConfig)
	if err != nil {
		panic(err)
	}
	envCfg := envconf.New().WithRandomNamespace().WithClient(client)
	testenv = env.NewWithConfig(envCfg)

	_ = clientgoscheme.AddToScheme(scheme.Scheme)
	_ = v1beta1.AddToScheme(scheme.Scheme)

	testenv.Setup(
		envfuncs.CreateNamespace(envCfg.Namespace()),
		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			containerNetworkPods, err = utils.GetAvailableContainerNetworkPods(ctx, cfg.Client())
			return ctx, err
		},
	)

	testenv.Finish(
		envfuncs.DeleteNamespace(envCfg.Namespace()),
	)

	os.Exit(testenv.Run(m))
}
