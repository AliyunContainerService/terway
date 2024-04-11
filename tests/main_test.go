//go:build e2e

package tests

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	k8sErr "k8s.io/apimachinery/pkg/api/errors"

	"go.uber.org/atomic"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
	"sigs.k8s.io/e2e-framework/pkg/features"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

var (
	testenv env.Environment
)

var (
	testIPv4           bool
	testIPv6           bool
	testNetworkPolicy  bool
	testTrunk          bool
	testPodLevelConfig bool

	ipvlan bool

	terway string

	vSwitchIDs       string
	securityGroupIDs string

	repo          string
	timeout       string
	parsedTimeout time.Duration

	lock sync.Mutex

	isFailed atomic.Bool

	nginxImage string
)

func init() {
	flag.StringVar(&repo, "repo", "registry.cn-hangzhou.aliyuncs.com/build-test", "image repo")
	flag.StringVar(&timeout, "timeout", "2m", "2m")
	flag.StringVar(&vSwitchIDs, "vswitch-ids", "", "extra vSwitchIDs")
	flag.StringVar(&securityGroupIDs, "security-group-ids", "", "extra securityGroupIDs")
}

func TestMain(m *testing.M) {
	flag.Parse()

	nginxImage = repo + "/nginx:1.23.1"
	var err error
	parsedTimeout, err = time.ParseDuration(timeout)
	if err != nil {
		panic("error parse timeout")
	}
	_ = clientgoscheme.AddToScheme(scheme.Scheme)
	_ = networkv1beta1.AddToScheme(scheme.Scheme)

	home, err := os.UserHomeDir()
	if err != nil {
		panic("error get home path")
	}

	envCfg := envconf.NewWithKubeConfig(filepath.Join(home, ".kube", "config")).
		WithRandomNamespace()

	testenv = env.NewWithConfig(envCfg)
	testenv.Setup(
		envfuncs.CreateNamespace(envCfg.Namespace()),
		patchNamespace,
		checkENIConfig,
		setNodeLabel,
		setPodNetworking,
	)
	testenv.AfterEachFeature(func(ctx context.Context, config *envconf.Config, t *testing.T, feature features.Feature) (context.Context, error) {
		objs, ok := ctx.Value(resourceKey).([]k8s.Object)
		if !ok {
			return ctx, nil
		}
		for _, obj := range objs {
			_ = config.Client().Resources().Delete(ctx, obj)

			err = wait.For(conditions.New(config.Client().Resources()).ResourceDeleted(obj), wait.WithContext(ctx),
				wait.WithTimeout(parsedTimeout),
				wait.WithInterval(1*time.Second))
			if err != nil {
				t.Error(err)
				t.FailNow()
			}
		}
		return ctx, nil
	})

	testenv.Finish(func(ctx context.Context, config *envconf.Config) (context.Context, error) {
		if !isFailed.Load() {
			return envfuncs.DeleteNamespace(envCfg.Namespace())(ctx, config)
		}

		pn := &networkv1beta1.PodNetworking{}
		pn.Name = "trunk"

		_ = config.Client().Resources().Delete(ctx, pn)
		return ctx, nil
	})

	os.Exit(testenv.Run(m))
}

