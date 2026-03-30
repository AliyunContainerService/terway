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
// IP Prefix Dual Stack Tests
// =============================================================================

// TestPrefix_DualStack_1to1Ratio tests IPv4/IPv6 prefix 1:1 ratio
func TestPrefix_DualStack_1to1Ratio(t *testing.T) {
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
	// Check if dual stack is enabled
	if !testIPv6 {
		t.Skip("Dual stack not enabled in cluster")
		return
	}

	// Discover node capacities
	ctx := context.Background()
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types with capacity: %v", err)
	}

	// Find an IP Prefix node with dual stack support and equal IPv4/IPv6 capacity.
	// IP Prefix nodes have terway-config=e2e-ip-prefix set by Terraform.
	var qualifiedNode string
	for nodeName, cap := range nodeInfoWithCap.Capacities {
		if cap.NodeType != NodeTypeECSIPPrefix {
			continue
		}
		// Dual stack requires IPv4PerAdapter == IPv6PerAdapter
		if cap.IPv4PerAdapter == cap.IPv6PerAdapter && cap.IPv4PerAdapter >= 6 {
			qualifiedNode = nodeName
			break
		}
	}

	if qualifiedNode == "" {
		t.Skip("No qualified IP Prefix nodes found with equal IPv4/IPv6 capacity for dual stack testing")
		return
	}

	feat := features.New("Prefix/DualStack/1to1Ratio").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		WithLabel("ip-stack", "dual").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, qualifiedNode)
			return ctx
		}).
		Assess("test IPv4/IPv6 prefix 1:1 ratio", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessDualStack1to1Ratio(ctx, t, config, ctx.Value(qualifiedNodeContextKey).(string))
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

// TestPrefix_DualStack_CapacityConstraint tests dual stack capacity constraints
func TestPrefix_DualStack_CapacityConstraint(t *testing.T) {
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
	if !testIPv6 {
		t.Skip("Dual stack not enabled in cluster")
		return
	}

	// Discover node capacities
	ctx := context.Background()
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types with capacity: %v", err)
	}

	// Find an IP Prefix node with dual stack support.
	// IP Prefix nodes have terway-config=e2e-ip-prefix set by Terraform.
	var qualifiedNode string
	var ipv4PerAdapter, ipv6PerAdapter int
	for nodeName, cap := range nodeInfoWithCap.Capacities {
		if cap.NodeType != NodeTypeECSIPPrefix {
			continue
		}
		// For dual stack, require both IPv4 and IPv6 capacity
		if cap.IPv4PerAdapter > 0 && cap.IPv6PerAdapter > 0 {
			qualifiedNode = nodeName
			ipv4PerAdapter = cap.IPv4PerAdapter
			ipv6PerAdapter = cap.IPv6PerAdapter
			break
		}
	}

	if qualifiedNode == "" {
		t.Skip("No qualified IP Prefix nodes found with dual stack support")
		return
	}

	feat := features.New("Prefix/DualStack/CapacityConstraint").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		WithLabel("ip-stack", "dual").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, qualifiedNode)
			ctx = context.WithValue(ctx, ipv4PerAdapterContextKey, ipv4PerAdapter)
			ctx = context.WithValue(ctx, ipv6PerAdapterContextKey, ipv6PerAdapter)
			return ctx
		}).
		Assess("test dual stack capacity constraints", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeName := ctx.Value(qualifiedNodeContextKey).(string)
			v4PerAdapter := ctx.Value(ipv4PerAdapterContextKey).(int)
			v6PerAdapter := ctx.Value(ipv6PerAdapterContextKey).(int)
			return assessDualStackCapacityConstraint(ctx, t, config, nodeName, v4PerAdapter, v6PerAdapter)
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
// IP Prefix EFLO Exclusion Tests
// =============================================================================

// =============================================================================
// Assess Functions for Dual Stack Tests
// =============================================================================

// assessDualStack1to1Ratio tests that IPv4 and IPv6 prefixes are allocated in 1:1 ratio
func assessDualStack1to1Ratio(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) context.Context {
	t.Logf("Testing dual stack 1:1 ratio on node: %s", nodeName)

	// Reset prefix state before configuring new settings
	t.Log("Reset node prefix state before configuring dual stack")
	if resetErr := resetNodePrefixState(ctx, config, t, nodeName, 3*time.Minute); resetErr != nil {
		t.Logf("Warning: resetNodePrefixState failed (node may have no prefixes yet): %v", resetErr)
	}

	// Setup Dynamic Config with enable_ip_prefix=true
	// Dual stack mode does not support ipv4_prefix_count; count is determined by system capacity
	t.Log("Configure enable_ip_prefix=true via Dynamic Config")
	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, nodeName, `{"enable_ip_prefix":true}`)
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}

	// Restart terway
	t.Log("Restart terway-eniip to apply config")
	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Wait for at least 1 prefix pair to be allocated (system decides the count)
	t.Log("Wait for dual stack prefix allocation")
	err = waitForDualStackPrefixAllocation(ctx, config, t, nodeName, 1, 4*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for prefix allocation: %v", err)
	}

	// Verify 1:1 ratio
	t.Log("Verify IPv4 and IPv6 prefix counts")
	v4Count, v6Count, err := getDualStackPrefixCounts(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get dual stack prefix counts: %v", err)
	}

	t.Logf("IPv4 prefixes: %d, IPv6 prefixes: %d", v4Count, v6Count)

	if v4Count == 0 {
		t.Errorf("expected at least 1 IPv4 prefix, got 0")
	}
	if v6Count == 0 {
		t.Errorf("expected at least 1 IPv6 prefix, got 0")
	}
	if v4Count != v6Count {
		t.Errorf("IPv4 and IPv6 prefix counts should be equal: v4=%d, v6=%d", v4Count, v6Count)
	}

	t.Logf("Successfully verified 1:1 ratio: %d IPv4 prefixes, %d IPv6 prefixes", v4Count, v6Count)
	return MarkTestSuccess(ctx)
}

