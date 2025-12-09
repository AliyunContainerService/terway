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
// Shared ENI IP Warm-Up Tests
// =============================================================================

// MinIPsForWarmUpTest is the minimum number of IPs required for warm-up testing
// The test configures warm_up_size=10, so nodes must have capacity for at least 10 IPs
const MinIPsForWarmUpTest = 10

// TestSharedENI_WarmUp tests the IP warm-up feature
// for shared ENI mode on both ECS and Lingjun nodes.
// This test only covers centralized IPAM mode (ipam_type == "crd")
func TestSharedENI_WarmUp(t *testing.T) {
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
	if GetCachedTerwayDaemonSetName() != "terway-eniip" {
		t.Skipf("TestSharedENI_WarmUp requires terway-eniip daemonset, current: %s", GetCachedTerwayDaemonSetName())
		return
	}

	// Pre-check: terway version must be >= v1.16.4
	if !RequireTerwayVersion("v1.16.4") {
		t.Skipf("TestSharedENI_WarmUp requires terway version >= v1.16.4, current version: %s", GetCachedTerwayVersion())
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
		qualifiedNodes := getQualifiedNodesForWarmUp(nodeInfoWithCap, nodeType)
		if len(qualifiedNodes) == 0 {
			t.Logf("Skipping %s: no nodes meet capacity requirements (need %d adapters, IP capacity >= %d)",
				nodeType, MinAdaptersForSharedENI, MinIPsForWarmUpTest)
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
		feat := createWarmUpTestWithQualifiedNodes(nodeType, qualifiedNodes)
		feats = append(feats, feat)
	}

	if len(feats) == 0 {
		t.Skip("No qualified nodes found for warm-up testing")
		return
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// getQualifiedNodesForWarmUp returns nodes that meet capacity requirements for warm-up test
func getQualifiedNodesForWarmUp(nodeInfo *NodeTypeInfoWithCapacity, nodeType NodeType) []string {
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
		if totalIPCapacity >= MinIPsForWarmUpTest {
			qualified = append(qualified, nodeName)
		}
	}

	return qualified
}

// createWarmUpTestWithQualifiedNodes creates IP warm-up tests with qualified node filtering
func createWarmUpTestWithQualifiedNodes(nodeType NodeType, qualifiedNodes []string) features.Feature {
	eniMode := getENIModeFromNodeType(nodeType)
	machineType := getMachineTypeFromNodeType(nodeType)

	return features.New(fmt.Sprintf("%sENI/WarmUp/%s", eniMode, machineType)).
		WithLabel("eni-mode", eniMode).
		WithLabel("machine-type", machineType).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			t.Logf("Testing with qualified nodes: %v", qualifiedNodes)
			// Store qualified nodes in context for later use
			ctx = context.WithValue(ctx, warmUpQualifiedNodesContextKey, qualifiedNodes)
			return ctx
		}).
		Assess("basic warm-up functionality", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			qualifiedNodes := ctx.Value(warmUpQualifiedNodesContextKey).([]string)
			return assessWarmUpBasic(ctx, t, config, nodeType, qualifiedNodes)
		}).
		Assess("warm-up larger than max_pool_size", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			qualifiedNodes := ctx.Value(warmUpQualifiedNodesContextKey).([]string)
			return assessWarmUpLargerThanMaxPoolSize(ctx, t, config, nodeType, qualifiedNodes)
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Restore default configuration
			err := restoreDefaultWarmUpConfig(ctx, t, config)
			if err != nil {
				t.Logf("Warning: failed to restore warm-up configuration in teardown: %v", err)
			}
			return ctx
		}).
		Feature()
}

// warmUpQualifiedNodesContextKey is the context key for storing qualified node names
type warmUpQualifiedNodesContextKeyType struct{}

var warmUpQualifiedNodesContextKey = warmUpQualifiedNodesContextKeyType{}

// =============================================================================
// Assess Functions for Qualified Nodes
// =============================================================================

