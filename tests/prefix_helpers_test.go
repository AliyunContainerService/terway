//go:build e2e

package tests

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// =============================================================================
// Shared Test Helpers
// =============================================================================

// skipIfNotPrefixTestEnvironment skips the test when cluster prerequisites for
// IP Prefix tests are not met: CRD IPAM mode, terway-eniip daemonset, v1.17.0+.
func skipIfNotPrefixTestEnvironment(t *testing.T) {
	t.Helper()
	if eniConfig == nil || eniConfig.IPAMType != "crd" {
		t.Skipf("skip: ipam type is not crd, current: %s", func() string {
			if eniConfig != nil {
				return eniConfig.IPAMType
			}
			return "<nil>"
		}())
	}
	if GetCachedTerwayDaemonSetName() != "terway-eniip" {
		t.Skipf("Requires terway-eniip daemonset, current: %s", GetCachedTerwayDaemonSetName())
	}
	if !RequireTerwayVersion("v1.17.0") {
		t.Skipf("Requires terway version >= v1.17.0")
	}
}

// runPrefixFeatureTest runs a feature test and records global failure state.
func runPrefixFeatureTest(t *testing.T, feat features.Feature) {
	t.Helper()
	testenv.Test(t, feat)
	if t.Failed() {
		isFailed.Store(true)
	}
}

// =============================================================================
// Context Keys for Prefix Tests
// =============================================================================

type originalConfigContextKeyType struct{}
type testResourcesContextKeyType struct{}

var originalConfigContextKey = originalConfigContextKeyType{}
var testResourcesContextKey = testResourcesContextKeyType{}

// OriginalConfig stores the original eni-config for restoration
type OriginalConfig struct {
	EniConf string `json:"eni_conf"`
}

// dynamicConfigName returns the name of the shared Dynamic Config ConfigMap
// All IP Prefix nodes use the same ConfigMap for terway dynamic config.
// This matches the ConfigMap created by Terraform: null_resource.create_e2e_ip_prefix_configmap
func dynamicConfigName(nodeName string) string {
	return sharedDynamicConfigName // "e2e-ip-prefix"
}

// =============================================================================
// Config Management Helpers
// =============================================================================

// saveOriginalConfig saves the original eni-config to context for later restoration
func saveOriginalConfig(ctx context.Context, config *envconf.Config, t *testing.T) context.Context {
	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		t.Logf("Warning: failed to get eni-config for saving: %v", err)
		return ctx
	}

	originalConf := &OriginalConfig{
		EniConf: cm.Data["eni_conf"],
	}

	return context.WithValue(ctx, originalConfigContextKey, originalConf)
}

// restoreOriginalConfig restores the original eni-config from context
func restoreOriginalConfig(ctx context.Context, config *envconf.Config, t *testing.T) {
	originalConf, ok := ctx.Value(originalConfigContextKey).(*OriginalConfig)
	if !ok || originalConf == nil {
		t.Log("No original config to restore")
		return
	}

	cm := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
	if err != nil {
		t.Logf("Warning: failed to get eni-config for restoration: %v", err)
		return
	}

	cm.Data["eni_conf"] = originalConf.EniConf
	err = config.Client().Resources().Update(ctx, cm)
	if err != nil {
		t.Logf("Warning: failed to restore eni-config: %v", err)
		return
	}

	t.Log("Restored original eni-config")
}

// =============================================================================
// Dynamic Config Helpers
// =============================================================================

// sharedDynamicConfigName is the name of the shared ConfigMap for all IP Prefix nodes
// This matches the ConfigMap created by Terraform: null_resource.create_e2e_ip_prefix_configmap
const sharedDynamicConfigName = "e2e-ip-prefix"

// setupNodeDynamicConfig updates the shared ConfigMap (e2e-ip-prefix) with the
// given eniConfJSON content.
//
// The "terway-config=e2e-ip-prefix" label on ip-prefix nodes is pre-configured
// by Terraform at node pool creation time and must NOT be modified by e2e tests.
// This function only updates the ConfigMap content.
func setupNodeDynamicConfig(ctx context.Context, config *envconf.Config, t *testing.T, eniConfJSON string) (context.Context, error) {
	cmName := sharedDynamicConfigName

	// Create or update the shared ConfigMap
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: "kube-system",
		},
		Data: map[string]string{
			"eni_conf": eniConfJSON,
		},
	}

	// Try to create; if already exists, update it
	existingCM := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, cmName, "kube-system", existingCM)
	if err != nil {
		// ConfigMap does not exist, create it
		if createErr := config.Client().Resources().Create(ctx, cm); createErr != nil {
			return ctx, fmt.Errorf("failed to create shared dynamic config ConfigMap %s: %w", cmName, createErr)
		}
		t.Logf("Created shared dynamic config ConfigMap: kube-system/%s", cmName)
	} else {
		// ConfigMap already exists, update it
		existingCM.Data = cm.Data
		if updateErr := config.Client().Resources().Update(ctx, existingCM); updateErr != nil {
			return ctx, fmt.Errorf("failed to update shared dynamic config ConfigMap %s: %w", cmName, updateErr)
		}
		t.Logf("Updated shared dynamic config ConfigMap: kube-system/%s", cmName)
	}

	return ctx, nil
}

