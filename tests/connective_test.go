//go:build e2e

package tests

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

func getStack() []string {
	var r []string
	if testIPv4 {
		r = append(r, "ipv4")
	}
	if testIPv6 {
		r = append(r, "ipv6")
	}
	return r
}

// =============================================================================
// Shared ENI Network Policy Tests
// =============================================================================

// TestSharedENI_NetworkPolicy_HealthCheck tests that pods with network policy can still pass health checks
// for shared ENI mode on both ECS and Lingjun nodes.
func TestSharedENI_NetworkPolicy_HealthCheck(t *testing.T) {
	if !testNetworkPolicy {
		t.Log("Skip networkPolicy tests")
		return
	}

	if terway != "terway-eniip" {
		t.Logf("TestSharedENI_NetworkPolicy_HealthCheck requires terway-eniip daemonset, current: %s, skipping", terway)
		return
	}

	nodeTypes := []NodeType{NodeTypeECSSharedENI, NodeTypeLingjunSharedENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		feat := createNetworkPolicyHealthCheckTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestSharedENI_NetworkPolicy_DenyIngressSameNode tests ingress deny policy for pods on the same node
// for shared ENI mode on both ECS and Lingjun nodes.
func TestSharedENI_NetworkPolicy_DenyIngressSameNode(t *testing.T) {
	if !testNetworkPolicy {
		t.Log("Skip networkPolicy tests")
		return
	}

	if terway != "terway-eniip" {
		t.Logf("TestSharedENI_NetworkPolicy_DenyIngressSameNode requires terway-eniip daemonset, current: %s, skipping", terway)
		return
	}

	nodeTypes := []NodeType{NodeTypeECSSharedENI, NodeTypeLingjunSharedENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		feat := createNetworkPolicyDenyIngressSameNodeTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestSharedENI_NetworkPolicy_DenyIngressCrossNode tests ingress deny policy for pods on different nodes
// for shared ENI mode on both ECS and Lingjun nodes.
func TestSharedENI_NetworkPolicy_DenyIngressCrossNode(t *testing.T) {
	if !testNetworkPolicy {
		t.Log("Skip networkPolicy tests")
		return
	}

	if terway != "terway-eniip" {
		t.Logf("TestSharedENI_NetworkPolicy_DenyIngressCrossNode requires terway-eniip daemonset, current: %s, skipping", terway)
		return
	}

	nodeTypes := []NodeType{NodeTypeECSSharedENI, NodeTypeLingjunSharedENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		feat := createNetworkPolicyDenyIngressCrossNodeTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestSharedENI_NetworkPolicy_DenyEgressSameNode tests egress deny policy for pods on the same node
// for shared ENI mode on both ECS and Lingjun nodes.
func TestSharedENI_NetworkPolicy_DenyEgressSameNode(t *testing.T) {
	if !testNetworkPolicy {
		t.Log("Skip networkPolicy tests")
		return
	}

	if terway != "terway-eniip" {
		t.Logf("TestSharedENI_NetworkPolicy_DenyEgressSameNode requires terway-eniip daemonset, current: %s, skipping", terway)
		return
	}

	nodeTypes := []NodeType{NodeTypeECSSharedENI, NodeTypeLingjunSharedENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		feat := createNetworkPolicyDenyEgressSameNodeTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestSharedENI_NetworkPolicy_DenyEgressCrossNode tests egress deny policy for pods on different nodes
// for shared ENI mode on both ECS and Lingjun nodes.
func TestSharedENI_NetworkPolicy_DenyEgressCrossNode(t *testing.T) {
	if !testNetworkPolicy {
		t.Log("Skip networkPolicy tests")
		return
	}

	if terway != "terway-eniip" {
		t.Logf("TestSharedENI_NetworkPolicy_DenyEgressCrossNode requires terway-eniip daemonset, current: %s, skipping", terway)
		return
	}

	nodeTypes := []NodeType{NodeTypeECSSharedENI, NodeTypeLingjunSharedENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		feat := createNetworkPolicyDenyEgressCrossNodeTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// =============================================================================
// Shared ENI HostPort Tests
// =============================================================================

// TestSharedENI_HostPort_NodeIP tests HostPort functionality via node internal IP
// for shared ENI mode on both ECS and Lingjun nodes.
func TestSharedENI_HostPort_NodeIP(t *testing.T) {
	if terway != "terway-eniip" {
		t.Logf("TestSharedENI_HostPort_NodeIP requires terway-eniip daemonset, current: %s, skipping", terway)
		return
	}

	nodeTypes := []NodeType{NodeTypeECSSharedENI, NodeTypeLingjunSharedENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		feat := createHostPortNodeIPTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestSharedENI_HostPort_ExternalIP tests HostPort functionality via node external IP
// for shared ENI mode on both ECS and Lingjun nodes.
func TestSharedENI_HostPort_ExternalIP(t *testing.T) {
	if terway != "terway-eniip" {
		t.Logf("TestSharedENI_HostPort_ExternalIP requires terway-eniip daemonset, current: %s, skipping", terway)
		return
	}

	nodeTypes := []NodeType{NodeTypeECSSharedENI, NodeTypeLingjunSharedENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		feat := createHostPortExternalIPTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// =============================================================================
// Network Policy Test Helpers
// =============================================================================

func createNetworkPolicyHealthCheckTest(nodeType NodeType) features.Feature {
	eniMode := getENIModeFromNodeType(nodeType)
	machineType := getMachineTypeFromNodeType(nodeType)
	serverName := fmt.Sprintf("server-healthcheck-%s", nodeType)

	return features.New(fmt.Sprintf("%sENI/NetworkPolicy/HealthCheck/%s", eniMode, machineType)).
		WithLabel("eni-mode", eniMode).
		WithLabel("machine-type", machineType).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("Failed to discover node types: %v", err)
			}

			requiredNodes := nodeInfo.GetNodesByType(nodeType)
			if len(requiredNodes) == 0 {
				t.Skipf("No nodes of type %s available", nodeType)
			}

			var objs []k8s.Object

			policy := NewNetworkPolicy(fmt.Sprintf("deny-ingress-%s", nodeType), config.Namespace()).
				WithPolicyType(networkingv1.PolicyTypeIngress)

			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{"app": "server", "node-type": string(nodeType)}).
				WithContainer("server", nginxImage, nil).
				WithHealthCheck(80)

			server = applyNodeAffinityAndTolerations(server, nodeType)

			objs = append(objs, policy.NetworkPolicy, server.Pod)

			for _, obj := range objs {
				err := config.Client().Resources().Create(ctx, obj)
				if err != nil {
					t.Fatalf("create resource failed: %v", err)
				}
			}

			ctx = SaveResources(ctx, objs...)
			return ctx
		}).
		Assess("Pod should be ready despite network policy", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: serverName, Namespace: config.Namespace()}}
			err := waitPodsReady(config.Client(), server)
			if err != nil {
				t.Fatalf("Pod failed to become ready: %v", err)
			}

			t.Logf("Pod is ready on node type %s", nodeType)
			return MarkTestSuccess(ctx)
		}).
		Feature()
}

func createNetworkPolicyDenyIngressSameNodeTest(nodeType NodeType) features.Feature {
	eniMode := getENIModeFromNodeType(nodeType)
	machineType := getMachineTypeFromNodeType(nodeType)
	clientName := fmt.Sprintf("client-ingress-same-%s", nodeType)
	serverName := fmt.Sprintf("server-ingress-same-%s", nodeType)
	serverDenyName := fmt.Sprintf("server-deny-ingress-same-%s", nodeType)

	return features.New(fmt.Sprintf("%sENI/NetworkPolicy/DenyIngressSameNode/%s", eniMode, machineType)).
		WithLabel("eni-mode", eniMode).
		WithLabel("machine-type", machineType).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("Failed to discover node types: %v", err)
			}

			requiredNodes := nodeInfo.GetNodesByType(nodeType)
			if len(requiredNodes) == 0 {
				t.Skipf("No nodes of type %s available", nodeType)
			}

			var objs []k8s.Object

			policy := NewNetworkPolicy(fmt.Sprintf("deny-ingress-same-%s", nodeType), config.Namespace()).
				WithPolicyType(networkingv1.PolicyTypeIngress).
				WithPodSelector(map[string]string{"app": "deny-ingress", "node-type": string(nodeType)})

			serverDenyIngress := NewPod(serverDenyName, config.Namespace()).
				WithLabels(map[string]string{"app": "deny-ingress", "node-type": string(nodeType)}).
				WithContainer("server", nginxImage, nil)
			serverDenyIngress = applyNodeAffinityAndTolerations(serverDenyIngress, nodeType)

			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{"app": "server", "node-type": string(nodeType)}).
				WithContainer("server", nginxImage, nil).
				WithPodAffinity(map[string]string{"app": "deny-ingress", "node-type": string(nodeType)})
			server = applyNodeAffinityAndTolerations(server, nodeType)

			client := NewPod(clientName, config.Namespace()).
				WithLabels(map[string]string{"app": "client", "node-type": string(nodeType)}).
				WithContainer("client", nginxImage, nil).
				WithPodAffinity(map[string]string{"app": "server", "node-type": string(nodeType)})
			client = applyNodeAffinityAndTolerations(client, nodeType)

			objs = append(objs, policy.NetworkPolicy, serverDenyIngress.Pod, server.Pod, client.Pod)

			for _, stack := range getStack() {
				denySvc := NewService(fmt.Sprintf("server-deny-%s-%s", nodeType, stack), config.Namespace(),
					map[string]string{"app": "deny-ingress", "node-type": string(nodeType)}).
					WithIPFamily(stack).ExposePort(80, "http")

				normalSvc := NewService(fmt.Sprintf("server-%s-%s", nodeType, stack), config.Namespace(),
					map[string]string{"app": "server", "node-type": string(nodeType)}).
					WithIPFamily(stack).ExposePort(80, "http")

				objs = append(objs, denySvc.Service, normalSvc.Service)
			}

			for _, obj := range objs {
				err := config.Client().Resources().Create(ctx, obj)
				if err != nil {
					t.Fatalf("create resource failed: %v", err)
				}
			}

			ctx = SaveResources(ctx, objs...)
			return ctx
		}).
		Assess("Ingress policy should deny traffic", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: clientName, Namespace: config.Namespace()}}
			server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: serverName, Namespace: config.Namespace()}}
			serverDeny := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: serverDenyName, Namespace: config.Namespace()}}

			err := waitPodsReady(config.Client(), client, server, serverDeny)
			if err != nil {
				t.Fatalf("wait pods ready failed: %v", err)
			}

			for _, stack := range getStack() {
				t.Logf("Testing %s: client should access server (no policy)", stack)
				_, err = Pull(config.Client(), client.Namespace, client.Name, "client", fmt.Sprintf("server-%s-%s", nodeType, stack), t)
				if err != nil {
					t.Errorf("client should access server (%s): %v", stack, err)
				}

				t.Logf("Testing %s: client should NOT access deny-ingress server (policy applied)", stack)
				_, err = PullFail(config.Client(), client.Namespace, client.Name, "client", fmt.Sprintf("server-deny-%s-%s", nodeType, stack), t)
				if err != nil {
					t.Errorf("client should not access deny-ingress server (%s): %v", stack, err)
				}
			}
			return MarkTestSuccess(ctx)
		}).
		Feature()
}

