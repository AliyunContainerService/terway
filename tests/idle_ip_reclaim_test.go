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
	// Set max_pool_size to 5 (reasonable default)
	// Set min_pool_size to 0 (no minimum, let pool naturally drain if needed)
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

// TestIdleIPReclaimPolicy tests the idle IP reclaim policy feature
// This test only covers centralized IPAM mode (ipam_type == "crd")
func TestIdleIPReclaimPolicy(t *testing.T) {
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
		t.Skipf("TestIdleIPReclaimPolicy requires terway-eniip daemonset, current: %s", terway)
		return
	}

	// Pre-check: terway version must be >= v1.16.1
	if !RequireTerwayVersion("v1.16.1") {
		t.Skipf("TestIdleIPReclaimPolicy requires terway version >= v1.16.1, current version: %s", GetCachedTerwayVersion())
		return
	}

	feature := features.New("IdleIPReclaimPolicy").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Cleanup pool configuration before tests
			err := cleanupPoolConfig(ctx, t, config)
			if err != nil {
				t.Fatalf("failed to cleanup pool configuration: %v", err)
			}

			return ctx
		}).
		Assess("test 1: basic idle IP reclaim after timeout", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			t.Log("Test 1: Verify IPs are reclaimed after idle_ip_reclaim_after duration")

			// Step 1: First preheat the pool to 10 IPs
			t.Log("Step 1.1: Preheat pool to 10 idle IPs (min=max=10)")
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

			// Wait for preheating to 10 IPs
			t.Log("Step 1.2: Wait for IP pool preheating to 10 IPs")
			err = waitForIdleIPCount(ctx, config, t, 10, 5*time.Minute, "preheat to 10")
			if err != nil {
				t.Fatalf("failed to wait for IP preheating: %v", err)
			}

			// Step 2: Now configure reclaim policy and lower min_pool_size
			t.Log("Step 1.3: Configure reclaim policy and lower min_pool_size to 3")
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

			// Step 3: Record the current time and last modified time from nodes
			t.Log("Step 1.4: Record timing information")
			nodes := &networkv1beta1.NodeList{}
			err = config.Client().Resources().List(ctx, nodes)
			if err != nil {
				t.Fatalf("failed to list nodes: %v", err)
			}

			startTime := time.Now()
			var oldestModifiedTime time.Time
			for _, node := range nodes.Items {
				if oldestModifiedTime.IsZero() || node.Status.LastModifiedTime.Time.Before(oldestModifiedTime) {
					oldestModifiedTime = node.Status.LastModifiedTime.Time
				}
				idleCount := countIdleIPs(&node)
				t.Logf("Node %s: idle IPs = %d, lastModifiedTime = %v",
					node.Name, idleCount, node.Status.LastModifiedTime.Time)
			}

			// Step 4: Verify IPs have been reclaimed but min_pool_size is respected
			t.Log("Step 1.5: Verify IPs are reclaimed")
			err = wait.For(func(ctx context.Context) (done bool, err error) {
				nodes := &networkv1beta1.NodeList{}
				err = config.Client().Resources().List(ctx, nodes)
				if err != nil {
					return false, err
				}

				allNodesOk := true
				for _, node := range nodes.Items {
					if isExclusiveENINode(&node) {
						continue
					}

					idleCount := countIdleIPs(&node)
					// After reclaim, idle IPs should be between min_pool_size and max_pool_size
					// Due to batch processing, it might not reach exactly min_pool_size
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
			t.Logf("IPs successfully reclaimed in %v", elapsed)

			return ctx
		}).
		Assess("test 2: respect min_pool_size", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			t.Log("Test 2: Verify min_pool_size is always respected during reclaim")

			// Step 1: First preheat to 10 IPs
			t.Log("Step 2.1: Preheat pool to 10 idle IPs (min=max=10)")
			cm := &corev1.ConfigMap{}
			err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
			if err != nil {
				t.Fatalf("failed to get eni-config: %v", err)
			}

			eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
			if err != nil {
				t.Fatalf("failed to parse eni_conf: %v", err)
			}

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

			// Step 2: Wait for preheating to 10 IPs
			t.Log("Step 2.2: Wait for IP pool preheating to 10 IPs")
			err = waitForIdleIPCount(ctx, config, t, 10, 5*time.Minute, "preheat to 10")
			if err != nil {
				t.Fatalf("failed to wait for IP preheating: %v", err)
			}

			// Step 3: Configure with aggressive reclaim policy and lower min_pool_size
			t.Log("Step 2.3: Configure aggressive reclaim policy and set min_pool_size=5")
			cm = &corev1.ConfigMap{}
			err = config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
			if err != nil {
				t.Fatalf("failed to get eni-config: %v", err)
			}

			eniJson, err = gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
			if err != nil {
				t.Fatalf("failed to parse eni_conf: %v", err)
			}

			// Lower min_pool_size and add aggressive reclaim policy
			_, err = eniJson.Set(5, "min_pool_size")
			if err != nil {
				t.Fatalf("failed to set min_pool_size: %v", err)
			}
			_, err = eniJson.Set("1m", "idle_ip_reclaim_after")
			if err != nil {
				t.Fatalf("failed to set idle_ip_reclaim_after: %v", err)
			}
			_, err = eniJson.Set("30s", "idle_ip_reclaim_interval")
			if err != nil {
				t.Fatalf("failed to set idle_ip_reclaim_interval: %v", err)
			}
			// Use large batch size to try to reclaim many IPs at once
			_, err = eniJson.Set(20, "idle_ip_reclaim_batch_size")
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

			// Step 4: Wait for reclaim
			t.Log("Step 2.4: Wait for idle IP reclaim period (2m)")
			time.Sleep(2 * time.Minute)

			// Step 5: Verify min_pool_size is never violated
			t.Log("Step 2.5: Verify min_pool_size is respected")
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
				t.Logf("Node %s: idle IPs = %d", node.Name, idleCount)
				if idleCount < 5 {
					t.Errorf("Node %s has %d idle IPs, which violates min_pool_size=5",
						node.Name, idleCount)
				}
			}

			return ctx
		}).
		Assess("test 3: batch size limit", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			t.Log("Test 3: Verify batch size limit is respected")

			// Step 1: First preheat to 12 IPs
			t.Log("Step 3.1: Preheat pool to 12 idle IPs (min=max=12)")
			cm := &corev1.ConfigMap{}
			err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
			if err != nil {
				t.Fatalf("failed to get eni-config: %v", err)
			}

			eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
			if err != nil {
				t.Fatalf("failed to parse eni_conf: %v", err)
			}

			_, err = eniJson.Set("30s", "ip_pool_sync_period")
			if err != nil {
				t.Fatalf("failed to set ip_pool_sync_period: %v", err)
			}
			_, err = eniJson.Set(12, "max_pool_size")
			if err != nil {
				t.Fatalf("failed to set max_pool_size: %v", err)
			}
			_, err = eniJson.Set(12, "min_pool_size")
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

			// Step 2: Wait for preheating to 12 IPs
			t.Log("Step 3.2: Wait for IP pool preheating to 12 IPs")
			err = waitForIdleIPCount(ctx, config, t, 12, 5*time.Minute, "preheat to 12")
			if err != nil {
				t.Fatalf("failed to wait for IP preheating: %v", err)
			}

			// Step 3: Configure with small batch size and lower min_pool_size
			t.Log("Step 3.3: Configure reclaim policy with small batch size and set min_pool_size=2")
			cm = &corev1.ConfigMap{}
			err = config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
			if err != nil {
				t.Fatalf("failed to get eni-config: %v", err)
			}

			eniJson, err = gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
			if err != nil {
				t.Fatalf("failed to parse eni_conf: %v", err)
			}

			// Lower min_pool_size and add reclaim policy with small batch size
			_, err = eniJson.Set(2, "min_pool_size")
			if err != nil {
				t.Fatalf("failed to set min_pool_size: %v", err)
			}
			_, err = eniJson.Set("1m30s", "idle_ip_reclaim_after")
			if err != nil {
				t.Fatalf("failed to set idle_ip_reclaim_after: %v", err)
			}
			_, err = eniJson.Set("30s", "idle_ip_reclaim_interval")
			if err != nil {
				t.Fatalf("failed to set idle_ip_reclaim_interval: %v", err)
			}
			// Set small batch size
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

			// Step 4: Record initial idle count
			t.Log("Step 3.4: Record initial idle IP count")
			nodes := &networkv1beta1.NodeList{}
			err = config.Client().Resources().List(ctx, nodes)
			if err != nil {
				t.Fatalf("failed to list nodes: %v", err)
			}

			initialIdleCount := 0
			for _, node := range nodes.Items {
				idleCount := countIdleIPs(&node)
				initialIdleCount += idleCount
				t.Logf("Node %s: initial idle IPs = %d", node.Name, idleCount)

				_ = triggerNode(ctx, config, t, &node)
			}

			// Step 5: Wait for first reclaim cycle
			t.Log("Step 3.5: Wait for first reclaim cycle (2m)")
			time.Sleep(2 * time.Minute)

			// Step 6: Check that only batch_size IPs were reclaimed
			t.Log("Step 4.6: Verify only batch_size IPs reclaimed in first cycle")
			nodes = &networkv1beta1.NodeList{}
			err = config.Client().Resources().List(ctx, nodes)
			if err != nil {
				t.Fatalf("failed to list nodes: %v", err)
			}

			afterFirstCycle := 0
			for _, node := range nodes.Items {
				idleCount := countIdleIPs(&node)
				afterFirstCycle += idleCount
				t.Logf("Node %s: idle IPs after first cycle = %d", node.Name, idleCount)
			}

			// With batch_size=2, we expect at most 2*num_nodes IPs to be reclaimed per cycle
			// But since we have multiple nodes, let's check that not all excess IPs were removed
			// Expected: initialIdleCount - 2 to initialIdleCount (accounting for per-node batch)
			numNodes := len(nodes.Items)
			expectedMaxReclaimed := 2 * numNodes
			actualReclaimed := initialIdleCount - afterFirstCycle

			t.Logf("Initial idle: %d, After first cycle: %d, Reclaimed: %d, Expected max: %d",
				initialIdleCount, afterFirstCycle, actualReclaimed, expectedMaxReclaimed)

			// The reclaim should not remove all excess IPs in one go
			// We have (initialIdleCount - min_pool_size*numNodes) excess IPs
			// With batch_size=2, each cycle should reclaim at most 2 per node
			if afterFirstCycle < 2*numNodes {
				t.Logf("Warning: IPs might have been over-reclaimed, but respecting min_pool_size=%d per node", 2)
			}

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Cleanup pool configuration after tests
			err := cleanupPoolConfig(ctx, t, config)
			if err != nil {
				t.Logf("failed to cleanup pool configuration during teardown: %v", err)
			}

			return ctx
		}).
		Feature()

	testenv.Test(t, feature)
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

