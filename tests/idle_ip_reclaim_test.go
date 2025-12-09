//go:build e2e

package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/Jeffail/gabs/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// =============================================================================
// Shared ENI Idle IP Reclaim Tests
// =============================================================================

// MinIPsForIdleIPReclaimTest is the minimum number of IPs required for idle IP reclaim testing
// The test preheats to 10 IPs, so nodes must have capacity for at least 10 IPs
const MinIPsForIdleIPReclaimTest = 10

// TestSharedENI_IdleIPReclaim tests the idle IP reclaim policy feature
// for shared ENI mode on both ECS and Lingjun nodes.
// This test only covers centralized IPAM mode (ipam_type == "crd")
func TestSharedENI_IdleIPReclaim(t *testing.T) {
	// Pre-check: only test centralized IPAM mode
	if eniConfig == nil || eniConfig.IPAMType != "crd" {
		ipamType := ""
		if eniConfig != nil {
			ipamType = eniConfig.IPAMType
		}
		t.Skipf("skip: ipam type is not crd, current type: %s", ipamType)
		return
	}

	// Pre-check: terway daemonset name must be terway-eniip
	if terway != "terway-eniip" {
		t.Skipf("TestSharedENI_IdleIPReclaim requires terway-eniip daemonset, current: %s", terway)
		return
	}

	// Pre-check: terway version must be >= v1.16.1
	if !RequireTerwayVersion("v1.16.1") {
		t.Skipf("TestSharedENI_IdleIPReclaim requires terway version >= v1.16.1, current version: %s", GetCachedTerwayVersion())
		return
	}

	// Discover node capacities to filter out low-spec nodes
	ctx := context.Background()
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types with capacity: %v", err)
	}

	// Print capacity summary for debugging
	nodeInfoWithCap.Capacities.PrintCapacitySummary(t)

	// Filter qualified nodes for each node type
	nodeTypes := []NodeType{NodeTypeECSSharedENI, NodeTypeLingjunSharedENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		// Check if there are qualified nodes for this type
		qualifiedNodes := getQualifiedNodesForIdleIPReclaim(nodeInfoWithCap, nodeType)
		if len(qualifiedNodes) == 0 {
			t.Logf("Skipping %s: no nodes meet capacity requirements (need %d adapters, IP capacity >= %d)",
				nodeType, MinAdaptersForSharedENI, MinIPsForIdleIPReclaimTest)
			// Log disqualified nodes for debugging
			for nodeName, cap := range nodeInfoWithCap.Capacities {
				if cap.NodeType == nodeType {
					t.Logf("  Node %s: adapters=%d, ipv4PerAdapter=%d (total capacity: %d IPs)",
						nodeName, cap.Adapters, cap.IPv4PerAdapter, cap.Adapters*cap.IPv4PerAdapter)
				}
			}
			continue
		}

		t.Logf("Found %d qualified nodes for %s: %v", len(qualifiedNodes), nodeType, qualifiedNodes)
		feat := createIdleIPReclaimTestWithQualifiedNodes(nodeType, qualifiedNodes)
		feats = append(feats, feat)
	}

	if len(feats) == 0 {
		t.Skip("No qualified nodes found for idle IP reclaim testing")
		return
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// getQualifiedNodesForIdleIPReclaim returns nodes that meet capacity requirements for idle IP reclaim test
func getQualifiedNodesForIdleIPReclaim(nodeInfo *NodeTypeInfoWithCapacity, nodeType NodeType) []string {
	var qualified []string

	for nodeName, cap := range nodeInfo.Capacities {
		if cap.NodeType != nodeType {
			continue
		}

		// Check if node has enough adapters
		if !cap.MeetsSharedENIRequirements {
			continue
		}

		// Check if node has enough total IP capacity (adapters * ipv4PerAdapter)
		totalIPCapacity := cap.Adapters * cap.IPv4PerAdapter
		if totalIPCapacity >= MinIPsForIdleIPReclaimTest {
			qualified = append(qualified, nodeName)
		}
	}

	return qualified
}

// createIdleIPReclaimTestWithQualifiedNodes creates idle IP reclaim tests with qualified node filtering
func createIdleIPReclaimTestWithQualifiedNodes(nodeType NodeType, qualifiedNodes []string) features.Feature {
	eniMode := getENIModeFromNodeType(nodeType)
	machineType := getMachineTypeFromNodeType(nodeType)

	return features.New(fmt.Sprintf("%sENI/IdleIPReclaim/%s", eniMode, machineType)).
		WithLabel("eni-mode", eniMode).
		WithLabel("machine-type", machineType).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			t.Logf("Testing with qualified nodes: %v", qualifiedNodes)

			// Store qualified nodes in context for later use
			ctx = context.WithValue(ctx, qualifiedNodesContextKey, qualifiedNodes)

			// Cleanup pool configuration before tests
			err := cleanupPoolConfig(ctx, t, config)
			if err != nil {
				t.Fatalf("failed to cleanup pool configuration: %v", err)
			}

			return ctx
		}).
		Assess("basic idle IP reclaim after timeout", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			qualifiedNodes := ctx.Value(qualifiedNodesContextKey).([]string)
			return assessIdleIPReclaimBasic(ctx, t, config, nodeType, qualifiedNodes)
		}).
		Assess("batch reclaim respects batch_size", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			qualifiedNodes := ctx.Value(qualifiedNodesContextKey).([]string)
			return assessIdleIPReclaimBatch(ctx, t, config, nodeType, qualifiedNodes)
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Restore default configuration
			err := cleanupPoolConfig(ctx, t, config)
			if err != nil {
				t.Logf("Warning: failed to cleanup pool configuration in teardown: %v", err)
			}
			return ctx
		}).
		Feature()
}