// assessWarmUpBasic tests basic warm-up functionality on qualified nodes
func assessWarmUpBasic(ctx context.Context, t *testing.T, config *envconf.Config, nodeType NodeType, qualifiedNodes []string) context.Context {
	t.Log("Verify basic warm-up functionality")
	t.Logf("Testing on qualified nodes: %v", qualifiedNodes)

	qualifiedSet := make(map[string]bool)
	for _, name := range qualifiedNodes {
		qualifiedSet[name] = true
	}

	// Step 1: Pause warm-up controller first
	t.Log("Step 1: Pause warm-up by setting warmUpCompleted=true on qualified nodes")
	err := pauseWarmUpByQualifiedNodes(ctx, t, config, qualifiedNodes)
	if err != nil {
		t.Fatalf("failed to pause warm-up: %v", err)
	}

	// Step 2: Clear IP pool on qualified nodes
	t.Log("Step 2: Clear IP pool to start from clean state on qualified nodes")
	err = clearIPPoolByQualifiedNodes(ctx, t, config, qualifiedNodes)
	if err != nil {
		t.Fatalf("failed to clear IP pool: %v", err)
	}

	// Step 3: Configure warm-up
	t.Log("Step 3: Configure warm-up with ip_warm_up_size=10, max_pool_size=20, min_pool_size=1")
	err = configureWarmUp(ctx, t, config, 10, 20, 1)
	if err != nil {
		t.Fatalf("failed to configure warm-up: %v", err)
	}

	// Step 4: Restart terway-eniip to apply new config
	t.Log("Step 4: Restart terway-eniip to apply new config")
	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Step 5: Clear warm-up state to trigger warm-up on qualified nodes
	t.Log("Step 5: Clear warm-up state to trigger warm-up on qualified nodes")
	err = clearWarmUpStateByQualifiedNodes(ctx, t, config, qualifiedNodes)
	if err != nil {
		t.Fatalf("failed to clear warm-up state: %v", err)
	}

	// Step 6: Wait for warm-up completion on qualified nodes
	t.Log("Step 6: Wait for warm-up completion on qualified nodes")
	err = waitForWarmUpCompletionByQualifiedNodes(ctx, t, config, qualifiedNodes, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("failed to wait for warm-up completion: %v", err)
	}

	// Step 7: Verify warm-up status on qualified nodes
	t.Log("Step 7: Verify warm-up status on qualified nodes")
	err = verifyWarmUpStatusByQualifiedNodes(ctx, t, config, qualifiedNodes, 10)
	if err != nil {
		t.Fatalf("failed to verify warm-up status: %v", err)
	}

	// Step 8: Verify idle IP count on qualified nodes
	t.Log("Step 8: Verify idle IP count >= warm-up target on qualified nodes")
	nodes := &networkv1beta1.NodeList{}
	err = config.Client().Resources().List(ctx, nodes)
	if err != nil {
		t.Fatalf("failed to list nodes: %v", err)
	}

	for _, node := range nodes.Items {
		if !qualifiedSet[node.Name] {
			continue
		}
		idleCount := countTotalIPs(&node)
		t.Logf("Node %s: total IPs = %d (expected >= 10)", node.Name, idleCount)
		if idleCount < 10 {
			t.Errorf("Node %s: expected total IPs >= 10, got %d", node.Name, idleCount)
		}
	}

	t.Logf("Basic warm-up functionality verified on qualified nodes of type %s", nodeType)
	return ctx
}

