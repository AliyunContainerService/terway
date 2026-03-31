//go:build e2e

package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// =============================================================================
// IP Prefix Boundary Value Tests
// =============================================================================

// TestPrefix_Boundary_APILimit tests the API limit boundary (10 prefixes per call)
func TestPrefix_Boundary_APILimit(t *testing.T) {
	skipIfNotPrefixTestEnvironment(t)

	// Discover node capacities
	ctx := context.Background()
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types with capacity: %v", err)
	}

	// Find a node with IP Prefix enabled (Node CR ENISpec.EnableIPPrefix=true)
	// and sufficient capacity for 20 prefixes.
	var qualifiedNode string
	for nodeName, cap := range nodeInfoWithCap.Capacities {
		if !cap.EnableIPPrefix {
			continue
		}
		// Need capacity for at least 20 prefixes
		maxPrefixes := cap.Adapters * (cap.IPv4PerAdapter - 1)
		if maxPrefixes >= 20 {
			qualifiedNode = nodeName
			break
		}
	}

	if qualifiedNode == "" {
		t.Skip("No qualified IP Prefix nodes found with capacity for 20 prefixes")
		return
	}

	feat := features.New("Prefix/Boundary/APILimit").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, qualifiedNode)
			return ctx
		}).
		Assess("test API limit of 10 prefixes per call", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessAPILimitBoundary(ctx, t, config, ctx.Value(qualifiedNodeContextKey).(string))
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			restoreOriginalConfig(ctx, config, t)
			return ctx
		}).
		Feature()

	runPrefixFeatureTest(t, feat)
}

// TestPrefix_Boundary_ENICapacity tests the ENI capacity boundary
func TestPrefix_Boundary_ENICapacity(t *testing.T) {
	skipIfNotPrefixTestEnvironment(t)

	// Discover node capacities
	ctx := context.Background()
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types with capacity: %v", err)
	}

	// Find an IP Prefix node where capacity can be tested.
	// IP Prefix nodes have terway-config=e2e-ip-prefix set by Terraform.
	// We want a node where we can request more than single ENI capacity.
	var qualifiedNode string
	var maxPrefixesPerENI int
	for nodeName, cap := range nodeInfoWithCap.Capacities {
		if cap.NodeType != NodeTypeECSIPPrefix {
			continue
		}
		maxPrefixesPerENI = cap.IPv4PerAdapter - 1
		// Request slightly more than single ENI capacity
		if maxPrefixesPerENI > 5 && cap.Adapters >= 2 {
			qualifiedNode = nodeName
			break
		}
	}

	if qualifiedNode == "" {
		t.Skip("No qualified IP Prefix nodes found for capacity boundary test")
		return
	}

	feat := features.New("Prefix/Boundary/ENICapacity").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, qualifiedNode)
			ctx = context.WithValue(ctx, maxPrefixPerENIContextKey, maxPrefixesPerENI)
			return ctx
		}).
		Assess("test ENI capacity boundary", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeName := ctx.Value(qualifiedNodeContextKey).(string)
			maxPerENI := ctx.Value(maxPrefixPerENIContextKey).(int)
			return assessENICapacityBoundary(ctx, t, config, nodeName, maxPerENI)
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			restoreOriginalConfig(ctx, config, t)
			return ctx
		}).
		Feature()

	runPrefixFeatureTest(t, feat)
}

// TestPrefix_Boundary_ZeroAndMin tests zero and minimum prefix count
func TestPrefix_Boundary_ZeroAndMin(t *testing.T) {
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

	feat := features.New("Prefix/Boundary/ZeroAndMin").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, nodeName)
			return ctx
		}).
		Assess("test zero and minimum prefix count", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessZeroAndMinBoundary(ctx, t, config, ctx.Value(qualifiedNodeContextKey).(string))
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			restoreOriginalConfig(ctx, config, t)
			return ctx
		}).
		Feature()

	runPrefixFeatureTest(t, feat)
}

// TestPrefix_Boundary_MaxValue tests maximum prefix count
func TestPrefix_Boundary_MaxValue(t *testing.T) {
	skipIfNotPrefixTestEnvironment(t)

	// Discover node capacities
	ctx := context.Background()
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types with capacity: %v", err)
	}

	// Find an IP Prefix node and calculate its max capacity.
	// IP Prefix nodes have terway-config=e2e-ip-prefix set by Terraform.
	var qualifiedNode string
	var maxCapacity int
	for nodeName, cap := range nodeInfoWithCap.Capacities {
		if cap.NodeType != NodeTypeECSIPPrefix {
			continue
		}
		// Max prefixes = (adapters - 1) * (IPv4PerAdapter - 1), excluding primary ENI
		nodeMax := (cap.Adapters - 1) * (cap.IPv4PerAdapter - 1)
		if nodeMax > maxCapacity {
			maxCapacity = nodeMax
			qualifiedNode = nodeName
		}
	}

	if qualifiedNode == "" {
		t.Skip("No qualified IP Prefix nodes found")
		return
	}

	// Request more than max capacity to test boundary
	requestedCount := maxCapacity + 50

	feat := features.New("Prefix/Boundary/MaxValue").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, qualifiedNode)
			ctx = context.WithValue(ctx, maxCapacityContextKey, maxCapacity)
			ctx = context.WithValue(ctx, requestedCountContextKey, requestedCount)
			return ctx
		}).
		Assess("test maximum prefix count boundary", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeName := ctx.Value(qualifiedNodeContextKey).(string)
			maxCap := ctx.Value(maxCapacityContextKey).(int)
			reqCount := ctx.Value(requestedCountContextKey).(int)
			return assessMaxValueBoundary(ctx, t, config, nodeName, maxCap, reqCount)
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			restoreOriginalConfig(ctx, config, t)
			return ctx
		}).
		Feature()

	runPrefixFeatureTest(t, feat)
}