func createNetworkPolicyDenyIngressCrossNodeTest(nodeType NodeType) features.Feature {
	eniMode := getENIModeFromNodeType(nodeType)
	machineType := getMachineTypeFromNodeType(nodeType)
	clientName := fmt.Sprintf("client-ingress-cross-%s", nodeType)
	serverName := fmt.Sprintf("server-ingress-cross-%s", nodeType)
	serverDenyName := fmt.Sprintf("server-deny-ingress-cross-%s", nodeType)

	return features.New(fmt.Sprintf("%sENI/NetworkPolicy/DenyIngressCrossNode/%s", eniMode, machineType)).
		WithLabel("eni-mode", eniMode).
		WithLabel("machine-type", machineType).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("Failed to discover node types: %v", err)
			}

			nodes := nodeInfo.GetNodesByType(nodeType)
			if len(nodes) < 2 {
				t.Skipf("Need at least 2 nodes of type %s, got %d", nodeType, len(nodes))
			}

			var objs []k8s.Object

			policy := NewNetworkPolicy(fmt.Sprintf("deny-ingress-cross-%s", nodeType), config.Namespace()).
				WithPolicyType(networkingv1.PolicyTypeIngress).
				WithPodSelector(map[string]string{"app": "deny-ingress-cross", "node-type": string(nodeType)})

			serverDenyIngress := NewPod(serverDenyName, config.Namespace()).
				WithLabels(map[string]string{"app": "deny-ingress-cross", "node-type": string(nodeType)}).
				WithContainer("server", nginxImage, nil)
			serverDenyIngress = applyNodeAffinityAndTolerations(serverDenyIngress, nodeType)

			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{"app": "server-cross", "node-type": string(nodeType)}).
				WithContainer("server", nginxImage, nil).
				WithPodAffinity(map[string]string{"app": "deny-ingress-cross", "node-type": string(nodeType)})
			server = applyNodeAffinityAndTolerations(server, nodeType)

			client := NewPod(clientName, config.Namespace()).
				WithLabels(map[string]string{"app": "client-cross", "node-type": string(nodeType)}).
				WithContainer("client", nginxImage, nil).
				WithPodAntiAffinity(map[string]string{"app": "server-cross", "node-type": string(nodeType)})
			client = applyNodeAffinityAndTolerations(client, nodeType)

			objs = append(objs, policy.NetworkPolicy, serverDenyIngress.Pod, server.Pod, client.Pod)

			for _, stack := range getStack() {
				denySvc := NewService(fmt.Sprintf("server-deny-cross-%s-%s", nodeType, stack), config.Namespace(),
					map[string]string{"app": "deny-ingress-cross", "node-type": string(nodeType)}).
					WithIPFamily(stack).ExposePort(80, "http")

				normalSvc := NewService(fmt.Sprintf("server-cross-%s-%s", nodeType, stack), config.Namespace(),
					map[string]string{"app": "server-cross", "node-type": string(nodeType)}).
					WithIPFamily(stack).ExposePort(80, "http")

				objs = append(objs, denySvc.Service, normalSvc.Service)
			}

			for _, obj := range objs {
				err := config.Client().Resources().Create(ctx, obj)
				if err != nil {
					t.Fatalf("create resource failed: %v", err)
				}
			}

			ctx = SaveResources(ctx, objs...)
			return ctx
		}).
		Assess("Cross-node ingress policy should deny traffic", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: clientName, Namespace: config.Namespace()}}
			server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: serverName, Namespace: config.Namespace()}}
			serverDeny := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: serverDenyName, Namespace: config.Namespace()}}

			err := waitPodsReady(config.Client(), client, server, serverDeny)
			if err != nil {
				t.Fatalf("wait pods ready failed: %v", err)
			}

			for _, stack := range getStack() {
				t.Logf("Testing %s: client should access server (no policy)", stack)
				_, err = Pull(config.Client(), client.Namespace, client.Name, "client", fmt.Sprintf("server-cross-%s-%s", nodeType, stack), t)
				if err != nil {
					t.Errorf("client should access server (%s): %v", stack, err)
				}

				t.Logf("Testing %s: client should NOT access deny-ingress server (policy applied)", stack)
				_, err = PullFail(config.Client(), client.Namespace, client.Name, "client", fmt.Sprintf("server-deny-cross-%s-%s", nodeType, stack), t)
				if err != nil {
					t.Errorf("client should not access deny-ingress server (%s): %v", stack, err)
				}
			}
			return MarkTestSuccess(ctx)
		}).
		Feature()
}

