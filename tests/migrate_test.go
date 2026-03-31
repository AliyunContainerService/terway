//go:build e2e

package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"github.com/Jeffail/gabs/v2"

	aliClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	terwayTypes "github.com/AliyunContainerService/terway/types"
)

// Migration E2E Test
//
// TestMigrate auto-discovers LingJun nodes and runs a per-node subtest that
// exercises the ENO ↔ ECS migration round-trip. Node type (shared vs exclusive)
// and API path (A vs B) are detected automatically:
//
//   - Shared ENI nodes:            path A ("" ↔ "ecs")
//   - Non-HDENI exclusive ENI:     path A ("" ↔ "ecs")
//   - HDENI exclusive ENI:         path B ("hdeni" ↔ "ecs-hdeni")
//
// Each node subtest: ensure ENO mode → create pods → verify ENI prefixes →
//
//	migrate to ECS → create pods → verify eni- prefixes + release →
//	migrate back → create pods → verify ENO prefixes again.
//
// Env vars:
//   - MIGRATE_NODES: comma-separated node names (default: auto-discover LingJun nodes)
//
// Node mutation policy: only k8s.aliyun.com/eno-api annotation is patched.
// Label k8s.aliyun.com/exclusive-mode-eni-type is read-only.

const (
	migratePostCheckTimeout  = 5 * time.Minute
	migratePostCheckInterval = 10 * time.Second
	migratePodRunningTimeout = 8 * time.Minute

	networkInterfaceNodeLabel = "k8s.aliyun.com/node"

	ipReleaseTimeout   = 3 * time.Minute
	ipReleaseInterval  = 10 * time.Second
	eniReleaseTimeout  = 3 * time.Minute
	eniReleaseInterval = 10 * time.Second

	phase1SharedPodCount    = 3
	phase1ExclusivePodCount = 2
	phase3PodCount          = 2
	maxPhase2PodCount       = 50
)

// migrateAPIConfig holds the annotation values for one migration path.
// Path A: efloAPI="" / ecsAPI="ecs"            — shared nodes and non-HDENI exclusive nodes.
// Path B: efloAPI="hdeni" / ecsAPI="ecs-hdeni" — HDENI-family exclusive nodes only.
type migrateAPIConfig struct {
	efloAPI string
	ecsAPI  string
}

// enoENIPrefixes returns the expected ENI ID prefixes when the node is in ENO mode.
// Path A (efloAPI=""): ENIs are leni-*.  Path B (efloAPI="hdeni"): ENIs are hdeni-*.
func (c migrateAPIConfig) enoENIPrefixes() []string {
	if c.efloAPI == terwayTypes.APIEnoHDeni {
		return []string{"hdeni-"}
	}
	return []string{"leni-"}
}

var (
	pathAConfig = migrateAPIConfig{efloAPI: "", ecsAPI: terwayTypes.APIEcs}
	pathBConfig = migrateAPIConfig{efloAPI: terwayTypes.APIEnoHDeni, ecsAPI: terwayTypes.APIEcsHDeni}
)

// =============================================================================
// Node helpers
// =============================================================================

func nodeIsExclusiveENI(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) bool {
	t.Helper()
	crNode := &networkv1beta1.Node{}
	if err := config.Client().Resources().Get(ctx, nodeName, "", crNode); err != nil {
		t.Fatalf("failed to get Node CR %s: %v", nodeName, err)
	}
	return crNode.Labels[terwayTypes.ExclusiveENIModeLabel] == string(terwayTypes.ExclusiveENIOnly)
}

func nodeENOApi(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) string {
	t.Helper()
	crNode := &networkv1beta1.Node{}
	if err := config.Client().Resources().Get(ctx, nodeName, "", crNode); err != nil {
		t.Fatalf("failed to get Node CR %s: %v", nodeName, err)
	}
	return crNode.Annotations[terwayTypes.ENOApi]
}

// nodeIsHDENIFamily reports whether the node is in the HDENI API family.
// Only HDENI exclusive nodes may use path B.
func nodeIsHDENIFamily(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) bool {
	t.Helper()
	anno := nodeENOApi(ctx, t, config, nodeName)
	return anno == terwayTypes.APIEnoHDeni || anno == terwayTypes.APIEcsHDeni
}

// getNodeAPIConfig returns path B for HDENI exclusive nodes, path A for everything else.
func getNodeAPIConfig(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) migrateAPIConfig {
	t.Helper()
	if nodeIsExclusiveENI(ctx, t, config, nodeName) && nodeIsHDENIFamily(ctx, t, config, nodeName) {
		return pathBConfig
	}
	return pathAConfig
}

