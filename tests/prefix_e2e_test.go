//go:build e2e

package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// =============================================================================
// IP Prefix End-to-End Tests
// =============================================================================

// ipPrefixNodesContextKey is used to store discovered IP Prefix nodes in context
type ipPrefixNodesContextKeyType struct{}

var ipPrefixNodesContextKey = ipPrefixNodesContextKeyType{}

// TestPrefix_E2E_PodIPAllocation is the core end-to-end test for IP Prefix mode.
// It uses dedicated IP Prefix nodes (with k8s.aliyun.com/ip-prefix=true label)
// and validates that pods scheduled to these nodes receive IPs from /28 prefix ranges.
//
// This test differs from TestPrefix_Basic_* tests in that:
//   - It uses a dedicated node pool (ip-prefix) with ignore-by-terway initially set
//   - It uses a shared ConfigMap (e2e-ip-prefix) instead of per-node Dynamic Config
//   - It validates actual Pod IP allocation against prefix CIDR ranges
//   - It is a more production-like end-to-end validation
func TestPrefix_E2E_PodIPAllocation(t *testing.T) {
	skipIfNotPrefixTestEnvironment(t)

	// Discover IP Prefix nodes
	ctx := context.Background()
	nodeInfo, err := DiscoverNodeTypes(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types: %v", err)
	}

	if len(nodeInfo.ECSIPPrefixNodes) == 0 {
		t.Skip("No ECS IP Prefix nodes found (nodes with k8s.aliyun.com/ip-prefix=true label)")
		return
	}

	t.Logf("Found %d IP Prefix nodes", len(nodeInfo.ECSIPPrefixNodes))

	feat := features.New("Prefix/E2E/PodIPAllocation").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		WithLabel("test-type", "e2e").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Store nodes in context for teardown
			ctx = context.WithValue(ctx, ipPrefixNodesContextKey, nodeInfo.ECSIPPrefixNodes)

			// Setup IP Prefix nodes: create ConfigMap, set labels, wait for terway
			var setupErr error
			ctx, setupErr = setupIPPrefixNodes(ctx, t, config, nodeInfo.ECSIPPrefixNodes, 3)
			if setupErr != nil {
				t.Fatalf("Failed to setup IP Prefix nodes: %v", setupErr)
			}

			// Restart terway to apply config
			if err := restartTerway(ctx, config); err != nil {
				t.Fatalf("Failed to restart terway: %v", err)
			}

			// Wait for prefix allocation on all nodes
			for _, node := range nodeInfo.ECSIPPrefixNodes {
				if err := waitForPrefixAllocation(ctx, config, t, node.Name, 3, 5*time.Minute); err != nil {
					t.Fatalf("Failed waiting for prefix allocation on node %s: %v", node.Name, err)
				}
			}

			return ctx
		}).
		Assess("single pod IP in prefix range", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessSinglePodIPInPrefix(ctx, t, config, nodeInfo.ECSIPPrefixNodes[0].Name)
		}).
		Assess("multiple pods IP allocation", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessMultiplePodsIPInPrefix(ctx, t, config, nodeInfo.ECSIPPrefixNodes[0].Name)
		}).
		Assess("pod recreation preserves prefix allocation", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessPodRecreationPrefix(ctx, t, config, nodeInfo.ECSIPPrefixNodes[0].Name)
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodes, _ := ctx.Value(ipPrefixNodesContextKey).([]corev1.Node)
			if len(nodes) > 0 {
				teardownIPPrefixNodes(ctx, t, config, nodes)
			}
			return ctx
		}).
		Feature()

	runPrefixFeatureTest(t, feat)
}