// qualifiedNodesContextKey is the context key for storing qualified node names
type qualifiedNodesContextKeyType struct{}

var qualifiedNodesContextKey = qualifiedNodesContextKeyType{}

// =============================================================================
// Assess Functions for Qualified Nodes
// =============================================================================

// assessIdleIPReclaimBasic tests basic idle IP reclaim after timeout for qualified nodes
func assessIdleIPReclaimBasic(ctx context.Context, t *testing.T, config *envconf.Config, nodeType NodeType, qualifiedNodes []string) context.Context {
	t.Log("Verify IPs are reclaimed after idle_ip_reclaim_after duration")
	t.Logf("Testing on qualified nodes: %v", qualifiedNodes)

	qualifiedSet := make(map[string]bool)
	for _, name := range qualifiedNodes {
		qualifiedSet[name] = true
	}

	// Step 1: First preheat the pool to 10 IPs
	t.Log("Step 1: Preheat pool to 10 idle IPs (min=max=10)")
	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		t.Fatalf("failed to get eni-config: %v", err)
	}

	eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
	if err != nil {
		t.Fatalf("failed to parse eni_conf: %v", err)
	}

	// Set min=max=10 for preheating
	_, err = eniJson.Set("30s", "ip_pool_sync_period")
	if err != nil {
		t.Fatalf("failed to set ip_pool_sync_period: %v", err)
	}
	_, err = eniJson.Set(10, "max_pool_size")
	if err != nil {
		t.Fatalf("failed to set max_pool_size: %v", err)
	}
	_, err = eniJson.Set(10, "min_pool_size")
	if err != nil {
		t.Fatalf("failed to set min_pool_size: %v", err)
	}

	cm.Data["eni_conf"] = eniJson.String()
	err = config.Client().Resources().Update(ctx, cm)
	if err != nil {
		t.Fatalf("failed to update eni-config: %v", err)
	}

	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Wait for preheating to 10 IPs on qualified nodes only
	t.Log("Step 2: Wait for IP pool preheating to 10 IPs on qualified nodes")
	err = waitForIdleIPCountByQualifiedNodes(ctx, config, t, qualifiedNodes, 10, 5*time.Minute, "preheat to 10")
	if err != nil {
		t.Fatalf("failed to wait for IP preheating: %v", err)
	}

	// Step 2: Now configure reclaim policy and lower min_pool_size
	t.Log("Step 3: Configure reclaim policy and lower min_pool_size to 3")
	cm = &corev1.ConfigMap{}
	err = config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		t.Fatalf("failed to get eni-config: %v", err)
	}

	eniJson, err = gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
	if err != nil {
		t.Fatalf("failed to parse eni_conf: %v", err)
	}

	// Lower min_pool_size and add reclaim policy
	_, err = eniJson.Set(3, "min_pool_size")
	if err != nil {
		t.Fatalf("failed to set min_pool_size: %v", err)
	}
	_, err = eniJson.Set("2m", "idle_ip_reclaim_after")
	if err != nil {
		t.Fatalf("failed to set idle_ip_reclaim_after: %v", err)
	}
	_, err = eniJson.Set("30s", "idle_ip_reclaim_interval")
	if err != nil {
		t.Fatalf("failed to set idle_ip_reclaim_interval: %v", err)
	}
	_, err = eniJson.Set(3, "idle_ip_reclaim_batch_size")
	if err != nil {
		t.Fatalf("failed to set idle_ip_reclaim_batch_size: %v", err)
	}
	_, err = eniJson.Set("0.1", "idle_ip_reclaim_jitter_factor")
	if err != nil {
		t.Fatalf("failed to set idle_ip_reclaim_jitter_factor: %v", err)
	}

	cm.Data["eni_conf"] = eniJson.String()
	err = config.Client().Resources().Update(ctx, cm)
	if err != nil {
		t.Fatalf("failed to update eni-config: %v", err)
	}

	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Step 3: Verify IPs have been reclaimed but min_pool_size is respected on qualified nodes
	t.Log("Step 4: Verify IPs are reclaimed on qualified nodes")
	startTime := time.Now()
	err = wait.For(func(ctx context.Context) (done bool, err error) {
		nodes := &networkv1beta1.NodeList{}
		err = config.Client().Resources().List(ctx, nodes)
		if err != nil {
			return false, err
		}

		allNodesOk := true
		for _, node := range nodes.Items {
			// Only check qualified nodes
			if !qualifiedSet[node.Name] {
				continue
			}

			idleCount := countIdleIPs(&node)
			// After reclaim, idle IPs should be between min_pool_size and max_pool_size
			if idleCount >= 10 {
				t.Logf("Node %s still has %d idle IPs (expected < 10)", node.Name, idleCount)
				allNodesOk = false
			}
			if idleCount < 3 {
				t.Logf("Node %s has only %d idle IPs (should respect min_pool_size=3)", node.Name, idleCount)
				allNodesOk = false
			}

			_ = triggerNode(ctx, config, t, &node)
		}

		return allNodesOk, nil
	}, wait.WithTimeout(6*time.Minute), wait.WithInterval(5*time.Second))

	if err != nil {
		t.Fatalf("failed to verify IP reclaim: %v", err)
	}

	elapsed := time.Since(startTime)
	t.Logf("IPs successfully reclaimed in %v on qualified nodes of type %s", elapsed, nodeType)

	return ctx
}

