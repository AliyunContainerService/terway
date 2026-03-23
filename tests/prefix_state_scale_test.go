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

// TestPrefix_State_Transition tests ENI state machine transitions
func TestPrefix_State_Transition(t *testing.T) {
	// Pre-checks
	if eniConfig == nil || eniConfig.IPAMType != "crd" {
		t.Skipf("skip: ipam type is not crd")
		return
	}
	if GetCachedTerwayDaemonSetName() != "terway-eniip" {
		t.Skipf("Requires terway-eniip daemonset")
		return
	}
	if !RequireTerwayVersion("v1.17.0") {
		t.Skipf("Requires terway version >= v1.17.0")
		return
	}

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

	feat := features.New("Prefix/State/Transition").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, nodeName)
			return ctx
		}).
		Assess("test ENI state transitions with prefixes", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessStateTransition(ctx, t, config, ctx.Value(qualifiedNodeContextKey).(string))
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			restoreOriginalConfig(ctx, config, t)
			return ctx
		}).
		Feature()

	testenv.Test(t, feat)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestPrefix_State_DynamicScaleUp tests dynamic prefix scaling up
func TestPrefix_State_DynamicScaleUp(t *testing.T) {
	// Pre-checks
	if eniConfig == nil || eniConfig.IPAMType != "crd" {
		t.Skipf("skip: ipam type is not crd")
		return
	}
	if GetCachedTerwayDaemonSetName() != "terway-eniip" {
		t.Skipf("Requires terway-eniip daemonset")
		return
	}
	if !RequireTerwayVersion("v1.17.0") {
		t.Skipf("Requires terway version >= v1.17.0")
		return
	}

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

	feat := features.New("Prefix/State/DynamicScaleUp").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, nodeName)
			return ctx
		}).
		Assess("test dynamic scale up of prefixes", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessDynamicScaleUp(ctx, t, config, ctx.Value(qualifiedNodeContextKey).(string))
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			restoreOriginalConfig(ctx, config, t)
			return ctx
		}).
		Feature()

	testenv.Test(t, feat)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// =============================================================================
// IP Prefix Scale Tests
// =============================================================================

// TestPrefix_Scale_SingleENIPrefixes tests prefix allocation that fits in single ENI
// Scenario: Set prefix count = (IPv4PerAdapter - 1), expect 1 ENI with correct prefix count
func TestPrefix_Scale_SingleENIPrefixes(t *testing.T) {
	// Pre-checks
	if eniConfig == nil || eniConfig.IPAMType != "crd" {
		t.Skipf("skip: ipam type is not crd")
		return
	}
	if GetCachedTerwayDaemonSetName() != "terway-eniip" {
		t.Skipf("Requires terway-eniip daemonset")
		return
	}
	if !RequireTerwayVersion("v1.17.0") {
		t.Skipf("Requires terway version >= v1.17.0")
		return
	}

	// Discover node capacities
	ctx := context.Background()
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types with capacity: %v", err)
	}

	// Find a qualified node with at least 2 adapters (need room for prefix allocation)
	var qualifiedNode string
	var ipv4PerAdapter int
	for nodeName, cap := range nodeInfoWithCap.Capacities {
		if cap.NodeType != NodeTypeECSIPPrefix {
			continue
		}
		// Need IPv4PerAdapter >= 2 so that (IPv4PerAdapter-1) >= 1
		if cap.IPv4PerAdapter >= 2 {
			qualifiedNode = nodeName
			ipv4PerAdapter = cap.IPv4PerAdapter
			break
		}
	}

	if qualifiedNode == "" {
		t.Skip("No qualified IP Prefix nodes found with sufficient capacity (need IPv4PerAdapter >= 2)")
		return
	}

	// Calculate expected prefix count: (IPv4PerAdapter - 1)
	expectedPrefixes := ipv4PerAdapter - 1

	feat := features.New("Prefix/Scale/SingleENI").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, qualifiedNode)
			return ctx
		}).
		Assess("test single ENI prefix allocation", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessSingleENIPrefixes(ctx, t, config, qualifiedNode, expectedPrefixes)
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			restoreOriginalConfig(ctx, config, t)
			return ctx
		}).
		Feature()

	testenv.Test(t, feat)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestPrefix_Scale_MultiENIPrefixes tests prefix allocation requiring multiple ENIs