func listNodeNetworkInterfaces(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) []networkv1beta1.NetworkInterface {
	t.Helper()
	niList := &networkv1beta1.NetworkInterfaceList{}
	if err := config.Client().Resources().List(ctx, niList); err != nil {
		t.Fatalf("failed to list NetworkInterface CRs: %v", err)
	}
	var result []networkv1beta1.NetworkInterface
	for i := range niList.Items {
		if niList.Items[i].Labels[networkInterfaceNodeLabel] == nodeName {
			result = append(result, niList.Items[i])
		}
	}
	return result
}

// getMigrateTargetNodes returns node names from MIGRATE_NODES env var, or auto-discovers
// LingJun nodes (both shared and exclusive).
func getMigrateTargetNodes(t *testing.T, nodeInfo *NodeTypeInfo) []string {
	t.Helper()
	if env := os.Getenv("MIGRATE_NODES"); env != "" {
		var nodes []string
		for _, n := range strings.Split(env, ",") {
			if n = strings.TrimSpace(n); n != "" {
				nodes = append(nodes, n)
			}
		}
		if len(nodes) > 0 {
			t.Logf("Using MIGRATE_NODES: %v", nodes)
			return nodes
		}
	}
	var nodes []string
	for _, n := range nodeInfo.LingjunSharedENINodes {
		nodes = append(nodes, n.Name)
	}
	for _, n := range nodeInfo.LingjunExclusiveENINodes {
		nodes = append(nodes, n.Name)
	}
	return nodes
}

// =============================================================================
// ENI state / prefix verification helpers
// =============================================================================

func verifyNodeENIState(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) {
	t.Helper()
	if nodeIsExclusiveENI(ctx, t, config, nodeName) {
		nis := listNodeNetworkInterfaces(ctx, t, config, nodeName)
		for _, ni := range nis {
			if ni.Status.Phase != networkv1beta1.ENIPhaseBind {
				t.Errorf("[ExclusiveENI] NetworkInterface %s on node %s: phase=%s, expected %s",
					ni.Name, nodeName, ni.Status.Phase, networkv1beta1.ENIPhaseBind)
			}
		}
		if !t.Failed() {
			t.Logf("[ExclusiveENI] Verified %d NetworkInterface CR(s) on node %s are in Bind phase", len(nis), nodeName)
		}
		return
	}
	crNode := &networkv1beta1.Node{}
	if err := config.Client().Resources().Get(ctx, nodeName, "", crNode); err != nil {
		t.Fatalf("failed to get Node CR %s: %v", nodeName, err)
	}
	for eniID, nic := range crNode.Status.NetworkInterfaces {
		if nic == nil {
			continue
		}
		if nic.Status != aliClient.ENIStatusInUse {
			t.Errorf("[SharedENI] ENI %s on node %s: status=%s, expected InUse", eniID, nodeName, nic.Status)
		}
		for addr, ip := range nic.IPv4 {
			if ip != nil && ip.Status != networkv1beta1.IPStatusValid {
				t.Errorf("[SharedENI] ENI %s IPv4 %s on node %s: status=%s, expected Valid", eniID, addr, nodeName, ip.Status)
			}
		}
		for addr, ip := range nic.IPv6 {
			if ip != nil && ip.Status != networkv1beta1.IPStatusValid {
				t.Errorf("[SharedENI] ENI %s IPv6 %s on node %s: status=%s, expected Valid", eniID, addr, nodeName, ip.Status)
			}
		}
	}
	if !t.Failed() {
		t.Logf("[SharedENI] Verified %d ENI(s) on node %s are healthy", len(crNode.Status.NetworkInterfaces), nodeName)
	}
}

func verifyNodeENOApi(ctx context.Context, t *testing.T, config *envconf.Config, nodeName, expected string) {
	t.Helper()
	crNode := &networkv1beta1.Node{}
	if err := config.Client().Resources().Get(ctx, nodeName, "", crNode); err != nil {
		t.Fatalf("failed to get Node CR %s: %v", nodeName, err)
	}
	if actual := crNode.Annotations[terwayTypes.ENOApi]; actual != expected {
		t.Fatalf("Node CR %s annotation %s=%q, expected %q", nodeName, terwayTypes.ENOApi, actual, expected)
	}
	t.Logf("Verified Node CR %s annotation %s=%q", nodeName, terwayTypes.ENOApi, displayENOApiForTest(expected))
}

// =============================================================================
// Pod IP / ENI allocation verification helpers
// =============================================================================

func findPodIPOnNodeCR(crNode *networkv1beta1.Node, podIP string) string {
	for eniID, nic := range crNode.Status.NetworkInterfaces {
		if nic == nil {
			continue
		}
		if _, ok := nic.IPv4[podIP]; ok {
			return eniID
		}
	}
	return ""
}

