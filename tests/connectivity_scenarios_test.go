//go:build e2e

package tests

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// =============================================================================
// Shared ENI Mode Connectivity Tests
// =============================================================================

// TestSharedENI_Connectivity_SameNode tests basic pod connectivity on the same node
// for shared ENI mode on both ECS and Lingjun nodes.
func TestSharedENI_Connectivity_SameNode(t *testing.T) {
	nodeTypes := []NodeType{NodeTypeECSSharedENI, NodeTypeLingjunSharedENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		feat := createSameNodeConnectivityTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestSharedENI_Connectivity_CrossNode tests pod connectivity across different nodes
// for shared ENI mode on both ECS and Lingjun nodes.
func TestSharedENI_Connectivity_CrossNode(t *testing.T) {
	nodeTypes := []NodeType{NodeTypeECSSharedENI, NodeTypeLingjunSharedENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		feat := createCrossNodeConnectivityTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestSharedENI_Connectivity_Hairpin tests hairpin connectivity (pod accessing itself via service)
// for shared ENI mode on both ECS and Lingjun nodes.
func TestSharedENI_Connectivity_Hairpin(t *testing.T) {
	nodeTypes := []NodeType{NodeTypeECSSharedENI, NodeTypeLingjunSharedENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		feat := createHairpinConnectivityTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// =============================================================================
// Exclusive ENI Mode Connectivity Tests
// =============================================================================

// TestExclusiveENI_Connectivity_SameNode tests basic pod connectivity on the same node
// for exclusive ENI mode on both ECS and Lingjun nodes.
func TestExclusiveENI_Connectivity_SameNode(t *testing.T) {
	nodeTypes := []NodeType{NodeTypeECSExclusiveENI, NodeTypeLingjunExclusiveENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		feat := createSameNodeConnectivityTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestExclusiveENI_Connectivity_CrossNode tests pod connectivity across different nodes
// for exclusive ENI mode on both ECS and Lingjun nodes.
func TestExclusiveENI_Connectivity_CrossNode(t *testing.T) {
	nodeTypes := []NodeType{NodeTypeECSExclusiveENI, NodeTypeLingjunExclusiveENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		feat := createCrossNodeConnectivityTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestExclusiveENI_Connectivity_Hairpin tests hairpin connectivity (pod accessing itself via service)
// for exclusive ENI mode on both ECS and Lingjun nodes.
func TestExclusiveENI_Connectivity_Hairpin(t *testing.T) {
	nodeTypes := []NodeType{NodeTypeECSExclusiveENI, NodeTypeLingjunExclusiveENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		feat := createHairpinConnectivityTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// =============================================================================
// Cross-Zone Connectivity Tests
// =============================================================================

// TestSharedENI_Connectivity_CrossZone tests pod connectivity across different availability zones
// for shared ENI mode on both ECS and Lingjun nodes.
func TestSharedENI_Connectivity_CrossZone(t *testing.T) {
	nodeTypes := []NodeType{NodeTypeECSSharedENI, NodeTypeLingjunSharedENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		feat := createCrossZoneConnectivityTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestExclusiveENI_Connectivity_CrossZone tests pod connectivity across different availability zones
// for exclusive ENI mode on both ECS and Lingjun nodes.
func TestExclusiveENI_Connectivity_CrossZone(t *testing.T) {
	nodeTypes := []NodeType{NodeTypeECSExclusiveENI, NodeTypeLingjunExclusiveENI}

	var feats []features.Feature
	for _, nodeType := range nodeTypes {
		feat := createCrossZoneConnectivityTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

// createSameNodeConnectivityTest creates a connectivity test for pods on the same node
func createSameNodeConnectivityTest(nodeType NodeType) features.Feature {
	eniMode := getENIModeFromNodeType(nodeType)
	machineType := getMachineTypeFromNodeType(nodeType)
	serverName := fmt.Sprintf("server-same-%s", nodeType)
	clientName := fmt.Sprintf("client-same-%s", nodeType)

	return features.New(fmt.Sprintf("%sENI/Connectivity/SameNode/%s", eniMode, machineType)).
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

			// Create server pod
			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{"app": "server", "node-type": string(nodeType)}).
				WithContainer("server", nginxImage, nil)

			server = applyNodeAffinityAndTolerations(server, nodeType)

			err = config.Client().Resources().Create(ctx, server.Pod)
			if err != nil {
				t.Fatalf("create server pod failed: %v", err)
			}

			// Create client pod on same node
			client := NewPod(clientName, config.Namespace()).
				WithLabels(map[string]string{"app": "client", "node-type": string(nodeType)}).
				WithContainer("client", nginxImage, nil).
				WithPodAffinity(map[string]string{"app": "server", "node-type": string(nodeType)})

			client = applyNodeAffinityAndTolerations(client, nodeType)

			err = config.Client().Resources().Create(ctx, client.Pod)
			if err != nil {
				t.Fatalf("create client pod failed: %v", err)
			}

			// Create services
			var objs []k8s.Object
			for _, stack := range getStack() {
				svc := NewService(fmt.Sprintf("server-same-%s-%s", nodeType, stack), config.Namespace(),
					map[string]string{"app": "server", "node-type": string(nodeType)}).
					ExposePort(80, "http").
					WithIPFamily(stack)

				err = config.Client().Resources().Create(ctx, svc.Service)
				if err != nil {
					t.Fatalf("create service failed: %v", err)
				}
				objs = append(objs, svc.Service)
			}

			ctx = SaveResources(ctx, server.Pod, client.Pod)
			ctx = SaveResources(ctx, objs...)
			return ctx
		}).
		Assess("pods should be running on same node", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: serverName, Namespace: config.Namespace()}}
			client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: clientName, Namespace: config.Namespace()}}

			err := waitPodsReady(config.Client(), server, client)
			if err != nil {
				t.Fatalf("wait pods ready failed: %v", err)
			}

			// Refresh pods to get node info
			err = config.Client().Resources().Get(ctx, serverName, config.Namespace(), server)
			if err != nil {
				t.Fatalf("get server pod failed: %v", err)
			}
			err = config.Client().Resources().Get(ctx, clientName, config.Namespace(), client)
			if err != nil {
				t.Fatalf("get client pod failed: %v", err)
			}

			if server.Spec.NodeName != client.Spec.NodeName {
				t.Fatalf("expected pods on same node, but server on %s, client on %s",
					server.Spec.NodeName, client.Spec.NodeName)
			}

			t.Logf("Both pods are running on node %s (type: %s)", server.Spec.NodeName, nodeType)
			return ctx
		}).
		Assess("same-node connectivity should work", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			for _, stack := range getStack() {
				serviceName := fmt.Sprintf("server-same-%s-%s", nodeType, stack)
				_, err := Pull(config.Client(), config.Namespace(), clientName, "client", serviceName, t)
				if err != nil {
					t.Errorf("same-node connectivity test failed for %s on %s: %v", stack, nodeType, err)
				}
			}
			return MarkTestSuccess(ctx)
		}).
		Feature()
}

// createCrossNodeConnectivityTest creates a connectivity test for pods on different nodes
func createCrossNodeConnectivityTest(nodeType NodeType) features.Feature {
	eniMode := getENIModeFromNodeType(nodeType)
	machineType := getMachineTypeFromNodeType(nodeType)
	serverName := fmt.Sprintf("server-cross-%s", nodeType)
	clientName := fmt.Sprintf("client-cross-%s", nodeType)

	return features.New(fmt.Sprintf("%sENI/Connectivity/CrossNode/%s", eniMode, machineType)).
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

			// Create server pod with node type affinity
			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{"app": "server", "test": "cross-node", "node-type": string(nodeType)}).
				WithContainer("server", nginxImage, nil)

			server = applyNodeAffinityAndTolerations(server, nodeType)

			err = config.Client().Resources().Create(ctx, server.Pod)
			if err != nil {
				t.Fatalf("create server pod failed: %v", err)
			}

			// Create client pod with pod anti-affinity to server (ensures different node)
			client := NewPod(clientName, config.Namespace()).
				WithLabels(map[string]string{"app": "client", "test": "cross-node", "node-type": string(nodeType)}).
				WithContainer("client", nginxImage, nil).
				WithPodAntiAffinity(map[string]string{"app": "server", "test": "cross-node", "node-type": string(nodeType)})

			client = applyNodeAffinityAndTolerations(client, nodeType)

			err = config.Client().Resources().Create(ctx, client.Pod)
			if err != nil {
				t.Fatalf("create client pod failed: %v", err)
			}

			// Create services
			var objs []k8s.Object
			for _, stack := range getStack() {
				svc := NewService(fmt.Sprintf("server-cross-%s-%s", nodeType, stack), config.Namespace(),
					map[string]string{"app": "server", "test": "cross-node", "node-type": string(nodeType)}).
					ExposePort(80, "http").
					WithIPFamily(stack)

				err = config.Client().Resources().Create(ctx, svc.Service)
				if err != nil {
					t.Fatalf("create service failed: %v", err)
				}
				objs = append(objs, svc.Service)
			}

			ctx = SaveResources(ctx, server.Pod, client.Pod)
			ctx = SaveResources(ctx, objs...)
			return ctx
		}).
		Assess("pods should be on different nodes", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: serverName, Namespace: config.Namespace()}}
			client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: clientName, Namespace: config.Namespace()}}

			err := waitPodsReady(config.Client(), server, client)
			if err != nil {
				t.Fatalf("wait pods ready failed: %v", err)
			}

			// Refresh pods to get node info
			err = config.Client().Resources().Get(ctx, serverName, config.Namespace(), server)
			if err != nil {
				t.Fatalf("get server pod failed: %v", err)
			}
			err = config.Client().Resources().Get(ctx, clientName, config.Namespace(), client)
			if err != nil {
				t.Fatalf("get client pod failed: %v", err)
			}

			if server.Spec.NodeName == client.Spec.NodeName {
				t.Fatalf("pods are on same node: %s", server.Spec.NodeName)
			}

			t.Logf("Pods on different nodes: server=%s, client=%s (type: %s)",
				server.Spec.NodeName, client.Spec.NodeName, nodeType)
			return ctx
		}).
		Assess("cross-node connectivity should work", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			for _, stack := range getStack() {
				serviceName := fmt.Sprintf("server-cross-%s-%s", nodeType, stack)
				_, err := Pull(config.Client(), config.Namespace(), clientName, "client", serviceName, t)
				if err != nil {
					t.Errorf("cross-node connectivity failed for %s on %s: %v", stack, nodeType, err)
				}
			}
			return MarkTestSuccess(ctx)
		}).
		Feature()
}

