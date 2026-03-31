//go:build e2e

package tests

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"k8s.io/client-go/kubernetes/scheme"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/e2e-framework/klient"
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

	kubeConfigPath := filepath.Join(home, ".kube", "config")
	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	if err != nil {
		panic(fmt.Sprintf("error build rest config: %v", err))
	}
	restConfig.QPS = 50
	restConfig.Burst = 100

	client, err := klient.New(restConfig)
	if err != nil {
		panic(fmt.Sprintf("error create klient: %v", err))
	}

	envCfg := envconf.New().WithClient(client).
		WithRandomNamespace().WithParallelTestEnabled()

	testenv = env.NewWithConfig(envCfg)
	testenv.Setup(
		cleanupNamespaces,
		envfuncs.CreateNamespace(envCfg.Namespace()),
		patchNamespace,
		checkENIConfig,
		configureKubeClientQPS,
		printClusterEnvironment,
		labelExternalIPNodes,
		labelIPPrefixNodes,
		resetNodeConfigToDefault,
	)
	testenv.AfterEachFeature(func(ctx context.Context, config *envconf.Config, t *testing.T, feature features.Feature) (context.Context, error) {
		if t.Skipped() {
			return ctx, nil
		}

		if !IsTestSuccess(ctx) {
			// Log debug info before cleaning up
			pod := &corev1.PodList{}
			if err := config.Client().Resources(envCfg.Namespace()).List(ctx, pod); err != nil {
				t.Logf("Warning: failed to list pods: %v", err)
			} else {
				t.Log("---------list pods---------")
				isTestFailed := false
				for _, printPod := range pod.Items {
					if printPod.Status.Phase != corev1.PodRunning {
						isTestFailed = true
					}
					t.Logf("Pod: %s/%s, Node: %s, Status: %s", printPod.Namespace, printPod.Name, printPod.Spec.NodeName, printPod.Status.Phase)
				}
				if isTestFailed {
					t.Log("---------list events---------")
					event := &corev1.EventList{}
					if err := config.Client().Resources(envCfg.Namespace()).List(ctx, event); err != nil {
						t.Logf("Warning: failed to list events: %v", err)
					} else {
						for _, printEvent := range event.Items {
							t.Logf("%s/%s, Event: %s %s, Time:%s", printEvent.InvolvedObject.Kind, printEvent.InvolvedObject.Name, printEvent.Reason, printEvent.Message, printEvent.LastTimestamp)
						}
					}
				}
			}
		}

		// Always clean up resources to prevent cascade failures
		lo.ForEach(ResourcesFromCtx(ctx), func(item k8s.Object, index int) {
			_ = config.Client().Resources().Delete(ctx, item)
			err := wait.For(conditions.New(config.Client().Resources()).ResourceDeleted(item),
				wait.WithInterval(1*time.Second), wait.WithImmediate(), wait.WithTimeout(1*time.Minute))
			if err != nil {
				t.Logf("Warning: failed waiting for resource %s to be deleted: %v", item.GetName(), err)
			}
		})

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
	eniConfMap["kube_client_qps"] = 200
	eniConfMap["kube_client_burst"] = 300

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

func labelExternalIPNodes(ctx context.Context, config *envconf.Config) (context.Context, error) {
	nodes := &corev1.NodeList{}
	err := config.Client().Resources().List(ctx, nodes)
	if err != nil {
		return ctx, err
	}

	for _, node := range nodes.Items {
		hasExternalIP := false
		for _, stack := range []string{"ipv4", "ipv6"} {
			_, externalIP := getNodeIPs(&node, stack == "ipv6")
			if externalIP != "" {
				hasExternalIP = true
				break
			}
		}

		if hasExternalIP {
			fmt.Printf("Labeling node %s with e2e.aliyun.com/external-ip=true\n", node.Name)
			patch, _ := json.Marshal(map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"e2e.aliyun.com/external-ip": "true",
					},
				},
			})
			err = config.Client().Resources().Patch(ctx, &node, k8s.Patch{PatchType: types.StrategicMergePatchType, Data: patch})
			if err != nil {
				fmt.Printf("Warning: failed to label node %s: %v\n", node.Name, err)
			}
		}
	}

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
	fmt.Printf("  - ECS IP Prefix: %d nodes\n", len(nodeInfoWithCap.ECSIPPrefixNodes))
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

	// Print IP Prefix enabled nodes (determined by Node CR ENISpec.EnableIPPrefix)
	var prefixNodes []string
	for nodeName, cap := range nodeInfoWithCap.Capacities {
		if cap.EnableIPPrefix {
			prefixNodes = append(prefixNodes, nodeName)
		}
	}
	if len(prefixNodes) > 0 {
		sort.Strings(prefixNodes)
		fmt.Printf("\n  IP Prefix enabled nodes (enableIPPrefix=true in Node CR): %d\n", len(prefixNodes))
		for _, nodeName := range prefixNodes {
			cap := nodeInfoWithCap.Capacities[nodeName]
			maxPrefixes := (cap.Adapters - 1) * (cap.IPv4PerAdapter - 1)
			fmt.Printf("    ✓ %s: adapters=%d, ipv4PerAdapter=%d, maxPrefixes=%d\n",
				nodeName, cap.Adapters, cap.IPv4PerAdapter, maxPrefixes)
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

// SecurityGroupTestConfig holds security group IDs for a specific test
type SecurityGroupTestConfig struct {
	TestName   string
	ClientSGID string
	ServerSGID string
}

// labelIPPrefixNodes labels nodes with k8s.aliyun.com/ip-prefix=true if their Node CR
// has EnableIPPrefix=true. This ensures IP Prefix nodes can be discovered by classifyNode
// even when the node pool label was not set in Terraform.
func labelIPPrefixNodes(ctx context.Context, config *envconf.Config) (context.Context, error) {
	nodeCRList := &networkv1beta1.NodeList{}
	err := config.Client().Resources().List(ctx, nodeCRList)
	if err != nil {
		fmt.Printf("Warning: failed to list Node CRs for IP prefix labeling: %v\n", err)
		return ctx, nil
	}

	for _, nodeCR := range nodeCRList.Items {
		if nodeCR.Spec.ENISpec == nil || !nodeCR.Spec.ENISpec.EnableIPPrefix {
			continue
		}

		node := &corev1.Node{}
		err := config.Client().Resources().Get(ctx, nodeCR.Name, "", node)
		if err != nil {
			fmt.Printf("Warning: failed to get node %s: %v\n", nodeCR.Name, err)
			continue
		}

		if node.Labels["k8s.aliyun.com/ip-prefix"] == "true" {
			continue
		}

		fmt.Printf("Labeling IP Prefix node %s with k8s.aliyun.com/ip-prefix=true\n", node.Name)
		patch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"k8s.aliyun.com/ip-prefix": "true",
				},
			},
		})
		err = config.Client().Resources().Patch(ctx, node, k8s.Patch{PatchType: types.StrategicMergePatchType, Data: patch})
		if err != nil {
			fmt.Printf("Warning: failed to label node %s: %v\n", node.Name, err)
		}
	}

	return ctx, nil
}