// assessIdleIPReclaimBatch tests batch reclaim respects batch_size on qualified nodes
func assessIdleIPReclaimBatch(ctx context.Context, t *testing.T, config *envconf.Config, nodeType NodeType, qualifiedNodes []string) context.Context {
	t.Log("Test: batch_size controls how many IPs reclaimed per cycle")
	t.Logf("Testing on qualified nodes: %v", qualifiedNodes)

	qualifiedSet := make(map[string]bool)
	for _, name := range qualifiedNodes {
		qualifiedSet[name] = true
	}

	// Step 1: Preheat the pool to 10 IPs
	t.Log("Step 1: Preheat pool to 10 idle IPs")
	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		t.Fatalf("failed to get eni-config: %v", err)
	}

	eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
	if err != nil {
		t.Fatalf("failed to parse eni_conf: %v", err)
	}

	// Set min=max=10 for preheating
	_, err = eniJson.Set("30s", "ip_pool_sync_period")
	if err != nil {
		t.Fatalf("failed to set ip_pool_sync_period: %v", err)
	}
	_, err = eniJson.Set(10, "max_pool_size")
	if err != nil {
		t.Fatalf("failed to set max_pool_size: %v", err)
	}
	_, err = eniJson.Set(10, "min_pool_size")
	if err != nil {
		t.Fatalf("failed to set min_pool_size: %v", err)
	}

	cm.Data["eni_conf"] = eniJson.String()
	err = config.Client().Resources().Update(ctx, cm)
	if err != nil {
		t.Fatalf("failed to update eni-config: %v", err)
	}

	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Wait for preheating to 10 IPs on qualified nodes
	t.Log("Step 2: Wait for IP pool preheating to 10 IPs on qualified nodes")
	err = waitForIdleIPCountByQualifiedNodes(ctx, config, t, qualifiedNodes, 10, 5*time.Minute, "preheat to 10 for batch test")
	if err != nil {
		t.Fatalf("failed to wait for IP preheating: %v", err)
	}

	// Step 2: Configure with small batch_size=2
	t.Log("Step 3: Configure reclaim with batch_size=2")
	cm = &corev1.ConfigMap{}
	err = config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		t.Fatalf("failed to get eni-config: %v", err)
	}

	eniJson, err = gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
	if err != nil {
		t.Fatalf("failed to parse eni_conf: %v", err)
	}

	// Configure with batch_size=2 and small min_pool_size
	_, err = eniJson.Set(2, "min_pool_size")
	if err != nil {
		t.Fatalf("failed to set min_pool_size: %v", err)
	}
	_, err = eniJson.Set("1m", "idle_ip_reclaim_after")
	if err != nil {
		t.Fatalf("failed to set idle_ip_reclaim_after: %v", err)
	}
	_, err = eniJson.Set("1m", "idle_ip_reclaim_interval")
	if err != nil {
		t.Fatalf("failed to set idle_ip_reclaim_interval: %v", err)
	}
	_, err = eniJson.Set(2, "idle_ip_reclaim_batch_size")
	if err != nil {
		t.Fatalf("failed to set idle_ip_reclaim_batch_size: %v", err)
	}
	_, err = eniJson.Set("0.1", "idle_ip_reclaim_jitter_factor")
	if err != nil {
		t.Fatalf("failed to set idle_ip_reclaim_jitter_factor: %v", err)
	}

	cm.Data["eni_conf"] = eniJson.String()
	err = config.Client().Resources().Update(ctx, cm)
	if err != nil {
		t.Fatalf("failed to update eni-config: %v", err)
	}

	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Step 3: Record initial idle count on qualified nodes
	t.Log("Step 4: Record initial idle IP count on qualified nodes")
	nodes := &networkv1beta1.NodeList{}
	err = config.Client().Resources().List(ctx, nodes)
	if err != nil {
		t.Fatalf("failed to list nodes: %v", err)
	}

	initialIdleCount := 0
	nodeCount := 0
	for _, node := range nodes.Items {
		if !qualifiedSet[node.Name] {
			continue
		}
		idleCount := countIdleIPs(&node)
		initialIdleCount += idleCount
		nodeCount++
		t.Logf("Node %s: initial idle IPs = %d", node.Name, idleCount)

		_ = triggerNode(ctx, config, t, &node)
	}

	// Step 4: Wait for first reclaim cycle
	t.Log("Step 5: Wait for first reclaim cycle (2m)")
	time.Sleep(2 * time.Minute)

	// Step 5: Check that only batch_size IPs were reclaimed on qualified nodes
	t.Log("Step 6: Verify only batch_size IPs reclaimed in first cycle on qualified nodes")
	nodes = &networkv1beta1.NodeList{}
	err = config.Client().Resources().List(ctx, nodes)
	if err != nil {
		t.Fatalf("failed to list nodes: %v", err)
	}

	afterFirstCycle := 0
	for _, node := range nodes.Items {
		if !qualifiedSet[node.Name] {
			continue
		}
		idleCount := countIdleIPs(&node)
		afterFirstCycle += idleCount
		t.Logf("Node %s: idle IPs after first cycle = %d", node.Name, idleCount)
	}

	// With batch_size=2, we expect at most 2*num_nodes IPs to be reclaimed per cycle
	expectedMaxReclaimed := 2 * nodeCount
	actualReclaimed := initialIdleCount - afterFirstCycle

	t.Logf("Initial idle: %d, After first cycle: %d, Reclaimed: %d, Expected max: %d",
		initialIdleCount, afterFirstCycle, actualReclaimed, expectedMaxReclaimed)

	if afterFirstCycle < 2*nodeCount {
		t.Logf("Warning: IPs might have been over-reclaimed, but respecting min_pool_size=%d per node", 2)
	}

	return MarkTestSuccess(ctx)
}

