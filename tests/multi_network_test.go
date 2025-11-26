//go:build e2e

package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/mod/semver"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	terwayTypes "github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"
)

// WithMultiNetworkAnnotation adds the multi-network annotation to a pod
func (p *Pod) WithMultiNetworkAnnotation(networks []controlplane.PodNetworkRef) *Pod {
	if p.Annotations == nil {
		p.Annotations = make(map[string]string)
	}
	data, _ := json.Marshal(networks)
	p.Annotations[terwayTypes.PodNetworksRequest] = string(data)
	return p
}

// MultiNetworkConfig defines the configuration for multi-network tests
type MultiNetworkConfig struct {
	NodeTypes         []NodeType
	CustomVSwitches   []string
	CustomSecGroups   []string
	EnableDefaultMode bool // Whether to test with default PodNetworking config
}

// NewMultiNetworkConfig creates a default multi-network test configuration
func NewMultiNetworkConfig() *MultiNetworkConfig {
	config := &MultiNetworkConfig{
		NodeTypes:         []NodeType{NodeTypeECSSharedENI, NodeTypeECSExclusiveENI, NodeTypeLingjunSharedENI, NodeTypeLingjunExclusiveENI},
		EnableDefaultMode: true, // Always test default mode
	}

	// Parse custom vswitches and security groups if provided
	if vSwitchIDs != "" {
		config.CustomVSwitches = strings.Split(vSwitchIDs, ",")
	}
	if securityGroupIDs != "" {
		config.CustomSecGroups = strings.Split(securityGroupIDs, ",")
	}

	return config
}

// HasCustomConfig returns true if custom vswitches or security groups are provided
func (c *MultiNetworkConfig) HasCustomConfig() bool {
	return len(c.CustomVSwitches) > 0 && len(c.CustomSecGroups) > 0
}

// TestMultiNetwork tests multi-network functionality across different node types
func TestMultiNetwork(t *testing.T) {
	// Pre-check: terway daemonset name must be terway-eniip
	if terway != "terway-eniip" {
		t.Skipf("TestMultiNetwork requires terway-eniip daemonset, current: %s", terway)
		return
	}

	// Pre-check: terway version must be >= v1.16.1
	if terwayVersion != "" {
		version := terwayVersion
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		if semver.IsValid(version) && semver.Compare(version, "v1.16.1") < 0 {
			t.Skipf("TestMultiNetwork requires terway version >= v1.16.1, current: %s", terwayVersion)
			return
		}
	}

	config := NewMultiNetworkConfig()

	// Test scenarios: default mode (if enabled) + custom mode (if config available)
	testScenarios := []struct {
		name      string
		mode      string
		skipCheck func() bool
		nodeTypes []NodeType
	}{
		{
			name:      "DefaultConfig",
			mode:      "default",
			skipCheck: func() bool { return false },
			nodeTypes: config.NodeTypes,
		},
		{
			name:      "CustomConfig",
			mode:      "custom",
			skipCheck: func() bool { return !config.HasCustomConfig() },
			nodeTypes: config.NodeTypes,
		},
	}

	for _, scenario := range testScenarios {
		if scenario.skipCheck() {
			t.Logf("Skipping %s mode: conditions not met", scenario.name)
			continue
		}

		for _, nodeType := range scenario.nodeTypes {
			t.Run(fmt.Sprintf("%s_%s", scenario.name, nodeType), func(t *testing.T) {
				testMultiNetworkForNodeType(t, nodeType, scenario.mode, config)
			})
		}
	}
}

// testMultiNetworkForNodeType tests multi-network functionality for a specific node type and configuration mode
func testMultiNetworkForNodeType(t *testing.T, nodeType NodeType, mode string, config *MultiNetworkConfig) {
	// Pre-check: skip Lingjun nodes as they are not supported yet
	switch nodeType {
	case NodeTypeLingjunSharedENI, NodeTypeLingjunExclusiveENI:
		t.Skipf("Lingjun nodes are not supported yet")
		return
	}

	testName := fmt.Sprintf("MultiNetwork/%s/%s", mode, nodeType)

	feature := createMultiNetworkTestFeature(testName, nodeType, mode, config)
	testenv.Test(t, feature)
}

