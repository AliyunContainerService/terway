//go:build e2e

package tests

import (
	"context"
	"encoding/json"
	"flag"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient"
)

var (
	upgradePhase            string
	upgradeExpectedDatapath string
)

const upgradeTestNamespace = "terway-upgrade-test"

const upgradeENIConfigSnapshotName = "upgrade-eni-config-snapshot"

var upgradeHairpinNodeTypes = []NodeType{
	NodeTypeECSSharedENI,
	NodeTypeECSExclusiveENI,
}

func init() {
	flag.StringVar(&upgradePhase, "upgrade-phase", "", "upgrade test phase: pre|post")
	flag.StringVar(&upgradeExpectedDatapath, "upgrade-expected-datapath", "", "expected eniip_virtual_type: unset, IPVlan, or datapathv2")
}

// TestUpgrade_PreUpgrade creates long-lived server and client pods that must
// survive the terway upgrade. Pods are NOT cleaned up — they persist for PostUpgrade.
//
// Run with: go test -tags e2e -run TestUpgrade_PreUpgrade -upgrade-phase pre
func TestUpgrade_PreUpgrade(t *testing.T) {
	if upgradePhase != "pre" {
		t.Skip("not pre-upgrade phase")
	}

	ctx := context.Background()
	client := testenv.EnvConf().Client()
	ns := upgradeTestNamespace

	t.Log("=== PreUpgrade: creating long-lived pods ===")
	snapshotUpgradeENIConfig(ctx, t, client, ns)

	// 1. Create server pod (nginx, listens on port 80)
	server := NewPod("upgrade-server", ns).
		WithLabels(map[string]string{"app": "upgrade-server"}).
		WithContainer("server", nginxImage, nil)
	if err := client.Resources().Create(ctx, server.Pod); err != nil {
		t.Fatalf("failed to create server pod: %v", err)
	}

	// 2. Create ClusterIP service for server
	svc := NewService("upgrade-server", ns, map[string]string{"app": "upgrade-server"}).
		ExposePort(80, "http").
		WithIPFamily("ipv4")
	if err := client.Resources().Create(ctx, svc.Service); err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	// 3. Create client pod — use preferred anti-affinity (best-effort cross-node, but works on single-node)
	clientPod := NewPod("upgrade-client", ns).
		WithLabels(map[string]string{"app": "upgrade-client"}).
		WithContainer("client", nginxImage, nil)
	// Soft anti-affinity: try different node, but allow same node if only 1 available
	if clientPod.Spec.Affinity == nil {
		clientPod.Spec.Affinity = &corev1.Affinity{}
	}
	clientPod.Spec.Affinity.PodAntiAffinity = &corev1.PodAntiAffinity{
		PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
			{
				Weight: 100,
				PodAffinityTerm: corev1.PodAffinityTerm{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "upgrade-server"},
					},
					TopologyKey: "kubernetes.io/hostname",
				},
			},
		},
	}
	if err := client.Resources().Create(ctx, clientPod.Pod); err != nil {
		t.Fatalf("failed to create client pod: %v", err)
	}

	// 4. Wait for both pods to be Ready
	if err := waitPodsReady(client, server.Pod, clientPod.Pod); err != nil {
		t.Fatalf("pods failed to become ready: %v", err)
	}

	// 5. Verify cross-node connectivity works
	_, err := Pull(client, ns, "upgrade-client", "client", "upgrade-server", t)
	if err != nil {
		t.Errorf("pre-upgrade connectivity test failed: %v", err)
	} else {
		t.Log("PreUpgrade: cross-node connectivity verified")
	}

	nodeInfo, err := DiscoverNodeTypes(ctx, client)
	if err != nil {
		t.Fatalf("discover node types for pre-upgrade hairpin tests: %v", err)
	}
	for _, nodeType := range upgradeHairpinNodeTypes {
		nodeType := nodeType
		t.Run("ExistingHairpin/"+string(nodeType), func(t *testing.T) {
			requireUpgradeNodeType(t, nodeInfo, nodeType)
			podName := upgradeHairpinName("existing", nodeType)
			createUpgradeHairpinWorkload(ctx, t, client, ns, podName, nodeType, true)
			verifyUpgradeHairpin(ctx, t, client, ns, podName, nodeType)
		})
	}

	// DON'T save resources to context — pods must persist for PostUpgrade.
	// AfterEachFeature only cleans up resources saved via SaveResources.
	t.Log("=== PreUpgrade complete: pods will persist through upgrade ===")
}

