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
	skipIfNotPrefixTestEnvironment(t)
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

	runPrefixFeatureTest(t, feat)
}

// TestPrefix_DualStack_CapacityConstraint tests dual stack capacity constraints
func TestPrefix_DualStack_CapacityConstraint(t *testing.T) {
	skipIfNotPrefixTestEnvironment(t)
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

	runPrefixFeatureTest(t, feat)
}

// =============================================================================
// IP Prefix EFLO Exclusion Tests
// =============================================================================

// =============================================================================
// Assess Functions for Dual Stack Tests
// =============================================================================

// assessDualStack1to1Ratio tests that IPv4 prefixes are allocated per config and
// each ENI with IPv4 prefix has at least 1 IPv6 prefix.
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
	ctx, err = setupNodeDynamicConfig(ctx, config, t, `{"enable_ip_prefix":true}`)
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

	// Verify allocation: IPv4 prefixes allocated, and each ENI with IPv4 prefix has >= 1 IPv6 prefix
	t.Log("Verify dual stack prefix allocation")
	info, err := getDualStackPrefixInfo(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get dual stack prefix info: %v", err)
	}

	t.Logf("IPv4 prefixes: %d, IPv6 prefixes: %d, ENIs with v4: %d, ENIs with v6: %d",
		info.TotalV4Prefixes, info.TotalV6Prefixes, info.ENIsWithV4Prefix, info.ENIsWithV6Prefix)

	if info.TotalV4Prefixes == 0 {
		t.Errorf("expected at least 1 IPv4 prefix, got 0")
	}
	if info.ENIsWithV4Prefix > 0 && info.ENIsWithV6Prefix < info.ENIsWithV4Prefix {
		t.Errorf("every ENI with IPv4 prefix should have at least 1 IPv6 prefix: ENIs with v4=%d, ENIs with v6=%d",
			info.ENIsWithV4Prefix, info.ENIsWithV6Prefix)
	}

	t.Logf("Successfully verified dual stack: %d IPv4 prefixes, %d/%d ENIs have IPv6 prefix",
		info.TotalV4Prefixes, info.ENIsWithV6Prefix, info.ENIsWithV4Prefix)
	return MarkTestSuccess(ctx)
}

// assessDualStackCapacityConstraint tests dual stack capacity constraints
func assessDualStackCapacityConstraint(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string, v4PerAdapter, v6PerAdapter int) context.Context {
	t.Logf("Testing dual stack capacity constraints on node: %s", nodeName)
	t.Logf("Node capacity: IPv4PerAdapter=%d, IPv6PerAdapter=%d", v4PerAdapter, v6PerAdapter)

	requestedCount := 20
	t.Logf("Requesting %d IPv4 prefixes", requestedCount)

	// Compute expected IPv4 count (capped at node capacity) before waiting
	expectedV4Count := requestedCount
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, config.Client())
	if err != nil {
		t.Fatalf("Failed to discover node capacities: %v", err)
	}
	if cap, ok := nodeInfoWithCap.Capacities[nodeName]; ok {
		maxTotalV4 := cap.Adapters * (cap.IPv4PerAdapter - 1)
		if expectedV4Count > maxTotalV4 {
			expectedV4Count = maxTotalV4
			t.Logf("Requested %d IPv4 prefixes, but max capacity is %d", requestedCount, maxTotalV4)
		}
	}

	// Reset prefix state before configuring new settings
	t.Log("Reset node prefix state before configuring dual stack capacity constraint")
	if resetErr := resetNodePrefixState(ctx, config, t, nodeName, 3*time.Minute); resetErr != nil {
		t.Logf("Warning: resetNodePrefixState failed (node may have no prefixes yet): %v", resetErr)
	}

	// Setup Dynamic Config with enable_ip_prefix=true and ipv4_prefix_count
	ctx, err = setupNodeDynamicConfig(ctx, config, t, fmt.Sprintf(`{"enable_ip_prefix":true,"ipv4_prefix_count":%d}`, requestedCount))
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}

	err = restartTerway(ctx, config)
	if err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Wait using the capacity-capped count so we don't time out
	err = waitForDualStackPrefixAllocation(ctx, config, t, nodeName, expectedV4Count, 5*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for prefix allocation: %v", err)
	}

	// Verify counts
	info, err := getDualStackPrefixInfo(ctx, config, nodeName)
	if err != nil {
		t.Fatalf("failed to get prefix info: %v", err)
	}

	t.Logf("Allocated: %d IPv4 prefixes, %d IPv6 prefixes, ENIs with v4=%d, ENIs with v6=%d",
		info.TotalV4Prefixes, info.TotalV6Prefixes, info.ENIsWithV4Prefix, info.ENIsWithV6Prefix)

	if info.TotalV4Prefixes != expectedV4Count {
		t.Errorf("expected %d IPv4 prefixes (capped at capacity), got %d", expectedV4Count, info.TotalV4Prefixes)
	}

	// Verify IPv6: each ENI with IPv4 prefix has at least 1 IPv6 prefix
	if info.ENIsWithV4Prefix > 0 && info.ENIsWithV6Prefix < info.ENIsWithV4Prefix {
		t.Errorf("every ENI with IPv4 prefix should have at least 1 IPv6 prefix: ENIs with v4=%d, ENIs with v6=%d",
			info.ENIsWithV4Prefix, info.ENIsWithV6Prefix)
	}

	t.Logf("Successfully verified dual stack capacity constraints")
	return MarkTestSuccess(ctx)
}