// assessDualStackCapacityConstraint tests dual stack capacity constraints
func assessDualStackCapacityConstraint(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string, v4PerAdapter, v6PerAdapter int) context.Context {
	t.Logf("Testing dual stack capacity constraints on node: %s", nodeName)
	t.Logf("Node capacity: IPv4PerAdapter=%d, IPv6PerAdapter=%d", v4PerAdapter, v6PerAdapter)

	// Request 20 prefixes - should respect capacity constraints
	requestedCount := 20
	t.Logf("Requesting %d prefixes", requestedCount)

	// Reset prefix state before configuring new settings
	t.Log("Reset node prefix state before configuring dual stack capacity constraint")
	if resetErr := resetNodePrefixState(ctx, config, t, nodeName, 3*time.Minute); resetErr != nil {
		t.Logf("Warning: resetNodePrefixState failed (node may have no prefixes yet): %v", resetErr)
	}

	// Setup Dynamic Config with enable_ip_prefix=true and ipv4_prefix_count
	// Note: ip_stack is a cluster-level parameter and should not be set in node-level Dynamic Config
	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, nodeName, fmt.Sprintf(`{"enable_ip_prefix":true,"ipv4_prefix_count":%d}`, requestedCount))
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}

	// Restart terway
	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Wait for allocation
	err = waitForDualStackPrefixAllocation(ctx, config, t, nodeName, requestedCount, 5*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for prefix allocation: %v", err)
	}

	// Verify counts
	v4Count, v6Count, err := getDualStackPrefixCounts(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get prefix counts: %v", err)
	}

	t.Logf("Allocated: %d IPv4 prefixes, %d IPv6 prefixes", v4Count, v6Count)

	// Verify 1:1 ratio is maintained
	if v4Count != v6Count {
		t.Errorf("IPv4 and IPv6 prefix counts should be equal: v4=%d, v6=%d", v4Count, v6Count)
	}

	// Verify actual count matches requested (or capped at capacity)
	expectedCount := requestedCount

	// Get node capacity
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, config.Client())
	if err != nil {
		t.Fatalf("Failed to discover node capacities: %v", err)
	}

	if cap, ok := nodeInfoWithCap.Capacities[nodeName]; ok {
		maxTotalV4 := cap.Adapters * (cap.IPv4PerAdapter - 1)
		maxTotalV6 := cap.Adapters * (cap.IPv6PerAdapter - 1)
		maxDualStack := maxTotalV4
		if maxTotalV6 < maxDualStack {
			maxDualStack = maxTotalV6
		}

		if expectedCount > maxDualStack {
			expectedCount = maxDualStack
			t.Logf("Requested %d prefixes, but max dual-stack capacity is %d", requestedCount, maxDualStack)
		}
	}

	if v4Count != expectedCount {
		t.Errorf("expected %d IPv4 prefixes (capped at capacity), got %d", expectedCount, v4Count)
	}
	if v6Count != expectedCount {
		t.Errorf("expected %d IPv6 prefixes (capped at capacity), got %d", expectedCount, v6Count)
	}

	t.Logf("Successfully verified dual stack capacity constraints")
	return MarkTestSuccess(ctx)
}

// =============================================================================
// Helper Functions for Dual Stack
// =============================================================================

// configureIPPrefixCountAndStack updates both ipv4_prefix_count and ip_stack in the node-specific
// Dynamic Config ConfigMap. The ConfigMap must have been created first via setupNodeDynamicConfig.

// waitForDualStackPrefixAllocation waits for both IPv4 and IPv6 prefix allocation
func waitForDualStackPrefixAllocation(ctx context.Context, config *envconf.Config, t *testing.T, nodeName string, expectedCount int, timeout time.Duration) error {
	return wait.For(func(ctx context.Context) (done bool, err error) {
		v4Count, v6Count, err := getDualStackPrefixCounts(ctx, config, nodeName)
		if err != nil {
			return false, err
		}

		if v4Count >= expectedCount && v6Count >= expectedCount {
			return true, nil
		}

		t.Logf("Node %s: waiting for %d prefixes each, currently have v4=%d, v6=%d",
			nodeName, expectedCount, v4Count, v6Count)

		return false, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(10*time.Second))
}

// getDualStackPrefixCounts returns the count of IPv4 and IPv6 prefixes
func getDualStackPrefixCounts(ctx context.Context, config *envconf.Config, nodeName string) (int, int, error) {
	node := &networkv1beta1.Node{}
	err := config.Client().Resources().Get(ctx, nodeName, "", node)
	if err != nil {
		return 0, 0, err
	}

	v4Count := 0
	v6Count := 0
	for _, nic := range node.Status.NetworkInterfaces {
		if nic.Status == aliyunClient.ENIStatusInUse {
			for _, prefix := range nic.IPv4Prefix {
				if prefix.Status == networkv1beta1.IPPrefixStatusValid {
					v4Count++
				}
			}
			for _, prefix := range nic.IPv6Prefix {
				if prefix.Status == networkv1beta1.IPPrefixStatusValid {
					v6Count++
				}
			}
		}
	}

	return v4Count, v6Count, nil
}

// =============================================================================
// Context Keys
// =============================================================================

type ipv4PerAdapterContextKeyType struct{}
type ipv6PerAdapterContextKeyType struct{}

var ipv4PerAdapterContextKey = ipv4PerAdapterContextKeyType{}
var ipv6PerAdapterContextKey = ipv6PerAdapterContextKeyType{}