// verifySharedPodsOnENOENIs checks that pod IPs are allocated on leni- ENIs.
// Shared ENI nodes always use path A, so only leni- prefix is expected in ENO mode.
func verifySharedPodsOnENOENIs(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string, pods []*corev1.Pod) {
	t.Helper()
	crNode := &networkv1beta1.Node{}
	if err := config.Client().Resources().Get(ctx, nodeName, "", crNode); err != nil {
		t.Fatalf("failed to get Node CR %s: %v", nodeName, err)
	}
	for _, pod := range pods {
		eniID := findPodIPOnNodeCR(crNode, pod.Status.PodIP)
		if eniID == "" {
			t.Errorf("[SharedENI-ENO] pod %s IP %s not found on any ENI of node %s", pod.Name, pod.Status.PodIP, nodeName)
			continue
		}
		if !strings.HasPrefix(eniID, "leni-") {
			t.Errorf("[SharedENI-ENO] pod %s IP %s on ENI %s, expected leni- prefix", pod.Name, pod.Status.PodIP, eniID)
		} else {
			t.Logf("[SharedENI-ENO] pod %s IP %s -> ENI %s", pod.Name, pod.Status.PodIP, eniID)
		}
	}
}

// verifySharedPodsOnECSPath verifies that at least some Phase 2 pods received IPs via ECS ENIs
// (eni- prefix). Pods may also land on existing ENO ENIs (secondary IP allocation on leni-),
// which is fine — we only require ECS path is actually used.
func verifySharedPodsOnECSPath(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string, pods []*corev1.Pod) {
	t.Helper()
	crNode := &networkv1beta1.Node{}
	if err := config.Client().Resources().Get(ctx, nodeName, "", crNode); err != nil {
		t.Fatalf("failed to get Node CR %s: %v", nodeName, err)
	}
	var enoPods, ecsPods, unknownPods []string
	for _, pod := range pods {
		eniID := findPodIPOnNodeCR(crNode, pod.Status.PodIP)
		entry := fmt.Sprintf("%s(%s@%s)", pod.Name, pod.Status.PodIP, eniID)
		switch {
		case eniID == "":
			unknownPods = append(unknownPods, fmt.Sprintf("%s(%s)", pod.Name, pod.Status.PodIP))
		case strings.HasPrefix(eniID, "leni-"):
			enoPods = append(enoPods, entry)
		case strings.HasPrefix(eniID, "eni-"):
			ecsPods = append(ecsPods, entry)
		default:
			unknownPods = append(unknownPods, entry)
		}
	}
	t.Logf("[SharedENI-ECS] Pods on ENO ENIs (leni-): %d %v", len(enoPods), enoPods)
	t.Logf("[SharedENI-ECS] Pods on ECS ENIs (eni-):  %d %v", len(ecsPods), ecsPods)
	if len(unknownPods) > 0 {
		t.Logf("[SharedENI-ECS] Pods with unknown ENI prefix:     %d %v", len(unknownPods), unknownPods)
	}
	if len(ecsPods) == 0 {
		t.Error("[SharedENI-ECS] expected at least one pod allocated via ECS API (eni- prefix), got none")
	}
}

// verifyExclusivePodENIPrefix checks that a PodENI's allocation ENI IDs match expected prefixes.
func verifyExclusivePodENIPrefix(t *testing.T, podENI *networkv1beta1.PodENI, podName string, expectedPrefixes []string) {
	t.Helper()
	for _, alloc := range podENI.Spec.Allocations {
		matched := false
		for _, prefix := range expectedPrefixes {
			if strings.HasPrefix(alloc.ENI.ID, prefix) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("[ExclusiveENI] pod %s PodENI ENI %s: expected prefix %v", podName, alloc.ENI.ID, expectedPrefixes)
		} else {
			t.Logf("[ExclusiveENI] pod %s PodENI ENI %s", podName, alloc.ENI.ID)
		}
	}
}

// verifySharedIPsReleased polls until pod IPs no longer have a PodID set on the Node CR.
func verifySharedIPsReleased(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string, podIPs map[string]string) {
	t.Helper()
	err := wait.For(func(ctx context.Context) (bool, error) {
		crNode := &networkv1beta1.Node{}
		if err := config.Client().Resources().Get(ctx, nodeName, "", crNode); err != nil {
			return false, nil
		}
		for _, nic := range crNode.Status.NetworkInterfaces {
			if nic == nil {
				continue
			}
			for ipAddr, ip := range nic.IPv4 {
				if _, isPodIP := podIPs[ipAddr]; isPodIP && ip != nil && ip.PodID != "" {
					return false, nil
				}
			}
		}
		return true, nil
	}, wait.WithTimeout(ipReleaseTimeout), wait.WithInterval(ipReleaseInterval))

	if err != nil {
		crNode := &networkv1beta1.Node{}
		if getErr := config.Client().Resources().Get(ctx, nodeName, "", crNode); getErr == nil {
			for _, nic := range crNode.Status.NetworkInterfaces {
				if nic == nil {
					continue
				}
				for ipAddr, ip := range nic.IPv4 {
					if podName, ok := podIPs[ipAddr]; ok && ip != nil && ip.PodID != "" {
						t.Errorf("[SharedENI] IP %s (ex-pod %s) still allocated: PodID=%s", ipAddr, podName, ip.PodID)
					}
				}
			}
		}
	} else {
		t.Logf("[SharedENI] All %d deleted pod IPs released on node %s", len(podIPs), nodeName)
	}
}

