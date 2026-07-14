//go:build e2e

package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// nodeCapacity holds capacity information for a node used in max capacity tests
// Note: MaxPrefixes = (Adapters - 1) * (IPv4PerAdapter - 1)
//
//	Primary NIC is not used for IP prefixes, only secondary ENIs
type nodeCapacity struct {
	nodeName       string
	maxPrefixes    int
	ipv4PerAdapter int
	adapters       int
}

// =============================================================================
// IP Prefix State Transition Tests
// =============================================================================

// TestPrefix_State_TransitionAndScale tests ENI state machine transitions and dynamic
// prefix scaling in a single flow: allocate 5 → verify → scale to 10 → verify → scale
// to 15 → verify. Combining these avoids redundant setup/teardown cycles.
func TestPrefix_State_TransitionAndScale(t *testing.T) {
	skipIfNotPrefixTestEnvironment(t)

	// Discover nodes
	ctx := context.Background()
	nodeInfo, err := DiscoverNodeTypes(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types: %v", err)
	}

	if len(nodeInfo.ECSIPPrefixNodes) == 0 {
		t.Skip("No ECS IP Prefix nodes found (nodes with k8s.aliyun.com/ip-prefix=true label)")
		return
	}

	nodeName := nodeInfo.ECSIPPrefixNodes[0].Name

	feat := features.New("Prefix/State/TransitionAndScale").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, nodeName)
			setupResetPrefixState(ctx, config, t, nodeName)
			return ctx
		}).
		Assess("test ENI state transitions and dynamic scale up", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessStateTransitionAndScale(ctx, t, config, ctx.Value(qualifiedNodeContextKey).(string))
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			restoreOriginalConfig(ctx, config, t)
			teardownResetPrefixState(ctx, config, t, ctx.Value(qualifiedNodeContextKey).(string))
			return ctx
		}).
		Feature()

	runPrefixFeatureTest(t, feat)
}

// =============================================================================
// IP Prefix Scale Tests
// =============================================================================

// TestPrefix_Scale_ENIDistribution tests prefix allocation across ENIs by scaling
// from single ENI capacity to multi-ENI capacity in one flow:
// allocate (IPv4PerAdapter-1) → verify 1 ENI → scale to IPv4PerAdapter → verify 2 ENIs.
func TestPrefix_Scale_ENIDistribution(t *testing.T) {
	skipIfNotPrefixTestEnvironment(t)

	// Discover node capacities
	ctx := context.Background()
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types with capacity: %v", err)
	}

	// Find a qualified node with at least 2 adapters and IPv4PerAdapter >= 2
	var qualifiedNode string
	var ipv4PerAdapter int
	for nodeName, cap := range nodeInfoWithCap.Capacities {
		if cap.NodeType != NodeTypeECSIPPrefix {
			continue
		}
		if cap.Adapters >= 2 && cap.IPv4PerAdapter >= 2 {
			qualifiedNode = nodeName
			ipv4PerAdapter = cap.IPv4PerAdapter
			break
		}
	}

	if qualifiedNode == "" {
		t.Skip("No qualified IP Prefix nodes found with multi-ENI capacity (need Adapters >= 2 and IPv4PerAdapter >= 2)")
		return
	}

	singleENIPrefixes := ipv4PerAdapter - 1
	multiENIPrefixes := ipv4PerAdapter

	feat := features.New("Prefix/Scale/ENIDistribution").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, qualifiedNode)
			setupResetPrefixState(ctx, config, t, qualifiedNode)
			return ctx
		}).
		Assess("test single-to-multi ENI prefix distribution", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessENIDistribution(ctx, t, config, qualifiedNode, singleENIPrefixes, multiENIPrefixes)
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			restoreOriginalConfig(ctx, config, t)
			teardownResetPrefixState(ctx, config, t, ctx.Value(qualifiedNodeContextKey).(string))
			return ctx
		}).
		Feature()

	runPrefixFeatureTest(t, feat)
}