func createNetworkPolicyDenyEgressSameNodeTest(nodeType NodeType) features.Feature {
	eniMode := getENIModeFromNodeType(nodeType)
	machineType := getMachineTypeFromNodeType(nodeType)
	clientName := fmt.Sprintf("client-egress-same-%s", nodeType)
	serverName := fmt.Sprintf("server-egress-same-%s", nodeType)

	return features.New(fmt.Sprintf("%sENI/NetworkPolicy/DenyEgressSameNode/%s", eniMode, machineType)).
		WithLabel("eni-mode", eniMode).
		WithLabel("machine-type", machineType).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("Failed to discover node types: %v", err)
			}

			requiredNodes := nodeInfo.GetNodesByType(nodeType)
			if len(requiredNodes) == 0 {
				t.Skipf("No nodes of type %s available", nodeType)
			}

			var objs []k8s.Object

			policy := NewNetworkPolicy(fmt.Sprintf("deny-egress-same-%s", nodeType), config.Namespace()).
				WithPolicyType(networkingv1.PolicyTypeEgress).
				WithPodSelector(map[string]string{"app": "client-egress", "node-type": string(nodeType)})

			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{"app": "server-egress", "node-type": string(nodeType)}).
				WithContainer("server", nginxImage, nil)
			server = applyNodeAffinityAndTolerations(server, nodeType)

			client := NewPod(clientName, config.Namespace()).
				WithLabels(map[string]string{"app": "client-egress", "node-type": string(nodeType)}).
				WithContainer("client", nginxImage, nil).
				WithPodAffinity(map[string]string{"app": "server-egress", "node-type": string(nodeType)})
			client = applyNodeAffinityAndTolerations(client, nodeType)

			objs = append(objs, policy.NetworkPolicy, server.Pod, client.Pod)

			for _, stack := range getStack() {
				normalSvc := NewService(fmt.Sprintf("server-egress-%s-%s", nodeType, stack), config.Namespace(),
					map[string]string{"app": "server-egress", "node-type": string(nodeType)}).
					WithIPFamily(stack).ExposePort(80, "http")

				objs = append(objs, normalSvc.Service)
			}

			for _, obj := range objs {
				err := config.Client().Resources().Create(ctx, obj)
				if err != nil {
					t.Fatalf("create resource failed: %v", err)
				}
			}

			ctx = SaveResources(ctx, objs...)
			return ctx
		}).
		Assess("Egress policy should deny traffic", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: clientName, Namespace: config.Namespace()}}
			server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: serverName, Namespace: config.Namespace()}}

			err := waitPodsReady(config.Client(), client, server)
			if err != nil {
				t.Fatalf("wait pods ready failed: %v", err)
			}

			for _, stack := range getStack() {
				t.Logf("Testing %s: client should NOT access server (egress policy denies all)", stack)
				_, err = PullFail(config.Client(), client.Namespace, client.Name, "client", fmt.Sprintf("server-egress-%s-%s", nodeType, stack), t)
				if err != nil {
					t.Errorf("client should not access server (%s): %v", stack, err)
				}
			}
			return MarkTestSuccess(ctx)
		}).
		Feature()
}

