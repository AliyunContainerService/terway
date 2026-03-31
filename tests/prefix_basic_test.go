//go:build e2e

package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/Jeffail/gabs/v2"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// =============================================================================
// IP Prefix Basic Functionality Tests
// =============================================================================

// TestPrefix_Basic_SingleENI tests single ENI prefix allocation
func TestPrefix_Basic_SingleENI(t *testing.T) {
	skipIfNotPrefixTestEnvironment(t)

	// Discover node capacities to find suitable nodes
	ctx := context.Background()
	nodeInfoWithCap, err := DiscoverNodeTypesWithCapacity(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types with capacity: %v", err)
	}

	// Find IP Prefix nodes with sufficient capacity for 5 prefixes.
	// IP Prefix nodes have terway-config=e2e-ip-prefix set by Terraform.
	var qualifiedNode string
	for nodeName, cap := range nodeInfoWithCap.Capacities {
		if cap.NodeType != NodeTypeECSIPPrefix {
			continue
		}
		// Need at least 6 slots (1 primary + 5 prefixes)
		if cap.IPv4PerAdapter >= 6 {
			qualifiedNode = nodeName
			break
		}
	}

	if qualifiedNode == "" {
		t.Skip("No qualified IP Prefix nodes found with sufficient capacity (need IPv4PerAdapter >= 6)")
		return
	}

	feat := features.New("Prefix/Basic/SingleENI").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, qualifiedNode)
			return ctx
		}).
		Assess("allocate prefixes on single ENI", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeName := ctx.Value(qualifiedNodeContextKey).(string)
			return assessPrefixSingleENI(ctx, t, config, nodeName)
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			restoreOriginalConfig(ctx, config, t)
			return ctx
		}).
		Feature()

	runPrefixFeatureTest(t, feat)
}

// =============================================================================
// Assess Functions
// =============================================================================

// assessPrefixSingleENI tests allocating prefixes on a single ENI
func assessPrefixSingleENI(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) context.Context {
	t.Logf("Testing single ENI prefix allocation on node: %s", nodeName)

	// Step 0: Reset node prefix state to ensure clean starting point
	t.Log("Step 0: Reset node prefix state")
	if err := resetNodePrefixState(ctx, config, t, nodeName, 3*time.Minute); err != nil {
		t.Logf("Warning: resetNodePrefixState failed (node may have no prefixes yet): %v", err)
	}

	// Configure ipv4_prefix_count=5 via Dynamic Config (node-specific ConfigMap)
	t.Log("Configure ipv4_prefix_count=5 via Dynamic Config")
	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, `{"enable_ip_prefix":true,"ipv4_prefix_count":5}`)
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
	err = waitForPrefixAllocation(ctx, config, t, nodeName, 5, 3*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for prefix allocation: %v", err)
	}

	// Verify all prefixes are on a single ENI
	t.Log("Verify all prefixes are on a single ENI")
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

	if len(prefixes) != 5 {
		t.Errorf("expected 5 prefixes, got %d", len(prefixes))
	}

	t.Logf("Successfully allocated %d prefixes on single ENI", len(prefixes))
	return MarkTestSuccess(ctx)
}

// =============================================================================
// Helper Functions
// =============================================================================

// configureIPPrefixCount updates ipv4_prefix_count in the node-specific Dynamic Config ConfigMap.
// The ConfigMap must have been created first via setupNodeDynamicConfig.
// nodeName is required to locate the correct per-node ConfigMap.
func configureIPPrefixCount(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string, count int) error {
	cmName := dynamicConfigName(nodeName)
	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, cmName, "kube-system", cm)
	if err != nil {
		return fmt.Errorf("failed to get dynamic config ConfigMap %s: %w", cmName, err)
	}

	eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
	if err != nil {
		return err
	}

	_, err = eniJson.Set(count, "ipv4_prefix_count")
	if err != nil {
		return err
	}

	cm.Data["eni_conf"] = eniJson.String()
	return config.Client().Resources().Update(ctx, cm)
}

// waitForPrefixAllocation waits for the specified number of prefixes to be allocated
func waitForPrefixAllocation(ctx context.Context, config *envconf.Config, t *testing.T, nodeName string, expectedCount int, timeout time.Duration) error {
	return wait.For(func(ctx context.Context) (done bool, err error) {
		prefixes, err := getAllocatedPrefixes(ctx, config, nodeName)
		if err != nil {
			return false, err
		}

		// Count valid prefixes
		validCount := 0
		for _, prefix := range prefixes {
			if prefix.Status == networkv1beta1.IPPrefixStatusValid {
				validCount++
			}
		}

		if validCount >= expectedCount {
			return true, nil
		}

		t.Logf("Node %s: waiting for %d prefixes, currently have %d valid prefixes",
			nodeName, expectedCount, validCount)

		// Trigger node reconciliation
		node := &networkv1beta1.Node{}
		err = config.Client().Resources().Get(ctx, nodeName, "", node)
		if err == nil {
			triggerNodeCR(ctx, config, t, node)
		}

		return false, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(10*time.Second))
}

// getAllocatedPrefixes gets all allocated prefixes for a node
func getAllocatedPrefixes(ctx context.Context, config *envconf.Config, nodeName string) ([]networkv1beta1.IPPrefix, error) {
	node := &networkv1beta1.Node{}
	err := config.Client().Resources().Get(ctx, nodeName, "", node)
	if err != nil {
		return nil, err
	}

	var prefixes []networkv1beta1.IPPrefix
	for _, nic := range node.Status.NetworkInterfaces {
		if nic.Status == aliyunClient.ENIStatusInUse {
			for _, p := range nic.IPv4Prefix {
				if p.Status == networkv1beta1.IPPrefixStatusValid {
					prefixes = append(prefixes, p)
				}
			}
		}
	}

	return prefixes, nil
}

// countENIsWithPrefixes counts the number of ENIs that have prefixes allocated
func countENIsWithPrefixes(ctx context.Context, config *envconf.Config, nodeName string) (int, error) {
	node := &networkv1beta1.Node{}
	err := config.Client().Resources().Get(ctx, nodeName, "", node)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, nic := range node.Status.NetworkInterfaces {
		if nic.Status == aliyunClient.ENIStatusInUse && len(nic.IPv4Prefix) > 0 {
			count++
		}
	}

	return count, nil
}

// =============================================================================
// Context Keys
// =============================================================================

type qualifiedNodeContextKeyType struct{}

var qualifiedNodeContextKey = qualifiedNodeContextKeyType{}