// =============================================================================
// Assess Functions for Boundary Tests
// =============================================================================

// assessAPILimitBoundary tests the 10-prefix API limit
func assessAPILimitBoundary(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) context.Context {
	t.Logf("Testing API limit boundary on node: %s", nodeName)

	// Setup Dynamic Config for this node (initial count, will be updated per test case)
	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, `{"enable_ip_prefix":true,"ipv4_prefix_count":10}`)
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}

	testCases := []struct {
		name         string
		prefixCount  int
		expectedENIs int
		description  string
	}{
		{"Case 1: exactly 10 prefixes (API limit)", 10, 1, "Single API call should be sufficient"},
		{"Case 2: 11 prefixes (exceeds API limit)", 11, 2, "Requires 2 API calls: 10+1"},
		{"Case 3: 20 prefixes (multiple API calls)", 20, 2, "Requires 2 API calls: 10+10"},
	}

	for _, tc := range testCases {
		t.Logf("Running: %s", tc.name)

		// Configure prefix count via Dynamic Config
		err = configureIPPrefixCount(ctx, t, config, nodeName, tc.prefixCount)
		if err != nil {
			t.Fatalf("failed to configure ipv4_prefix_count: %v", err)
		}

		// Restart terway
		err = restartTerway(ctx, config)
		if err != nil {
			t.Fatalf("failed to restart terway: %v", err)
		}

		// Wait for allocation
		err = waitForPrefixAllocation(ctx, config, t, nodeName, tc.prefixCount, 3*time.Minute)
		if err != nil {
			t.Fatalf("failed waiting for prefix allocation: %v", err)
		}

		// Verify allocation
		prefixes, err := getAllocatedPrefixes(ctx, config, nodeName)
		if err != nil {
			t.Fatalf("failed to get prefixes: %v", err)
		}

		if len(prefixes) != tc.prefixCount {
			t.Errorf("%s: expected %d prefixes, got %d", tc.name, tc.prefixCount, len(prefixes))
		}

		t.Logf("%s: successfully allocated %d prefixes", tc.name, len(prefixes))

		// Clean up for next test case
		err = cleanupNodePrefixes(ctx, config, t, nodeName)
		if err != nil {
			t.Logf("Warning: failed to cleanup prefixes: %v", err)
		}
	}

	return MarkTestSuccess(ctx)
}

// assessENICapacityBoundary tests ENI capacity limits
func assessENICapacityBoundary(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string, maxPerENI int) context.Context {
	t.Logf("Testing ENI capacity boundary on node: %s (max per ENI: %d)", nodeName, maxPerENI)

	// Reset state to ensure clean starting point
	t.Log("Reset node prefix state")
	if err := resetNodePrefixState(ctx, config, t, nodeName, 3*time.Minute); err != nil {
		t.Logf("Warning: resetNodePrefixState failed: %v", err)
	}

	// Request more than single ENI capacity
	requestedCount := maxPerENI + 3
	t.Logf("Requesting %d prefixes (exceeds single ENI capacity of %d)", requestedCount, maxPerENI)

	// Setup Dynamic Config for this node
	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, fmt.Sprintf(`{"enable_ip_prefix":true,"ipv4_prefix_count":%d}`, requestedCount))
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}

	// Restart terway
	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Wait for allocation
	err = waitForPrefixAllocation(ctx, config, t, nodeName, requestedCount, 4*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for prefix allocation: %v", err)
	}

	// Verify allocation
	prefixes, err := getAllocatedPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get prefixes: %v", err)
	}

	if len(prefixes) != requestedCount {
		t.Errorf("expected %d prefixes, got %d", requestedCount, len(prefixes))
	}

	// Verify ENI distribution
	eniCount, err := countENIsWithPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to count ENIs: %v", err)
	}

	// Should have at least 2 ENIs since we requested more than single ENI capacity
	if eniCount < 2 {
		t.Errorf("expected at least 2 ENIs, got %d (requested %d, max per ENI %d)",
			eniCount, requestedCount, maxPerENI)
	}

	t.Logf("Successfully allocated %d prefixes across %d ENIs", len(prefixes), eniCount)
	return MarkTestSuccess(ctx)
}