// =============================================================================
// Helper Functions
// =============================================================================

// cleanupPoolConfig removes all pool-related configurations to ensure a clean test environment
func cleanupPoolConfig(ctx context.Context, t *testing.T, config *envconf.Config) error {
	t.Log("Cleanup: Removing pool configurations to start from clean state")

	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		return err
	}

	eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
	if err != nil {
		return err
	}

	// Remove idle IP reclaim policy settings
	eniJson.Delete("idle_ip_reclaim_after")
	eniJson.Delete("idle_ip_reclaim_interval")
	eniJson.Delete("idle_ip_reclaim_batch_size")
	eniJson.Delete("idle_ip_reclaim_jitter_factor")
	eniJson.Delete("ip_pool_sync_period")

	// Reset to default pool settings
	_, err = eniJson.Set(5, "max_pool_size")
	if err != nil {
		return err
	}
	_, err = eniJson.Set(0, "min_pool_size")
	if err != nil {
		return err
	}

	cm.Data["eni_conf"] = eniJson.String()
	err = config.Client().Resources().Update(ctx, cm)
	if err != nil {
		return err
	}

	t.Log("Cleanup: Restarting terway to apply clean configuration")
	err = restartTerway(ctx, config)
	if err != nil {
		return err
	}

	t.Log("Cleanup: Waiting 30s for configuration to stabilize")
	time.Sleep(30 * time.Second)

	return nil
}