// TestUpgrade_PostUpgrade verifies existing long-lived pods survived the upgrade
// and creates new pods to verify basic connectivity works after upgrade.
//
// For chain upgrades (e.g. 1.7.4→1.9.18→1.17.6), this test runs after EACH hop.
// The long-lived pods from PreUpgrade must survive ALL hops.
//
// Run with: go test -tags e2e -run TestUpgrade_PostUpgrade -upgrade-phase post
func TestUpgrade_PostUpgrade(t *testing.T) {
	if upgradePhase != "post" {
		t.Skip("not post-upgrade phase")
	}

	ctx := context.Background()
	client := testenv.EnvConf().Client()
	ns := upgradeTestNamespace
	verifyUpgradeENIConfigUnchanged(ctx, t, client, ns)

	// === Step 1: Verify existing long-lived pods survived the upgrade ===
	t.Run("VerifyLongLivedPods", func(t *testing.T) {
		// Check server pod
		serverPod := &corev1.Pod{}
		if err := client.Resources().Get(ctx, "upgrade-server", ns, serverPod); err != nil {
			t.Fatalf("server pod not found (did PreUpgrade run?): %v", err)
		}
		if serverPod.Status.Phase != corev1.PodRunning {
			t.Fatalf("server pod not Running: phase=%s", serverPod.Status.Phase)
		}
		for _, cs := range serverPod.Status.ContainerStatuses {
			if cs.RestartCount > 0 {
				t.Errorf("server pod restarted %d times during upgrade", cs.RestartCount)
			}
		}
		t.Logf("Server pod OK: node=%s, restartCount=0", serverPod.Spec.NodeName)

		// Check client pod
		clientPod := &corev1.Pod{}
		if err := client.Resources().Get(ctx, "upgrade-client", ns, clientPod); err != nil {
			t.Fatalf("client pod not found (did PreUpgrade run?): %v", err)
		}
		if clientPod.Status.Phase != corev1.PodRunning {
			t.Fatalf("client pod not Running: phase=%s", clientPod.Status.Phase)
		}
		for _, cs := range clientPod.Status.ContainerStatuses {
			if cs.RestartCount > 0 {
				t.Errorf("client pod restarted %d times during upgrade", cs.RestartCount)
			}
		}
		t.Logf("Client pod OK: node=%s, restartCount=0", clientPod.Spec.NodeName)

		// Verify server and client are on different nodes (cross-node)
		if serverPod.Spec.NodeName == clientPod.Spec.NodeName {
			t.Logf("Warning: server and client on same node %s (anti-affinity may not have worked)", serverPod.Spec.NodeName)
		}

		// Verify connectivity still works from surviving pods
		_, err := Pull(client, ns, "upgrade-client", "client", "upgrade-server", t)
		if err != nil {
			t.Errorf("post-upgrade connectivity from surviving client failed: %v", err)
		} else {
			t.Log("Long-lived pods survived upgrade, connectivity still works")
		}
	})

	// === Step 2: Verify hairpin before unrelated connectivity checks can block ===
	// Cover both shared and exclusive ENI nodes. Node type alone does not imply
	// that a Pod is represented by a Cilium generic-veth endpoint.
	nodeInfo, err := DiscoverNodeTypes(ctx, client)
	if err != nil {
		t.Fatalf("discover node types for post-upgrade hairpin tests: %v", err)
	}
	for _, nodeType := range upgradeHairpinNodeTypes {
		nodeType := nodeType
		t.Run("ExistingHairpin/"+string(nodeType), func(t *testing.T) {
			requireUpgradeNodeType(t, nodeInfo, nodeType)
			verifyUpgradeHairpin(ctx, t, client, ns, upgradeHairpinName("existing", nodeType), nodeType)
		})
		t.Run("NewHairpin/"+string(nodeType), func(t *testing.T) {
			requireUpgradeNodeType(t, nodeInfo, nodeType)
			podName := upgradeHairpinName("new", nodeType)
			createUpgradeHairpinWorkload(ctx, t, client, ns, podName, nodeType, false)
			verifyUpgradeHairpin(ctx, t, client, ns, podName, nodeType)
		})
	}

	// === Step 3: Create new pods and verify basic connectivity ===
	t.Run("NewPodConnectivity", func(t *testing.T) {
		newServer := NewPod("upgrade-server-new", ns).
			WithLabels(map[string]string{"app": "upgrade-server-new"}).
			WithContainer("server", nginxImage, nil)
		if err := client.Resources().Create(ctx, newServer.Pod); err != nil {
			t.Fatalf("create new server pod: %v", err)
		}
		defer func() { _ = client.Resources().Delete(ctx, newServer.Pod) }()

		newSvc := NewService("upgrade-server-new", ns, map[string]string{"app": "upgrade-server-new"}).
			ExposePort(80, "http").
			WithIPFamily("ipv4")
		if err := client.Resources().Create(ctx, newSvc.Service); err != nil {
			t.Fatalf("create new service: %v", err)
		}
		defer func() { _ = client.Resources().Delete(ctx, newSvc.Service) }()

		newClient := NewPod("upgrade-client-new", ns).
			WithLabels(map[string]string{"app": "upgrade-client-new"}).
			WithContainer("client", nginxImage, nil)
		// Soft anti-affinity for new client too
		if newClient.Spec.Affinity == nil {
			newClient.Spec.Affinity = &corev1.Affinity{}
		}
		newClient.Spec.Affinity.PodAntiAffinity = &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
				{
					Weight: 100,
					PodAffinityTerm: corev1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"app": "upgrade-server-new"},
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		}
		if err := client.Resources().Create(ctx, newClient.Pod); err != nil {
			t.Fatalf("create new client pod: %v", err)
		}
		defer func() { _ = client.Resources().Delete(ctx, newClient.Pod) }()

		if err := waitPodsReady(client, newServer.Pod, newClient.Pod); err != nil {
			t.Fatalf("new pods failed to become ready: %v", err)
		}

		_, err := Pull(client, ns, "upgrade-client-new", "client", "upgrade-server-new", t)
		if err != nil {
			t.Errorf("new pod connectivity failed: %v", err)
		} else {
			t.Log("New pod connectivity verified after upgrade")
		}
	})

	if t.Failed() {
		isFailed.Store(true)
	}
}