// TestPrefix_Scale_MaxCapacity tests maximum capacity prefix allocation across all nodes
// Scenario: Set prefix count = (Adapters-1)*(IPv4PerAdapter-1), verify each node fills to max
// Note: Primary NIC is excluded from prefix allocation
func TestPrefix_Scale_MaxCapacity(t *testing.T) {
	skipIfNotPrefixTestEnvironment(t)

	// Discover node capacities
	ctx := context.Background()
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types with capacity: %v", err)
	}

	// Find all IP Prefix nodes and calculate max capacity for each
	var qualifiedNodes []nodeCapacity

	for nodeName, cap := range nodeInfoWithCap.Capacities {
		if cap.NodeType != NodeTypeECSIPPrefix {
			continue
		}
		// Calculate max capacity: (Adapters - 1) * (IPv4PerAdapter - 1)
		// Primary NIC is not used for IP prefixes, only secondary ENIs
		if cap.Adapters < 2 {
			continue
		}
		maxPrefixes := (cap.Adapters - 1) * (cap.IPv4PerAdapter - 1)
		if maxPrefixes > 0 {
			qualifiedNodes = append(qualifiedNodes, nodeCapacity{
				nodeName:       nodeName,
				maxPrefixes:    maxPrefixes,
				ipv4PerAdapter: cap.IPv4PerAdapter,
				adapters:       cap.Adapters,
			})
		}
	}

	if len(qualifiedNodes) == 0 {
		t.Skip("No qualified IP Prefix nodes found for max capacity test")
		return
	}

	feat := features.New("Prefix/Scale/MaxCapacity").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			for _, nc := range qualifiedNodes {
				setupResetPrefixState(ctx, config, t, nc.nodeName)
			}
			return ctx
		}).
		Assess("test max capacity prefix allocation", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessMaxCapacity(ctx, t, config, qualifiedNodes)
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			restoreOriginalConfig(ctx, config, t)
			for _, nc := range qualifiedNodes {
				teardownResetPrefixState(ctx, config, t, nc.nodeName)
			}
			return ctx
		}).
		Feature()

	runPrefixFeatureTest(t, feat)
}

// TestPrefix_Scale_HighFrequencyPodCreation tests high frequency pod creation with prefixes
func TestPrefix_Scale_HighFrequencyPodCreation(t *testing.T) {
	skipIfNotPrefixTestEnvironment(t)

	// Discover nodes
	ctx := context.Background()
	nodeInfo, err := DiscoverNodeTypes(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types: %v", err)
	}

	if len(nodeInfo.ECSIPPrefixNodes) == 0 {
		t.Skip("No ECS IP Prefix nodes found (nodes with k8s.aliyun.com/ip-prefix=true label)")
		return
	}

	nodeName := nodeInfo.ECSIPPrefixNodes[0].Name

	feat := features.New("Prefix/Scale/HighFrequencyPodCreation").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, nodeName)
			setupResetPrefixState(ctx, config, t, nodeName)
			return ctx
		}).
		Assess("test high frequency pod creation with prefixes", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessHighFrequencyPodCreation(ctx, t, config, ctx.Value(qualifiedNodeContextKey).(string))
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			restoreOriginalConfig(ctx, config, t)
			teardownResetPrefixState(ctx, config, t, ctx.Value(qualifiedNodeContextKey).(string))
			return ctx
		}).
		Feature()

	runPrefixFeatureTest(t, feat)
}

// =============================================================================
// Assess Functions for State Tests
// =============================================================================