// assessWarmUpLargerThanMaxPoolSize tests warm-up larger than max_pool_size on qualified nodes
func assessWarmUpLargerThanMaxPoolSize(ctx context.Context, t *testing.T, config *envconf.Config, nodeType NodeType, qualifiedNodes []string) context.Context {
	t.Log("Verify warm-up larger than max_pool_size")
	t.Logf("Testing on qualified nodes: %v", qualifiedNodes)

	qualifiedSet := make(map[string]bool)
	for _, name := range qualifiedNodes {
		qualifiedSet[name] = true
	}

	// Step 1: Pause warm-up controller first
	t.Log("Step 1: Pause warm-up by setting warmUpCompleted=true on qualified nodes")
	err := pauseWarmUpByQualifiedNodes(ctx, t, config, qualifiedNodes)
	if err != nil {
		t.Fatalf("failed to pause warm-up: %v", err)
	}

	// Step 2: Clear IP pool on qualified nodes
	t.Log("Step 2: Clear IP pool to start from clean state on qualified nodes")
	err = clearIPPoolByQualifiedNodes(ctx, t, config, qualifiedNodes)
	if err != nil {
		t.Fatalf("failed to clear IP pool: %v", err)
	}

	// Step 3: Configure warm-up with warm_up_size > max_pool_size
	t.Log("Step 3: Configure warm-up with ip_warm_up_size=10, max_pool_size=5, min_pool_size=1")
	err = configureWarmUp(ctx, t, config, 10, 5, 1)
	if err != nil {
		t.Fatalf("failed to configure warm-up: %v", err)
	}

	// Step 4: Restart terway-eniip to apply new config
	t.Log("Step 4: Restart terway-eniip to apply new config")
	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Step 5: Clear warm-up state to trigger warm-up on qualified nodes
	t.Log("Step 5: Clear warm-up state to trigger warm-up on qualified nodes")
	err = clearWarmUpStateByQualifiedNodes(ctx, t, config, qualifiedNodes)
	if err != nil {
		t.Fatalf("failed to clear warm-up state: %v", err)
	}

	// Step 6: Wait for warm-up completion on qualified nodes
	t.Log("Step 6: Wait for warm-up completion on qualified nodes")
	err = waitForWarmUpCompletionByQualifiedNodes(ctx, t, config, qualifiedNodes, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("failed to wait for warm-up completion: %v", err)
	}

	// Step 7: Verify warm-up allocated count >= warm_up_size on qualified nodes
	t.Log("Step 7: Verify warm-up allocated count >= warm_up_size on qualified nodes")
	nodes := &networkv1beta1.NodeList{}
	err = config.Client().Resources().List(ctx, nodes)
	if err != nil {
		t.Fatalf("failed to list nodes: %v", err)
	}

	for _, node := range nodes.Items {
		if !qualifiedSet[node.Name] {
			continue
		}
		t.Logf("Node %s: warmUpTarget=%d, warmUpAllocatedCount=%d, warmUpCompleted=%v",
			node.Name, node.Status.WarmUpTarget, node.Status.WarmUpAllocatedCount, node.Status.WarmUpCompleted)

		if node.Status.WarmUpAllocatedCount < 10 {
			t.Errorf("Node %s: expected warmUpAllocatedCount >= 10, got %d", node.Name, node.Status.WarmUpAllocatedCount)
		}
	}

	t.Logf("Warm-up larger than max_pool_size verified on qualified nodes of type %s", nodeType)
	return MarkTestSuccess(ctx)
}

// restoreDefaultWarmUpConfig restores default warm-up configuration
func restoreDefaultWarmUpConfig(ctx context.Context, t *testing.T, config *envconf.Config) error {
	t.Log("Restoring default configuration")

	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		return err
	}

	eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
	if err != nil {
		return err
	}

	// Remove warm-up configuration
	eniJson.Delete("ip_warm_up_size")

	// Restore default pool settings
	_, err = eniJson.Set(5, "max_pool_size")
	if err != nil {
		return err
	}
	_, err = eniJson.Set(0, "min_pool_size")
	if err != nil {
		return err
	}
	eniJson.Delete("ip_pool_sync_period")

	cm.Data["eni_conf"] = eniJson.String()
	return config.Client().Resources().Update(ctx, cm)
}

// =============================================================================
// Helper Functions
// =============================================================================