func snapshotUpgradeENIConfig(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
	t.Helper()
	data := getAndValidateUpgradeENIConfig(ctx, t, client)
	snapshot := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: upgradeENIConfigSnapshotName, Namespace: namespace},
		Data:       data,
	}
	if err := client.Resources().Create(ctx, snapshot); err != nil {
		t.Fatalf("create eni-config snapshot: %v", err)
	}
}

func verifyUpgradeENIConfigUnchanged(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
	t.Helper()
	want := &corev1.ConfigMap{}
	if err := client.Resources().Get(ctx, upgradeENIConfigSnapshotName, namespace, want); err != nil {
		t.Fatalf("get pre-upgrade eni-config snapshot: %v", err)
	}
	got := getAndValidateUpgradeENIConfig(ctx, t, client)
	if !reflect.DeepEqual(got, want.Data) {
		t.Fatalf("eni-config changed during upgrade: before=%q after=%q", want.Data, got)
	}
	t.Log("eni-config remained byte-for-byte unchanged during upgrade")
}

func getAndValidateUpgradeENIConfig(ctx context.Context, t *testing.T, client klient.Client) map[string]string {
	t.Helper()
	cm := &corev1.ConfigMap{}
	if err := client.Resources().Get(ctx, "eni-config", "kube-system", cm); err != nil {
		t.Fatalf("get kube-system/eni-config: %v", err)
	}

	eniConf := map[string]any{}
	if err := json.Unmarshal([]byte(cm.Data["eni_conf"]), &eniConf); err != nil {
		t.Fatalf("parse eni-config eni_conf: %v", err)
	}
	if value, exists := eniConf["ipam_type"]; exists {
		t.Fatalf("BYO upgrade scenario must not set eni_conf.ipam_type, got %v", value)
	}

	cniConf := map[string]any{}
	if err := json.Unmarshal([]byte(cm.Data["10-terway.conf"]), &cniConf); err != nil {
		t.Fatalf("parse eni-config 10-terway.conf: %v", err)
	}
	virtualType, exists := cniConf["eniip_virtual_type"]
	switch upgradeExpectedDatapath {
	case "":
	case "unset":
		if exists {
			t.Fatalf("upgrade scenario requires veth mode with eniip_virtual_type unset, got %v", virtualType)
		}
	case "IPVlan", "datapathv2":
		if virtualType != upgradeExpectedDatapath {
			t.Fatalf("upgrade scenario requires eniip_virtual_type=%s, got %v", upgradeExpectedDatapath, virtualType)
		}
	default:
		t.Fatalf("unsupported -upgrade-expected-datapath value %q", upgradeExpectedDatapath)
	}

	return map[string]string{
		"eni_conf":       cm.Data["eni_conf"],
		"10-terway.conf": cm.Data["10-terway.conf"],
	}
}

