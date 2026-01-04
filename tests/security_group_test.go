//go:build e2e

package tests

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

// =============================================================================
// Security Group Connectivity Tests
// =============================================================================

// TestSecurityGroup_TrunkMode tests security group connectivity for Trunk ENI mode.
// This test verifies that:
// 1. Client pod can access server pod on port 80 (client SG allows egress 80, server SG allows ingress 80)
// 2. Server pod cannot access client pod (server SG denies egress 80 to private IPs)
//
// Prerequisites:
// - Two enterprise security groups must be created:
//   - Client SG:
//   - Egress:  ALLOW TCP port 80 to all (0.0.0.0/0)
//   - Ingress: ALLOW TCP port 80 from private IP ranges
//   - Server SG:
//   - Ingress: ALLOW TCP port 80 from private IP ranges
//   - Egress:  DENY  TCP port 80 to private IP ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
//
// Configuration:
// - Set via environment variable: export TERWAY_SG_TEST_CONFIG="TestSecurityGroup_TrunkMode:sg-client-id:sg-server-id"
func TestSecurityGroup_TrunkMode(t *testing.T) {
	const testName = "TestSecurityGroup_TrunkMode"

	// Check prerequisites
	if terway != "terway-eniip" {
		t.Skipf("%s requires terway-eniip, but terway is %s", testName, terway)
	}

	envInfo := collectClusterEnvInfo(t)
	if !envInfo.ENIInfo.HasMemberENIResource() {
		t.Skipf("%s requires aliyun/member-eni resource, but no nodes have it (total: %d)", testName, envInfo.ENIInfo.TotalMemberENI)
	}

	// Check if we have enough nodes for anti-affinity
	nodeInfo, err := DiscoverNodeTypes(context.Background(), testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("Failed to discover node types: %v", err)
	}
	if len(nodeInfo.ECSSharedENINodes) < 2 {
		t.Skipf("%s requires at least 2 ECS shared ENI nodes for anti-affinity, but only %d available", testName, len(nodeInfo.ECSSharedENINodes))
	}

	// Get security group configuration from environment variable
	sgConfig := GetSecurityGroupTestConfig(testName)
	if sgConfig == nil {
		t.Skipf("%s requires security group configuration. Set environment variable: export TERWAY_SG_TEST_CONFIG=\"%s:CLIENT_SG_ID:SERVER_SG_ID\"", testName, testName)
	}

	t.Logf("Security Group Test Configuration:")
	t.Logf("  - Client Security Group: %s", sgConfig.ClientSGID)
	t.Logf("  - Server Security Group: %s", sgConfig.ServerSGID)

	feature := createSecurityGroupConnectivityTest(testName, sgConfig.ClientSGID, sgConfig.ServerSGID)
	testenv.Test(t, feature)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// createSecurityGroupConnectivityTest creates a feature test for security group connectivity
func createSecurityGroupConnectivityTest(testName, clientSGID, serverSGID string) features.Feature {
	clientPNName := "sg-test-client-pn"
	serverPNName := "sg-test-server-pn"
	clientPodName := "sg-test-client"
	serverPodName := "sg-test-server"

	return features.New(fmt.Sprintf("SecurityGroup/%s", testName)).
		WithLabel("env", "security-group").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			var objs []k8s.Object

			// Create PodNetworking for client with client security group
			clientPN := NewPodNetworking(clientPNName).
				WithPodSelector(&metav1.LabelSelector{
					MatchLabels: map[string]string{"netplan": clientPNName},
				}).
				WithENIAttachType(networkv1beta1.ENIOptionTypeTrunk).
				WithSecurityGroups([]string{clientSGID})

			err := CreatePodNetworkingAndWaitReady(ctx, config.Client(), clientPN.PodNetworking)
			if err != nil {
				t.Fatalf("create client PodNetworking failed: %v", err)
			}
			objs = append(objs, clientPN.PodNetworking)
			t.Logf("Created client PodNetworking %s with security group %s", clientPNName, clientSGID)

			// Create PodNetworking for server with server security group
			serverPN := NewPodNetworking(serverPNName).
				WithPodSelector(&metav1.LabelSelector{
					MatchLabels: map[string]string{"netplan": serverPNName},
				}).
				WithENIAttachType(networkv1beta1.ENIOptionTypeTrunk).
				WithSecurityGroups([]string{serverSGID})

			err = CreatePodNetworkingAndWaitReady(ctx, config.Client(), serverPN.PodNetworking)
			if err != nil {
				t.Fatalf("create server PodNetworking failed: %v", err)
			}
			objs = append(objs, serverPN.PodNetworking)
			t.Logf("Created server PodNetworking %s with security group %s", serverPNName, serverSGID)

			// Create server pod first (nginx serving on port 80)
			serverPod := NewPod(serverPodName, config.Namespace()).
				WithLabels(map[string]string{
					"app":     "sg-test-server",
					"netplan": serverPNName,
				}).
				WithContainer("nginx", nginxImage, nil)

			err = config.Client().Resources().Create(ctx, serverPod.Pod)
			if err != nil {
				t.Fatalf("create server pod failed: %v", err)
			}
			objs = append(objs, serverPod.Pod)
			t.Logf("Created server pod %s", serverPodName)

			// Wait for server pod to be scheduled before creating client pod
			err = wait.For(conditions.New(config.Client().Resources()).PodConditionMatch(
				serverPod.Pod, corev1.PodScheduled, corev1.ConditionTrue),
				wait.WithTimeout(parsedTimeout))
			if err != nil {
				t.Fatalf("server pod not scheduled: %v", err)
			}

			// Refresh server pod to get node name
			err = config.Client().Resources().Get(ctx, serverPodName, config.Namespace(), serverPod.Pod)
			if err != nil {
				t.Fatalf("get server pod failed: %v", err)
			}

			// Create client pod with anti-affinity to ensure it's on a different node
			clientPod := NewPod(clientPodName, config.Namespace()).
				WithLabels(map[string]string{
					"app":     "sg-test-client",
					"netplan": clientPNName,
				}).
				WithContainer("client", nginxImage, nil).
				WithPodAntiAffinity(map[string]string{"app": "sg-test-server"})

			err = config.Client().Resources().Create(ctx, clientPod.Pod)
			if err != nil {
				t.Fatalf("create client pod failed: %v", err)
			}
			objs = append(objs, clientPod.Pod)
			t.Logf("Created client pod %s with anti-affinity to server", clientPodName)

			ctx = SaveResources(ctx, objs...)
			return ctx
		}).
		Assess("pods should be ready on different nodes", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			serverPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: serverPodName, Namespace: config.Namespace()}}
			clientPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: clientPodName, Namespace: config.Namespace()}}

			err := waitPodsReady(config.Client(), serverPod, clientPod)
			if err != nil {
				t.Fatalf("wait pods ready failed: %v", err)
			}

			err = config.Client().Resources().Get(ctx, serverPodName, config.Namespace(), serverPod)
			if err != nil {
				t.Fatalf("get server pod failed: %v", err)
			}
			err = config.Client().Resources().Get(ctx, clientPodName, config.Namespace(), clientPod)
			if err != nil {
				t.Fatalf("get client pod failed: %v", err)
			}

			// Verify anti-affinity worked
			if serverPod.Spec.NodeName == clientPod.Spec.NodeName {
				t.Fatalf("pods should be on different nodes for cross-node security group test, but both on %s", serverPod.Spec.NodeName)
			}
			t.Logf("✓ Pods are on different nodes: server=%s, client=%s", serverPod.Spec.NodeName, clientPod.Spec.NodeName)

			// Verify pods are using correct PodNetworking
			if err := ValidatePodHasPodNetworking(serverPod, serverPNName); err != nil {
				t.Fatalf("server pod should use PodNetworking %s: %v", serverPNName, err)
			}
			if err := ValidatePodHasPodNetworking(clientPod, clientPNName); err != nil {
				t.Fatalf("client pod should use PodNetworking %s: %v", clientPNName, err)
			}
			t.Logf("✓ Pods are using correct PodNetworking configurations")

			// Store pod IPs for connectivity tests
			ctx = context.WithValue(ctx, "serverPodIP", serverPod.Status.PodIP)
			ctx = context.WithValue(ctx, "clientPodIP", clientPod.Status.PodIP)
			t.Logf("  Server pod IP: %s", serverPod.Status.PodIP)
			t.Logf("  Client pod IP: %s", clientPod.Status.PodIP)

			return ctx
		}).
		Assess("client should reach server on port 80", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			serverPodIP := ctx.Value("serverPodIP").(string)
			target := fmt.Sprintf("http://%s:80", serverPodIP)

			t.Logf("Testing client -> server connectivity: %s", target)
			_, err := Pull(config.Client(), config.Namespace(), clientPodName, "client", target, t)
			if err != nil {
				t.Fatalf("client -> server connectivity FAILED (expected to succeed): %v", err)
			}
			t.Logf("✓ Client -> Server connectivity succeeded (as expected)")

			return ctx
		}).
		Assess("server should NOT reach client on port 80", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			clientPodIP := ctx.Value("clientPodIP").(string)
			target := fmt.Sprintf("http://%s:80", clientPodIP)

			t.Logf("Testing server -> client connectivity (expecting failure): %s", target)
			_, err := PullFail(config.Client(), config.Namespace(), serverPodName, "nginx", target, t)
			if err != nil {
				t.Fatalf("server -> client connectivity SUCCEEDED (expected to fail due to security group rules): %v", err)
			}
			t.Logf("✓ Server -> Client connectivity failed (as expected due to security group rules)")

			return MarkTestSuccess(ctx)
		}).
		Feature()
}