// verifyExclusiveENIsReleased waits for NetworkInterface CRs to be gone or Deleting.
func verifyExclusiveENIsReleased(ctx context.Context, t *testing.T, config *envconf.Config, niNames []string) {
	t.Helper()
	if len(niNames) == 0 {
		return
	}
	err := wait.For(func(ctx context.Context) (bool, error) {
		for _, name := range niNames {
			ni := &networkv1beta1.NetworkInterface{}
			if err := config.Client().Resources().Get(ctx, name, "", ni); err == nil {
				if ni.Status.Phase != networkv1beta1.Phase(networkv1beta1.ENIPhaseDeleting) {
					return false, nil
				}
			}
		}
		return true, nil
	}, wait.WithTimeout(eniReleaseTimeout), wait.WithInterval(eniReleaseInterval))

	if err != nil {
		t.Errorf("[ExclusiveENI] NetworkInterface CRs %v not cleaned up within timeout", niNames)
	} else {
		t.Logf("[ExclusiveENI] All %d NetworkInterface CRs cleaned up: %v", len(niNames), niNames)
	}
}

// =============================================================================
// Pod lifecycle helpers
// =============================================================================

func createAndWaitPods(ctx context.Context, t *testing.T, config *envconf.Config, nodeName, namePrefix string, count int) []*corev1.Pod {
	t.Helper()
	var pods []*corev1.Pod
	for i := 0; i < count; i++ {
		podName := fmt.Sprintf("%s-%d", namePrefix, i)
		if len(podName) > 63 {
			podName = podName[:63]
		}
		pod := NewPod(podName, config.Namespace()).
			WithContainer("nginx", nginxImage, nil).
			WithNodeName(nodeName).
			WithLingjunToleration()
		if err := config.Client().Resources().Create(ctx, pod.Pod); err != nil {
			t.Fatalf("failed to create pod %s: %v", podName, err)
		}
		pods = append(pods, pod.Pod)
	}
	for _, pod := range pods {
		if err := wait.For(conditions.New(config.Client().Resources()).PodRunning(pod),
			wait.WithTimeout(migratePodRunningTimeout), wait.WithInterval(5*time.Second)); err != nil {
			t.Fatalf("pod %s not Running: %v", pod.Name, err)
		}
		if err := config.Client().Resources().Get(ctx, pod.Name, pod.Namespace, pod); err != nil {
			t.Fatalf("failed to refresh pod %s: %v", pod.Name, err)
		}
		if pod.Status.PodIP == "" {
			t.Fatalf("pod %s has no IP", pod.Name)
		}
		t.Logf("Pod %s running with IP %s", pod.Name, pod.Status.PodIP)
	}
	return pods
}

func deleteAndWaitPods(ctx context.Context, t *testing.T, config *envconf.Config, pods []*corev1.Pod) {
	t.Helper()
	for _, pod := range pods {
		if err := config.Client().Resources().Delete(ctx, pod); err != nil {
			t.Logf("Warning: failed to delete pod %s: %v", pod.Name, err)
		}
	}
	for _, pod := range pods {
		_ = wait.For(conditions.New(config.Client().Resources()).ResourceDeleted(pod),
			wait.WithTimeout(2*time.Minute), wait.WithInterval(2*time.Second))
	}
	t.Logf("Deleted and confirmed removal of %d pod(s)", len(pods))
}

// =============================================================================
// Node control helpers
// =============================================================================

