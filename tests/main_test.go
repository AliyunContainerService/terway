//go:build e2e

package tests

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samber/lo"
	"golang.org/x/mod/semver"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"

	"go.uber.org/atomic"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
	regionID          string
	testIPv4          bool
	testIPv6          bool
	testNetworkPolicy bool
	testTrunk         bool

	ipvlan bool

	terway        string
	terwayVersion string
	k8sVersion    string

	vSwitchIDs       string
	securityGroupIDs string

	repo          string
	timeout       string
	parsedTimeout time.Duration

	lock sync.Mutex

	isFailed atomic.Bool

	nginxImage string

	affinityLabel string

	eniConfig *Config
)

func init() {
	flag.StringVar(&regionID, "region-id", "cn-hangzhou", "region id")
	flag.StringVar(&repo, "repo", "registry.cn-hangzhou.aliyuncs.com/build-test", "image repo")
	flag.StringVar(&timeout, "timeout", "2m", "2m")
	flag.StringVar(&vSwitchIDs, "vswitch-ids", "", "extra vSwitchIDs")
	flag.StringVar(&securityGroupIDs, "security-group-ids", "", "extra securityGroupIDs")
	flag.StringVar(&affinityLabel, "label", "", "node affinity, format as key:value")
	flag.BoolVar(&testTrunk, "enable-trunk", true, "enable trunk test")
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
		WithRandomNamespace().WithParallelTestEnabled()

	testenv = env.NewWithConfig(envCfg)
	testenv.Setup(
		cleanupNamespaces,
		envfuncs.CreateNamespace(envCfg.Namespace()),
		patchNamespace,
		checkENIConfig,
		configureKubeClientQPS,
		printClusterEnvironment,
	)
	testenv.AfterEachFeature(func(ctx context.Context, config *envconf.Config, t *testing.T, feature features.Feature) (context.Context, error) {
		// If test was skipped, don't do anything
		if t.Skipped() {
			t.Log("Test was skipped, cleaning up")
			return ctx, nil
		}

		if IsTestSuccess(ctx) {
			t.Log("Test succeeded, cleaning up resources")
			lo.ForEach(ResourcesFromCtx(ctx), func(item k8s.Object, index int) {
				_ = config.Client().Resources().Delete(ctx, item)
				err := wait.For(conditions.New(config.Client().Resources()).ResourceDeleted(item),
					wait.WithInterval(1*time.Second), wait.WithImmediate(), wait.WithTimeout(1*time.Minute))
				if err != nil {
					t.Fatal("failed waiting for pods to be deleted", err)
				}
			})
		} else {
			t.Log("Test did not succeed, keeping resources for debugging")

			pod := &corev1.PodList{}
			err = config.Client().Resources(envCfg.Namespace()).List(ctx, pod)
			t.Log("---------list pods---------")
			// Filter pods that are not in Running state
			isTestFailed := false
			for _, printPod := range pod.Items {
				if printPod.Status.Phase != corev1.PodRunning {
					isTestFailed = true
				}
				t.Logf("Pod: %s/%s, Node: %s, Status: %s\n", printPod.Namespace, printPod.Name, printPod.Spec.NodeName, printPod.Status.Phase)
			}
			if isTestFailed {
				t.Log("---------list events---------")
				// List events
				event := &corev1.EventList{}
				err = config.Client().Resources(envCfg.Namespace()).List(ctx, event)
				for _, printEvent := range event.Items {
					t.Logf("%s/%s, Event: %s %s, Time:%s\n", printEvent.InvolvedObject.Kind, printEvent.InvolvedObject.Name, printEvent.Reason, printEvent.Message, printEvent.LastTimestamp)
				}
			}
		}
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

			// Get version from the first init container's image tag (more reliable)
			if len(d.Spec.Template.Spec.InitContainers) > 0 {
				image := d.Spec.Template.Spec.InitContainers[0].Image
				parts := strings.Split(image, ":")
				if len(parts) >= 2 {
					terwayVersion = parts[len(parts)-1]
				}
			} else if len(d.Spec.Template.Spec.Containers) > 0 {
				// Fallback to container image tag
				image := d.Spec.Template.Spec.Containers[0].Image
				parts := strings.Split(image, ":")
				if len(parts) >= 2 {
					terwayVersion = parts[len(parts)-1]
				}
			}

			break
		}
	}

	// Get k8s version from node
	nodes := &corev1.NodeList{}
	err = config.Client().Resources().List(ctx, nodes)
	if err != nil {
		return ctx, err
	}
	if len(nodes.Items) > 0 {
		k8sVersion = nodes.Items[0].Status.NodeInfo.KubeletVersion
		if !strings.HasPrefix(k8sVersion, "v") {
			k8sVersion = "v" + k8sVersion
		}
	}

	fmt.Printf("Detected terway daemonset: %s, version: %s, k8s version: %s\n", terway, terwayVersion, k8sVersion)

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
	ebpf := false
	if strings.Contains(cm.Data["10-terway.conf"], "ebpf") {
		ebpf = true
	}
	switch cm.Data["disable_network_policy"] {
	case "", "false":
		testNetworkPolicy = true
		if ebpf {
			ipvlan = true
		}
	}

	if ebpf && semver.Compare(terwayVersion, "v1.9.0") > 0 && semver.Compare(terwayVersion, "v1.10.0") < 0 {
		ipvlan = true
	}

	cfg := &Config{}
	err = json.Unmarshal([]byte(cm.Data["eni_conf"]), cfg)
	if err != nil {
		return nil, err
	}
	if !cfg.EnableENITrunking {
		testTrunk = false
	}

	if cfg.IPStack == "" || cfg.IPStack == "ipv4" || cfg.IPStack == "dual" {
		testIPv4 = true
	}
	if cfg.IPStack == "dual" || cfg.IPStack == "ipv6" {
		testIPv6 = true
	}

	eniConfig = cfg

	return ctx, nil
}