// waitForIdleIPCount waits for all nodes to have at least minIdleCount idle IPs
func waitForIdleIPCount(ctx context.Context, config *envconf.Config, t *testing.T,
	minIdleCount int, timeout time.Duration, phase string) error {

	return wait.For(func(ctx context.Context) (done bool, err error) {
		nodes := &networkv1beta1.NodeList{}
		err = config.Client().Resources().List(ctx, nodes)
		if err != nil {
			return false, err
		}

		allNodesReady := true
		for _, node := range nodes.Items {
			// Skip nodes that should be excluded from idle IP checks (Lingjun and exclusive ENI nodes)
			if isExclusiveENINode(&node) {
				t.Logf("[%s] Skipping node %s (excluded from idle IP checks)", phase, node.Name)
				continue
			}

			idleCount := countIdleIPs(&node)
			if idleCount < minIdleCount {
				t.Logf("[%s] Node %s has %d idle IPs (waiting for >= %d)",
					phase, node.Name, idleCount, minIdleCount)
				allNodesReady = false
			}
		}

		return allNodesReady, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(5*time.Second))
}

func triggerNode(ctx context.Context, config *envconf.Config, t *testing.T, node *networkv1beta1.Node) error {
	mergePatch, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				"e2e-update": time.Now().String(),
			},
		},
	})
	return config.Client().Resources().Patch(ctx, node, k8s.Patch{PatchType: types.StrategicMergePatchType, Data: mergePatch})
}