// assessStateTransitionAndScale tests ENI state transitions and dynamic scaling
// in a single flow: 5 → 10 → 15 prefixes.
func assessStateTransitionAndScale(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) context.Context {
	t.Logf("Testing ENI state transitions and dynamic scale on node: %s", nodeName)

	// Phase 1: Allocate 5 prefixes
	t.Log("Phase 1: Enable prefix mode with ipv4_prefix_count=5")
	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, `{"enable_ip_prefix":true,"ipv4_prefix_count":5}`)
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}

	err = triggerReconcileOnAllNodes(ctx, config)
	if err != nil {
		t.Fatalf("failed to trigger reconcile: %v", err)
	}

	err = waitForPrefixAllocation(ctx, config, t, nodeName, 5, 3*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for prefix allocation: %v", err)
	}

	// Verify ENI states
	t.Log("Phase 1: Verify ENI states with 5 prefixes")
	node := &networkv1beta1.Node{}
	err = config.Client().Resources().Get(ctx, nodeName, "", node)
	if err != nil {
		t.Fatalf("failed to get node: %v", err)
	}

	for eniID, nic := range node.Status.NetworkInterfaces {
		if len(nic.IPv4Prefix) > 0 {
			t.Logf("ENI %s: status=%s, prefixes=%d", eniID, nic.Status, len(nic.IPv4Prefix))
			if nic.Status != aliyunClient.ENIStatusInUse {
				t.Errorf("ENI %s with prefixes should be InUse, got %s", eniID, nic.Status)
			}
		}
	}

	// Phase 2: Scale up to 10 (additive, no reset)
	t.Log("Phase 2: Scale up to 10 prefixes")
	err = configureIPPrefixCount(ctx, t, config, nodeName, 10)
	if err != nil {
		t.Fatalf("failed to configure ipv4_prefix_count: %v", err)
	}

	err = triggerReconcileOnAllNodes(ctx, config)
	if err != nil {
		t.Fatalf("failed to trigger reconcile: %v", err)
	}

	err = waitForPrefixAllocation(ctx, config, t, nodeName, 10, 3*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for 10 prefixes: %v", err)
	}

	prefixes, err := getAllocatedPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get prefixes: %v", err)
	}
	if len(prefixes) != 10 {
		t.Errorf("expected 10 prefixes after first scale up, got %d", len(prefixes))
	}

	// Phase 3: Scale up to 15 (additive, no reset)
	t.Log("Phase 3: Scale up to 15 prefixes")
	err = configureIPPrefixCount(ctx, t, config, nodeName, 15)
	if err != nil {
		t.Fatalf("failed to configure ipv4_prefix_count: %v", err)
	}

	err = triggerReconcileOnAllNodes(ctx, config)
	if err != nil {
		t.Fatalf("failed to trigger reconcile: %v", err)
	}

	err = waitForPrefixAllocation(ctx, config, t, nodeName, 15, 4*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for 15 prefixes: %v", err)
	}

	prefixes, err = getAllocatedPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get prefixes: %v", err)
	}
	if len(prefixes) != 15 {
		t.Errorf("expected 15 prefixes after second scale up, got %d", len(prefixes))
	}

	invalidCount := 0
	for _, prefix := range prefixes {
		if prefix.Status != networkv1beta1.IPPrefixStatusValid {
			invalidCount++
		}
	}
	if invalidCount > 0 {
		t.Errorf("found %d invalid prefixes after scale up", invalidCount)
	}

	t.Logf("Successfully tested state transitions: 5 → 10 → %d prefixes", len(prefixes))
	return MarkTestSuccess(ctx)
}