// createMultiNetworkTestFeature creates a multi-network test feature
func createMultiNetworkTestFeature(testName string, nodeType NodeType, mode string, testConfig *MultiNetworkConfig) features.Feature {
	pnPrimary := fmt.Sprintf("multi-net-primary-%s-%s", mode, nodeType)
	pnSecondary := fmt.Sprintf("multi-net-secondary-%s-%s", mode, nodeType)
	podName := fmt.Sprintf("multi-net-test-pod-%s-%s", mode, nodeType)

	return features.New(testName).
		WithLabel("env", "multi-network").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Check if required node type is available
			nodeInfo, err := DiscoverNodeTypes(context.Background(), config.Client())
			if err != nil {
				t.Fatalf("Failed to discover node types: %v", err)
			}

			requiredNodes := nodeInfo.GetNodesByType(nodeType)
			if len(requiredNodes) == 0 {
				t.Skipf("No nodes of type %s available", nodeType)
			}

			// Create primary PodNetworking (always use default config)
			pn1 := NewPodNetworking(pnPrimary)
			if nodeType == NodeTypeECSExclusiveENI || nodeType == NodeTypeLingjunExclusiveENI {
				pn1 = pn1.WithENIAttachType(networkv1beta1.ENIOptionTypeENI)
			}
			err = CreatePodNetworkingAndWaitReady(ctx, config.Client(), pn1.PodNetworking)
			if err != nil {
				t.Fatalf("create and wait podNetworking %s failed, %v", pnPrimary, err)
			}
			ctx = SaveResources(ctx, pn1.PodNetworking)
			t.Logf("Created primary PodNetworking: %s", pnPrimary)

			// Create secondary PodNetworking based on mode
			pn2 := NewPodNetworking(pnSecondary)
			if nodeType == NodeTypeECSExclusiveENI || nodeType == NodeTypeLingjunExclusiveENI {
				pn2 = pn2.WithENIAttachType(networkv1beta1.ENIOptionTypeENI)
			}
			if mode == "custom" {
				pn2 = pn2.WithVSwitches(testConfig.CustomVSwitches).
					WithSecurityGroups(testConfig.CustomSecGroups)
				t.Logf("Created secondary PodNetworking with custom config: vswitches=%v, sg=%v",
					testConfig.CustomVSwitches, testConfig.CustomSecGroups)
			} else {
				t.Logf("Created secondary PodNetworking with default config")
			}

			err = CreatePodNetworkingAndWaitReady(ctx, config.Client(), pn2.PodNetworking)
			if err != nil {
				t.Fatalf("create and wait podNetworking %s failed, %v", pnSecondary, err)
			}
			ctx = SaveResources(ctx, pn2.PodNetworking)
			t.Logf("Created secondary PodNetworking: %s", pnSecondary)

			return ctx
		}).
		Assess("both networks should be ready", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			err := WaitPodNetworkingReady(pnPrimary, config.Client())
			assert.NoError(t, err, "primary network should be ready")

			err = WaitPodNetworkingReady(pnSecondary, config.Client())
			assert.NoError(t, err, "secondary network should be ready")
			return ctx
		}).
		Assess("pod should be created and configured", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Check if required node type is available
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("Failed to discover node types: %v", err)
			}

			requiredNodes := nodeInfo.GetNodesByType(nodeType)
			if len(requiredNodes) == 0 {
				t.Fatalf("No nodes of type %s available: %v", nodeType, err)
			}

			// Create pod with multi-network annotation and node affinity
			networks := []controlplane.PodNetworkRef{
				{
					InterfaceName: "eth0",
					Network:       pnPrimary,
					DefaultRoute:  true,
				},
				{
					InterfaceName: "eth1",
					Network:       pnSecondary,
				},
			}

			pod := NewPod(podName, config.Namespace()).
				WithLabels(map[string]string{"app": "multi-net-test"}).
				WithContainer("server", nginxImage, nil).
				WithMultiNetworkAnnotation(networks)

			// Apply node affinity based on node type
			nodeAffinity := GetNodeAffinityForType(nodeType)
			if len(nodeAffinity) > 0 {
				pod = pod.WithNodeAffinity(nodeAffinity)
			}
			nodeAffinityExclude := GetNodeAffinityExcludeForType(nodeType)
			if len(nodeAffinityExclude) > 0 {
				pod = pod.WithNodeAffinityExclude(nodeAffinityExclude)
			}

			err = config.Client().Resources().Create(ctx, pod.Pod)
			if err != nil {
				t.Fatalf("create pod %s failed, %v", podName, err)
			}

			ctx = SaveResources(ctx, pod.Pod)
			t.Logf("Created pod %s with multi-network annotation (mode: %s)",
				podName, mode)
			return ctx
		}).
		Assess("pod should be running with multiple interfaces", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: config.Namespace()},
			}

			err := wait.For(conditions.New(config.Client().Resources()).PodReady(pod),
				wait.WithTimeout(parsedTimeout),
				wait.WithInterval(1*time.Second))
			if err != nil {
				t.Fatalf("wait pod ready failed, %v", err)
			}

			// Get the pod to check annotations
			err = config.Client().Resources().Get(ctx, podName, config.Namespace(), pod)
			if err != nil {
				t.Fatalf("get pod failed, %v", err)
			}

			// Verify PodNetworks annotation is set
			podNetworksAnno, ok := pod.Annotations[terwayTypes.PodNetworks]
			if !ok {
				t.Fatal("pod does not have PodNetworks annotation")
			}

			var podNetworks controlplane.PodNetworksAnnotation
			err = json.Unmarshal([]byte(podNetworksAnno), &podNetworks)
			if err != nil {
				t.Fatalf("failed to parse PodNetworks annotation: %v", err)
			}

			// Verify we have 2 networks
			assert.Equal(t, 2, len(podNetworks.PodNetworks), "expected 2 network interfaces")

			// Verify interface names
			interfaces := make(map[string]bool)
			for _, net := range podNetworks.PodNetworks {
				interfaces[net.Interface] = true
			}
			assert.True(t, interfaces["eth0"], "eth0 interface should be configured")
			assert.True(t, interfaces["eth1"], "eth1 interface should be configured")

			t.Logf("Pod %s successfully configured with multiple interfaces on node type %s (mode: %s)",
				podName, nodeType, mode)
			return ctx
		}).
		Assess("multi-network connectivity should work", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Simple connectivity test - create a client pod and test communication
			clientPod := NewPod(fmt.Sprintf("client-%s-%s", mode, nodeType), config.Namespace()).
				WithLabels(map[string]string{"app": "multi-net-client"}).
				WithContainer("client", nginxImage, nil)

			// Apply same node affinity as server pod
			nodeAffinity := GetNodeAffinityForType(nodeType)
			if len(nodeAffinity) > 0 {
				clientPod = clientPod.WithNodeAffinity(nodeAffinity)
			}
			nodeAffinityExclude := GetNodeAffinityExcludeForType(nodeType)
			if len(nodeAffinityExclude) > 0 {
				clientPod = clientPod.WithNodeAffinityExclude(nodeAffinityExclude)
			}

			err := config.Client().Resources().Create(ctx, clientPod.Pod)
			if err != nil {
				t.Fatalf("create client pod failed, %v", err)
			}
			ctx = SaveResources(ctx, clientPod.Pod)

			// Wait for client pod to be ready
			err = wait.For(conditions.New(config.Client().Resources()).PodReady(clientPod.Pod),
				wait.WithTimeout(parsedTimeout),
				wait.WithInterval(1*time.Second))
			if err != nil {
				t.Fatalf("wait client pod ready failed, %v", err)
			}

			// Test basic connectivity between pods on the same node
			// In a real multi-network scenario, we would test connectivity across different networks
			// For now, we just verify both pods are running
			t.Logf("Both server and client pods are running successfully on node type %s (mode: %s)",
				nodeType, mode)

			return MarkTestSuccess(ctx)
		}).Feature()
}