// checkNodeCRPrefixEnabled verifies that the Node CR has enableIPPrefix=true.
// Nodes are expected to have this field set correctly at creation time via the
// node pool configuration. If the field is not enabled, it indicates a
// misconfiguration that should be fixed at the infrastructure level, not patched
// by the test.
func checkNodeCRPrefixEnabled(ctx context.Context, config *envconf.Config, t *testing.T, nodeName string) error {
	nodeCR := &networkv1beta1.Node{}
	if err := config.Client().Resources().Get(ctx, nodeName, "", nodeCR); err != nil {
		return fmt.Errorf("failed to get Node CR %s: %w", nodeName, err)
	}

	if nodeCR.Spec.ENISpec == nil || !nodeCR.Spec.ENISpec.EnableIPPrefix {
		return fmt.Errorf("node CR %s has enableIPPrefix=false; node pool must be configured with enableIPPrefix=true before running prefix e2e tests", nodeName)
	}

	t.Logf("Node CR %s: enableIPPrefix=true OK", nodeName)
	return nil
}

// =============================================================================
// Resource Cleanup Helpers
// =============================================================================

// AddResourcesForCleanup adds resources to be cleaned up after the test
func AddResourcesForCleanup(ctx context.Context, resources ...k8s.Object) context.Context {
	existingResources, _ := ctx.Value(testResourcesContextKey).([]k8s.Object)
	existingResources = append(existingResources, resources...)
	return context.WithValue(ctx, testResourcesContextKey, existingResources)
}

// =============================================================================
// Node CR Prefix Cleanup Helpers
// =============================================================================

// cleanupNodePrefixes cleans up all prefixes and secondary IPs on a node's ENIs.
//
// Rules per ENI type:
//   - Primary ENI: skipped entirely (must not be deleted).
//   - Trunk ENI: marks all prefixes (IPv4/IPv6) AND all non-primary secondary IPs as Deleting.
//   - Secondary / Member ENIs: marks the entire ENI as Deleting (cascades to all prefixes).
func cleanupNodePrefixes(ctx context.Context, config *envconf.Config, t *testing.T, nodeName string) error {
	node := &networkv1beta1.Node{}
	if err := config.Client().Resources().Get(ctx, nodeName, "", node); err != nil {
		return err
	}

	cleanedCount := 0
	for eniID, nic := range node.Status.NetworkInterfaces {
		switch nic.NetworkInterfaceType {
		case networkv1beta1.ENITypePrimary:
			// Primary ENI must never be deleted.
			continue

		case networkv1beta1.ENITypeTrunk:
			// Trunk ENI: mark prefixes as Deleting and non-primary secondary IPs as Deleting.
			hasWork := false

			for i := range nic.IPv4Prefix {
				nic.IPv4Prefix[i].Status = networkv1beta1.IPPrefixStatusDeleting
				hasWork = true
			}
			for i := range nic.IPv6Prefix {
				nic.IPv6Prefix[i].Status = networkv1beta1.IPPrefixStatusDeleting
				hasWork = true
			}

			// Mark non-primary secondary IPv4 IPs as Deleting.
			for ip, ipInfo := range nic.IPv4 {
				if !ipInfo.Primary {
					ipInfo.Status = networkv1beta1.IPStatusDeleting
					nic.IPv4[ip] = ipInfo
					hasWork = true
				}
			}
			// Mark non-primary secondary IPv6 IPs as Deleting.
			for ip, ipInfo := range nic.IPv6 {
				if !ipInfo.Primary {
					ipInfo.Status = networkv1beta1.IPStatusDeleting
					nic.IPv6[ip] = ipInfo
					hasWork = true
				}
			}

			if hasWork {
				t.Logf("[cleanup] Trunk ENI %s: marked %d IPv4 + %d IPv6 prefixes and non-primary IPs as Deleting",
					eniID, len(nic.IPv4Prefix), len(nic.IPv6Prefix))
				cleanedCount++
			}

		default:
			// Secondary / Member ENI: mark the entire ENI as Deleting (cascades to all prefixes).
			nic.Status = aliyunClient.ENIStatusDeleting
			t.Logf("[cleanup] ENI %s (type=%s): marked as Deleting (cascades to %d prefixes)",
				eniID, nic.NetworkInterfaceType, len(nic.IPv4Prefix)+len(nic.IPv6Prefix))
			cleanedCount++
		}

		node.Status.NetworkInterfaces[eniID] = nic
	}

	if cleanedCount == 0 {
		t.Logf("[cleanup] No ENIs to clean up on node %s", nodeName)
		return nil
	}

	return config.Client().Resources().UpdateStatus(ctx, node)
}