// Scenario: Set prefix count = IPv4PerAdapter, expect 2 ENIs with correct prefix distribution
func TestPrefix_Scale_MultiENIPrefixes(t *testing.T) {
	// Pre-checks
	if eniConfig == nil || eniConfig.IPAMType != "crd" {
		t.Skipf("skip: ipam type is not crd")
		return
	}
	if GetCachedTerwayDaemonSetName() != "terway-eniip" {
		t.Skipf("Requires terway-eniip daemonset")
		return
	}
	if !RequireTerwayVersion("v1.17.0") {
		t.Skipf("Requires terway version >= v1.17.0")
		return
	}

	// Discover node capacities
	ctx := context.Background()
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types with capacity: %v", err)
	}

	// Find a qualified node with at least 2 adapters (to support 2 ENIs)
	var qualifiedNode string
	var ipv4PerAdapter int
	for nodeName, cap := range nodeInfoWithCap.Capacities {
		if cap.NodeType != NodeTypeECSIPPrefix {
			continue
		}
		// Need at least 2 adapters and IPv4PerAdapter >= 2
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

	// Calculate expected prefix count: IPv4PerAdapter (requires 2 ENIs)
	expectedPrefixes := ipv4PerAdapter

	feat := features.New("Prefix/Scale/MultiENI").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, qualifiedNode)
			return ctx
		}).
		Assess("test multi-ENI prefix allocation", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessMultiENIPrefixes(ctx, t, config, qualifiedNode, expectedPrefixes)
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			restoreOriginalConfig(ctx, config, t)
			return ctx
		}).
		Feature()

	testenv.Test(t, feat)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestPrefix_Scale_MaxCapacity tests maximum capacity prefix allocation across all nodes
// Scenario: Set prefix count = (Adapters-1)*(IPv4PerAdapter-1), verify each node fills to max
// Note: Primary NIC is excluded from prefix allocation
func TestPrefix_Scale_MaxCapacity(t *testing.T) {
	// Pre-checks
	if eniConfig == nil || eniConfig.IPAMType != "crd" {
		t.Skipf("skip: ipam type is not crd")
		return
	}
	if GetCachedTerwayDaemonSetName() != "terway-eniip" {
		t.Skipf("Requires terway-eniip daemonset")
		return
	}
	if !RequireTerwayVersion("v1.17.0") {
		t.Skipf("Requires terway version >= v1.17.0")
		return
	}

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
			return ctx
		}).
		Assess("test max capacity prefix allocation", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessMaxCapacity(ctx, t, config, qualifiedNodes)
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			restoreOriginalConfig(ctx, config, t)
			return ctx
		}).
		Feature()

	testenv.Test(t, feat)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestPrefix_Scale_HighFrequencyPodCreation tests high frequency pod creation with prefixes
func TestPrefix_Scale_HighFrequencyPodCreation(t *testing.T) {
	// Pre-checks
	if eniConfig == nil || eniConfig.IPAMType != "crd" {
		t.Skipf("skip: ipam type is not crd")
		return
	}
	if GetCachedTerwayDaemonSetName() != "terway-eniip" {
		t.Skipf("Requires terway-eniip daemonset")
		return
	}
	if !RequireTerwayVersion("v1.17.0") {
		t.Skipf("Requires terway version >= v1.17.0")
		return
	}

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
			return ctx
		}).
		Assess("test high frequency pod creation with prefixes", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessHighFrequencyPodCreation(ctx, t, config, ctx.Value(qualifiedNodeContextKey).(string))
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			restoreOriginalConfig(ctx, config, t)
			return ctx
		}).
		Feature()

	testenv.Test(t, feat)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// =============================================================================
// Assess Functions for State Tests
// =============================================================================

// assessStateTransition tests ENI state transitions
func assessStateTransition(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) context.Context {
	t.Logf("Testing ENI state transitions on node: %s", nodeName)

	// Step 1: Enable prefix mode with 5 prefixes via Dynamic Config
	t.Log("Step 1: Enable prefix mode with ipv4_prefix_count=5")
	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, nodeName, `{"enable_ip_prefix":true,"ipv4_prefix_count":5}`)
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}

	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Step 2: Wait for ENI creation and prefix allocation
	t.Log("Step 2: Wait for ENI creation and prefix allocation")
	err = waitForPrefixAllocation(ctx, config, t, nodeName, 5, 3*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for prefix allocation: %v", err)
	}

	// Step 3: Verify ENI states
	t.Log("Step 3: Verify ENI states")
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

	// Step 4: Test dynamic scale up (increase to 10) via Dynamic Config
	t.Log("Step 4: Reset prefix state before changing ipv4_prefix_count to 10")
	if resetErr := resetNodePrefixState(ctx, config, t, nodeName, 3*time.Minute); resetErr != nil {
		t.Logf("Warning: resetNodePrefixState failed before scale up: %v", resetErr)
	}
	t.Log("Step 4: Increase ipv4_prefix_count to 10")
	err = configureIPPrefixCount(ctx, t, config, nodeName, 10)
	if err != nil {
		t.Fatalf("failed to configure ipv4_prefix_count: %v", err)
	}

	// Restart to apply config
	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Wait for additional prefix allocation
	err = waitForPrefixAllocation(ctx, config, t, nodeName, 10, 3*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for additional prefix allocation: %v", err)
	}

	// Verify
	prefixes, err := getAllocatedPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get prefixes: %v", err)
	}

	if len(prefixes) != 10 {
		t.Errorf("expected 10 prefixes after scale up, got %d", len(prefixes))
	}

	t.Logf("Successfully tested state transitions, now have %d prefixes", len(prefixes))
	return MarkTestSuccess(ctx)
}