// createHairpinConnectivityTest creates a hairpin connectivity test (pod accessing itself via service)
func createHairpinConnectivityTest(nodeType NodeType) features.Feature {
	eniMode := getENIModeFromNodeType(nodeType)
	machineType := getMachineTypeFromNodeType(nodeType)
	serverName := fmt.Sprintf("server-hairpin-%s", nodeType)

	return features.New(fmt.Sprintf("%sENI/Connectivity/Hairpin/%s", eniMode, machineType)).
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

			// Create server pod
			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{"app": "server", "node-type": string(nodeType)}).
				WithContainer("server", nginxImage, nil)

			server = applyNodeAffinityAndTolerations(server, nodeType)

			err = config.Client().Resources().Create(ctx, server.Pod)
			if err != nil {
				t.Fatalf("create server pod failed: %v", err)
			}

			// Create services
			var objs []k8s.Object
			for _, stack := range getStack() {
				svc := NewService(fmt.Sprintf("server-hairpin-%s-%s", nodeType, stack), config.Namespace(),
					map[string]string{"app": "server", "node-type": string(nodeType)}).
					ExposePort(80, "http").
					WithIPFamily(stack)

				err = config.Client().Resources().Create(ctx, svc.Service)
				if err != nil {
					t.Fatalf("create service failed: %v", err)
				}
				objs = append(objs, svc.Service)
			}

			ctx = SaveResources(ctx, server.Pod)
			ctx = SaveResources(ctx, objs...)
			return ctx
		}).
		Assess("pod should be running", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: serverName, Namespace: config.Namespace()}}

			err := waitPodsReady(config.Client(), server)
			if err != nil {
				t.Fatalf("wait pod ready failed: %v", err)
			}

			t.Logf("Pod is running on node type %s", nodeType)
			return ctx
		}).
		Assess("hairpin connectivity should work", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			for _, stack := range getStack() {
				// Skip IPv6 hairpin test for ipvlan mode (known limitation)
				if stack == "ipv6" && ipvlan {
					t.Logf("Skipping IPv6 hairpin test for ipvlan mode (known limitation)")
					continue
				}
				serviceName := fmt.Sprintf("server-hairpin-%s-%s", nodeType, stack)
				_, err := Pull(config.Client(), config.Namespace(), serverName, "server", serviceName, t)
				if err != nil {
					t.Errorf("hairpin connectivity test failed for %s on %s: %v", stack, nodeType, err)
				}
			}
			return MarkTestSuccess(ctx)
		}).
		Feature()
}