func setNodeENOApi(ctx context.Context, t *testing.T, config *envconf.Config, nodeName, enoApi string) {
	t.Helper()
	switch enoApi {
	case "", terwayTypes.APIEcs, terwayTypes.APIEnoHDeni, terwayTypes.APIEcsHDeni:
	default:
		t.Fatalf("setNodeENOApi: invalid %s value %q", terwayTypes.ENOApi, enoApi)
	}
	crNode := &networkv1beta1.Node{}
	if err := config.Client().Resources().Get(ctx, nodeName, "", crNode); err != nil {
		t.Fatalf("failed to get Node CR %s: %v", nodeName, err)
	}
	var val interface{}
	if enoApi != "" {
		val = enoApi
	}
	patchData, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{terwayTypes.ENOApi: val},
		},
	})
	if err := config.Client().Resources().Patch(ctx, crNode, k8s.Patch{
		PatchType: types.MergePatchType,
		Data:      patchData,
	}); err != nil {
		t.Fatalf("failed to patch Node CR %s ENOApi to %q: %v", nodeName, enoApi, err)
	}
	t.Logf("Set Node CR %s annotation %s=%q", nodeName, terwayTypes.ENOApi, displayENOApiForTest(enoApi))
}

func drainNodePods(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) {
	t.Helper()
	podList := &corev1.PodList{}
	if err := config.Client().Resources().List(ctx, podList); err != nil {
		t.Fatalf("failed to list pods: %v", err)
	}
	var toDelete []*corev1.Pod
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Spec.NodeName != nodeName || pod.Namespace == "kube-system" {
			continue
		}
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		toDelete = append(toDelete, pod)
	}
	if len(toDelete) == 0 {
		t.Logf("No non-system pods on node %s to drain", nodeName)
		return
	}
	t.Logf("Draining %d non-system pod(s) from node %s", len(toDelete), nodeName)
	for _, pod := range toDelete {
		if err := config.Client().Resources().Delete(ctx, pod); err != nil {
			t.Logf("  Warning: failed to delete pod %s/%s: %v", pod.Namespace, pod.Name, err)
		}
	}
	for _, pod := range toDelete {
		_ = wait.For(conditions.New(config.Client().Resources()).ResourceDeleted(pod),
			wait.WithTimeout(1*time.Minute), wait.WithInterval(2*time.Second))
	}
	t.Logf("Drain complete for node %s", nodeName)
}

func waitForNodeStable(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) {
	t.Helper()
	exclusive := nodeIsExclusiveENI(ctx, t, config, nodeName)
	t.Logf("Waiting for node %s to stabilize...", nodeName)
	err := wait.For(func(ctx context.Context) (bool, error) {
		if exclusive {
			niList := &networkv1beta1.NetworkInterfaceList{}
			if err := config.Client().Resources().List(ctx, niList); err != nil {
				return false, nil
			}
			for i := range niList.Items {
				ni := &niList.Items[i]
				if ni.Labels[networkInterfaceNodeLabel] != nodeName {
					continue
				}
				if ni.Status.Phase != networkv1beta1.ENIPhaseBind {
					return false, nil
				}
			}
		} else {
			crNode := &networkv1beta1.Node{}
			if err := config.Client().Resources().Get(ctx, nodeName, "", crNode); err != nil {
				return false, nil
			}
			for eniID, nic := range crNode.Status.NetworkInterfaces {
				if nic == nil {
					continue
				}
				if nic.Status != aliClient.ENIStatusInUse {
					t.Logf("  ENI %s status=%s, waiting...", eniID, nic.Status)
					return false, nil
				}
				for addr, ip := range nic.IPv4 {
					if ip != nil && ip.Status != networkv1beta1.IPStatusValid {
						t.Logf("  ENI %s IPv4 %s status=%s, waiting...", eniID, addr, ip.Status)
						return false, nil
					}
				}
				for addr, ip := range nic.IPv6 {
					if ip != nil && ip.Status != networkv1beta1.IPStatusValid {
						t.Logf("  ENI %s IPv6 %s status=%s, waiting...", eniID, addr, ip.Status)
						return false, nil
					}
				}
			}
		}
		podList := &corev1.PodList{}
		if err := config.Client().Resources().WithNamespace("kube-system").List(ctx, podList); err != nil {
			return false, nil
		}
		for i := range podList.Items {
			pod := &podList.Items[i]
			if pod.Spec.NodeName != nodeName {
				continue
			}
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil &&
					(cs.State.Waiting.Reason == "ContainerCreating" || cs.State.Waiting.Reason == "PodInitializing") {
					return false, nil
				}
			}
		}
		return true, nil
	}, wait.WithTimeout(migratePostCheckTimeout), wait.WithInterval(migratePostCheckInterval))

	if err != nil {
		t.Fatalf("node %s did not stabilize within %s: %v", nodeName, migratePostCheckTimeout, err)
	}
	t.Logf("Node %s is stable", nodeName)
}