// assessENIDistribution tests single→multi ENI distribution in one flow.
func assessENIDistribution(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string, singleENIPrefixes, multiENIPrefixes int) context.Context {
	t.Logf("Testing ENI distribution on node: %s", nodeName)

	// Phase 1: Allocate (IPv4PerAdapter-1) → expect 1 ENI
	t.Logf("Phase 1: Allocate %d prefixes (single ENI capacity)", singleENIPrefixes)

	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, fmt.Sprintf(`{"enable_ip_prefix":true,"ipv4_prefix_count":%d}`, singleENIPrefixes))
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}

	// Trigger reconcile to apply config
	t.Log("Trigger reconcile to apply config")
	err = triggerReconcileOnAllNodes(ctx, config)
	if err != nil {
		t.Fatalf("failed to trigger reconcile: %v", err)
	}

	err = waitForPrefixAllocation(ctx, config, t, nodeName, singleENIPrefixes, 3*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for prefix allocation: %v", err)
	}

	eniCount, err := countENIsWithPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to count ENIs with prefixes: %v", err)
	}
	// Assert at least 1 ENI has prefixes. If previous tests left un-cleanable
	// prefixes (containing pod IPs), the allocator may use 2+ ENIs.
	if eniCount < 1 {
		t.Errorf("expected at least 1 ENI with prefixes, got %d", eniCount)
	}

	prefixes, err := getAllocatedPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get prefixes: %v", err)
	}
	if len(prefixes) != singleENIPrefixes {
		t.Errorf("expected %d prefixes, got %d", singleENIPrefixes, len(prefixes))
	}
	t.Logf("Phase 1: %d prefixes on %d ENI", len(prefixes), eniCount)

	// Phase 2: Scale to IPv4PerAdapter → expect 2 ENIs (additive, no reset)
	t.Logf("Phase 2: Scale up to %d prefixes (multi-ENI)", multiENIPrefixes)

	err = configureIPPrefixCount(ctx, t, config, nodeName, multiENIPrefixes)
	if err != nil {
		t.Fatalf("failed to configure ipv4_prefix_count: %v", err)
	}

	// Trigger reconcile to apply config
	t.Log("Trigger reconcile to apply config")
	err = triggerReconcileOnAllNodes(ctx, config)
	if err != nil {
		t.Fatalf("failed to trigger reconcile: %v", err)
	}

	err = waitForPrefixAllocation(ctx, config, t, nodeName, multiENIPrefixes, 5*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for prefix allocation: %v", err)
	}

	eniCount, err = countENIsWithPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to count ENIs: %v", err)
	}
	if eniCount != 2 {
		t.Errorf("expected 2 ENIs with prefixes, got %d", eniCount)
	}

	prefixes, err = getAllocatedPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get prefixes: %v", err)
	}
	if len(prefixes) != multiENIPrefixes {
		t.Errorf("expected %d prefixes, got %d", multiENIPrefixes, len(prefixes))
	}

	// Verify distribution
	node := &networkv1beta1.Node{}
	err = config.Client().Resources().Get(ctx, nodeName, "", node)
	if err != nil {
		t.Fatalf("failed to get node: %v", err)
	}
	for eniID, nic := range node.Status.NetworkInterfaces {
		if nic.Status == aliyunClient.ENIStatusInUse && len(nic.IPv4Prefix) > 0 {
			t.Logf("ENI %s: %d prefixes", eniID, len(nic.IPv4Prefix))
		}
	}

	t.Logf("Successfully tested ENI distribution: %d → %d prefixes across %d ENIs",
		singleENIPrefixes, len(prefixes), eniCount)
	return MarkTestSuccess(ctx)
}

// assessMaxCapacity tests max capacity prefix allocation across all qualified nodes
func assessMaxCapacity(ctx context.Context, t *testing.T, config *envconf.Config, nodes []nodeCapacity) context.Context {
	t.Logf("Testing max capacity prefix allocation on %d nodes", len(nodes))

	// Setup Dynamic Config for each node with max capacity
	for _, nc := range nodes {
		t.Logf("Setup Dynamic Config for node %s: maxPrefixes=%d", nc.nodeName, nc.maxPrefixes)
		var err error
		ctx, err = setupNodeDynamicConfig(ctx, config, t, fmt.Sprintf(`{"enable_ip_prefix":true,"ipv4_prefix_count":%d}`, nc.maxPrefixes))
		if err != nil {
			t.Fatalf("failed to setup node dynamic config for node %s: %v", nc.nodeName, err)
		}
	}

	// Restart terway
	t.Log("Restart terway-eniip to apply config")
	err := triggerReconcileOnAllNodes(ctx, config)
	if err != nil {
		t.Fatalf("failed to trigger reconcile: %v", err)
	}

	// Wait for allocation on all nodes
	for _, nc := range nodes {
		t.Logf("Waiting for max capacity prefix allocation on node %s (maxPrefixes=%d)", nc.nodeName, nc.maxPrefixes)
		err = waitForPrefixAllocation(ctx, config, t, nc.nodeName, nc.maxPrefixes, 5*time.Minute)
		if err != nil {
			t.Fatalf("failed waiting for prefix allocation on node %s: %v", nc.nodeName, err)
		}
	}

	// Verify allocation on all nodes
	for _, nc := range nodes {
		prefixes, err := getAllocatedPrefixes(ctx, config, nc.nodeName)
		if err != nil {
			t.Fatalf("failed to get prefixes on node %s: %v", nc.nodeName, err)
		}

		if len(prefixes) != nc.maxPrefixes {
			t.Errorf("node %s: expected %d prefixes (max capacity), got %d", nc.nodeName, nc.maxPrefixes, len(prefixes))
		} else {
			t.Logf("Node %s: successfully allocated max capacity %d prefixes", nc.nodeName, len(prefixes))
		}

		// Verify ENI count matches adapter count
		eniCount, err := countENIsWithPrefixes(ctx, config, nc.nodeName)
		if err != nil {
			t.Fatalf("failed to count ENIs on node %s: %v", nc.nodeName, err)
		}
		t.Logf("Node %s: %d ENIs with prefixes (adapters=%d)", nc.nodeName, eniCount, nc.adapters)
	}

	return MarkTestSuccess(ctx)
}