// waitForPrefixCleanup waits until there are no prefixes on the node (including Deleting ones).
// This ensures the controller has actually released them from the cloud.
func waitForPrefixCleanup(ctx context.Context, config *envconf.Config, t *testing.T, nodeName string, timeout time.Duration) error {
	return wait.For(func(ctx context.Context) (done bool, err error) {
		node := &networkv1beta1.Node{}
		err = config.Client().Resources().Get(ctx, nodeName, "", node)
		if err != nil {
			return false, err
		}

		totalCount := 0
		eniCount := 0
		for _, nic := range node.Status.NetworkInterfaces {
			if len(nic.IPv4Prefix) > 0 || len(nic.IPv6Prefix) > 0 {
				eniCount++
				totalCount += len(nic.IPv4Prefix) + len(nic.IPv6Prefix)
			}
		}

		if totalCount == 0 {
			t.Logf("[cleanup] Node %s: all prefixes cleaned up", nodeName)
			return true, nil
		}

		t.Logf("[cleanup] Node %s: waiting for cleanup, %d prefixes on %d ENIs remaining",
			nodeName, totalCount, eniCount)
		return false, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(10*time.Second))
}


// waitForNodeCRIPv4PrefixCount waits until the Node CR Spec.ENISpec.IPv4PrefixCount equals
// expectedCount. This confirms Terway has read the Dynamic Config and propagated the new
// ipv4_prefix_count value into the Node CR spec.
func waitForNodeCRIPv4PrefixCount(ctx context.Context, config *envconf.Config, t *testing.T,
	nodeName string, expectedCount int, timeout time.Duration) error {

	return wait.For(func(ctx context.Context) (done bool, err error) {
		node := &networkv1beta1.Node{}
		if err = config.Client().Resources().Get(ctx, nodeName, "", node); err != nil {
			return false, err
		}

		if node.Spec.ENISpec == nil {
			t.Logf("[reset] Node %s: ENISpec is nil, waiting...", nodeName)
			return false, nil
		}

		actual := node.Spec.ENISpec.IPv4PrefixCount
		if actual == expectedCount {
			t.Logf("[reset] Node %s: IPv4PrefixCount=%d matches expected %d", nodeName, actual, expectedCount)
			return true, nil
		}

		t.Logf("[reset] Node %s: IPv4PrefixCount=%d, waiting for %d", nodeName, actual, expectedCount)
		return false, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(10*time.Second))
}

// resetNodePrefixState is a one-stop helper that fully resets the prefix state on a node:
//
//  1. Sets ipv4_prefix_count=0 in the Dynamic Config ConfigMap to stop Terway from allocating
//     new prefixes.
//  2. Restarts Terway so it picks up the updated config.
//  3. Waits for the Node CR Spec.ENISpec.IPPrefixCount to reflect 0, confirming Terway has
//     reconciled the config change.
//  4. Marks all non-Trunk ENIs as Deleting; for the Trunk ENI, marks all prefixes and
//     non-primary secondary IPs as Deleting.
//  5. Triggers a Node CR reconcile to kick off the actual cloud resource cleanup.
//  6. Waits until all prefixes have been removed from the Node CR.
func resetNodePrefixState(ctx context.Context, config *envconf.Config, t *testing.T, nodeName string, timeout time.Duration) error {
	t.Logf("[reset] Starting prefix state reset on node %s", nodeName)

	// Step 1: Set ipv4_prefix_count=0 in Dynamic Config so Terway stops allocating new prefixes.
	t.Log("[reset] Step 1: Setting ipv4_prefix_count=0 in Dynamic Config")
	if err := configureIPPrefixCount(ctx, t, config, nodeName, 0); err != nil {
		return fmt.Errorf("configureIPPrefixCount(0): %w", err)
	}

	// Step 2: Restart Terway to apply the config change.
	t.Log("[reset] Step 2: Restarting Terway")
	if err := restartTerway(ctx, config); err != nil {
		return fmt.Errorf("restartTerway: %w", err)
	}

	// Step 3: Wait for the Node CR to reflect IPv4PrefixCount=0 (controller has reconciled the config).
	t.Logf("[reset] Step 3: Waiting for Node CR IPv4PrefixCount=0 on node %s", nodeName)
	if err := waitForNodeCRIPv4PrefixCount(ctx, config, t, nodeName, 0, timeout); err != nil {
		// Non-fatal: log and continue so the manual cleanup below still runs.
		t.Logf("[reset] Warning: waitForNodeCRIPv4PrefixCount: %v", err)
	}

	// Step 4: Mark ENIs for deletion:
	//   - Non-Trunk ENIs (Secondary/Member): mark the entire ENI as Deleting.
	//   - Trunk ENI: mark all prefixes and non-primary secondary IPs as Deleting.
	t.Log("[reset] Step 4: Marking ENIs/Prefixes for deletion")
	if err := cleanupNodePrefixes(ctx, config, t, nodeName); err != nil {
		return fmt.Errorf("cleanupNodePrefixes: %w", err)
	}

	// Step 5: Trigger a Node CR reconcile so the controller processes the deletions immediately.
	node := &networkv1beta1.Node{}
	if err := config.Client().Resources().Get(ctx, nodeName, "", node); err != nil {
		return fmt.Errorf("get node CR: %w", err)
	}
	if err := triggerNodeCR(ctx, config, t, node); err != nil {
		return fmt.Errorf("triggerNodeCR: %w", err)
	}

	// Step 6: Wait for all prefixes to be removed from the Node CR.
	t.Logf("[reset] Step 6: Waiting for prefix cleanup on node %s (timeout: %v)", nodeName, timeout)
	return waitForPrefixCleanup(ctx, config, t, nodeName, timeout)
}

// =============================================================================
// IP Prefix E2E Test Helper Functions
// =============================================================================

// setupIPPrefixNodes initializes IP Prefix nodes for E2E testing.
// The ConfigMap e2e-ip-prefix and node labels (terway-config, k8s.aliyun.com/ignore-by-terway)
// are pre-configured by Terraform. This function only:
// - Updates the ConfigMap content with the desired ipv4_prefix_count
// - Waits for terway pods to be ready
// - Ensures Node CR has enableIPPrefix=true
func setupIPPrefixNodes(ctx context.Context, t *testing.T, config *envconf.Config, nodes []corev1.Node, prefixCount int) (context.Context, error) {
	t.Logf("Setting up %d IP Prefix nodes with prefix_count=%d (ConfigMap and labels pre-configured by Terraform)", len(nodes), prefixCount)

	// Step 1: Update shared ConfigMap e2e-ip-prefix content
	// Note: ConfigMap is created by Terraform, we just update the ipv4_prefix_count
	cmData := fmt.Sprintf(`{"enable_ip_prefix":true,"ipv4_prefix_count":%d}`, prefixCount)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-ip-prefix",
			Namespace: "kube-system",
		},
		Data: map[string]string{
			"eni_conf": cmData,
		},
	}

	existingCM := &corev1.ConfigMap{}
	err := config.Client().Resources().Get(ctx, "e2e-ip-prefix", "kube-system", existingCM)
	if err != nil {
		if createErr := config.Client().Resources().Create(ctx, cm); createErr != nil {
			return ctx, fmt.Errorf("failed to create ConfigMap e2e-ip-prefix: %w", createErr)
		}
		t.Log("Created ConfigMap kube-system/e2e-ip-prefix")
	} else {
		existingCM.Data = cm.Data
		if updateErr := config.Client().Resources().Update(ctx, existingCM); updateErr != nil {
			return ctx, fmt.Errorf("failed to update ConfigMap e2e-ip-prefix: %w", updateErr)
		}
		t.Logf("Updated ConfigMap kube-system/e2e-ip-prefix with ipv4_prefix_count=%d", prefixCount)
	}

	// Step 2: Wait for terway pods to be ready on all prefix nodes in parallel.
	// (Nodes are already labeled by Terraform, terway should be scheduling)
	errs := make([]error, len(nodes))
	var wg sync.WaitGroup
	for i, node := range nodes {
		wg.Add(1)
		go func(i int, nodeName string) {
			defer wg.Done()
			errs[i] = waitForTerwayPodReady(ctx, t, config, nodeName, 5*time.Minute)
		}(i, node.Name)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return ctx, fmt.Errorf("terway pod not ready on node %s: %w", nodes[i].Name, err)
		}
	}

	// Step 3: Verify Node CR has enableIPPrefix=true (must be pre-configured at node pool creation)
	for _, node := range nodes {
		if err := checkNodeCRPrefixEnabled(ctx, config, t, node.Name); err != nil {
			return ctx, fmt.Errorf("enableIPPrefix check failed on node %s: %w", node.Name, err)
		}
	}

	t.Logf("Successfully set up %d IP Prefix nodes", len(nodes))
	return ctx, nil
}

