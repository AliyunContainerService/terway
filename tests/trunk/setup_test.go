//go:build e2e
// +build e2e

package trunk

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
)

var (
	testenv env.Environment
)

const (
	pnName  = "default-config"
	podName = "pod-use-trunking"
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
	_ = networkv1beta1.AddToScheme(scheme.Scheme)

	testenv.Setup(
		envfuncs.CreateNamespace(envCfg.Namespace()),
		func(ctx context.Context, config *envconf.Config) (context.Context, error) {
			ns := &corev1.Namespace{}
			err := config.Client().Resources().Get(ctx, config.Namespace(), "", ns)
			if err != nil {
				return nil, err
			}
			mergePatch, _ := json.Marshal(map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"trunking-pod": "true",
					},
				},
			})
			err = config.Client().Resources().Patch(ctx, ns, k8s.Patch{PatchType: types.StrategicMergePatchType, Data: mergePatch})
			return ctx, err
		},
		func(ctx context.Context, config *envconf.Config) (context.Context, error) {
			_ = config.Client().Resources().Delete(ctx, newPodNetworking(pnName, nil, nil, nil, nil))
			return ctx, nil
		},
	)

	testenv.Finish(
		envfuncs.DeleteNamespace(envCfg.Namespace()),
	)

	os.Exit(testenv.Run(m))
}