// =============================================================================
// Helper Functions for Dual Stack
// =============================================================================

// dualStackPrefixInfo holds per-node dual stack prefix allocation details.
type dualStackPrefixInfo struct {
	TotalV4Prefixes  int
	TotalV6Prefixes  int
	ENIsWithV4Prefix int
	ENIsWithV6Prefix int
	InUseENIs        int
}

// getDualStackPrefixInfo returns detailed dual stack prefix allocation info for a node.
func getDualStackPrefixInfo(ctx context.Context, config *envconf.Config, nodeName string) (*dualStackPrefixInfo, error) {
	node := &networkv1beta1.Node{}
	err := config.Client().Resources().Get(ctx, nodeName, "", node)
	if err != nil {
		return nil, err
	}

	info := &dualStackPrefixInfo{}
	for _, nic := range node.Status.NetworkInterfaces {
		if nic.Status != aliyunClient.ENIStatusInUse {
			continue
		}
		info.InUseENIs++

		v4 := 0
		for _, prefix := range nic.IPv4Prefix {
			if prefix.Status == networkv1beta1.IPPrefixStatusValid {
				v4++
			}
		}
		v6 := 0
		for _, prefix := range nic.IPv6Prefix {
			if prefix.Status == networkv1beta1.IPPrefixStatusValid {
				v6++
			}
		}

		info.TotalV4Prefixes += v4
		info.TotalV6Prefixes += v6
		if v4 > 0 {
			info.ENIsWithV4Prefix++
		}
		if v6 > 0 {
			info.ENIsWithV6Prefix++
		}
	}

	return info, nil
}

// waitForDualStackPrefixAllocation waits until:
//   - total IPv4 prefixes >= expectedV4Count (user-configured count)
//   - every ENI that has IPv4 prefixes also has at least 1 IPv6 prefix
func waitForDualStackPrefixAllocation(ctx context.Context, config *envconf.Config, t *testing.T, nodeName string, expectedV4Count int, timeout time.Duration) error {
	return wait.For(func(ctx context.Context) (done bool, err error) {
		info, err := getDualStackPrefixInfo(ctx, config, nodeName)
		if err != nil {
			return false, err
		}

		v4Ready := info.TotalV4Prefixes >= expectedV4Count
		v6Ready := info.ENIsWithV4Prefix > 0 && info.ENIsWithV6Prefix >= info.ENIsWithV4Prefix

		if v4Ready && v6Ready {
			return true, nil
		}

		t.Logf("Node %s: want v4>=%d (have %d), want v6 on each ENI with v4 (%d/%d ENIs), in-use ENIs=%d",
			nodeName, expectedV4Count, info.TotalV4Prefixes,
			info.ENIsWithV6Prefix, info.ENIsWithV4Prefix, info.InUseENIs)

		return false, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(10*time.Second))
}

// getDualStackPrefixCounts returns the total count of IPv4 and IPv6 prefixes across all in-use ENIs.
func getDualStackPrefixCounts(ctx context.Context, config *envconf.Config, nodeName string) (int, int, error) {
	info, err := getDualStackPrefixInfo(ctx, config, nodeName)
	if err != nil {
		return 0, 0, err
	}
	return info.TotalV4Prefixes, info.TotalV6Prefixes, nil
}

// =============================================================================
// Context Keys
// =============================================================================

type ipv4PerAdapterContextKeyType struct{}
type ipv6PerAdapterContextKeyType struct{}

var ipv4PerAdapterContextKey = ipv4PerAdapterContextKeyType{}
var ipv6PerAdapterContextKey = ipv6PerAdapterContextKeyType{}