// pauseWarmUpByQualifiedNodes sets warmUpCompleted=true on qualified nodes
func pauseWarmUpByQualifiedNodes(ctx context.Context, t *testing.T, config *envconf.Config, qualifiedNodes []string) error {
	qualifiedSet := make(map[string]bool)
	for _, name := range qualifiedNodes {
		qualifiedSet[name] = true
	}

	t.Logf("Pausing warm-up by setting warmUpCompleted=true on qualified nodes: %v", qualifiedNodes)

	nodes := &networkv1beta1.NodeList{}
	err := config.Client().Resources().List(ctx, nodes)
	if err != nil {
		return err
	}

	for _, node := range nodes.Items {
		if !qualifiedSet[node.Name] {
			continue
		}

		mergePatch, _ := json.Marshal(map[string]interface{}{
			"status": map[string]interface{}{
				"warmUpCompleted": true,
			},
		})

		err = config.Client().Resources().Patch(ctx, &node, k8s.Patch{
			PatchType: types.MergePatchType,
			Data:      mergePatch,
		})
		if err != nil {
			t.Logf("Warning: failed to pause warm-up for node %s: %v", node.Name, err)
		} else {
			t.Logf("Paused warm-up for node %s", node.Name)
		}
	}

	return nil
}

// clearWarmUpStateByQualifiedNodes clears warm-up status from qualified nodes
func clearWarmUpStateByQualifiedNodes(ctx context.Context, t *testing.T, config *envconf.Config, qualifiedNodes []string) error {
	qualifiedSet := make(map[string]bool)
	for _, name := range qualifiedNodes {
		qualifiedSet[name] = true
	}

	t.Logf("Clearing warm-up state from qualified nodes to trigger warm-up")

	nodes := &networkv1beta1.NodeList{}
	err := config.Client().Resources().List(ctx, nodes)
	if err != nil {
		return err
	}

	for _, node := range nodes.Items {
		if !qualifiedSet[node.Name] {
			continue
		}

		mergePatch, _ := json.Marshal(map[string]interface{}{
			"status": map[string]interface{}{
				"warmUpTarget":         0,
				"warmUpAllocatedCount": 0,
				"warmUpCompleted":      false,
			},
		})

		err = config.Client().Resources().Patch(ctx, &node, k8s.Patch{
			PatchType: types.MergePatchType,
			Data:      mergePatch,
		})
		if err != nil {
			t.Logf("Warning: failed to clear warm-up state for node %s: %v", node.Name, err)
		} else {
			t.Logf("Cleared warm-up state for node %s", node.Name)
		}
	}

	return nil
}

// clearIPPoolByQualifiedNodes clears IP pool from qualified nodes
func clearIPPoolByQualifiedNodes(ctx context.Context, t *testing.T, config *envconf.Config, qualifiedNodes []string) error {
	qualifiedSet := make(map[string]bool)
	for _, name := range qualifiedNodes {
		qualifiedSet[name] = true
	}

	t.Logf("Clearing IP pool from qualified nodes: %v", qualifiedNodes)

	nodes := &networkv1beta1.NodeList{}
	err := config.Client().Resources().List(ctx, nodes)
	if err != nil {
		return err
	}

	for _, node := range nodes.Items {
		if !qualifiedSet[node.Name] {
			continue
		}

		t.Logf("Clearing IP pool for node %s", node.Name)

		// Update node CR to mark all secondary IPs as deleting
		for _, nic := range node.Status.NetworkInterfaces {
			if nic.NetworkInterfaceType != networkv1beta1.ENITypeSecondary {
				continue
			}
			for ipAddr, ip := range nic.IPv4 {
				if ip.Primary {
					continue
				}
				ip.Status = networkv1beta1.IPStatusDeleting
				nic.IPv4[ipAddr] = ip
			}
		}

		err = config.Client().Resources().Update(ctx, &node)
		if err != nil {
			t.Logf("Warning: failed to clear IP pool for node %s: %v", node.Name, err)
		}
	}

	// Wait for IPs to be released
	time.Sleep(30 * time.Second)

	return nil
}

