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

// TestConnectivity_AllNodeTypes tests basic connectivity across all available node types and ENI modes
// Tests all combinations: ECS/Lingjun nodes × Shared/Exclusive ENI modes
func TestConnectivity_AllNodeTypes(t *testing.T) {
	var feats []features.Feature

	// Test ECS nodes with shared ENI mode
	ecsSharedFeature := createConnectivityTest("Connectivity/ECS-SharedENI", NodeTypeECSSharedENI, "ecs-shared")
	feats = append(feats, ecsSharedFeature)

	// Test ECS nodes with exclusive ENI mode
	ecsExclusiveFeature := createConnectivityTest("Connectivity/ECS-ExclusiveENI", NodeTypeECSExclusiveENI, "ecs-exclusive")
	feats = append(feats, ecsExclusiveFeature)

	// Test Lingjun nodes with shared ENI mode
	lingjunSharedFeature := createConnectivityTest("Connectivity/Lingjun-SharedENI", NodeTypeLingjunSharedENI, "lingjun-shared")
	feats = append(feats, lingjunSharedFeature)

	// Test Lingjun nodes with exclusive ENI mode
	lingjunExclusiveFeature := createConnectivityTest("Connectivity/Lingjun-ExclusiveENI", NodeTypeLingjunExclusiveENI, "lingjun-exclusive")
	feats = append(feats, lingjunExclusiveFeature)

	testenv.Test(t, feats...)
}

// createConnectivityTest creates a basic connectivity test for a specific node type
func createConnectivityTest(testName string, nodeType NodeType, label string) features.Feature {
	serverName := fmt.Sprintf("server-%s", nodeType)
	clientName := fmt.Sprintf("client-%s", nodeType)

	return features.New(testName).
		WithLabel("env", label).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Discover node types
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("Failed to discover node types: %v", err)
			}

			// Check if required node type is available
			requiredNodes := nodeInfo.GetNodesByType(nodeType)
			if len(requiredNodes) == 0 {
				t.Skipf("No nodes of type %s available", nodeType)
			}

			// Create server pod
			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{"app": "server", "node-type": string(nodeType)}).
				WithContainer("server", nginxImage, nil)

			// Apply node affinity
			nodeAffinity := GetNodeAffinityForType(nodeType)
			if len(nodeAffinity) > 0 {
				server = server.WithNodeAffinity(nodeAffinity)
			}
			nodeAffinityExclude := GetNodeAffinityExcludeForType(nodeType)
			if len(nodeAffinityExclude) > 0 {
				server = server.WithNodeAffinityExclude(nodeAffinityExclude)
			}

			// Add tolerations for Lingjun nodes (both shared and exclusive ENI modes)
			if nodeType == NodeTypeLingjunSharedENI || nodeType == NodeTypeLingjunExclusiveENI {
				server = server.WithTolerations([]corev1.Toleration{
					{
						Key:      "node-role.alibabacloud.com/lingjun",
						Operator: corev1.TolerationOpExists,
						Effect:   corev1.TaintEffectNoSchedule,
					},
				})
			}

			err = config.Client().Resources().Create(ctx, server.Pod)
			if err != nil {
				t.Fatalf("create server pod failed, %v", err)
			}

			// Create client pod
			client := NewPod(clientName, config.Namespace()).
				WithLabels(map[string]string{"app": "client", "node-type": string(nodeType)}).
				WithContainer("client", nginxImage, nil).
				WithPodAffinity(map[string]string{"app": "server", "node-type": string(nodeType)})

			// Apply node affinity
			if len(nodeAffinity) > 0 {
				client = client.WithNodeAffinity(nodeAffinity)
			}
			if len(nodeAffinityExclude) > 0 {
				client = client.WithNodeAffinityExclude(nodeAffinityExclude)
			}

			// Add tolerations for Lingjun nodes (both shared and exclusive ENI modes)
			if nodeType == NodeTypeLingjunSharedENI || nodeType == NodeTypeLingjunExclusiveENI {
				client = client.WithTolerations([]corev1.Toleration{
					{
						Key:      "node-role.alibabacloud.com/lingjun",
						Operator: corev1.TolerationOpExists,
						Effect:   corev1.TaintEffectNoSchedule,
					},
				})
			}

			err = config.Client().Resources().Create(ctx, client.Pod)
			if err != nil {
				t.Fatalf("create client pod failed, %v", err)
			}

			// Create services
			var objs []k8s.Object
			for _, stack := range getStack() {
				svc := NewService(fmt.Sprintf("server-%s-%s", nodeType, stack), config.Namespace(),
					map[string]string{"app": "server", "node-type": string(nodeType)}).
					ExposePort(80, "http").
					WithIPFamily(stack)

				err = config.Client().Resources().Create(ctx, svc.Service)
				if err != nil {
					t.Fatalf("create service failed, %v", err)
				}
				objs = append(objs, svc.Service)
			}

			ctx = SaveResources(ctx, server.Pod, client.Pod)
			ctx = SaveResources(ctx, objs...)
			return ctx
		}).
		Assess("pods should be running", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			server := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: serverName, Namespace: config.Namespace()},
			}
			client := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: clientName, Namespace: config.Namespace()},
			}

			err := waitPodsReady(config.Client(), server, client)
			if err != nil {
				t.Fatalf("wait pods ready failed, %v", err)
			}

			t.Logf("✓ Both pods are running on node type %s", nodeType)
			return ctx
		}).
		Assess("basic connectivity should work", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			for _, stack := range getStack() {
				serviceName := fmt.Sprintf("server-%s-%s", nodeType, stack)
				_, err := Pull(config.Client(), config.Namespace(), clientName, "client", serviceName, t)
				if err != nil {
					t.Errorf("connectivity test failed for %s on node type %s: %v", stack, nodeType, err)
				}
			}
			return ctx
		}).
		Assess("hairpin connectivity should work", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			for _, stack := range getStack() {
				// Skip IPv6 hairpin test for ipvlan mode (known limitation)
				if stack == "ipv6" && ipvlan {
					continue
				}
				serviceName := fmt.Sprintf("server-%s-%s", nodeType, stack)
				_, err := Pull(config.Client(), config.Namespace(), serverName, "server", serviceName, t)
				if err != nil {
					t.Errorf("hairpin connectivity test failed for %s on node type %s: %v", stack, nodeType, err)
				}
			}
			return MarkTestSuccess(ctx)
		}).
		Feature()
}