func cleanupNamespaces(ctx context.Context, config *envconf.Config) (context.Context, error) {
	// List all namespaces with the terway-e2e label
	nsList := &corev1.NamespaceList{}
	labelSelector := "k8s.aliyun.com/terway-e2e=true"
	err := config.Client().Resources().List(ctx, nsList, resources.WithLabelSelector(labelSelector))
	if err != nil {
		return ctx, fmt.Errorf("failed to list namespaces with terway-e2e label: %v", err)
	}

	// Delete each namespace found
	for _, ns := range nsList.Items {
		// Skip kube-system and other system namespaces
		if ns.Name == "kube-system" || ns.Name == "default" || strings.HasPrefix(ns.Name, "kube-") {
			continue
		}

		// Use fmt.Printf for logging during setup (before test starts)
		fmt.Printf("Cleaning up namespace: %s\n", ns.Name)
		err = config.Client().Resources().Delete(ctx, &ns)
		if err != nil {
			fmt.Printf("Warning: failed to delete namespace %s: %v\n", ns.Name, err)
			continue
		}

		// Wait for namespace deletion
		err = wait.For(conditions.New(config.Client().Resources()).ResourceDeleted(&ns),
			wait.WithTimeout(2*time.Minute), wait.WithInterval(5*time.Second))
		if err != nil {
			fmt.Printf("Warning: namespace %s deletion did not complete: %v\n", ns.Name, err)
		} else {
			fmt.Printf("Successfully deleted namespace: %s\n", ns.Name)
		}
	}

	return ctx, nil
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
				"node-local-dns-injection":  "enabled",
				"k8s.aliyun.com/terway-e2e": "true",
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

// configureKubeClientQPS configures kube_client_qps and kube_client_burst for all tests
func configureKubeClientQPS(ctx context.Context, config *envconf.Config) (context.Context, error) {
	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctx, nil
		}
		return ctx, err
	}

	eniConf := cm.Data["eni_conf"]
	if eniConf == "" {
		return ctx, nil
	}

	// Parse and update eni_conf
	var eniConfMap map[string]interface{}
	if err := json.Unmarshal([]byte(eniConf), &eniConfMap); err != nil {
		return ctx, fmt.Errorf("failed to parse eni_conf: %v", err)
	}

	// Set kube_client_qps and kube_client_burst
	eniConfMap["kube_client_qps"] = 50
	eniConfMap["kube_client_burst"] = 100

	updatedConf, err := json.Marshal(eniConfMap)
	if err != nil {
		return ctx, fmt.Errorf("failed to marshal eni_conf: %v", err)
	}

	cm.Data["eni_conf"] = string(updatedConf)
	err = config.Client().Resources().Update(ctx, cm)
	if err != nil {
		return ctx, fmt.Errorf("failed to update eni-config: %v", err)
	}

	fmt.Printf("Configured kube_client_qps=50, kube_client_burst=100\n")
	return ctx, nil
}