// createCrossZoneConnectivityTest creates a connectivity test for pods in different availability zones
func createCrossZoneConnectivityTest(nodeType NodeType) features.Feature {
	eniMode := getENIModeFromNodeType(nodeType)
	machineType := getMachineTypeFromNodeType(nodeType)
	serverName := fmt.Sprintf("server-zone-%s", nodeType)
	clientName := fmt.Sprintf("client-zone-%s", nodeType)

	return features.New(fmt.Sprintf("%sENI/Connectivity/CrossZone/%s", eniMode, machineType)).
		WithLabel("eni-mode", eniMode).
		WithLabel("machine-type", machineType).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("Failed to discover node types: %v", err)
			}

			// Check if we have nodes in multiple zones
			if !nodeInfo.HasMultipleZones(nodeType) {
				zones := nodeInfo.GetZonesForNodeType(nodeType)
				t.Skipf("Need nodes in at least 2 different zones for type %s, got zones: %v", nodeType, zones)
			}

			// Create server pod with node type affinity
			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{"app": "server", "test": "cross-zone", "node-type": string(nodeType)}).
				WithContainer("server", nginxImage, nil)

			server = applyNodeAffinityAndTolerations(server, nodeType)

			err = config.Client().Resources().Create(ctx, server.Pod)
			if err != nil {
				t.Fatalf("create server pod failed: %v", err)
			}

			// Create client pod with zone-based anti-affinity (ensures different zone)
			client := NewPod(clientName, config.Namespace()).
				WithLabels(map[string]string{"app": "client", "test": "cross-zone", "node-type": string(nodeType)}).
				WithContainer("client", nginxImage, nil).
				WithPodAntiAffinityByZone(map[string]string{"app": "server", "test": "cross-zone", "node-type": string(nodeType)})

			client = applyNodeAffinityAndTolerations(client, nodeType)

			err = config.Client().Resources().Create(ctx, client.Pod)
			if err != nil {
				t.Fatalf("create client pod failed: %v", err)
			}

			// Create services
			var objs []k8s.Object
			for _, stack := range getStack() {
				svc := NewService(fmt.Sprintf("server-zone-%s-%s", nodeType, stack), config.Namespace(),
					map[string]string{"app": "server", "test": "cross-zone", "node-type": string(nodeType)}).
					ExposePort(80, "http").
					WithIPFamily(stack)

				err = config.Client().Resources().Create(ctx, svc.Service)
				if err != nil {
					t.Fatalf("create service failed: %v", err)
				}
				objs = append(objs, svc.Service)
			}

			ctx = SaveResources(ctx, server.Pod, client.Pod)
			ctx = SaveResources(ctx, objs...)
			return ctx
		}).
		Assess("pods should be in different zones", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: serverName, Namespace: config.Namespace()}}
			client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: clientName, Namespace: config.Namespace()}}

			err := waitPodsReady(config.Client(), server, client)
			if err != nil {
				t.Fatalf("wait pods ready failed: %v", err)
			}

			// Refresh pods to get node info
			err = config.Client().Resources().Get(ctx, serverName, config.Namespace(), server)
			if err != nil {
				t.Fatalf("get server pod failed: %v", err)
			}
			err = config.Client().Resources().Get(ctx, clientName, config.Namespace(), client)
			if err != nil {
				t.Fatalf("get client pod failed: %v", err)
			}

			// Get node info to check zones
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("Failed to discover node types: %v", err)
			}

			var serverZone, clientZone string
			for _, node := range nodeInfo.AllNodes {
				if node.Name == server.Spec.NodeName {
					serverZone = GetNodeZone(&node)
				}
				if node.Name == client.Spec.NodeName {
					clientZone = GetNodeZone(&node)
				}
			}

			if serverZone == clientZone {
				t.Fatalf("pods are in same zone: server=%s (zone=%s), client=%s (zone=%s)",
					server.Spec.NodeName, serverZone, client.Spec.NodeName, clientZone)
			}

			t.Logf("Pods in different zones: server=%s (zone=%s), client=%s (zone=%s), type=%s",
				server.Spec.NodeName, serverZone, client.Spec.NodeName, clientZone, nodeType)
			return ctx
		}).
		Assess("cross-zone connectivity should work", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			for _, stack := range getStack() {
				serviceName := fmt.Sprintf("server-zone-%s-%s", nodeType, stack)
				_, err := Pull(config.Client(), config.Namespace(), clientName, "client", serviceName, t)
				if err != nil {
					t.Errorf("cross-zone connectivity failed for %s on %s: %v", stack, nodeType, err)
				}
			}
			return MarkTestSuccess(ctx)
		}).
		Feature()
}

