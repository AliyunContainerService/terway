//go:build e2e

package tests

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// =============================================================================
// IP Prefix Status Lifecycle Tests
//
// These tests validate the prefix status state machine:
//   Frozen (expired) → Valid (revert)
// =============================================================================

// TestPrefix_Status_FrozenExpireRevert tests that Frozen prefixes whose
// FrozenExpireAt has passed are reverted back to Valid by the Controller,
// making them available for allocation again.
func TestPrefix_Status_FrozenExpireRevert(t *testing.T) {
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

	feat := features.New("Prefix/Status/FrozenExpireRevert").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, nodeName)
			return ctx
		}).
		Assess("expired Frozen prefix reverts to Valid", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessFrozenExpireRevert(ctx, t, config, ctx.Value(qualifiedNodeContextKey).(string))
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

// TestPrefix_Status_PodAllocFromValidOnly tests that Pods only get IPs from
// Valid prefixes, not from Frozen/Invalid/Deleting ones.
func TestPrefix_Status_PodAllocFromValidOnly(t *testing.T) {
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

	feat := features.New("Prefix/Status/PodAllocFromValidOnly").
		WithLabel("eni-mode", "shared").
		WithLabel("machine-type", "ecs").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			ctx = saveOriginalConfig(ctx, config, t)
			ctx = context.WithValue(ctx, qualifiedNodeContextKey, nodeName)
			return ctx
		}).
		Assess("pods get IPs only from Valid prefixes", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			return assessPodAllocFromValidOnly(ctx, t, config, ctx.Value(qualifiedNodeContextKey).(string))
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
// Assess Functions
// =============================================================================

// assessFrozenExpireRevert tests the Frozen prefix expiration revert:
// 1. Allocate prefixes
// 2. Manually set one prefix to Frozen with an already-expired FrozenExpireAt
// 3. Trigger reconcile
// 4. Verify the prefix reverts to Valid
func assessFrozenExpireRevert(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) context.Context {
	t.Logf("Testing Frozen prefix expire revert on node: %s", nodeName)

	// Step 1: Enable prefix mode with 3 prefixes
	t.Log("Step 1: Enable prefix mode with ipv4_prefix_count=3")
	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, nodeName, `{"enable_ip_prefix":true,"ipv4_prefix_count":3}`)
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}
	if err = restartTerway(ctx, config); err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}
	if err = waitForPrefixAllocation(ctx, config, t, nodeName, 3, 3*time.Minute); err != nil {
		t.Fatalf("failed waiting for prefix allocation: %v", err)
	}

	// Step 2: Set one prefix to Frozen with an already-expired FrozenExpireAt
	t.Log("Step 2: Set one prefix to Frozen with expired FrozenExpireAt")
	node := &networkv1beta1.Node{}
	if err = config.Client().Resources().Get(ctx, nodeName, "", node); err != nil {
		t.Fatalf("failed to get node: %v", err)
	}

	var frozenPrefix string
	for _, nic := range node.Status.NetworkInterfaces {
		for i, prefix := range nic.IPv4Prefix {
			if prefix.Status == networkv1beta1.IPPrefixStatusValid {
				nic.IPv4Prefix[i].Status = networkv1beta1.IPPrefixStatusFrozen
				nic.IPv4Prefix[i].FrozenExpireAt = metav1.NewTime(time.Now().Add(-1 * time.Hour))
				frozenPrefix = prefix.Prefix
				break
			}
		}
		if frozenPrefix != "" {
			break
		}
	}

	if frozenPrefix == "" {
		t.Fatal("no Valid prefix found to mark as Frozen")
	}

	t.Logf("Set prefix %s to Frozen with expired FrozenExpireAt", frozenPrefix)
	if err = config.Client().Resources().UpdateStatus(ctx, node); err != nil {
		t.Fatalf("failed to update node status: %v", err)
	}

	// Step 3: Trigger reconcile and wait for prefix to revert to Valid
	t.Log("Step 3: Wait for Controller to revert expired Frozen prefix to Valid")
	err = waitForPrefixStatusChange(ctx, config, t, nodeName, frozenPrefix,
		networkv1beta1.IPPrefixStatusValid, 2*time.Minute)
	if err != nil {
		t.Fatalf("failed waiting for prefix revert: %v", err)
	}

	// Step 4: Verify FrozenExpireAt is cleared
	t.Log("Step 4: Verify FrozenExpireAt is cleared")
	node = &networkv1beta1.Node{}
	if err = config.Client().Resources().Get(ctx, nodeName, "", node); err != nil {
		t.Fatalf("failed to get node: %v", err)
	}

	for _, nic := range node.Status.NetworkInterfaces {
		for _, prefix := range nic.IPv4Prefix {
			if prefix.Prefix == frozenPrefix {
				if !prefix.FrozenExpireAt.IsZero() {
					t.Errorf("FrozenExpireAt should be zero after revert, got %v", prefix.FrozenExpireAt)
				}
			}
		}
	}

	t.Logf("Successfully verified Frozen prefix %s reverted to Valid", frozenPrefix)
	return MarkTestSuccess(ctx)
}