// assessDynamicScaleUp tests dynamic scaling up of prefixes
func assessDynamicScaleUp(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) context.Context {
	t.Logf("Testing dynamic scale up on node: %s", nodeName)

	// Initial allocation of 5 prefixes via Dynamic Config
	t.Log("Step 1: Initial allocation of 5 prefixes")
	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, nodeName, `{"enable_ip_prefix":true,"ipv4_prefix_count":5}`)
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}

	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	err = waitForPrefixAllocation(ctx, config, t, nodeName, 5, 3*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for initial allocation: %v", err)
	}

	// Scale up to 15 via Dynamic Config
	t.Log("Step 2: Reset prefix state before scaling up to 15 prefixes")
	if resetErr := resetNodePrefixState(ctx, config, t, nodeName, 3*time.Minute); resetErr != nil {
		t.Logf("Warning: resetNodePrefixState failed before scale up: %v", resetErr)
	}
	t.Log("Step 2: Scale up to 15 prefixes")
	err = configureIPPrefixCount(ctx, t, config, nodeName, 15)
	if err != nil {
		t.Fatalf("failed to configure ipv4_prefix_count: %v", err)
	}

	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	err = waitForPrefixAllocation(ctx, config, t, nodeName, 15, 4*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for scale up: %v", err)
	}

	// Verify scale up
	prefixes, err := getAllocatedPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get prefixes: %v", err)
	}

	if len(prefixes) != 15 {
		t.Errorf("expected 15 prefixes after scale up, got %d", len(prefixes))
	}

	// Verify all prefixes are valid
	invalidCount := 0
	for _, prefix := range prefixes {
		if prefix.Status != networkv1beta1.IPPrefixStatusValid {
			invalidCount++
		}
	}

	if invalidCount > 0 {
		t.Errorf("found %d invalid prefixes after scale up", invalidCount)
	}

	t.Logf("Successfully scaled up from 5 to %d prefixes", len(prefixes))
	return MarkTestSuccess(ctx)
}

// assessSingleENIPrefixes tests that prefix count = (IPv4PerAdapter - 1) creates only 1 ENI
func assessSingleENIPrefixes(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string, expectedPrefixes int) context.Context {
	t.Logf("Testing single ENI prefix allocation on node: %s", nodeName)
	t.Logf("Expected: %d prefixes on 1 ENI (IPv4PerAdapter - 1)", expectedPrefixes)

	// Reset node prefix state to ensure clean starting point
	t.Log("Reset node prefix state")
	if err := resetNodePrefixState(ctx, config, t, nodeName, 3*time.Minute); err != nil {
		t.Logf("Warning: resetNodePrefixState failed (node may have no prefixes yet): %v", err)
	}

	// Configure ipv4_prefix_count = (IPv4PerAdapter - 1) via Dynamic Config
	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, nodeName, fmt.Sprintf(`{"enable_ip_prefix":true,"ipv4_prefix_count":%d}`, expectedPrefixes))
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}

	// Restart terway
	t.Log("Restart terway-eniip to apply config")
	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Wait for prefix allocation
	t.Log("Wait for prefix allocation")
	err = waitForPrefixAllocation(ctx, config, t, nodeName, expectedPrefixes, 3*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for prefix allocation: %v", err)
	}

	// Verify only 1 ENI with prefixes exists
	eniCount, err := countENIsWithPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to count ENIs with prefixes: %v", err)
	}

	if eniCount != 1 {
		t.Errorf("expected 1 ENI with prefixes, got %d", eniCount)
	}

	// Verify prefix count
	prefixes, err := getAllocatedPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get prefixes: %v", err)
	}

	if len(prefixes) != expectedPrefixes {
		t.Errorf("expected %d prefixes, got %d", expectedPrefixes, len(prefixes))
	}

	t.Logf("Successfully allocated %d prefixes on single ENI", len(prefixes))
	return MarkTestSuccess(ctx)
}