// printClusterEnvironment prints the cluster environment information including node capacities
func printClusterEnvironment(ctx context.Context, config *envconf.Config) (context.Context, error) {
	fmt.Println("=== Cluster Environment Information ===")

	// Discover node types and capacities
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, config.Client())
	if err != nil {
		fmt.Printf("Warning: failed to discover node types with capacity: %v\n", err)
		return ctx, nil // Don't fail the test setup
	}

	// Print node type summary
	fmt.Printf("Total Nodes: %d\n", len(nodeInfoWithCap.AllNodes))
	fmt.Printf("  - ECS Shared ENI: %d nodes\n", len(nodeInfoWithCap.ECSSharedENINodes))
	fmt.Printf("  - ECS Exclusive ENI: %d nodes\n", len(nodeInfoWithCap.ECSExclusiveENINodes))
	fmt.Printf("  - EFLO/Lingjun Shared ENI: %d nodes\n", len(nodeInfoWithCap.LingjunSharedENINodes))
	fmt.Printf("  - EFLO/Lingjun Exclusive ENI: %d nodes\n", len(nodeInfoWithCap.LingjunExclusiveENINodes))

	// Print capacity summary
	fmt.Println("\nNode Capacity Summary:")
	for _, nodeType := range GetAllNodeTypes() {
		qualified := nodeInfoWithCap.Capacities.GetQualifiedNodesForTest(nodeType)
		disqualified := nodeInfoWithCap.Capacities.GetDisqualifiedNodes(nodeType)

		if len(qualified) > 0 || len(disqualified) > 0 {
			fmt.Printf("  %s: %d qualified, %d disqualified\n", nodeType, len(qualified), len(disqualified))

			for _, nodeName := range qualified {
				cap := nodeInfoWithCap.Capacities[nodeName]
				fmt.Printf("    ✓ %s: adapters=%d, ipv4PerAdapter=%d (total IP capacity: %d)\n",
					nodeName, cap.Adapters, cap.IPv4PerAdapter, cap.Adapters*cap.IPv4PerAdapter)
			}

			for nodeName, reason := range disqualified {
				fmt.Printf("    ✗ %s: %s\n", nodeName, reason)
			}
		}
	}

	// Discover ENI resources
	eniInfo, err := DiscoverENIResources(ctx, config.Client())
	if err != nil {
		fmt.Printf("Warning: failed to discover ENI resources: %v\n", err)
	} else {
		fmt.Println("\nENI Resources:")
		fmt.Printf("  - aliyun/eni (Exclusive): %d total\n", eniInfo.TotalExclusiveENI)
		fmt.Printf("  - aliyun/member-eni (Trunk): %d total\n", eniInfo.TotalMemberENI)
	}

	fmt.Println("========================================")

	return ctx, nil
}