// assessPodAllocFromValidOnly tests that pods only receive IPs from Valid prefixes:
// 1. Allocate prefixes
// 2. Mark some prefixes as Frozen
// 3. Create pods and verify their IPs are within Valid prefix ranges
func assessPodAllocFromValidOnly(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) context.Context {
	t.Logf("Testing pod allocation from Valid-only prefixes on node: %s", nodeName)

	// Step 1: Enable prefix mode with 5 prefixes
	t.Log("Step 1: Enable prefix mode with ipv4_prefix_count=5")
	var err error
	ctx, err = setupNodeDynamicConfig(ctx, config, t, nodeName, `{"enable_ip_prefix":true,"ipv4_prefix_count":5}`)
	if err != nil {
		t.Fatalf("failed to setup node dynamic config: %v", err)
	}
	if err = restartTerway(ctx, config); err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}
	if err = waitForPrefixAllocation(ctx, config, t, nodeName, 5, 3*time.Minute); err != nil {
		t.Fatalf("failed waiting for prefix allocation: %v", err)
	}

	// Step 2: Get current prefixes and mark 2 as Frozen
	t.Log("Step 2: Mark 2 prefixes as Frozen")
	node := &networkv1beta1.Node{}
	if err = config.Client().Resources().Get(ctx, nodeName, "", node); err != nil {
		t.Fatalf("failed to get node: %v", err)
	}

	var frozenPrefixes []string
	var validPrefixes []string
	frozenCount := 0
	for _, nic := range node.Status.NetworkInterfaces {
		for i, prefix := range nic.IPv4Prefix {
			if prefix.Status == networkv1beta1.IPPrefixStatusValid {
				if frozenCount < 2 {
					nic.IPv4Prefix[i].Status = networkv1beta1.IPPrefixStatusFrozen
					frozenPrefixes = append(frozenPrefixes, prefix.Prefix)
					frozenCount++
				} else {
					validPrefixes = append(validPrefixes, prefix.Prefix)
				}
			}
		}
	}

	if len(frozenPrefixes) < 2 {
		t.Fatalf("not enough Valid prefixes to freeze, got %d", len(frozenPrefixes))
	}

	t.Logf("Frozen prefixes: %v, Valid prefixes: %v", frozenPrefixes, validPrefixes)
	if err = config.Client().Resources().UpdateStatus(ctx, node); err != nil {
		t.Fatalf("failed to update node status: %v", err)
	}

	// Restart terway so Daemon picks up the new prefix states
	if err = restartTerway(ctx, config); err != nil {
		t.Fatalf("failed to restart terway: %v", err)
	}

	// Wait for terway to be ready
	time.Sleep(15 * time.Second)

	// Step 3: Create pods and verify IPs are within Valid prefix ranges
	t.Log("Step 3: Create pods and verify IPs are from Valid prefixes only")
	podCount := 3
	var createdPods []string
	for i := 0; i < podCount; i++ {
		podName := fmt.Sprintf("prefix-valid-test-%d", i)
		pod := NewPod(podName, config.Namespace()).
			WithContainer("nginx", nginxImage, nil).
			WithNodeAffinity(map[string]string{"kubernetes.io/hostname": nodeName})

		if err = config.Client().Resources().Create(ctx, pod.Pod); err != nil {
			t.Fatalf("failed to create pod %s: %v", podName, err)
		}
		createdPods = append(createdPods, podName)
	}

	// Wait for pods to be running
	for _, podName := range createdPods {
		if err = waitForPodRunning(ctx, config, podName, config.Namespace(), 2*time.Minute); err != nil {
			t.Fatalf("pod %s did not reach Running state: %v", podName, err)
		}
	}

	// Verify pod IPs are within Valid prefix ranges
	for _, podName := range createdPods {
		podIP, err := getPodIP(ctx, config, podName, config.Namespace())
		if err != nil {
			t.Errorf("failed to get IP for pod %s: %v", podName, err)
			continue
		}

		inFrozen := isIPInPrefixes(podIP, frozenPrefixes)
		inValid := isIPInPrefixes(podIP, validPrefixes)

		if inFrozen {
			t.Errorf("pod %s got IP %s from Frozen prefix, expected only Valid prefixes", podName, podIP)
		}
		if !inValid {
			t.Logf("pod %s got IP %s (not in tracked Valid prefixes, may be from a new prefix)", podName, podIP)
		} else {
			t.Logf("pod %s got IP %s from Valid prefix ✓", podName, podIP)
		}
	}

	// Cleanup pods
	for _, podName := range createdPods {
		pod := NewPod(podName, config.Namespace())
		_ = config.Client().Resources().Delete(ctx, pod.Pod)
	}

	return MarkTestSuccess(ctx)
}