func cordonNode(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) {
	t.Helper()
	node := &corev1.Node{}
	if err := config.Client().Resources().Get(ctx, nodeName, "", node); err != nil {
		t.Fatalf("failed to get k8s node %s: %v", nodeName, err)
	}
	patchData, _ := json.Marshal(map[string]interface{}{"spec": map[string]interface{}{"unschedulable": true}})
	if err := config.Client().Resources().Patch(ctx, node, k8s.Patch{PatchType: types.MergePatchType, Data: patchData}); err != nil {
		t.Fatalf("failed to cordon node %s: %v", nodeName, err)
	}
	t.Logf("Cordoned node %s", nodeName)
}

func uncordonNode(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) {
	t.Helper()
	node := &corev1.Node{}
	if err := config.Client().Resources().Get(ctx, nodeName, "", node); err != nil {
		t.Logf("Warning: failed to get k8s node %s for uncordon: %v", nodeName, err)
		return
	}
	if !node.Spec.Unschedulable {
		return
	}
	patchData, _ := json.Marshal(map[string]interface{}{"spec": map[string]interface{}{"unschedulable": false}})
	if err := config.Client().Resources().Patch(ctx, node, k8s.Patch{PatchType: types.MergePatchType, Data: patchData}); err != nil {
		t.Logf("Warning: failed to uncordon node %s: %v", nodeName, err)
		return
	}
	t.Logf("Uncordoned node %s", nodeName)
}

func displayENOApiForTest(api string) string {
	if api == "" {
		return "(eno)"
	}
	return api
}

// =============================================================================
// Per-node migration flows
// =============================================================================

// runSharedENIMigration executes the ENO→ECS→ENO round-trip on a shared ENI node.
func runSharedENIMigration(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string) {
	t.Helper()

	anno := nodeENOApi(ctx, t, config, nodeName)
	if anno != "" && anno != terwayTypes.APIEcs {
		t.Skipf("node %s eno-api=%q outside path A matrix", nodeName, anno)
	}

	// Phase 1: ENO mode — verify pods land on leni- ENIs
	drainNodePods(ctx, t, config, nodeName)
	setNodeENOApi(ctx, t, config, nodeName, "")
	waitForNodeStable(ctx, t, config, nodeName)
	verifyNodeENOApi(ctx, t, config, nodeName, "")

	phase1Pods := createAndWaitPods(ctx, t, config, nodeName, "migrate-s-p1", phase1SharedPodCount)
	verifySharedPodsOnENOENIs(ctx, t, config, nodeName, phase1Pods)
	t.Logf("Phase 1 complete: %d pods verified on leni- ENIs", len(phase1Pods))

	// Phase 2: migrate to ECS — verify mixed ENO+ECS allocation, then IP release
	cordonNode(ctx, t, config, nodeName)
	setNodeENOApi(ctx, t, config, nodeName, terwayTypes.APIEcs)
	waitForNodeStable(ctx, t, config, nodeName)
	verifyNodeENOApi(ctx, t, config, nodeName, terwayTypes.APIEcs)
	verifyNodeENIState(ctx, t, config, nodeName)
	uncordonNode(ctx, t, config, nodeName)

	crNode := &networkv1beta1.Node{}
	if err := config.Client().Resources().Get(ctx, nodeName, "", crNode); err != nil {
		t.Fatalf("failed to get Node CR: %v", err)
	}
	ipv4PerAdapter := crNode.Spec.NodeCap.IPv4PerAdapter
	var totalAvailable int
	for eniID, nic := range crNode.Status.NetworkInterfaces {
		if nic == nil || nic.NetworkInterfaceType == networkv1beta1.ENITypePrimary || !strings.HasPrefix(eniID, "leni-") {
			continue
		}
		if avail := ipv4PerAdapter - len(nic.IPv4); avail > 0 {
			totalAvailable += avail
		}
	}
	phase2Count := totalAvailable + 2
	if phase2Count > maxPhase2PodCount {
		phase2Count = maxPhase2PodCount
	}
	if phase2Count < 2 {
		phase2Count = 2
	}
	t.Logf("Phase 2: creating %d pods (leni- available=%d + 2 overflow for new eni-)", phase2Count, totalAvailable)

	phase2Pods := createAndWaitPods(ctx, t, config, nodeName, "migrate-s-p2", phase2Count)
	verifySharedPodsOnECSPath(ctx, t, config, nodeName, phase2Pods)

	phase2IPs := make(map[string]string, len(phase2Pods))
	for _, pod := range phase2Pods {
		phase2IPs[pod.Status.PodIP] = pod.Name
	}
	deleteAndWaitPods(ctx, t, config, phase2Pods)
	verifySharedIPsReleased(ctx, t, config, nodeName, phase2IPs)

	phase1IPs := make(map[string]string, len(phase1Pods))
	for _, pod := range phase1Pods {
		phase1IPs[pod.Status.PodIP] = pod.Name
	}
	deleteAndWaitPods(ctx, t, config, phase1Pods)
	verifySharedIPsReleased(ctx, t, config, nodeName, phase1IPs)
	t.Logf("Phase 2 complete: ECS allocation and IP release verified")

	// Phase 3: migrate back to ENO — verify pods land on leni- ENIs again
	setNodeENOApi(ctx, t, config, nodeName, "")
	waitForNodeStable(ctx, t, config, nodeName)
	verifyNodeENOApi(ctx, t, config, nodeName, "")
	verifyNodeENIState(ctx, t, config, nodeName)

	phase3Pods := createAndWaitPods(ctx, t, config, nodeName, "migrate-s-p3", phase3PodCount)
	verifySharedPodsOnENOENIs(ctx, t, config, nodeName, phase3Pods)
	phase3IPs := make(map[string]string, len(phase3Pods))
	for _, pod := range phase3Pods {
		phase3IPs[pod.Status.PodIP] = pod.Name
	}
	deleteAndWaitPods(ctx, t, config, phase3Pods)
	verifySharedIPsReleased(ctx, t, config, nodeName, phase3IPs)
	t.Logf("Phase 3 complete: ECS→ENO migration verified, pods on leni- ENIs")
}