// TestPrefix_E2E_NodeSetup validates the node configuration flow for IP Prefix mode.
// It verifies that the ConfigMap, node labels, and Node CR are correctly configured.
func TestPrefix_E2E_NodeSetup(t *testing.T) {
	// Pre-checks
	skipIfNotPrefixTestEnvironment(t)

	ctx := context.Background()
	nodeInfo, err := DiscoverNodeTypes(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types: %v", err)
	}

	if len(nodeInfo.ECSIPPrefixNodes) == 0 {
		t.Skip("No ECS IP Prefix nodes found")
		return
	}

	feat := features.New("Prefix/E2E/NodeSetup").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		WithLabel("test-type", "e2e").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = context.WithValue(ctx, ipPrefixNodesContextKey, nodeInfo.ECSIPPrefixNodes)
			var setupErr error
			ctx, setupErr = setupIPPrefixNodes(ctx, t, config, nodeInfo.ECSIPPrefixNodes, 3)
			if setupErr != nil {
				t.Fatalf("Failed to setup IP Prefix nodes: %v", setupErr)
			}
			if err := restartTerway(ctx, config); err != nil {
				t.Fatalf("Failed to restart terway: %v", err)
			}
			return ctx
		}).
		Assess("verify node configuration", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessNodeSetupVerification(ctx, t, config, nodeInfo.ECSIPPrefixNodes)
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodes, _ := ctx.Value(ipPrefixNodesContextKey).([]corev1.Node)
			if len(nodes) > 0 {
				teardownIPPrefixNodes(ctx, t, config, nodes)
			}
			return ctx
		}).
		Feature()

	runPrefixFeatureTest(t, feat)
}

// =============================================================================
// Assess Functions
// =============================================================================

// assessSinglePodIPInPrefix creates a single pod on an IP Prefix node and verifies
// its IP is within a /28 prefix range.
func assessSinglePodIPInPrefix(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) context.Context {
	t.Logf("Testing single pod IP allocation on IP Prefix node: %s", nodeName)

	affinityLabels := GetNodeAffinityForType(NodeTypeECSIPPrefix)
	pod := NewPod("prefix-e2e-single", config.Namespace()).
		WithContainer("nginx", nginxImage, nil).
		WithNodeAffinity(affinityLabels)

	if err := config.Client().Resources().Create(ctx, pod.Pod); err != nil {
		t.Fatalf("Failed to create pod: %v", err)
	}
	ctx = AddResourcesForCleanup(ctx, pod.Pod)

	// Wait for pod to be running
	err := wait.For(
		conditions.New(config.Client().Resources()).PodRunning(pod.Pod),
		wait.WithTimeout(3*time.Minute),
		wait.WithInterval(5*time.Second),
	)
	if err != nil {
		t.Fatalf("Pod did not reach Running state: %v", err)
	}

	// Re-fetch pod to get assigned IP and node
	if err := config.Client().Resources().Get(ctx, pod.Name, pod.Namespace, pod.Pod); err != nil {
		t.Fatalf("Failed to get pod: %v", err)
	}

	podIP := pod.Status.PodIP
	if podIP == "" {
		t.Fatal("Pod has no IP assigned")
	}

	t.Logf("Pod %s assigned IP: %s on node: %s", pod.Name, podIP, pod.Spec.NodeName)

	// Verify IP is within a prefix range
	if !verifyPodIPInPrefix(ctx, t, config, podIP, pod.Spec.NodeName) {
		t.Errorf("Pod IP %s is not within any allocated prefix on node %s", podIP, pod.Spec.NodeName)
	}

	return MarkTestSuccess(ctx)
}

