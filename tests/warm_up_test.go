//go:build e2e

package tests

import (
	"context"
	"encoding/json"
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

// pauseWarmUp sets warmUpCompleted=true on all Node CRs to pause the warm-up controller
func pauseWarmUp(ctx context.Context, t *testing.T, config *envconf.Config) error {
	t.Log("Pausing warm-up by setting warmUpCompleted=true on all Node CRs")

	nodes := &networkv1beta1.NodeList{}
	err := config.Client().Resources().List(ctx, nodes)
	if err != nil {
		return err
	}

	for _, node := range nodes.Items {
		// Skip exclusive ENI nodes
		if isExclusiveENINode(&node) {
			continue
		}

		// Set warmUpCompleted=true to pause the controller
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

// clearWarmUpState clears warm-up status from all Node CRs to trigger warm-up.
// This should be called AFTER config is updated and terway is restarted,
// so the controller will immediately calculate and execute warm-up.
func clearWarmUpState(ctx context.Context, t *testing.T, config *envconf.Config) error {
	t.Log("Clearing warm-up state from all Node CRs to trigger warm-up")

	nodes := &networkv1beta1.NodeList{}
	err := config.Client().Resources().List(ctx, nodes)
	if err != nil {
		return err
	}

	for _, node := range nodes.Items {
		// Skip exclusive ENI nodes
		if isExclusiveENINode(&node) {
			continue
		}

		// Clear warm-up status fields using JSON merge patch
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

// isIPPoolReleased checks if the IP pool has been fully released on a node.
// Returns true if only primary IPs remain (all secondary IPs have been released).
// Primary IPs are expected to be retained and are not counted as unreleased.
func isIPPoolReleased(node *networkv1beta1.Node) bool {
	for _, eni := range node.Status.NetworkInterfaces {
		if eni.Status != aliyunClient.ENIStatusInUse {
			continue
		}

		// Check IPv4 IPs - only primary IP should remain idle
		if node.Spec.ENISpec != nil && node.Spec.ENISpec.EnableIPv4 {
			for _, ip := range eni.IPv4 {
				if ip.Primary {
					continue // Primary IP is expected to remain
				}
				if ip.Status == networkv1beta1.IPStatusValid && ip.PodID == "" {
					return false // Found unreleased secondary IP
				}
			}
		}

		// Check IPv6 IPs
		if node.Spec.ENISpec != nil && node.Spec.ENISpec.EnableIPv6 && !node.Spec.ENISpec.EnableIPv4 {
			for _, ip := range eni.IPv6 {
				if ip.Status == networkv1beta1.IPStatusValid && ip.PodID == "" {
					return false // Found unreleased IPv6 IP
				}
			}
		}
	}
	return true
}

// clearIPPool releases all idle IPs by setting max_pool_size=0 and min_pool_size=0.
// Note: Primary IPs will NOT be released as this is expected behavior - primary IPs are
// always retained on each ENI.
func clearIPPool(ctx context.Context, t *testing.T, config *envconf.Config) error {
	t.Log("Clearing IP pool by setting max_pool_size=0, min_pool_size=0")

	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		return err
	}

	eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
	if err != nil {
		return err
	}

	// Clear warm-up config
	eniJson.Delete("ip_warm_up_size")

	// Set pool size to 0 to release all secondary IPs (primary IPs are always retained)
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

	// Wait for IP pool to be fully released (only primary IPs should remain)
	t.Log("Waiting for IP pool to be released (primary IPs will be retained)")
	err = wait.For(func(ctx context.Context) (done bool, err error) {
		nodes := &networkv1beta1.NodeList{}
		err = config.Client().Resources().List(ctx, nodes)
		if err != nil {
			return false, err
		}

		for _, node := range nodes.Items {
			if isExclusiveENINode(&node) {
				continue
			}
			if !isIPPoolReleased(&node) {
				t.Logf("Node %s IP pool not fully released yet, triggering update", node.Name)

				_ = triggerNode(ctx, config, t, &node)

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

// waitForWarmUpCompletion waits for warm-up to complete on all shared ENI nodes
func waitForWarmUpCompletion(ctx context.Context, t *testing.T, config *envconf.Config,
	expectedTarget int, timeout time.Duration) error {

	return wait.For(func(ctx context.Context) (done bool, err error) {
		nodes := &networkv1beta1.NodeList{}
		err = config.Client().Resources().List(ctx, nodes)
		if err != nil {
			return false, err
		}

		allCompleted := true
		for _, node := range nodes.Items {
			if isExclusiveENINode(&node) {
				continue
			}

			t.Logf("Node %s: warmUpTarget=%d, warmUpAllocatedCount=%d, warmUpCompleted=%v",
				node.Name, node.Status.WarmUpTarget, node.Status.WarmUpAllocatedCount, node.Status.WarmUpCompleted)

			if !node.Status.WarmUpCompleted {
				allCompleted = false
			}
		}

		return allCompleted, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(5*time.Second))
}

// verifyWarmUpStatus verifies warm-up status fields on all shared ENI nodes
func verifyWarmUpStatus(ctx context.Context, t *testing.T, config *envconf.Config,
	expectedTarget int) error {

	nodes := &networkv1beta1.NodeList{}
	err := config.Client().Resources().List(ctx, nodes)
	if err != nil {
		return err
	}

	for _, node := range nodes.Items {
		if isExclusiveENINode(&node) {
			continue
		}

		t.Logf("Verifying node %s: warmUpTarget=%d, warmUpAllocatedCount=%d, warmUpCompleted=%v",
			node.Name, node.Status.WarmUpTarget, node.Status.WarmUpAllocatedCount, node.Status.WarmUpCompleted)

		// Verify warm-up target matches configuration
		if node.Status.WarmUpTarget != expectedTarget {
			t.Errorf("Node %s: expected warmUpTarget=%d, got %d",
				node.Name, expectedTarget, node.Status.WarmUpTarget)
		}

		// Verify warm-up is completed
		if !node.Status.WarmUpCompleted {
			t.Errorf("Node %s: expected warmUpCompleted=true, got false", node.Name)
		}

		// Verify allocated count meets target
		if node.Status.WarmUpAllocatedCount < expectedTarget {
			t.Errorf("Node %s: expected warmUpAllocatedCount >= %d, got %d",
				node.Name, expectedTarget, node.Status.WarmUpAllocatedCount)
		}

		// Verify idle IPs
		idleCount := countIdleIPs(&node)
		t.Logf("Node %s: idle IPs = %d", node.Name, idleCount)
	}

	return nil
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

// TestIPWarmUp tests the IP warm-up feature
// This test only covers centralized IPAM mode (ipam_type == "crd")
// and shared ENI nodes (ECS shared ENI and Lingjun shared ENI)
func TestIPWarmUp(t *testing.T) {
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
		t.Skipf("TestIPWarmUp requires terway-eniip daemonset, current: %s", GetCachedTerwayDaemonSetName())
		return
	}

	// Pre-check: terway version must be >= v1.16.4
	if !RequireTerwayVersion("v1.16.4") {
		t.Skipf("TestIPWarmUp requires terway version >= v1.16.4, current version: %s", GetCachedTerwayVersion())
		return
	}

	feature := features.New("IPWarmUp").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Check if cluster has shared ENI nodes
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("failed to discover node types: %v", err)
			}

			if len(nodeInfo.ECSSharedENINodes) == 0 && len(nodeInfo.LingjunSharedENINodes) == 0 {
				t.Skipf("TestIPWarmUp requires shared ENI nodes (ECS or Lingjun), none found")
			}

			t.Logf("Found %d ECS shared ENI nodes, %d Lingjun shared ENI nodes",
				len(nodeInfo.ECSSharedENINodes), len(nodeInfo.LingjunSharedENINodes))

			return ctx
		}).
		Assess("test 1: basic warm-up functionality", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			t.Log("Test 1: Verify basic warm-up functionality")

			// Step 1: Pause warm-up controller first
			t.Log("Step 1.1: Pause warm-up by setting warmUpCompleted=true")
			err := pauseWarmUp(ctx, t, config)
			if err != nil {
				t.Fatalf("failed to pause warm-up: %v", err)
			}

			// Step 2: Clear IP pool
			t.Log("Step 1.2: Clear IP pool to start from clean state")
			err = clearIPPool(ctx, t, config)
			if err != nil {
				t.Fatalf("failed to clear IP pool: %v", err)
			}

			// Step 3: Configure warm-up
			t.Log("Step 1.3: Configure warm-up with ip_warm_up_size=10, max_pool_size=20, min_pool_size=1")
			err = configureWarmUp(ctx, t, config, 10, 20, 1)
			if err != nil {
				t.Fatalf("failed to configure warm-up: %v", err)
			}

			// Step 4: Restart terway-eniip to apply new config
			t.Log("Step 1.4: Restart terway-eniip to apply new config")
			err = restartTerway(ctx, config)
			if err != nil {
				t.Fatalf("failed to restart terway: %v", err)
			}

			// Step 5: Clear warm-up state to trigger warm-up
			t.Log("Step 1.5: Clear warm-up state to trigger warm-up")
			err = clearWarmUpState(ctx, t, config)
			if err != nil {
				t.Fatalf("failed to clear warm-up state: %v", err)
			}

			// Step 6: Wait for warm-up completion
			t.Log("Step 1.6: Wait for warm-up completion")
			err = waitForWarmUpCompletion(ctx, t, config, 10, 5*time.Minute)
			if err != nil {
				t.Fatalf("failed to wait for warm-up completion: %v", err)
			}

			// Step 7: Verify warm-up status
			t.Log("Step 1.7: Verify warm-up status")
			err = verifyWarmUpStatus(ctx, t, config, 10)
			if err != nil {
				t.Fatalf("failed to verify warm-up status: %v", err)
			}

			// Step 8: Verify idle IP count
			t.Log("Step 1.8: Verify idle IP count >= warm-up target")
			nodes := &networkv1beta1.NodeList{}
			err = config.Client().Resources().List(ctx, nodes)
			if err != nil {
				t.Fatalf("failed to list nodes: %v", err)
			}

			for _, node := range nodes.Items {
				if isExclusiveENINode(&node) {
					continue
				}
				idleCount := countIdleIPs(&node)
				t.Logf("Node %s: idle IPs = %d (expected >= 10)", node.Name, idleCount)
				if idleCount < 10 {
					t.Errorf("Node %s: expected idle IPs >= 10, got %d", node.Name, idleCount)
				}
			}

			t.Log("Test 1 completed: Basic warm-up functionality verified")
			return ctx
		}).
		Assess("test 2: warm-up larger than max_pool_size", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			t.Log("Test 2: Verify warm-up larger than max_pool_size")

			// Step 1: Pause warm-up controller first
			t.Log("Step 2.1: Pause warm-up by setting warmUpCompleted=true")
			err := pauseWarmUp(ctx, t, config)
			if err != nil {
				t.Fatalf("failed to pause warm-up: %v", err)
			}

			// Step 2: Clear IP pool
			t.Log("Step 2.2: Clear IP pool to start from clean state")
			err = clearIPPool(ctx, t, config)
			if err != nil {
				t.Fatalf("failed to clear IP pool: %v", err)
			}

			// Step 3: Configure warm-up with size larger than max_pool_size
			t.Log("Step 2.3: Configure warm-up with ip_warm_up_size=5, max_pool_size=5, min_pool_size=0")
			err = configureWarmUp(ctx, t, config, 10, 5, 0)
			if err != nil {
				t.Fatalf("failed to configure warm-up: %v", err)
			}

			// Step 4: Restart terway-eniip to apply new config
			t.Log("Step 2.4: Restart terway-eniip to apply new config")
			err = restartTerway(ctx, config)
			if err != nil {
				t.Fatalf("failed to restart terway: %v", err)
			}

			// Step 5: Clear warm-up state to trigger warm-up
			t.Log("Step 2.5: Clear warm-up state to trigger warm-up")
			err = clearWarmUpState(ctx, t, config)
			if err != nil {
				t.Fatalf("failed to clear warm-up state: %v", err)
			}

			// Step 6: Wait for warm-up completion
			t.Log("Step 2.6: Wait for warm-up completion")
			err = waitForWarmUpCompletion(ctx, t, config, 10, 5*time.Minute)
			if err != nil {
				t.Fatalf("failed to wait for warm-up completion: %v", err)
			}

			// Step 7: Verify warm-up status
			t.Log("Step 2.7: Verify warm-up status")
			err = verifyWarmUpStatus(ctx, t, config, 10)
			if err != nil {
				t.Fatalf("failed to verify warm-up status: %v", err)
			}

			// Step 8: Wait for pool management to release excess IPs
			t.Log("Step 2.8: Wait for pool management to release excess IPs to max_pool_size")
			err = wait.For(func(ctx context.Context) (done bool, err error) {
				nodes := &networkv1beta1.NodeList{}
				err = config.Client().Resources().List(ctx, nodes)
				if err != nil {
					return false, err
				}

				allWithinLimit := true
				for _, node := range nodes.Items {
					if isExclusiveENINode(&node) {
						continue
					}
					idleCount := countIdleIPs(&node)
					t.Logf("Node %s: idle IPs = %d (expected <= 10)", node.Name, idleCount)
					if idleCount > 10 {
						allWithinLimit = false
					}

					_ = triggerNode(ctx, config, t, &node)
				}
				return allWithinLimit, nil
			}, wait.WithTimeout(5*time.Minute), wait.WithInterval(5*time.Second))
			if err != nil {
				t.Fatalf("failed to wait for pool to be within max_pool_size: %v", err)
			}

			t.Log("Test 2 completed: Warm-up larger than max_pool_size verified")
			return ctx
		}).
		Assess("test 3: warm-up state not re-initialized on restart", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			t.Log("Test 3: Verify warm-up state is not re-initialized on terway restart")

			// Step 1: Record current warm-up status
			t.Log("Step 3.1: Record current warm-up status")
			nodes := &networkv1beta1.NodeList{}
			err := config.Client().Resources().List(ctx, nodes)
			if err != nil {
				t.Fatalf("failed to list nodes: %v", err)
			}

			type nodeWarmUpStatus struct {
				target    int
				allocated int
				completed bool
			}
			beforeStatus := make(map[string]nodeWarmUpStatus)

			for _, node := range nodes.Items {
				if isExclusiveENINode(&node) {
					continue
				}
				beforeStatus[node.Name] = nodeWarmUpStatus{
					target:    node.Status.WarmUpTarget,
					allocated: node.Status.WarmUpAllocatedCount,
					completed: node.Status.WarmUpCompleted,
				}
				t.Logf("Before restart - Node %s: warmUpTarget=%d, warmUpAllocatedCount=%d, warmUpCompleted=%v",
					node.Name, node.Status.WarmUpTarget, node.Status.WarmUpAllocatedCount, node.Status.WarmUpCompleted)
			}

			// Step 2: Restart terway-eniip
			t.Log("Step 3.2: Restart terway-eniip")
			err = restartTerway(ctx, config)
			if err != nil {
				t.Fatalf("failed to restart terway: %v", err)
			}

			// Step 3: Wait a moment and verify warm-up status is unchanged
			t.Log("Step 3.3: Wait and verify warm-up status is unchanged")
			time.Sleep(30 * time.Second)

			nodes = &networkv1beta1.NodeList{}
			err = config.Client().Resources().List(ctx, nodes)
			if err != nil {
				t.Fatalf("failed to list nodes: %v", err)
			}

			for _, node := range nodes.Items {
				if isExclusiveENINode(&node) {
					continue
				}

				before, ok := beforeStatus[node.Name]
				if !ok {
					continue
				}

				t.Logf("After restart - Node %s: warmUpTarget=%d, warmUpAllocatedCount=%d, warmUpCompleted=%v",
					node.Name, node.Status.WarmUpTarget, node.Status.WarmUpAllocatedCount, node.Status.WarmUpCompleted)

				// Warm-up target should not change
				if node.Status.WarmUpTarget != before.target {
					t.Errorf("Node %s: warmUpTarget changed from %d to %d after restart",
						node.Name, before.target, node.Status.WarmUpTarget)
				}

				// Warm-up completed status should not change (from completed to not completed)
				if before.completed && !node.Status.WarmUpCompleted {
					t.Errorf("Node %s: warmUpCompleted changed from true to false after restart", node.Name)
				}
			}

			t.Log("Test 3 completed: Warm-up state persistence verified")
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			t.Log("Teardown: Restoring default configuration")

			cm := &corev1.ConfigMap{}
			err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
			if err != nil {
				t.Logf("failed to get eni-config during teardown: %v", err)
				return ctx
			}

			eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
			if err != nil {
				t.Logf("failed to parse eni_conf during teardown: %v", err)
				return ctx
			}

			// Remove warm-up configuration
			eniJson.Delete("ip_warm_up_size")

			// Restore default pool settings
			_, err = eniJson.Set(5, "max_pool_size")
			if err != nil {
				t.Logf("failed to set max_pool_size: %v", err)
			}
			_, err = eniJson.Set(0, "min_pool_size")
			if err != nil {
				t.Logf("failed to set min_pool_size: %v", err)
			}
			eniJson.Delete("ip_pool_sync_period")

			cm.Data["eni_conf"] = eniJson.String()
			err = config.Client().Resources().Update(ctx, cm)
			if err != nil {
				t.Logf("failed to update eni-config during teardown: %v", err)
			}

			return ctx
		}).
		Feature()

	testenv.Test(t, feature)
}