// TestConnectivity_CrossNode tests pod connectivity across different nodes
func TestConnectivity_CrossNode(t *testing.T) {
	var feats []features.Feature

	for _, nodeType := range []NodeType{NodeTypeECSSharedENI, NodeTypeECSExclusiveENI, NodeTypeLingjunSharedENI, NodeTypeLingjunExclusiveENI} {
		feat := createCrossNodeTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)
}

func createCrossNodeTest(nodeType NodeType) features.Feature {
	serverName := fmt.Sprintf("server-cross-%s", nodeType)
	clientName := fmt.Sprintf("client-cross-%s", nodeType)

	return features.New(fmt.Sprintf("Connectivity/CrossNode/%s", nodeType)).
		WithLabel("env", "connectivity").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("Failed to discover node types: %v", err)
			}

			nodes := nodeInfo.GetNodesByType(nodeType)
			if len(nodes) < 2 {
				t.Skipf("Need at least 2 nodes of type %s, got %d", nodeType, len(nodes))
			}

			// Create server on node 0
			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{"app": "server", "test": "cross-node"}).
				WithContainer("server", nginxImage, nil).
				WithNodeAffinity(map[string]string{"kubernetes.io/hostname": nodes[0].Name})

			nodeAffinity := GetNodeAffinityForType(nodeType)
			if len(nodeAffinity) > 0 {
				server = server.WithNodeAffinity(nodeAffinity)
			}
			nodeAffinityExclude := GetNodeAffinityExcludeForType(nodeType)
			if len(nodeAffinityExclude) > 0 {
				server = server.WithNodeAffinityExclude(nodeAffinityExclude)
			}

			// Add tolerations for Lingjun nodes (both shared and exclusive ENI modes)
			if nodeType == NodeTypeLingjunSharedENI || nodeType == NodeTypeLingjunExclusiveENI {
				server = server.WithTolerations([]corev1.Toleration{
					{
						Key:      "node-role.alibabacloud.com/lingjun",
						Operator: corev1.TolerationOpExists,
						Effect:   corev1.TaintEffectNoSchedule,
					},
				})
			}

			err = config.Client().Resources().Create(ctx, server.Pod)
			if err != nil {
				t.Fatalf("create server pod failed, %v", err)
			}

			// Create client on node 1
			client := NewPod(clientName, config.Namespace()).
				WithLabels(map[string]string{"app": "client", "test": "cross-node"}).
				WithContainer("client", nginxImage, nil).
				WithNodeAffinity(map[string]string{"kubernetes.io/hostname": nodes[1].Name})

			if len(nodeAffinity) > 0 {
				client = client.WithNodeAffinity(nodeAffinity)
			}
			if len(nodeAffinityExclude) > 0 {
				client = client.WithNodeAffinityExclude(nodeAffinityExclude)
			}

			// Add tolerations for Lingjun nodes (both shared and exclusive ENI modes)
			if nodeType == NodeTypeLingjunSharedENI || nodeType == NodeTypeLingjunExclusiveENI {
				client = client.WithTolerations([]corev1.Toleration{
					{
						Key:      "node-role.alibabacloud.com/lingjun",
						Operator: corev1.TolerationOpExists,
						Effect:   corev1.TaintEffectNoSchedule,
					},
				})
			}

			err = config.Client().Resources().Create(ctx, client.Pod)
			if err != nil {
				t.Fatalf("create client pod failed, %v", err)
			}

			// Create services
			var objs []k8s.Object
			for _, stack := range getStack() {
				svc := NewService(fmt.Sprintf("server-cross-%s-%s", nodeType, stack), config.Namespace(),
					map[string]string{"app": "server", "test": "cross-node"}).
					ExposePort(80, "http").
					WithIPFamily(stack)

				err = config.Client().Resources().Create(ctx, svc.Service)
				if err != nil {
					t.Fatalf("create service failed, %v", err)
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
				t.Fatalf("wait pods ready failed, %v", err)
			}

			if server.Spec.NodeName == client.Spec.NodeName {
				t.Fatalf("pods are on same node: %s", server.Spec.NodeName)
			}

			t.Logf("✓ Pods on different nodes: server=%s, client=%s", server.Spec.NodeName, client.Spec.NodeName)
			return ctx
		}).
		Assess("cross-node connectivity should work", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			for _, stack := range getStack() {
				serviceName := fmt.Sprintf("server-cross-%s-%s", nodeType, stack)
				_, err := Pull(config.Client(), config.Namespace(), clientName, "client", serviceName, t)
				if err != nil {
					t.Errorf("cross-node connectivity failed for %s: %v", stack, err)
				}
			}
			return MarkTestSuccess(ctx)
		}).
		Feature()
}