// assessMultiplePodsIPInPrefix creates multiple pods and verifies all IPs are within prefix ranges.
func assessMultiplePodsIPInPrefix(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) context.Context {
	t.Logf("Testing multiple pod IP allocation on IP Prefix node: %s", nodeName)

	const podCount = 5
	affinityLabels := GetNodeAffinityForType(NodeTypeECSIPPrefix)
	var pods []*Pod

	// Create multiple pods
	for i := 0; i < podCount; i++ {
		pod := NewPod(fmt.Sprintf("prefix-e2e-multi-%d", i), config.Namespace()).
			WithContainer("nginx", nginxImage, nil).
			WithNodeAffinity(affinityLabels)

		if err := config.Client().Resources().Create(ctx, pod.Pod); err != nil {
			t.Fatalf("Failed to create pod %d: %v", i, err)
		}
		ctx = AddResourcesForCleanup(ctx, pod.Pod)
		pods = append(pods, pod)
	}

	// Wait for all pods to be running
	for _, pod := range pods {
		err := wait.For(
			conditions.New(config.Client().Resources()).PodRunning(pod.Pod),
			wait.WithTimeout(3*time.Minute),
			wait.WithInterval(5*time.Second),
		)
		if err != nil {
			t.Fatalf("Pod %s did not reach Running state: %v", pod.Name, err)
		}
	}

	// Verify all pod IPs are within prefix ranges
	allInPrefix := true
	for _, pod := range pods {
		// Re-fetch pod to get current status
		if err := config.Client().Resources().Get(ctx, pod.Name, pod.Namespace, pod.Pod); err != nil {
			t.Fatalf("Failed to get pod %s: %v", pod.Name, err)
		}

		podIP := pod.Status.PodIP
		if podIP == "" {
			t.Errorf("Pod %s has no IP assigned", pod.Name)
			allInPrefix = false
			continue
		}

		t.Logf("Pod %s assigned IP: %s on node: %s", pod.Name, podIP, pod.Spec.NodeName)

		if !verifyPodIPInPrefix(ctx, t, config, podIP, pod.Spec.NodeName) {
			t.Errorf("Pod %s IP %s is not within any allocated prefix", pod.Name, podIP)
			allInPrefix = false
		}
	}

	if !allInPrefix {
		t.Error("Not all pod IPs are within allocated prefix ranges")
	}

	t.Logf("Successfully verified %d pods have IPs within prefix ranges", podCount)
	return MarkTestSuccess(ctx)
}

// assessPodRecreationPrefix deletes a pod and recreates it, verifying the new IP
// is still within a prefix range.
func assessPodRecreationPrefix(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) context.Context {
	t.Logf("Testing pod recreation on IP Prefix node: %s", nodeName)

	affinityLabels := GetNodeAffinityForType(NodeTypeECSIPPrefix)

	// Create initial pod
	pod := NewPod("prefix-e2e-recreate", config.Namespace()).
		WithContainer("nginx", nginxImage, nil).
		WithNodeAffinity(affinityLabels)

	if err := config.Client().Resources().Create(ctx, pod.Pod); err != nil {
		t.Fatalf("Failed to create pod: %v", err)
	}

	err := wait.For(
		conditions.New(config.Client().Resources()).PodRunning(pod.Pod),
		wait.WithTimeout(3*time.Minute),
		wait.WithInterval(5*time.Second),
	)
	if err != nil {
		t.Fatalf("Pod did not reach Running state: %v", err)
	}

	// Get original IP
	if err := config.Client().Resources().Get(ctx, pod.Name, pod.Namespace, pod.Pod); err != nil {
		t.Fatalf("Failed to get pod: %v", err)
	}
	originalIP := pod.Status.PodIP
	originalNode := pod.Spec.NodeName
	t.Logf("Original pod IP: %s on node: %s", originalIP, originalNode)

	// Delete the pod
	if err := config.Client().Resources().Delete(ctx, pod.Pod); err != nil {
		t.Fatalf("Failed to delete pod: %v", err)
	}
	err = wait.For(
		conditions.New(config.Client().Resources()).ResourceDeleted(pod.Pod),
		wait.WithTimeout(1*time.Minute),
		wait.WithInterval(3*time.Second),
	)
	if err != nil {
		t.Fatalf("Pod deletion did not complete: %v", err)
	}
	t.Log("Original pod deleted")

	// Recreate the pod with a new name
	newPod := NewPod("prefix-e2e-recreate-new", config.Namespace()).
		WithContainer("nginx", nginxImage, nil).
		WithNodeAffinity(affinityLabels)

	if err := config.Client().Resources().Create(ctx, newPod.Pod); err != nil {
		t.Fatalf("Failed to create new pod: %v", err)
	}
	ctx = AddResourcesForCleanup(ctx, newPod.Pod)

	err = wait.For(
		conditions.New(config.Client().Resources()).PodRunning(newPod.Pod),
		wait.WithTimeout(3*time.Minute),
		wait.WithInterval(5*time.Second),
	)
	if err != nil {
		t.Fatalf("New pod did not reach Running state: %v", err)
	}

	// Get new IP
	if err := config.Client().Resources().Get(ctx, newPod.Name, newPod.Namespace, newPod.Pod); err != nil {
		t.Fatalf("Failed to get new pod: %v", err)
	}
	newIP := newPod.Status.PodIP
	newNode := newPod.Spec.NodeName
	t.Logf("New pod IP: %s on node: %s", newIP, newNode)

	// Verify new IP is within prefix range
	if !verifyPodIPInPrefix(ctx, t, config, newIP, newNode) {
		t.Errorf("New pod IP %s is not within any allocated prefix on node %s", newIP, newNode)
	}

	t.Logf("Pod recreation successful: original IP=%s, new IP=%s", originalIP, newIP)
	return MarkTestSuccess(ctx)
}