// applyNodeAffinityAndTolerations applies node affinity and tolerations based on node type
func applyNodeAffinityAndTolerations(pod *Pod, nodeType NodeType) *Pod {
	// Apply node affinity
	nodeAffinity := GetNodeAffinityForType(nodeType)
	if len(nodeAffinity) > 0 {
		pod = pod.WithNodeAffinity(nodeAffinity)
	}
	nodeAffinityExclude := GetNodeAffinityExcludeForType(nodeType)
	if len(nodeAffinityExclude) > 0 {
		pod = pod.WithNodeAffinityExclude(nodeAffinityExclude)
	}

	// Add tolerations for Lingjun nodes
	if nodeType == NodeTypeLingjunSharedENI || nodeType == NodeTypeLingjunExclusiveENI {
		pod = pod.WithTolerations([]corev1.Toleration{
			{
				Key:      "node-role.alibabacloud.com/lingjun",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			},
		})
	}

	return pod
}

// getENIModeFromNodeType returns "Shared" or "Exclusive" based on node type
func getENIModeFromNodeType(nodeType NodeType) string {
	switch nodeType {
	case NodeTypeECSSharedENI, NodeTypeLingjunSharedENI:
		return "Shared"
	case NodeTypeECSExclusiveENI, NodeTypeLingjunExclusiveENI:
		return "Exclusive"
	default:
		return "Unknown"
	}
}

// getMachineTypeFromNodeType returns "ECS" or "Lingjun" based on node type
func getMachineTypeFromNodeType(nodeType NodeType) string {
	switch nodeType {
	case NodeTypeECSSharedENI, NodeTypeECSExclusiveENI:
		return "ECS"
	case NodeTypeLingjunSharedENI, NodeTypeLingjunExclusiveENI:
		return "Lingjun"
	default:
		return "Unknown"
	}
}