// TestConnectivity_CrossZone tests pod connectivity across different availability zones
func TestConnectivity_CrossZone(t *testing.T) {
	var feats []features.Feature

	for _, nodeType := range []NodeType{NodeTypeECSSharedENI, NodeTypeECSExclusiveENI, NodeTypeLingjunSharedENI, NodeTypeLingjunExclusiveENI} {
		feat := createCrossZoneTest(nodeType)
		feats = append(feats, feat)
	}

	testenv.Test(t, feats...)
}

func createCrossZoneTest(nodeType NodeType) features.Feature {
	serverName := fmt.Sprintf("server-zone-%s", nodeType)
	clientName := fmt.Sprintf("client-zone-%s", nodeType)

	return features.New(fmt.Sprintf("Connectivity/CrossZone/%s", nodeType)).
		WithLabel("env", "connectivity").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("Failed to discover node types: %v", err)
			}

			nodes := nodeInfo.GetNodesByType(nodeType)

			// Group nodes by zone
			zoneNodes := make(map[string][]corev1.Node)
			for _, node := range nodes {
				zone := node.Labels["topology.kubernetes.io/zone"]
				if zone == "" {
					zone = node.Labels["failure-domain.beta.kubernetes.io/zone"]
				}
				if zone != "" {
					zoneNodes[zone] = append(zoneNodes[zone], node)
				}
			}

			if len(zoneNodes) < 2 {
				t.Skipf("Need at least 2 zones for cross-zone test, got %d", len(zoneNodes))
			}

			// Pick first node from first two zones
			var selectedNodes []corev1.Node
			for _, nodesInZone := range zoneNodes {
				if len(nodesInZone) > 0 {
					selectedNodes = append(selectedNodes, nodesInZone[0])
					if len(selectedNodes) == 2 {
						break
					}
				}
			}

			// Create server on node in zone 1
			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{"app": "server", "test": "cross-zone"}).
				WithContainer("server", nginxImage, nil).
				WithNodeAffinity(map[string]string{"kubernetes.io/hostname": selectedNodes[0].Name})

			nodeAffinity := GetNodeAffinityForType(nodeType)
			if len(nodeAffinity) > 0 {
				server = server.WithNodeAffinity(nodeAffinity)
			}
			nodeAffinityExclude := GetNodeAffinityExcludeForType(nodeType)
			if len(nodeAffinityExclude) > 0 {
				server = server.WithNodeAffinityExclude(nodeAffinityExclude)
			}

			// Add tolerations for Lingjun nodes (both shared and exclusive ENI modes)
			if nodeType == NodeTypeLingjunSharedENI || nodeType == NodeTypeLingjunExclusiveENI {
				server = server.WithTolerations([]corev1.Toleration{
					{
						Key:      "node-role.alibabacloud.com/lingjun",
						Operator: corev1.TolerationOpExists,
						Effect:   corev1.TaintEffectNoSchedule,
					},
				})
			}

			err = config.Client().Resources().Create(ctx, server.Pod)
			if err != nil {
				t.Fatalf("create server pod failed, %v", err)
			}

			// Create client on node in zone 2
			client := NewPod(clientName, config.Namespace()).
				WithLabels(map[string]string{"app": "client", "test": "cross-zone"}).
				WithContainer("client", nginxImage, nil).
				WithNodeAffinity(map[string]string{"kubernetes.io/hostname": selectedNodes[1].Name})

			if len(nodeAffinity) > 0 {
				client = client.WithNodeAffinity(nodeAffinity)
			}
			if len(nodeAffinityExclude) > 0 {
				client = client.WithNodeAffinityExclude(nodeAffinityExclude)
			}

			// Add tolerations for Lingjun nodes (both shared and exclusive ENI modes)
			if nodeType == NodeTypeLingjunSharedENI || nodeType == NodeTypeLingjunExclusiveENI {
				client = client.WithTolerations([]corev1.Toleration{
					{
						Key:      "node-role.alibabacloud.com/lingjun",
						Operator: corev1.TolerationOpExists,
						Effect:   corev1.TaintEffectNoSchedule,
					},
				})
			}

			err = config.Client().Resources().Create(ctx, client.Pod)
			if err != nil {
				t.Fatalf("create client pod failed, %v", err)
			}

			// Create services
			var objs []k8s.Object
			for _, stack := range getStack() {
				svc := NewService(fmt.Sprintf("server-zone-%s-%s", nodeType, stack), config.Namespace(),
					map[string]string{"app": "server", "test": "cross-zone"}).
					ExposePort(80, "http").
					WithIPFamily(stack)

				err = config.Client().Resources().Create(ctx, svc.Service)
				if err != nil {
					t.Fatalf("create service failed, %v", err)
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
				t.Fatalf("wait pods ready failed, %v", err)
			}

			// Get node zones
			var serverNode, clientNode corev1.Node
			err = config.Client().Resources().Get(ctx, server.Spec.NodeName, "", &serverNode)
			if err != nil {
				t.Fatalf("get server node failed, %v", err)
			}
			err = config.Client().Resources().Get(ctx, client.Spec.NodeName, "", &clientNode)
			if err != nil {
				t.Fatalf("get client node failed, %v", err)
			}

			getZone := func(node *corev1.Node) string {
				zone := node.Labels["topology.kubernetes.io/zone"]
				if zone == "" {
					zone = node.Labels["failure-domain.beta.kubernetes.io/zone"]
				}
				return zone
			}

			serverZone := getZone(&serverNode)
			clientZone := getZone(&clientNode)

			if serverZone == clientZone {
				t.Fatalf("pods in same zone: %s", serverZone)
			}

			t.Logf("✓ Pods in different zones: server=%s (node=%s), client=%s (node=%s)",
				serverZone, server.Spec.NodeName, clientZone, client.Spec.NodeName)
			return ctx
		}).
		Assess("cross-zone connectivity should work", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			for _, stack := range getStack() {
				serviceName := fmt.Sprintf("server-zone-%s-%s", nodeType, stack)
				_, err := Pull(config.Client(), config.Namespace(), clientName, "client", serviceName, t)
				if err != nil {
					t.Errorf("cross-zone connectivity failed for %s: %v", stack, err)
				}
			}
			return MarkTestSuccess(ctx)
		}).
		Feature()
}