func createNetworkPolicyDenyEgressCrossNodeTest(nodeType NodeType) features.Feature {
	eniMode := getENIModeFromNodeType(nodeType)
	machineType := getMachineTypeFromNodeType(nodeType)
	clientName := fmt.Sprintf("client-egress-cross-%s", nodeType)
	serverName := fmt.Sprintf("server-egress-cross-%s", nodeType)

	return features.New(fmt.Sprintf("%sENI/NetworkPolicy/DenyEgressCrossNode/%s", eniMode, machineType)).
		WithLabel("eni-mode", eniMode).
		WithLabel("machine-type", machineType).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("Failed to discover node types: %v", err)
			}

			nodes := nodeInfo.GetNodesByType(nodeType)
			if len(nodes) < 2 {
				t.Skipf("Need at least 2 nodes of type %s, got %d", nodeType, len(nodes))
			}

			var objs []k8s.Object

			policy := NewNetworkPolicy(fmt.Sprintf("deny-egress-cross-%s", nodeType), config.Namespace()).
				WithPolicyType(networkingv1.PolicyTypeEgress).
				WithPodSelector(map[string]string{"app": "client-egress-cross", "node-type": string(nodeType)})

			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{"app": "server-egress-cross", "node-type": string(nodeType)}).
				WithContainer("server", nginxImage, nil)
			server = applyNodeAffinityAndTolerations(server, nodeType)

			client := NewPod(clientName, config.Namespace()).
				WithLabels(map[string]string{"app": "client-egress-cross", "node-type": string(nodeType)}).
				WithContainer("client", nginxImage, nil).
				WithPodAntiAffinity(map[string]string{"app": "server-egress-cross", "node-type": string(nodeType)})
			client = applyNodeAffinityAndTolerations(client, nodeType)

			objs = append(objs, policy.NetworkPolicy, server.Pod, client.Pod)

			for _, stack := range getStack() {
				normalSvc := NewService(fmt.Sprintf("server-egress-cross-%s-%s", nodeType, stack), config.Namespace(),
					map[string]string{"app": "server-egress-cross", "node-type": string(nodeType)}).
					WithIPFamily(stack).ExposePort(80, "http")

				objs = append(objs, normalSvc.Service)
			}

			for _, obj := range objs {
				err := config.Client().Resources().Create(ctx, obj)
				if err != nil {
					t.Fatalf("create resource failed: %v", err)
				}
			}

			ctx = SaveResources(ctx, objs...)
			return ctx
		}).
		Assess("Cross-node egress policy should deny traffic", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: clientName, Namespace: config.Namespace()}}
			server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: serverName, Namespace: config.Namespace()}}

			err := waitPodsReady(config.Client(), client, server)
			if err != nil {
				t.Fatalf("wait pods ready failed: %v", err)
			}

			for _, stack := range getStack() {
				t.Logf("Testing %s: client should NOT access server (egress policy denies all)", stack)
				_, err = PullFail(config.Client(), client.Namespace, client.Name, "client", fmt.Sprintf("server-egress-cross-%s-%s", nodeType, stack), t)
				if err != nil {
					t.Errorf("client should not access server (%s): %v", stack, err)
				}
			}
			return MarkTestSuccess(ctx)
		}).
		Feature()
}