// waitForWarmUpCompletionByQualifiedNodes waits for warm-up to complete on qualified nodes
func waitForWarmUpCompletionByQualifiedNodes(ctx context.Context, t *testing.T, config *envconf.Config,
	qualifiedNodes []string, expectedTarget int, timeout time.Duration) error {

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

		allCompleted := true
		for _, node := range nodes.Items {
			if !qualifiedSet[node.Name] {
				continue
			}

			if !node.Status.WarmUpCompleted {
				t.Logf("Node %s: waiting for warm-up completion (target=%d, allocated=%d)",
					node.Name, node.Status.WarmUpTarget, node.Status.WarmUpAllocatedCount)
				allCompleted = false
			}

			_ = triggerNodeCR(ctx, config, t, &node)
		}

		return allCompleted, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(5*time.Second))
}

// verifyWarmUpStatusByQualifiedNodes verifies warm-up status on qualified nodes
func verifyWarmUpStatusByQualifiedNodes(ctx context.Context, t *testing.T, config *envconf.Config,
	qualifiedNodes []string, expectedTarget int) error {

	qualifiedSet := make(map[string]bool)
	for _, name := range qualifiedNodes {
		qualifiedSet[name] = true
	}

	nodes := &networkv1beta1.NodeList{}
	err := config.Client().Resources().List(ctx, nodes)
	if err != nil {
		return err
	}

	for _, node := range nodes.Items {
		if !qualifiedSet[node.Name] {
			continue
		}

		if node.Status.WarmUpTarget != expectedTarget {
			return fmt.Errorf("node %s: expected warmUpTarget=%d, got %d",
				node.Name, expectedTarget, node.Status.WarmUpTarget)
		}

		if node.Status.WarmUpAllocatedCount < expectedTarget {
			return fmt.Errorf("node %s: expected warmUpAllocatedCount>=%d, got %d",
				node.Name, expectedTarget, node.Status.WarmUpAllocatedCount)
		}

		if !node.Status.WarmUpCompleted {
			return fmt.Errorf("node %s: expected warmUpCompleted=true, got false", node.Name)
		}

		t.Logf("Node %s: warm-up verified (target=%d, allocated=%d, completed=%v)",
			node.Name, node.Status.WarmUpTarget, node.Status.WarmUpAllocatedCount, node.Status.WarmUpCompleted)
	}

	return nil
}

// pauseWarmUpByNodeType sets warmUpCompleted=true on Node CRs of specific type
func pauseWarmUpByNodeType(ctx context.Context, t *testing.T, config *envconf.Config, nodeType NodeType) error {
	t.Logf("Pausing warm-up by setting warmUpCompleted=true on nodes of type %s", nodeType)

	nodes := &networkv1beta1.NodeList{}
	err := config.Client().Resources().List(ctx, nodes)
	if err != nil {
		return err
	}

	for _, node := range nodes.Items {
		if !isNodeOfType(&node, nodeType) {
			continue
		}

		mergePatch, _ := json.Marshal(map[string]interface{}{
			"status": map[string]interface{}{
				"warmUpCompleted": true,
			},
		})

		err = config.Client().Resources().Patch(ctx, &node, k8s.Patch{
			PatchType: types.MergePatchType,
			Data:      mergePatch,
		})
		if err != nil {
			t.Logf("Warning: failed to pause warm-up for node %s: %v", node.Name, err)
		} else {
			t.Logf("Paused warm-up for node %s", node.Name)
		}
	}

	return nil
}

// clearWarmUpStateByNodeType clears warm-up status from Node CRs of specific type
func clearWarmUpStateByNodeType(ctx context.Context, t *testing.T, config *envconf.Config, nodeType NodeType) error {
	t.Logf("Clearing warm-up state from nodes of type %s to trigger warm-up", nodeType)

	nodes := &networkv1beta1.NodeList{}
	err := config.Client().Resources().List(ctx, nodes)
	if err != nil {
		return err
	}

	for _, node := range nodes.Items {
		if !isNodeOfType(&node, nodeType) {
			continue
		}

		mergePatch, _ := json.Marshal(map[string]interface{}{
			"status": map[string]interface{}{
				"warmUpTarget":         0,
				"warmUpAllocatedCount": 0,
				"warmUpCompleted":      false,
			},
		})

		err = config.Client().Resources().PatchStatus(ctx, &node, k8s.Patch{
			PatchType: types.MergePatchType,
			Data:      mergePatch,
		})
		if err != nil {
			t.Logf("Warning: failed to clear warm-up state for node %s: %v", node.Name, err)
		} else {
			t.Logf("Cleared warm-up state for node %s", node.Name)
		}
	}

	return nil
}