// defaultIPv4PrefixCount is the default number of IPv4 prefixes allocated on prefix nodes
// during initialization. This ensures prefix nodes participate in standard shared ENI tests.
const defaultIPv4PrefixCount = 5

// resetNodeConfigToDefault resets the eni-config pool settings to default values
// before any test runs. This ensures a clean starting state where:
//   - IP pool preheating is disabled (max_pool_size=0, min_pool_size=0)
//   - Prefix node e2e-ip-prefix ConfigMap has the default ipv4_prefix_count
//
// Prefix node state is NOT forcefully cleaned here; prefix tests manage their own
// node state through prefix_helpers_test.go. Directly writing Node CR status from
// global setup corrupts the data plane on other node types sharing the cluster.
func resetNodeConfigToDefault(ctx context.Context, config *envconf.Config) (context.Context, error) {
	if terway != "terway-eniip" {
		fmt.Printf("Skipping node config reset: terway=%s (only supported for terway-eniip)\n", terway)
		return ctx, nil
	}

	fmt.Println("=== Resetting node configuration to defaults ===")

	// Step 1: Configure eni-config with default pool settings
	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		if errors.IsNotFound(err) {
			fmt.Println("eni-config not found, skipping configuration reset")
			return ctx, nil
		}
		return ctx, fmt.Errorf("failed to get eni-config: %w", err)
	}

	eniConf := cm.Data["eni_conf"]
	if eniConf != "" {
		var eniConfMap map[string]interface{}
		if err := json.Unmarshal([]byte(eniConf), &eniConfMap); err != nil {
			return ctx, fmt.Errorf("failed to parse eni_conf: %w", err)
		}

		eniConfMap["max_pool_size"] = 0
		eniConfMap["min_pool_size"] = 0
		delete(eniConfMap, "ip_pool_sync_period")

		updatedConf, err := json.Marshal(eniConfMap)
		if err != nil {
			return ctx, fmt.Errorf("failed to marshal eni_conf: %w", err)
		}

		cm.Data["eni_conf"] = string(updatedConf)
		if err := config.Client().Resources().Update(ctx, cm); err != nil {
			return ctx, fmt.Errorf("failed to update eni-config: %w", err)
		}
		fmt.Println("Configured eni-config: max_pool_size=0, min_pool_size=0")
	}

	// Step 2: Ensure e2e-ip-prefix ConfigMap exists with default prefix count (if prefix nodes exist).
	// This only updates the ConfigMap; it does NOT forcefully clean up Node CR status.
	// Terway will reconcile prefix nodes to the desired state after restart.
	hasPrefixNodes := false
	nodeCRList := &networkv1beta1.NodeList{}
	if err := config.Client().Resources().List(ctx, nodeCRList); err != nil {
		fmt.Printf("Warning: failed to list Node CRs: %v\n", err)
	} else {
		for _, nodeCR := range nodeCRList.Items {
			if nodeCR.Spec.ENISpec != nil && nodeCR.Spec.ENISpec.EnableIPPrefix {
				hasPrefixNodes = true
				break
			}
		}
	}

	if hasPrefixNodes {
		prefixCMData := fmt.Sprintf(`{"enable_ip_prefix":true,"ipv4_prefix_count":%d}`, defaultIPv4PrefixCount)
		existingCM := &corev1.ConfigMap{}
		err = config.Client().Resources().Get(ctx, "e2e-ip-prefix", "kube-system", existingCM)
		if err != nil {
			newCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "e2e-ip-prefix",
					Namespace: "kube-system",
				},
				Data: map[string]string{
					"eni_conf": prefixCMData,
				},
			}
			if createErr := config.Client().Resources().Create(ctx, newCM); createErr != nil {
				return ctx, fmt.Errorf("failed to create e2e-ip-prefix: %w", createErr)
			}
			fmt.Printf("Created e2e-ip-prefix ConfigMap: ipv4_prefix_count=%d\n", defaultIPv4PrefixCount)
		} else {
			existingCM.Data["eni_conf"] = prefixCMData
			if updateErr := config.Client().Resources().Update(ctx, existingCM); updateErr != nil {
				return ctx, fmt.Errorf("failed to update e2e-ip-prefix: %w", updateErr)
			}
			fmt.Printf("Updated e2e-ip-prefix ConfigMap: ipv4_prefix_count=%d\n", defaultIPv4PrefixCount)
		}
	}

	// Step 3: Restart terway to apply configuration
	fmt.Println("Restarting terway to apply default configuration...")
	if err := restartTerway(ctx, config); err != nil {
		return ctx, fmt.Errorf("failed to restart terway: %w", err)
	}

	// Step 4: Verify shared ENI nodes have converged to pool=0.
	// Prefix and exclusive ENI nodes are skipped — prefix tests handle their own state,
	// and exclusive ENI nodes don't use pool-based IP management.
	fmt.Println("Verifying shared ENI node state after configuration reset...")
	if err := verifyNodeCRDefaultState(ctx, config, hasPrefixNodes); err != nil {
		return ctx, fmt.Errorf("Node CR verification failed after config reset: %w", err)
	}

	fmt.Println("=== Node configuration reset complete ===")
	return ctx, nil
}