// assessNodeSetupVerification verifies that IP Prefix node configuration is correct.
func assessNodeSetupVerification(ctx context.Context, t *testing.T, config *envconf.Config, nodes []corev1.Node) context.Context {
	t.Logf("Verifying node setup for %d IP Prefix nodes", len(nodes))

	// Verify ConfigMap exists
	cm := &corev1.ConfigMap{}
	if err := config.Client().Resources().Get(ctx, "e2e-ip-prefix", "kube-system", cm); err != nil {
		t.Fatalf("ConfigMap e2e-ip-prefix not found: %v", err)
	}
	t.Log("ConfigMap e2e-ip-prefix exists")

	for _, node := range nodes {
		nodeName := node.Name

		// Re-fetch node to get current labels
		currentNode := &corev1.Node{}
		if err := config.Client().Resources().Get(ctx, nodeName, "", currentNode); err != nil {
			t.Fatalf("Failed to get node %s: %v", nodeName, err)
		}

		// Verify terway-config label
		if currentNode.Labels["terway-config"] != "e2e-ip-prefix" {
			t.Errorf("Node %s: expected terway-config=e2e-ip-prefix, got %s",
				nodeName, currentNode.Labels["terway-config"])
		} else {
			t.Logf("Node %s: terway-config=e2e-ip-prefix OK", nodeName)
		}

		// Verify ignore-by-terway label is removed
		if currentNode.Labels["k8s.aliyun.com/ignore-by-terway"] == "true" {
			t.Errorf("Node %s: ignore-by-terway label should be removed", nodeName)
		} else {
			t.Logf("Node %s: ignore-by-terway label removed OK", nodeName)
		}

		// Verify Node CR enableIPPrefix is pre-configured correctly
		if err := checkNodeCRPrefixEnabled(ctx, config, t, nodeName); err != nil {
			t.Errorf("Node %s: enableIPPrefix check failed: %v", nodeName, err)
		}

		// Verify prefix allocation
		prefixes, err := getAllocatedPrefixes(ctx, config, nodeName)
		if err != nil {
			t.Errorf("Node %s: failed to get prefixes: %v", nodeName, err)
		} else if len(prefixes) == 0 {
			t.Errorf("Node %s: no prefixes allocated", nodeName)
		} else {
			t.Logf("Node %s: %d prefixes allocated OK", nodeName, len(prefixes))
		}
	}

	return MarkTestSuccess(ctx)
}