// =============================================================================
// HostPort Test Helpers
// =============================================================================

// hostPortTestState stores state for HostPort test configuration and restoration
type hostPortTestState struct {
	backup     *ENIConfigBackup
	configured bool
}

func createHostPortNodeIPTest(nodeType NodeType) features.Feature {
	eniMode := getENIModeFromNodeType(nodeType)
	machineType := getMachineTypeFromNodeType(nodeType)
	serverName := fmt.Sprintf("server-hostport-%s", nodeType)
	clientName := fmt.Sprintf("client-hostport-%s", nodeType)
	state := &hostPortTestState{}

	return features.New(fmt.Sprintf("%sENI/HostPort/NodeIP/%s", eniMode, machineType)).
		WithLabel("eni-mode", eniMode).
		WithLabel("machine-type", machineType).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			if !RequireTerwayVersion("v1.15.0") {
				t.Skipf("HostPort requires terway version >= v1.15.0, current: %s", GetCachedTerwayVersion())
			}

			if !RequireK8sVersion("v1.34.0") {
				t.Skipf("HostPort requires k8s version >= v1.34.0, current: %s", GetCachedK8sVersion())
			}

			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("Failed to discover node types: %v", err)
			}

			nodes := nodeInfo.GetNodesByType(nodeType)
			if len(nodes) < 2 {
				t.Skipf("Need at least 2 nodes of type %s, got %d", nodeType, len(nodes))
			}

			// Check and configure portmap if needed
			hasNoKubeProxy, err := CheckNoKubeProxyLabel(ctx, config.Client())
			if err != nil {
				t.Fatalf("failed to check no-kube-proxy label: %v", err)
			}

			if !hasNoKubeProxy {
				t.Log("Configuring CNI chain with portmap plugin")
				state.backup, err = BackupENIConfig(ctx, config)
				if err != nil {
					t.Fatalf("failed to backup eni-config: %v", err)
				}

				isDatapathV2, err := IsDatapathV2Enabled(ctx, config)
				if err != nil {
					t.Fatalf("failed to check datapath v2: %v", err)
				}

				err = ConfigureHostPortCNIChain(ctx, config, isDatapathV2)
				if err != nil {
					t.Fatalf("failed to configure CNI chain: %v", err)
				}

				err = restartTerway(ctx, config)
				if err != nil {
					t.Fatalf("failed to restart terway: %v", err)
				}

				state.configured = true
			}

			var objs []k8s.Object

			// Use unique hostPort based on node type to avoid conflicts
			hostPort := 8080 + int(nodeType[0])%10

			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{"app": "server-hostport", "node-type": string(nodeType)}).
				WithContainer("server", nginxImage, nil).
				WithHostPort(80, int32(hostPort))
			server = applyNodeAffinityAndTolerations(server, nodeType)

			err = config.Client().Resources().Create(ctx, server.Pod)
			if err != nil {
				t.Fatalf("create server pod failed: %v", err)
			}

			client := NewPod(clientName, config.Namespace()).
				WithLabels(map[string]string{"app": "client-hostport", "node-type": string(nodeType)}).
				WithContainer("client", nginxImage, nil).
				WithPodAntiAffinity(map[string]string{"app": "server-hostport", "node-type": string(nodeType)})
			client = applyNodeAffinityAndTolerations(client, nodeType)

			err = config.Client().Resources().Create(ctx, client.Pod)
			if err != nil {
				t.Fatalf("create client pod failed: %v", err)
			}

			objs = append(objs, server.Pod, client.Pod)
			ctx = SaveResources(ctx, objs...)
			return ctx
		}).
		Assess("Client can access server via node IP + hostPort", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: serverName, Namespace: config.Namespace()}}
			client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: clientName, Namespace: config.Namespace()}}

			err := waitPodsReady(config.Client(), server, client)
			if err != nil {
				t.Fatalf("wait pods ready failed: %v", err)
			}

			err = config.Client().Resources().Get(ctx, serverName, config.Namespace(), server)
			if err != nil {
				t.Fatalf("get server pod failed: %v", err)
			}

			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: server.Spec.NodeName}}
			err = config.Client().Resources().Get(ctx, server.Spec.NodeName, "", node)
			if err != nil {
				t.Fatalf("get node failed: %v", err)
			}

			hostPort := 8080 + int(nodeType[0])%10

			for _, stack := range getStack() {
				isIPv6 := stack == "ipv6"

				internalIP, _ := getNodeIPs(node, isIPv6)
				if internalIP == "" {
					t.Logf("No internal %s address found, skipping", stack)
					continue
				}

				target := net.JoinHostPort(internalIP, fmt.Sprintf("%d", hostPort))
				t.Logf("Testing %s connectivity to %s", stack, target)

				if isIPv6 {
					_, err = PullWithIPv6(config.Client(), client.Namespace, client.Name, "client", target, t)
				} else {
					_, err = Pull(config.Client(), client.Namespace, client.Name, "client", target, t)
				}
				if err != nil {
					t.Errorf("Failed to connect via %s: %v", stack, err)
				}
			}

			return MarkTestSuccess(ctx)
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			if state.configured && state.backup != nil {
				err := RestoreENIConfig(ctx, config, state.backup)
				if err != nil {
					t.Logf("Warning: failed to restore eni-config: %v", err)
				} else {
					_ = restartTerway(ctx, config)
				}
				state.configured = false
				state.backup = nil
			}
			return ctx
		}).
		Feature()
}