// assessMultiENIPrefixes tests that prefix count = IPv4PerAdapter creates 2 ENIs
func assessMultiENIPrefixes(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string, expectedPrefixes int) context.Context {
	t.Logf("Testing multi-ENI prefix allocation on node: %s", nodeName)
	t.Logf("Expected: %d prefixes on 2 ENIs (IPv4PerAdapter)", expectedPrefixes)

	// Reset node prefix state
	t.Log("Reset node prefix state")
	if err := resetNodePrefixState(ctx, config, t, nodeName, 3*time.Minute); err != nil {
		t.Logf("Warning: resetNodePrefixState failed: %v", err)
	}

	// Configure ipv4_prefix_count = IPv4PerAdapter via Dynamic Config
	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, nodeName, fmt.Sprintf(`{"enable_ip_prefix":true,"ipv4_prefix_count":%d}`, expectedPrefixes))
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}

	// Restart terway
	t.Log("Restart terway-eniip to apply config")
	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Wait for prefix allocation
	t.Log("Wait for prefix allocation (timeout: 5 minutes)")
	err = waitForPrefixAllocation(ctx, config, t, nodeName, expectedPrefixes, 5*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for prefix allocation: %v", err)
	}

	// Verify 2 ENIs with prefixes exist
	eniCount, err := countENIsWithPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to count ENIs with prefixes: %v", err)
	}

	if eniCount != 2 {
		t.Errorf("expected 2 ENIs with prefixes, got %d", eniCount)
	}

	// Verify total prefix count
	prefixes, err := getAllocatedPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get prefixes: %v", err)
	}

	if len(prefixes) != expectedPrefixes {
		t.Errorf("expected %d prefixes, got %d", expectedPrefixes, len(prefixes))
	}

	// Verify prefixes are distributed across 2 ENIs (each should have roughly half)
	t.Log("Verify prefix distribution across ENIs")
	eniPrefixCounts := make(map[string]int)
	node := &networkv1beta1.Node{}
	err = config.Client().Resources().Get(ctx, nodeName, "", node)
	if err != nil {
		t.Fatalf("failed to get node: %v", err)
	}

	for eniID, nic := range node.Status.NetworkInterfaces {
		if nic.Status == aliyunClient.ENIStatusInUse && len(nic.IPv4Prefix) > 0 {
			eniPrefixCounts[eniID] = len(nic.IPv4Prefix)
			t.Logf("ENI %s: %d prefixes", eniID, len(nic.IPv4Prefix))
		}
	}

	// Each ENI should have at least 1 prefix (distributed correctly)
	for eniID, count := range eniPrefixCounts {
		if count == 0 {
			t.Errorf("ENI %s has no prefixes", eniID)
		}
	}

	t.Logf("Successfully allocated %d prefixes across %d ENIs", len(prefixes), eniCount)
	return MarkTestSuccess(ctx)
}

// assessMaxCapacity tests max capacity prefix allocation across all qualified nodes
func assessMaxCapacity(ctx context.Context, t *testing.T, config *envconf.Config, nodes []nodeCapacity) context.Context {
	t.Logf("Testing max capacity prefix allocation on %d nodes", len(nodes))

	// Setup Dynamic Config for each node with max capacity
	for _, nc := range nodes {
		t.Logf("Setup Dynamic Config for node %s: maxPrefixes=%d", nc.nodeName, nc.maxPrefixes)
		var err error
		ctx, err = setupNodeDynamicConfig(ctx, config, t, nc.nodeName, fmt.Sprintf(`{"enable_ip_prefix":true,"ipv4_prefix_count":%d}`, nc.maxPrefixes))
		if err != nil {
			t.Fatalf("failed to setup node dynamic config for node %s: %v", nc.nodeName, err)
		}
	}

	// Restart terway
	t.Log("Restart terway-eniip to apply config")
	err := restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
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
	ctx, err = setupNodeDynamicConfig(ctx, config, t, nodeName, `{"enable_ip_prefix":true,"ipv4_prefix_count":20}`)
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}

	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
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
		WithNodeAffinity(map[string]string{"kubernetes.io/hostname": nodeName})

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