func checkENIConfig(ctx context.Context, config *envconf.Config) (context.Context, error) {
	ds := &appsv1.DaemonSetList{}
	err := config.Client().Resources().WithNamespace("kube-system").List(ctx, ds)
	if err != nil {
		return ctx, err
	}
	for _, d := range ds.Items {
		switch d.Name {
		case "terway", "terway-eni", "terway-eniip":
			terway = d.Name
			break
		}
	}

	// we can determine cluster config by terway eni-conifg
	cm := &corev1.ConfigMap{}
	err = config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		if errors.IsNotFound(err) {
			testIPv4 = true
			return ctx, nil
		}
		return ctx, err
	}
	if strings.Contains(cm.Data["10-terway.conf"], "IPVlan") {
		ipvlan = true
	}
	if strings.Contains(cm.Data["10-terway.conf"], "datapathv2") {
		ipvlan = true
	}
	switch cm.Data["disable_network_policy"] {
	case "", "false":
		testNetworkPolicy = true
	}
	cfg := &Config{}
	err = json.Unmarshal([]byte(cm.Data["eni_conf"]), cfg)
	if err != nil {
		return nil, err
	}
	if cfg.EnableENITrunking {
		testTrunk = true
		testPodLevelConfig = true
	}
	if cfg.IPAMType == "crd" {
		testPodLevelConfig = true
	}
	if cfg.IPStack == "" || cfg.IPStack == "ipv4" || cfg.IPStack == "dual" {
		testIPv4 = true
	}
	if cfg.IPStack == "dual" || cfg.IPStack == "ipv6" {
		testIPv6 = true
	}

	return ctx, nil
}

func setNodeLabel(ctx context.Context, config *envconf.Config) (context.Context, error) {
	nodes := &corev1.NodeList{}
	err := config.Client().Resources().List(ctx, nodes)
	if err != nil {
		return nil, err
	}
	for _, node := range nodes.Items {
		val := ""
		if strings.Contains(node.Status.NodeInfo.OSImage, "Soaring Falcon") {
			val = "alinux3"
		}
		if strings.Contains(node.Status.NodeInfo.OSImage, "Hunting Beagle") {
			val = "alinux2"
		}
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"e2e-os": val,
				},
			},
		})
		err = config.Client().Resources().Patch(ctx, &node, k8s.Patch{PatchType: types.StrategicMergePatchType, Data: mergePatch})
		if err != nil {
			return nil, err
		}
	}
	return ctx, nil
}

func setPodNetworking(ctx context.Context, config *envconf.Config) (context.Context, error) {
	if !testTrunk {
		return ctx, nil
	}

	pn := &networkv1beta1.PodNetworking{}
	pn.Name = "trunk"
	pn.Spec.Selector = networkv1beta1.Selector{PodSelector: &metav1.LabelSelector{
		MatchLabels: map[string]string{"trunk": "enable"},
	}}
	if securityGroupIDs != "" {
		pn.Spec.SecurityGroupIDs = strings.Split(securityGroupIDs, ",")
	}
	if vSwitchIDs != "" {
		pn.Spec.VSwitchOptions = strings.Split(vSwitchIDs, ",")
	}

	err := config.Client().Resources().Create(ctx, pn)
	if err != nil {
		if !k8sErr.IsAlreadyExists(err) {
			return nil, err
		}
	}

	err = wait.For(func(ctx context.Context) (bool, error) {
		pn := &networkv1beta1.PodNetworking{}
		err := config.Client().Resources().Get(ctx, "trunk", "", pn)
		if err != nil {
			return false, err
		}
		if pn.Status.Status != networkv1beta1.NetworkingStatusReady {
			return false, nil
		}
		return true, nil
	},
		wait.WithTimeout(10*time.Second),
		wait.WithInterval(1*time.Second))

	return ctx, err
}

func patchNamespace(ctx context.Context, config *envconf.Config) (context.Context, error) {
	ns := &corev1.Namespace{}
	err := config.Client().Resources().Get(ctx, config.Namespace(), "", ns)
	if err != nil {
		return ctx, err
	}
	mergePatch, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"ns":                       config.Namespace(),
				"node-local-dns-injection": "enabled",
			},
		},
	})
	err = config.Client().Resources().Patch(ctx, ns, k8s.Patch{PatchType: types.StrategicMergePatchType, Data: mergePatch})
	return ctx, err
}

type Config struct {
	EnableENITrunking bool   `json:"enable_eni_trunking"`
	IPStack           string `json:"ip_stack"`
	IPAMType          string `json:"ipam_type"`
}
