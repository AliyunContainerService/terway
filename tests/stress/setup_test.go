//go:build e2e

package stress

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/AliyunContainerService/terway/tests/utils"
	"k8s.io/client-go/kubernetes/scheme"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
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

	envCfg := envconf.NewWithKubeConfig(filepath.Join(home, ".kube", "config")).
		WithRandomNamespace()

	testenv = env.NewWithConfig(envCfg)

	_ = clientgoscheme.AddToScheme(scheme.Scheme)

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