// runExclusiveENIMigration executes the ENO→ECS→ENO round-trip on an exclusive ENI node.
// API path (A or B) is determined by apiCfg.
func runExclusiveENIMigration(ctx context.Context, t *testing.T, config *envconf.Config, nodeName string, apiCfg migrateAPIConfig) {
	t.Helper()

	anno := nodeENOApi(ctx, t, config, nodeName)
	if apiCfg != pathBConfig && anno != "" && anno != terwayTypes.APIEcs {
		t.Skipf("node %s eno-api=%q outside path A matrix", nodeName, anno)
	}

	enoPrefixes := apiCfg.enoENIPrefixes()

	// Phase 1: ENO mode — verify PodENI allocations on ENO ENIs
	drainNodePods(ctx, t, config, nodeName)
	setNodeENOApi(ctx, t, config, nodeName, apiCfg.efloAPI)
	waitForNodeStable(ctx, t, config, nodeName)
	verifyNodeENOApi(ctx, t, config, nodeName, apiCfg.efloAPI)

	phase1Pods := createAndWaitPods(ctx, t, config, nodeName, "migrate-e-p1", phase1ExclusivePodCount)
	var phase1NINames []string
	for _, pod := range phase1Pods {
		podENI, err := WaitPodENIReady(ctx, pod.Namespace, pod.Name, config.Client())
		if err != nil {
			t.Fatalf("[Phase1] PodENI not ready for pod %s: %v", pod.Name, err)
		}
		verifyExclusivePodENIPrefix(t, podENI, pod.Name, enoPrefixes)
		for _, alloc := range podENI.Spec.Allocations {
			if alloc.ENI.ID != "" {
				phase1NINames = append(phase1NINames, alloc.ENI.ID)
			}
		}
	}
	t.Logf("Phase 1 complete: %d pods verified on %v ENIs", len(phase1Pods), enoPrefixes)

	// Phase 2: migrate to ECS — verify PodENI allocations on eni- ENIs, then ENI release
	cordonNode(ctx, t, config, nodeName)
	setNodeENOApi(ctx, t, config, nodeName, apiCfg.ecsAPI)
	waitForNodeStable(ctx, t, config, nodeName)
	verifyNodeENOApi(ctx, t, config, nodeName, apiCfg.ecsAPI)
	verifyNodeENIState(ctx, t, config, nodeName)
	uncordonNode(ctx, t, config, nodeName)

	phase2Pods := createAndWaitPods(ctx, t, config, nodeName, "migrate-e-p2", phase1ExclusivePodCount)
	var phase2NINames []string
	for _, pod := range phase2Pods {
		podENI, err := WaitPodENIReady(ctx, pod.Namespace, pod.Name, config.Client())
		if err != nil {
			t.Fatalf("[Phase2] PodENI not ready for pod %s: %v", pod.Name, err)
		}
		verifyExclusivePodENIPrefix(t, podENI, pod.Name, []string{"eni-"})
		for _, alloc := range podENI.Spec.Allocations {
			if alloc.ENI.ID != "" {
				phase2NINames = append(phase2NINames, alloc.ENI.ID)
			}
		}
	}

	deleteAndWaitPods(ctx, t, config, phase2Pods)
	verifyExclusiveENIsReleased(ctx, t, config, phase2NINames)

	deleteAndWaitPods(ctx, t, config, phase1Pods)
	verifyExclusiveENIsReleased(ctx, t, config, phase1NINames)
	t.Logf("Phase 2 complete: eni- allocation and ENI release verified")

	// Phase 3: migrate back to ENO — verify PodENI allocations on ENO ENIs again
	setNodeENOApi(ctx, t, config, nodeName, apiCfg.efloAPI)
	waitForNodeStable(ctx, t, config, nodeName)
	verifyNodeENOApi(ctx, t, config, nodeName, apiCfg.efloAPI)
	verifyNodeENIState(ctx, t, config, nodeName)

	phase3Pods := createAndWaitPods(ctx, t, config, nodeName, "migrate-e-p3", phase3PodCount)
	var phase3NINames []string
	for _, pod := range phase3Pods {
		podENI, err := WaitPodENIReady(ctx, pod.Namespace, pod.Name, config.Client())
		if err != nil {
			t.Fatalf("[Phase3] PodENI not ready for pod %s: %v", pod.Name, err)
		}
		verifyExclusivePodENIPrefix(t, podENI, pod.Name, enoPrefixes)
		for _, alloc := range podENI.Spec.Allocations {
			if alloc.ENI.ID != "" {
				phase3NINames = append(phase3NINames, alloc.ENI.ID)
			}
		}
	}
	deleteAndWaitPods(ctx, t, config, phase3Pods)
	verifyExclusiveENIsReleased(ctx, t, config, phase3NINames)
	t.Logf("Phase 3 complete: ECS→ENO migration verified, pods on %v ENIs", enoPrefixes)
}