// =============================================================================
// Helper Functions for Status Lifecycle Tests
// =============================================================================

// waitForPrefixStatusChange waits until the specified prefix reaches the expected status.
func waitForPrefixStatusChange(ctx context.Context, config *envconf.Config, t *testing.T,
	nodeName, targetPrefix string, expectedStatus networkv1beta1.IPPrefixStatus, timeout time.Duration) error {

	return wait.For(func(ctx context.Context) (done bool, err error) {
		node := &networkv1beta1.Node{}
		if err = config.Client().Resources().Get(ctx, nodeName, "", node); err != nil {
			return false, err
		}

		// Trigger reconcile
		triggerNodeCR(ctx, config, t, node)

		for _, nic := range node.Status.NetworkInterfaces {
			for _, prefix := range nic.IPv4Prefix {
				if prefix.Prefix == targetPrefix {
					if prefix.Status == expectedStatus {
						t.Logf("Prefix %s reached expected status %s", targetPrefix, expectedStatus)
						return true, nil
					}
					t.Logf("Prefix %s status is %s, waiting for %s", targetPrefix, prefix.Status, expectedStatus)
					return false, nil
				}
			}
		}

		t.Logf("Prefix %s not found in Node CR", targetPrefix)
		return false, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(10*time.Second))
}

// waitForPodRunning waits for a pod to reach Running phase.
func waitForPodRunning(ctx context.Context, config *envconf.Config,
	podName, namespace string, timeout time.Duration) error {

	return wait.For(func(ctx context.Context) (done bool, err error) {
		pod := &corev1.Pod{}
		if err = config.Client().Resources(namespace).Get(ctx, podName, namespace, pod); err != nil {
			return false, err
		}
		return pod.Status.Phase == corev1.PodRunning, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(5*time.Second))
}

// getPodIP returns the pod's IP address.
func getPodIP(ctx context.Context, config *envconf.Config, podName, namespace string) (string, error) {
	pod := &corev1.Pod{}
	if err := config.Client().Resources(namespace).Get(ctx, podName, namespace, pod); err != nil {
		return "", err
	}
	if pod.Status.PodIP == "" {
		return "", fmt.Errorf("pod %s has no IP assigned", podName)
	}
	return pod.Status.PodIP, nil
}

// isIPInPrefixes checks if an IP address falls within any of the given CIDR prefixes.
func isIPInPrefixes(ip string, prefixes []string) bool {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}
	for _, prefix := range prefixes {
		_, cidr, err := net.ParseCIDR(prefix)
		if err != nil {
			continue
		}
		if cidr.Contains(parsedIP) {
			return true
		}
	}
	return false
}