// teardownIPPrefixNodes cleans up IP Prefix nodes after E2E testing.
// The ConfigMap e2e-ip-prefix, node labels, and Node CR enableIPPrefix are all
// managed by Terraform and must not be modified by e2e tests.
func teardownIPPrefixNodes(ctx context.Context, t *testing.T, config *envconf.Config, nodes []corev1.Node) {
	t.Logf("Tearing down %d IP Prefix nodes (all node configuration managed by Terraform)", len(nodes))
	t.Log("IP Prefix nodes teardown complete")
}

// waitForTerwayPodReady waits for a terway DaemonSet pod to be Running and Ready
// on the specified node.
func waitForTerwayPodReady(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string, timeout time.Duration) error {
	return wait.For(func(ctx context.Context) (done bool, err error) {
		pods := &corev1.PodList{}
		err = config.Client().Resources("kube-system").List(ctx, pods)
		if err != nil {
			return false, err
		}

		for _, pod := range pods.Items {
			if pod.Spec.NodeName != nodeName {
				continue
			}
			// Match terway daemonset pods (terway-eniip-xxxxx)
			dsName := GetCachedTerwayDaemonSetName()
			if dsName == "" {
				dsName = "terway-eniip"
			}
			ownerMatch := false
			for _, ref := range pod.OwnerReferences {
				if ref.Kind == "DaemonSet" {
					ownerMatch = true
					break
				}
			}
			if !ownerMatch {
				continue
			}
			if pod.Labels["app"] != dsName {
				continue
			}

			if pod.Status.Phase == corev1.PodRunning {
				// Check all containers are ready
				allReady := true
				for _, cs := range pod.Status.ContainerStatuses {
					if !cs.Ready {
						allReady = false
						break
					}
				}
				if allReady {
					t.Logf("Terway pod %s is Running and Ready on node %s", pod.Name, nodeName)
					return true, nil
				}
			}
			t.Logf("Terway pod %s on node %s: phase=%s", pod.Name, nodeName, pod.Status.Phase)
		}

		t.Logf("Waiting for terway pod to be ready on node %s", nodeName)
		return false, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(10*time.Second))
}

// verifyPodIPInPrefix verifies that a pod's IP address falls within one of the
// allocated /28 IPv4 prefix CIDR ranges on the node.
func verifyPodIPInPrefix(ctx context.Context, t *testing.T, config *envconf.Config, podIP string, nodeName string) bool {
	prefixes, err := getAllocatedPrefixes(ctx, config, nodeName)
	if err != nil {
		t.Logf("Warning: failed to get allocated prefixes for node %s: %v", nodeName, err)
		return false
	}

	if len(prefixes) == 0 {
		t.Logf("No prefixes allocated on node %s", nodeName)
		return false
	}

	ip := net.ParseIP(podIP)
	if ip == nil {
		t.Logf("Invalid pod IP: %s", podIP)
		return false
	}

	for _, prefix := range prefixes {
		_, cidr, err := net.ParseCIDR(prefix.Prefix)
		if err != nil {
			t.Logf("Warning: failed to parse prefix CIDR %s: %v", prefix.Prefix, err)
			continue
		}
		if cidr.Contains(ip) {
			t.Logf("Pod IP %s is within prefix %s on node %s", podIP, prefix.Prefix, nodeName)
			return true
		}
	}

	t.Logf("Pod IP %s is NOT within any allocated prefix on node %s", podIP, nodeName)
	for _, prefix := range prefixes {
		t.Logf("  Allocated prefix: %s (status: %s)", prefix.Prefix, prefix.Status)
	}
	return false
}