func upgradeHairpinName(lifecycle string, nodeType NodeType) string {
	return "upgrade-hp-" + lifecycle + "-" + string(nodeType)
}

func requireUpgradeNodeType(t *testing.T, nodeInfo *NodeTypeInfo, nodeType NodeType) {
	t.Helper()
	if len(nodeInfo.GetNodesByType(nodeType)) == 0 {
		t.Skipf("no nodes of type %s available", nodeType)
	}
}

func createUpgradeHairpinWorkload(ctx context.Context, t *testing.T, client klient.Client, namespace, name string, nodeType NodeType, persist bool) {
	t.Helper()
	pod := NewPod(name, namespace).
		WithLabels(map[string]string{"app": name, "upgrade-node-type": string(nodeType)}).
		WithContainer("server", nginxImage, nil)
	pod = applyNodeAffinityAndTolerations(pod, nodeType)
	if err := client.Resources().Create(ctx, pod.Pod); err != nil {
		t.Fatalf("create %s hairpin pod: %v", nodeType, err)
	}

	svc := NewService(name, namespace, map[string]string{"app": name, "upgrade-node-type": string(nodeType)}).
		ExposePort(80, "http").
		WithIPFamily("ipv4")
	if err := client.Resources().Create(ctx, svc.Service); err != nil {
		_ = client.Resources().Delete(ctx, pod.Pod)
		t.Fatalf("create %s hairpin service: %v", nodeType, err)
	}
	if !persist {
		t.Cleanup(func() {
			_ = client.Resources().Delete(context.Background(), svc.Service)
			_ = client.Resources().Delete(context.Background(), pod.Pod)
		})
	}
	if err := waitPodsReady(client, pod.Pod); err != nil {
		t.Fatalf("wait for %s hairpin pod: %v", nodeType, err)
	}
}

func verifyUpgradeHairpin(ctx context.Context, t *testing.T, client klient.Client, namespace, name string, nodeType NodeType) {
	t.Helper()
	pod := &corev1.Pod{}
	if err := client.Resources().Get(ctx, name, namespace, pod); err != nil {
		t.Fatalf("get %s hairpin pod %s: %v", nodeType, name, err)
	}
	if pod.Status.Phase != corev1.PodRunning {
		t.Fatalf("%s hairpin pod %s is not Running: %s", nodeType, name, pod.Status.Phase)
	}
	svc := &corev1.Service{}
	if err := client.Resources().Get(ctx, name, namespace, svc); err != nil {
		t.Fatalf("get %s hairpin service %s: %v", nodeType, name, err)
	}
	if svc.Spec.ClusterIP == "" || svc.Spec.ClusterIP == corev1.ClusterIPNone {
		t.Fatalf("%s hairpin service %s has invalid ClusterIP %q", nodeType, name, svc.Spec.ClusterIP)
	}
	t.Logf("Hairpin target: lifecycle=%s nodeType=%s node=%s podIP=%s serviceIP=%s", name, nodeType, pod.Spec.NodeName, pod.Status.PodIP, svc.Spec.ClusterIP)
	// Use the ClusterIP explicitly. The pod and service intentionally share a
	// name, so resolving the bare name inside the pod can hit the pod's
	// /etc/hosts entry and bypass service hairpin processing entirely.
	if _, err := Pull(client, namespace, name, "server", "http://"+svc.Spec.ClusterIP, t); err != nil {
		t.Errorf("hairpin connectivity failed for %s pod %s on node %s: %v", nodeType, name, pod.Spec.NodeName, err)
	}
}