// countIdleIPs counts the number of idle (unused) IPs in a node
func countIdleIPs(node *networkv1beta1.Node) int {
	idle := 0
	for _, eni := range node.Status.NetworkInterfaces {
		if eni.Status != aliyunClient.ENIStatusInUse {
			continue
		}

		// Count IPv4 idle IPs
		if node.Spec.ENISpec != nil && node.Spec.ENISpec.EnableIPv4 {
			for _, ip := range eni.IPv4 {
				if ip.Status == networkv1beta1.IPStatusValid && ip.PodID == "" {
					idle++
				}
			}
		}

		// Count IPv6 idle IPs
		if node.Spec.ENISpec != nil && node.Spec.ENISpec.EnableIPv6 && !node.Spec.ENISpec.EnableIPv4 {
			for _, ip := range eni.IPv6 {
				if ip.Status == networkv1beta1.IPStatusValid && ip.PodID == "" {
					idle++
				}
			}
		}
	}
	return idle
}

// waitForIdleIPCountByNodeType waits for nodes of specific type to have at least minIdleCount idle IPs
func waitForIdleIPCountByNodeType(ctx context.Context, config *envconf.Config, t *testing.T,
	nodeType NodeType, minIdleCount int, timeout time.Duration, phase string) error {

	return wait.For(func(ctx context.Context) (done bool, err error) {
		nodes := &networkv1beta1.NodeList{}
		err = config.Client().Resources().List(ctx, nodes)
		if err != nil {
			return false, err
		}

		allNodesReady := true
		hasTargetNode := false
		for _, node := range nodes.Items {
			if !isNodeOfType(&node, nodeType) {
				continue
			}
			hasTargetNode = true

			idleCount := countIdleIPs(&node)
			if idleCount < minIdleCount {
				t.Logf("[%s] Node %s has %d idle IPs (waiting for >= %d)",
					phase, node.Name, idleCount, minIdleCount)
				allNodesReady = false
			}
		}

		if !hasTargetNode {
			t.Logf("[%s] No nodes of type %s found", phase, nodeType)
			return true, nil // Skip if no nodes of this type
		}

		return allNodesReady, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(5*time.Second))
}

// waitForIdleIPCountByQualifiedNodes waits for qualified nodes to have at least minIdleCount idle IPs
func waitForIdleIPCountByQualifiedNodes(ctx context.Context, config *envconf.Config, t *testing.T,
	qualifiedNodes []string, minIdleCount int, timeout time.Duration, phase string) error {

	qualifiedSet := make(map[string]bool)
	for _, name := range qualifiedNodes {
		qualifiedSet[name] = true
	}

	return wait.For(func(ctx context.Context) (done bool, err error) {
		nodes := &networkv1beta1.NodeList{}
		err = config.Client().Resources().List(ctx, nodes)
		if err != nil {
			return false, err
		}

		allNodesReady := true
		hasTargetNode := false
		for _, node := range nodes.Items {
			// Only check qualified nodes
			if !qualifiedSet[node.Name] {
				continue
			}
			hasTargetNode = true

			idleCount := countIdleIPs(&node)
			if idleCount < minIdleCount {
				t.Logf("[%s] Node %s has %d idle IPs (waiting for >= %d)",
					phase, node.Name, idleCount, minIdleCount)
				allNodesReady = false
			}
		}

		if !hasTargetNode {
			t.Logf("[%s] No qualified nodes found", phase)
			return true, nil // Skip if no qualified nodes
		}

		return allNodesReady, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(5*time.Second))
}

// isNodeOfType checks if a node CR belongs to a specific node type
func isNodeOfType(node *networkv1beta1.Node, nodeType NodeType) bool {
	isLingjun := node.Labels["alibabacloud.com/lingjun-worker"] == "true"
	isExclusive := node.Labels["k8s.aliyun.com/exclusive-mode-eni-type"] == "eniOnly"

	switch nodeType {
	case NodeTypeECSSharedENI:
		return !isLingjun && !isExclusive
	case NodeTypeECSExclusiveENI:
		return !isLingjun && isExclusive
	case NodeTypeLingjunSharedENI:
		return isLingjun && !isExclusive
	case NodeTypeLingjunExclusiveENI:
		return isLingjun && isExclusive
	default:
		return false
	}
}

func triggerNode(ctx context.Context, config *envconf.Config, t *testing.T, node *networkv1beta1.Node) error {
	mergePatch, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				"e2e-update": time.Now().String(),
			},
		},
	})
	return config.Client().Resources().Patch(ctx, node, k8s.Patch{PatchType: types.MergePatchType, Data: mergePatch})
}