// isIPPoolReleasedForNode checks if the IP pool has been fully released on a node
func isIPPoolReleasedForNode(node *networkv1beta1.Node) bool {
	for _, eni := range node.Status.NetworkInterfaces {
		if eni.Status != aliyunClient.ENIStatusInUse {
			continue
		}

		if node.Spec.ENISpec != nil && node.Spec.ENISpec.EnableIPv4 {
			for _, ip := range eni.IPv4 {
				if ip.Primary {
					continue
				}
				if ip.Status == networkv1beta1.IPStatusValid && ip.PodID == "" {
					return false
				}
			}
		}

		if node.Spec.ENISpec != nil && node.Spec.ENISpec.EnableIPv6 && !node.Spec.ENISpec.EnableIPv4 {
			for _, ip := range eni.IPv6 {
				if ip.Status == networkv1beta1.IPStatusValid && ip.PodID == "" {
					return false
				}
			}
		}
	}
	return true
}

// clearIPPoolByNodeType releases all idle IPs on nodes of specific type
func clearIPPoolByNodeType(ctx context.Context, t *testing.T, config *envconf.Config, nodeType NodeType) error {
	t.Logf("Clearing IP pool for nodes of type %s", nodeType)

	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		return err
	}

	eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
	if err != nil {
		return err
	}

	eniJson.Delete("ip_warm_up_size")
	_, err = eniJson.Set(0, "max_pool_size")
	if err != nil {
		return err
	}
	_, err = eniJson.Set(0, "min_pool_size")
	if err != nil {
		return err
	}
	_, err = eniJson.Set("30s", "ip_pool_sync_period")
	if err != nil {
		return err
	}

	cm.Data["eni_conf"] = eniJson.String()
	err = config.Client().Resources().Update(ctx, cm)
	if err != nil {
		return err
	}

	err = restartTerway(ctx, config)
	if err != nil {
		return err
	}

	t.Log("Waiting for IP pool to be released (primary IPs will be retained)")
	err = wait.For(func(ctx context.Context) (done bool, err error) {
		nodes := &networkv1beta1.NodeList{}
		err = config.Client().Resources().List(ctx, nodes)
		if err != nil {
			return false, err
		}

		for _, node := range nodes.Items {
			if !isNodeOfType(&node, nodeType) {
				continue
			}
			if !isIPPoolReleasedForNode(&node) {
				t.Logf("Node %s IP pool not fully released yet, triggering update", node.Name)
				_ = triggerNodeCR(ctx, config, t, &node)
				return false, nil
			}
		}
		return true, nil
	}, wait.WithTimeout(5*time.Minute), wait.WithInterval(5*time.Second))

	return err
}

// configureWarmUp configures warm-up settings in eni-config
func configureWarmUp(ctx context.Context, t *testing.T, config *envconf.Config, warmUpSize, maxPoolSize, minPoolSize int) error {
	t.Logf("Configuring warm-up: ip_warm_up_size=%d, max_pool_size=%d, min_pool_size=%d",
		warmUpSize, maxPoolSize, minPoolSize)

	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		return err
	}

	eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
	if err != nil {
		return err
	}

	_, err = eniJson.Set(warmUpSize, "ip_warm_up_size")
	if err != nil {
		return err
	}
	_, err = eniJson.Set(maxPoolSize, "max_pool_size")
	if err != nil {
		return err
	}
	_, err = eniJson.Set(minPoolSize, "min_pool_size")
	if err != nil {
		return err
	}
	_, err = eniJson.Set("30s", "ip_pool_sync_period")
	if err != nil {
		return err
	}

	cm.Data["eni_conf"] = eniJson.String()
	return config.Client().Resources().Update(ctx, cm)
}