// =============================================================================
// Test: Unified migration test
//
// Auto-discovers all LingJun nodes and runs a per-node subtest. Each subtest
// detects whether the node is shared or exclusive ENI and runs the
// corresponding migration flow (runSharedENIMigration / runExclusiveENIMigration).
// =============================================================================

func TestMigrate(t *testing.T) {
	feat := features.New("Migrate").
		WithLabel("suite", "migrate").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatal(err)
			}
			if !nodeInfo.HasLingjunNodes() {
				t.Skip("no LingJun nodes available")
			}
			if err := updateENIConfigWithRetry(ctx, config, func(eniJson *gabs.Container) error {
				_, _ = eniJson.Set(0, "max_pool_size")
				_, _ = eniJson.Set(0, "min_pool_size")
				return nil
			}); err != nil {
				t.Fatalf("failed to set pool config: %v", err)
			}
			if err := restartTerway(ctx, config); err != nil {
				t.Fatalf("failed to restart Terway: %v", err)
			}
			return ctx
		}).
		Assess("ENO↔ECS round-trip per node", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatal(err)
			}
			targetNodes := getMigrateTargetNodes(t, nodeInfo)
			if len(targetNodes) == 0 {
				t.Skip("no LingJun nodes matched MIGRATE_NODES filter")
			}

			// Barrier: the outer t.Run blocks until all parallel subtests complete,
			// preventing Teardown from starting while migrations are still in flight.
			t.Run("nodes", func(t *testing.T) {
				for _, nodeName := range targetNodes {
					nodeName := nodeName
					t.Run(nodeName, func(t *testing.T) {
						t.Parallel()
						exclusive := nodeIsExclusiveENI(ctx, t, config, nodeName)
						if exclusive {
							apiCfg := getNodeAPIConfig(ctx, t, config, nodeName)
							t.Logf("Node %s: exclusive ENI, path=%s", nodeName, displayENOApiForTest(apiCfg.efloAPI))
							runExclusiveENIMigration(ctx, t, config, nodeName, apiCfg)
						} else {
							t.Logf("Node %s: shared ENI, path A", nodeName)
							runSharedENIMigration(ctx, t, config, nodeName)
						}
					})
				}
			})

			return MarkTestSuccess(ctx)
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeInfo, _ := DiscoverNodeTypes(ctx, config.Client())
			if nodeInfo == nil {
				return ctx
			}
			targetNodes := getMigrateTargetNodes(t, nodeInfo)
			for _, nodeName := range targetNodes {
				uncordonNode(ctx, t, config, nodeName)
				apiCfg := getNodeAPIConfig(ctx, t, config, nodeName)
				setNodeENOApi(ctx, t, config, nodeName, apiCfg.efloAPI)
				waitForNodeStable(ctx, t, config, nodeName)
			}
			_ = updateENIConfigWithRetry(ctx, config, func(eniJson *gabs.Container) error {
				_, _ = eniJson.Set(5, "max_pool_size")
				_, _ = eniJson.Set(0, "min_pool_size")
				return nil
			})
			_ = restartTerway(ctx, config)
			return ctx
		}).
		Feature()

	testenv.Test(t, feat)
}

// =============================================================================
// Pod Builder Extensions
// =============================================================================

func (p *Pod) WithNodeName(nodeName string) *Pod {
	p.Pod.Spec.NodeName = nodeName
	return p
}