func createHostPortExternalIPTest(nodeType NodeType) features.Feature {
	eniMode := getENIModeFromNodeType(nodeType)
	machineType := getMachineTypeFromNodeType(nodeType)
	serverName := fmt.Sprintf("server-hostport-ext-%s", nodeType)
	clientName := fmt.Sprintf("client-hostport-ext-%s", nodeType)
	state := &hostPortTestState{}

	return features.New(fmt.Sprintf("%sENI/HostPort/ExternalIP/%s", eniMode, machineType)).
		WithLabel("eni-mode", eniMode).
		WithLabel("machine-type", machineType).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			if !RequireTerwayVersion("v1.15.0") {
				t.Skipf("HostPort requires terway version >= v1.15.0, current: %s", GetCachedTerwayVersion())
			}

			if !RequireK8sVersion("v1.34.0") {
				t.Skipf("HostPort requires k8s version >= v1.34.0, current: %s", GetCachedK8sVersion())
			}

			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("Failed to discover node types: %v", err)
			}

			nodes := nodeInfo.GetNodesByType(nodeType)
			if len(nodes) < 2 {
				t.Skipf("Need at least 2 nodes of type %s, got %d", nodeType, len(nodes))
			}

			// Check if any node has external IP
			hasExternalIP := false
			for _, node := range nodes {
				for _, stack := range getStack() {
					isIPv6 := stack == "ipv6"
					_, externalIP := getNodeIPs(&node, isIPv6)
					if externalIP != "" {
						hasExternalIP = true
						break
					}
				}
				if hasExternalIP {
					break
				}
			}

			if !hasExternalIP {
				t.Skipf("No node of type %s has external IP", nodeType)
			}

			// Check and configure portmap if needed
			hasNoKubeProxy, err := CheckNoKubeProxyLabel(ctx, config.Client())
			if err != nil {
				t.Fatalf("failed to check no-kube-proxy label: %v", err)
			}

			if !hasNoKubeProxy {
				t.Log("Configuring CNI chain with portmap plugin")
				state.backup, err = BackupENIConfig(ctx, config)
				if err != nil {
					t.Fatalf("failed to backup eni-config: %v", err)
				}

				isDatapathV2, err := IsDatapathV2Enabled(ctx, config)
				if err != nil {
					t.Fatalf("failed to check datapath v2: %v", err)
				}

				err = ConfigureHostPortCNIChain(ctx, config, isDatapathV2)
				if err != nil {
					t.Fatalf("failed to configure CNI chain: %v", err)
				}

				err = restartTerway(ctx, config)
				if err != nil {
					t.Fatalf("failed to restart terway: %v", err)
				}

				state.configured = true
			}

			var objs []k8s.Object

			// Use unique hostPort based on node type to avoid conflicts
			hostPort := 8090 + int(nodeType[0])%10

			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{"app": "server-hostport-ext", "node-type": string(nodeType)}).
				WithContainer("server", nginxImage, nil).
				WithHostPort(80, int32(hostPort)).
				WithNodeAffinity(map[string]string{"e2e.aliyun.com/external-ip": "true"})
			server = applyNodeAffinityAndTolerations(server, nodeType)

			err = config.Client().Resources().Create(ctx, server.Pod)
			if err != nil {
				t.Fatalf("create server pod failed: %v", err)
			}

			client := NewPod(clientName, config.Namespace()).
				WithLabels(map[string]string{"app": "client-hostport-ext", "node-type": string(nodeType)}).
				WithContainer("client", nginxImage, nil).
				WithPodAntiAffinity(map[string]string{"app": "server-hostport-ext", "node-type": string(nodeType)}).
				WithNodeAffinity(map[string]string{"e2e.aliyun.com/external-ip": "true"})
			client = applyNodeAffinityAndTolerations(client, nodeType)

			err = config.Client().Resources().Create(ctx, client.Pod)
			if err != nil {
				t.Fatalf("create client pod failed: %v", err)
			}

			objs = append(objs, server.Pod, client.Pod)
			ctx = SaveResources(ctx, objs...)
			return ctx
		}).
		Assess("Client can access server via external IP + hostPort", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: serverName, Namespace: config.Namespace()}}
			client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: clientName, Namespace: config.Namespace()}}

			err := waitPodsReady(config.Client(), server, client)
			if err != nil {
				t.Fatalf("wait pods ready failed: %v", err)
			}

			err = config.Client().Resources().Get(ctx, serverName, config.Namespace(), server)
			if err != nil {
				t.Fatalf("get server pod failed: %v", err)
			}

			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: server.Spec.NodeName}}
			err = config.Client().Resources().Get(ctx, server.Spec.NodeName, "", node)
			if err != nil {
				t.Fatalf("get node failed: %v", err)
			}

			hostPort := 8090 + int(nodeType[0])%10

			for _, stack := range getStack() {
				isIPv6 := stack == "ipv6"

				_, externalIP := getNodeIPs(node, isIPv6)
				if externalIP == "" {
					t.Logf("No external %s address found, skipping", stack)
					continue
				}

				target := net.JoinHostPort(externalIP, fmt.Sprintf("%d", hostPort))
				t.Logf("Testing %s connectivity to external IP %s", stack, target)

				if isIPv6 {
					_, err = PullWithIPv6(config.Client(), client.Namespace, client.Name, "client", target, t)
				} else {
					_, err = Pull(config.Client(), client.Namespace, client.Name, "client", target, t)
				}
				if err != nil {
					t.Errorf("Failed to connect via %s: %v", stack, err)
				}
			}

			return MarkTestSuccess(ctx)
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			if state.configured && state.backup != nil {
				err := RestoreENIConfig(ctx, config, state.backup)
				if err != nil {
					t.Logf("Warning: failed to restore eni-config: %v", err)
				} else {
					_ = restartTerway(ctx, config)
				}
				state.configured = false
				state.backup = nil
			}
			return ctx
		}).
		Feature()
}