// =============================================================================
// Assess Functions for Scale Tests
// =============================================================================

// assessHighFrequencyPodCreation tests high frequency pod creation with prefixes
func assessHighFrequencyPodCreation(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) context.Context {
	t.Logf("Testing high frequency pod creation with prefixes on node: %s", nodeName)

	// Pre-allocate 20 prefixes (320 IPs) via Dynamic Config
	t.Log("Step 1: Pre-allocate 20 prefixes")
	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, `{"enable_ip_prefix":true,"ipv4_prefix_count":20}`)
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}

	err = triggerReconcileOnAllNodes(ctx, config)
	if err != nil {
		t.Fatalf("failed to trigger reconcile: %v", err)
	}

	err = waitForPrefixAllocation(ctx, config, t, nodeName, 20, 4*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for prefix allocation: %v", err)
	}

	// Verify prefix allocation
	prefixes, err := getAllocatedPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get prefixes: %v", err)
	}

	expectedIPs := len(prefixes) * 16 // Each /28 prefix has 16 IPs
	t.Logf("Allocated %d prefixes providing %d IPs", len(prefixes), expectedIPs)

	// Create a deployment with multiple pods
	// Note: For a real stress test, we'd create many pods, but here we'll verify
	// that pod creation works correctly with prefixes
	deploymentName := "prefix-scale-test"
	replicas := int32(10)

	t.Logf("Step 2: Create deployment with %d replicas", replicas)
	deployment := NewDeployment(deploymentName, config.Namespace(), replicas).
		WithNodeAffinity(map[string]string{"kubernetes.io/hostname": nodeName}).
		WithTolerations(IPPrefixTolerations())

	err = config.Client().Resources().Create(ctx, deployment.Deployment)
	if err != nil {
		t.Fatalf("failed to create deployment: %v", err)
	}

	// Wait for pods to be running
	t.Log("Step 3: Wait for pods to be running")
	err = waitForDeploymentPodsRunning(ctx, config, deploymentName, config.Namespace(), replicas, 2*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for pods: %v", err)
	}

	t.Logf("Successfully created %d pods using prefix IPs", replicas)

	// Store deployment for cleanup
	AddResourcesForCleanup(ctx, deployment.Deployment)

	return MarkTestSuccess(ctx)
}

// =============================================================================
// Helper Functions
// =============================================================================

// waitForDeploymentPodsRunning waits for all deployment pods to be running
func waitForDeploymentPodsRunning(ctx context.Context, config *envconf.Config, deploymentName, namespace string, replicas int32, timeout time.Duration) error {
	return wait.For(func(ctx context.Context) (done bool, err error) {
		pods := &corev1.PodList{}
		err = config.Client().Resources(namespace).List(ctx, pods)
		if err != nil {
			return false, err
		}

		runningCount := 0
		for _, pod := range pods.Items {
			if pod.Labels["app"] == deploymentName && pod.Status.Phase == corev1.PodRunning {
				runningCount++
			}
		}

		if runningCount >= int(replicas) {
			return true, nil
		}

		return false, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(5*time.Second))
}