// assessZeroAndMinBoundary tests zero and minimum prefix counts
func assessZeroAndMinBoundary(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) context.Context {
	t.Logf("Testing zero and minimum boundary on node: %s", nodeName)

	// Reset state to ensure clean starting point
	t.Log("Reset node prefix state")
	if err := resetNodePrefixState(ctx, config, t, nodeName, 3*time.Minute); err != nil {
		t.Logf("Warning: resetNodePrefixState failed: %v", err)
	}

	// Test Case 1: ipv4_prefix_count=0 via Dynamic Config
	t.Log("Test Case 1: ipv4_prefix_count=0")
	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, `{"enable_ip_prefix":true,"ipv4_prefix_count":0}`)
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}

	// Trigger a reconcile on the Node CR to ensure the controller processes the config
	nodeCR := &networkv1beta1.Node{}
	if getErr := config.Client().Resources().Get(ctx, nodeName, "", nodeCR); getErr == nil {
		triggerNodeCR(ctx, config, t, nodeCR)
	}

	// Wait a bit and check - should have no prefixes
	time.Sleep(20 * time.Second)
	prefixes, err := getAllocatedPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get prefixes: %v", err)
	}

	if len(prefixes) > 0 {
		t.Errorf("expected 0 prefixes with ipv4_prefix_count=0, got %d", len(prefixes))
	}
	t.Logf("Verified: ipv4_prefix_count=0 results in 0 prefixes")

	// Clean up
	err = cleanupNodePrefixes(ctx, config, t, nodeName)
	if err != nil {
		t.Logf("Warning: failed to cleanup: %v", err)
	}

	// Test Case 2: ipv4_prefix_count=1 via Dynamic Config
	t.Log("Test Case 2: ipv4_prefix_count=1")
	err = configureIPPrefixCount(ctx, t, config, nodeName, 1)
	if err != nil {
		t.Fatalf("failed to configure ipv4_prefix_count=1: %v", err)
	}

	// Restart terway
	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Wait for allocation
	err = waitForPrefixAllocation(ctx, config, t, nodeName, 1, 2*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for prefix allocation: %v", err)
	}

	// Verify allocation
	prefixes, err = getAllocatedPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get prefixes: %v", err)
	}

	if len(prefixes) != 1 {
		t.Errorf("expected 1 prefix, got %d", len(prefixes))
	}

	t.Logf("Verified: ipv4_prefix_count=1 results in 1 prefix")
	return MarkTestSuccess(ctx)
}

// assessMaxValueBoundary tests maximum value boundary
func assessMaxValueBoundary(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string, maxCapacity int, requestedCount int) context.Context {
	t.Logf("Testing max value boundary on node: %s", nodeName)
	t.Logf("Node max capacity: %d prefixes, Requested: %d", maxCapacity, requestedCount)

	// Setup Dynamic Config with requested count exceeding capacity
	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, fmt.Sprintf(`{"enable_ip_prefix":true,"ipv4_prefix_count":%d}`, requestedCount))
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}

	// Restart terway
	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Wait for allocation (system should allocate exactly up to max capacity)
	allocationTimeout := 5 * time.Minute
	err = waitForPrefixAllocationWithMax(ctx, config, t, nodeName, maxCapacity, allocationTimeout)
	if err != nil {
		t.Fatalf("timed out waiting for %d prefixes: %v", maxCapacity, err)
	}

	prefixes, err := getAllocatedPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get prefixes: %v", err)
	}

	if len(prefixes) != maxCapacity {
		t.Errorf("expected exactly %d prefixes (max capacity), got %d", maxCapacity, len(prefixes))
	} else {
		t.Logf("System correctly allocated max capacity of %d prefixes", maxCapacity)
	}

	return MarkTestSuccess(ctx)
}

// =============================================================================
// Helper Functions for Boundary Tests
// =============================================================================

// waitForPrefixAllocationWithMax waits for prefix allocation up to a maximum
func waitForPrefixAllocationWithMax(ctx context.Context, config *envconf.Config, t *testing.T, nodeName string, maxCount int, timeout time.Duration) error {
	return wait.For(func(ctx context.Context) (done bool, err error) {
		node := &networkv1beta1.Node{}
		err = config.Client().Resources().Get(ctx, nodeName, "", node)
		if err != nil {
			return false, err
		}

		// Count valid prefixes
		validCount := 0
		for _, nic := range node.Status.NetworkInterfaces {
			if nic.Status == aliyunClient.ENIStatusInUse {
				for _, prefix := range nic.IPv4Prefix {
					if prefix.Status == networkv1beta1.IPPrefixStatusValid {
						validCount++
					}
				}
			}
		}

		// Done if we have at least maxCount or if count stabilized
		if validCount >= maxCount {
			return true, nil
		}

		t.Logf("Node %s: waiting for prefixes, currently have %d valid (max: %d)",
			nodeName, validCount, maxCount)

		return false, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(10*time.Second))
}

// =============================================================================
// Context Keys
// =============================================================================

type maxPrefixPerENIContextKeyType struct{}
type maxCapacityContextKeyType struct{}
type requestedCountContextKeyType struct{}

var maxPrefixPerENIContextKey = maxPrefixPerENIContextKeyType{}
var maxCapacityContextKey = maxCapacityContextKeyType{}
var requestedCountContextKey = requestedCountContextKeyType{}