// =============================================================================
// Deprecated Tests - Keep for backward compatibility, will be removed
// =============================================================================

// TestNormal_HostNetworkConnectivity tests connectivity from host network pods to regular pods
// Deprecated: This test does not follow the new naming convention
func TestNormal_HostNetworkConnectivity(t *testing.T) {
	var feats []features.Feature

	hostToPodSameNode := features.New("Legacy/HostNetworkConnectivity").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			var objs []k8s.Object

			server := NewPod("server", config.Namespace()).
				WithLabels(map[string]string{"app": "server"}).
				WithContainer("server", nginxImage, nil)

			err := config.Client().Resources().Create(ctx, server.Pod)
			if err != nil {
				t.Fatalf("create server pod failed: %v", err)
			}

			client := NewPod("client", config.Namespace()).
				WithLabels(map[string]string{"app": "client"}).
				WithContainer("client", nginxImage, nil).
				WithPodAffinity(map[string]string{"app": "server"}).
				WithDNSPolicy(corev1.DNSClusterFirstWithHostNet).
				WithHostNetwork()

			err = config.Client().Resources().Create(ctx, client.Pod)
			if err != nil {
				t.Fatalf("create client pod failed: %v", err)
			}

			objs = append(objs, server.Pod, client.Pod)

			for _, stack := range getStack() {
				svc := NewService("server-"+stack, config.Namespace(), map[string]string{"app": "server"}).
					ExposePort(80, "http").
					WithIPFamily(stack)

				err = config.Client().Resources().Create(ctx, svc.Service)
				if err != nil {
					t.Fatalf("create service failed: %v", err)
				}
				objs = append(objs, svc.Service)
			}
			ctx = SaveResources(ctx, objs...)

			return ctx
		}).
		Assess("Host network pod can access server", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			server := NewPod("server", config.Namespace()).
				WithLabels(map[string]string{"app": "server"}).
				WithContainer("server", nginxImage, nil)

			client := NewPod("client", config.Namespace()).
				WithLabels(map[string]string{"app": "client"}).
				WithContainer("client", nginxImage, nil).
				WithPodAffinity(map[string]string{"app": "server"})

			err := wait.For(conditions.New(config.Client().Resources()).PodReady(client.Pod),
				wait.WithTimeout(parsedTimeout),
				wait.WithInterval(1*time.Second))
			if err != nil {
				t.Fatalf("wait client ready failed: %v", err)
			}
			err = wait.For(conditions.New(config.Client().Resources()).PodReady(server.Pod),
				wait.WithTimeout(parsedTimeout),
				wait.WithInterval(1*time.Second))
			if err != nil {
				t.Fatalf("wait server ready failed: %v", err)
			}

			for _, stack := range getStack() {
				_, err = Pull(config.Client(), client.Namespace, client.Name, "client", "server-"+stack, t)
				if err != nil {
					t.Errorf("connectivity test failed for %s: %v", stack, err)
				}
			}

			return MarkTestSuccess(ctx)
		}).
		Feature()

	feats = append(feats, hostToPodSameNode)

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}