// verifyNodeCRDefaultState polls Node CRs until shared ENI nodes have converged
// to pool=0 (no idle IPs). Prefix and exclusive ENI nodes are skipped:
//   - Exclusive ENI nodes don't use pool-based IP management
//   - Prefix nodes manage their own state through prefix-specific tests
func verifyNodeCRDefaultState(ctx context.Context, config *envconf.Config, _ bool) error {
	return wait.For(func(ctx context.Context) (done bool, err error) {
		nodeCRList := &networkv1beta1.NodeList{}
		if err := config.Client().Resources().List(ctx, nodeCRList); err != nil {
			return false, err
		}

		allGood := true
		for i := range nodeCRList.Items {
			node := &nodeCRList.Items[i]

			if isExclusiveENINode(node) || isPrefixNode(node) {
				continue
			}

			if !verifyPoolNodeState(node) {
				allGood = false
			}
		}

		return allGood, nil
	}, wait.WithTimeout(5*time.Minute), wait.WithInterval(10*time.Second))
}

// verifyPoolNodeState checks that a non-prefix shared ENI node has no idle IPs (pool disabled).
func verifyPoolNodeState(node *networkv1beta1.Node) bool {
	idle := 0
	for _, eni := range node.Status.NetworkInterfaces {
		if eni.Status != aliyunClient.ENIStatusInUse {
			continue
		}
		for _, ip := range eni.IPv4 {
			if !ip.Primary && ip.Status == networkv1beta1.IPStatusValid && ip.PodID == "" {
				idle++
			}
		}
	}
	if idle > 0 {
		fmt.Printf("  [verify] Node %s (shared): %d idle IPs, expecting 0\n", node.Name, idle)
		return false
	}
	return true
}

// GetSecurityGroupTestConfig returns the security group configuration for a specific test
// Uses environment variable TERWAY_SG_TEST_CONFIG
// Config format: TEST_NAME:CLIENT_SG:SERVER_SG;TEST_NAME2:CLIENT_SG2:SERVER_SG2
// Example: export TERWAY_SG_TEST_CONFIG="TestSecurityGroup_TrunkMode:sg-xxx:sg-yyy"
func GetSecurityGroupTestConfig(testName string) *SecurityGroupTestConfig {
	configStr := os.Getenv("TERWAY_SG_TEST_CONFIG")
	if configStr == "" {
		return nil
	}

	// Parse each config entry separated by semicolon
	entries := strings.Split(configStr, ";")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		parts := strings.Split(entry, ":")
		if len(parts) != 3 {
			continue
		}

		name := strings.TrimSpace(parts[0])
		clientSG := strings.TrimSpace(parts[1])
		serverSG := strings.TrimSpace(parts[2])

		if name == testName && clientSG != "" && serverSG != "" {
			return &SecurityGroupTestConfig{
				TestName:   name,
				ClientSGID: clientSG,
				ServerSGID: serverSG,
			}
		}
	}

	return nil
}