// waitForWarmUpCompletionByNodeType waits for warm-up to complete on nodes of specific type
func waitForWarmUpCompletionByNodeType(ctx context.Context, t *testing.T, config *envconf.Config,
	nodeType NodeType, expectedTarget int, timeout time.Duration) error {

	return wait.For(func(ctx context.Context) (done bool, err error) {
		nodes := &networkv1beta1.NodeList{}
		err = config.Client().Resources().List(ctx, nodes)
		if err != nil {
			return false, err
		}

		allCompleted := true
		hasTargetNode := false
		for _, node := range nodes.Items {
			if !isNodeOfType(&node, nodeType) {
				continue
			}
			hasTargetNode = true

			t.Logf("Node %s: warmUpTarget=%d, warmUpAllocatedCount=%d, warmUpCompleted=%v",
				node.Name, node.Status.WarmUpTarget, node.Status.WarmUpAllocatedCount, node.Status.WarmUpCompleted)

			if !node.Status.WarmUpCompleted {
				allCompleted = false
			}
		}

		if !hasTargetNode {
			return true, nil // Skip if no nodes of this type
		}

		return allCompleted, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(5*time.Second))
}

// verifyWarmUpStatusByNodeType verifies warm-up status fields on nodes of specific type
func verifyWarmUpStatusByNodeType(ctx context.Context, t *testing.T, config *envconf.Config,
	nodeType NodeType, expectedTarget int) error {

	nodes := &networkv1beta1.NodeList{}
	err := config.Client().Resources().List(ctx, nodes)
	if err != nil {
		return err
	}

	for _, node := range nodes.Items {
		if !isNodeOfType(&node, nodeType) {
			continue
		}

		t.Logf("Verifying node %s: warmUpTarget=%d, warmUpAllocatedCount=%d, warmUpCompleted=%v",
			node.Name, node.Status.WarmUpTarget, node.Status.WarmUpAllocatedCount, node.Status.WarmUpCompleted)

		if node.Status.WarmUpTarget != expectedTarget {
			t.Errorf("Node %s: expected warmUpTarget=%d, got %d",
				node.Name, expectedTarget, node.Status.WarmUpTarget)
		}

		if !node.Status.WarmUpCompleted {
			t.Errorf("Node %s: expected warmUpCompleted=true, got false", node.Name)
		}

		if node.Status.WarmUpAllocatedCount < expectedTarget {
			t.Errorf("Node %s: expected warmUpAllocatedCount >= %d, got %d",
				node.Name, expectedTarget, node.Status.WarmUpAllocatedCount)
		}

		idleCount := countIdleIPs(&node)
		t.Logf("Node %s: idle IPs = %d", node.Name, idleCount)
	}

	return nil
}

// triggerNodeCR triggers a node CR update to force reconciliation
func triggerNodeCR(ctx context.Context, config *envconf.Config, t *testing.T, node *networkv1beta1.Node) error {
	mergePatch, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				"e2e-update": time.Now().String(),
			},
		},
	})
	return config.Client().Resources().Patch(ctx, node, k8s.Patch{PatchType: types.MergePatchType, Data: mergePatch})
}

// countTotalIPs counts the total number of valid IPs (both idle and in-use) in a node
func countTotalIPs(node *networkv1beta1.Node) int {
	total := 0
	for _, eni := range node.Status.NetworkInterfaces {
		if eni.Status != aliyunClient.ENIStatusInUse {
			continue
		}

		if node.Spec.ENISpec != nil && node.Spec.ENISpec.EnableIPv4 {
			for _, ip := range eni.IPv4 {
				if ip.Status == networkv1beta1.IPStatusValid && !ip.Primary {
					total++
				}
			}
		}

		if node.Spec.ENISpec != nil && node.Spec.ENISpec.EnableIPv6 && !node.Spec.ENISpec.EnableIPv4 {
			for _, ip := range eni.IPv6 {
				if ip.Status == networkv1beta1.IPStatusValid {
					total++
				}
			}
		}
	}
	return total
}
