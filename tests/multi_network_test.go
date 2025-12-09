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

// =============================================================================
// PodNetworking Multi-Network Tests
// =============================================================================

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
	CustomVSwitches []string
	CustomSecGroups []string
}

// NewMultiNetworkConfig creates a default multi-network test configuration
func NewMultiNetworkConfig() *MultiNetworkConfig {
	config := &MultiNetworkConfig{}

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

// TestPodNetworking_MultiNetwork_Default tests multi-network functionality with default config
// This test uses automatic scheduling - pods will be scheduled to nodes with required resources.
// Trunk mode pods require aliyun/member-eni resource (ECS shared ENI nodes).
// Exclusive ENI mode pods require aliyun/eni resource (ECS/Lingjun exclusive ENI nodes).
func TestPodNetworking_MultiNetwork_Default(t *testing.T) {
	// Pre-check: terway daemonset name must be terway-eniip
	if terway != "terway-eniip" {
		t.Skipf("TestPodNetworking_MultiNetwork requires terway-eniip daemonset, current: %s", terway)
		return
	}

	// Pre-check: terway version must be >= v1.16.1
	if terwayVersion != "" {
		version := terwayVersion
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		if semver.IsValid(version) && semver.Compare(version, "v1.16.1") < 0 {
			t.Skipf("TestPodNetworking_MultiNetwork requires terway version >= v1.16.1, current: %s", terwayVersion)
			return
		}
	}

	var feats []features.Feature

	// Test with Trunk mode (shared ENI, member-eni resource)
	trunkFeat := createMultiNetworkTest("Trunk", networkv1beta1.ENIOptionTypeTrunk)
	feats = append(feats, trunkFeat)

	// Test with exclusive ENI mode
	exclusiveFeat := createMultiNetworkTest("ExclusiveENI", networkv1beta1.ENIOptionTypeENI)
	feats = append(feats, exclusiveFeat)

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestPodNetworking_MultiNetwork_Custom tests multi-network functionality with custom config
// This test requires custom vswitches and security groups to be provided via flags.
func TestPodNetworking_MultiNetwork_Custom(t *testing.T) {
	config := NewMultiNetworkConfig()
	if !config.HasCustomConfig() {
		t.Skipf("TestPodNetworking_MultiNetwork_Custom requires custom vswitches and security groups")
		return
	}

	// Pre-check: terway daemonset name must be terway-eniip
	if terway != "terway-eniip" {
		t.Skipf("TestPodNetworking_MultiNetwork requires terway-eniip daemonset, current: %s", terway)
		return
	}

	// Pre-check: terway version must be >= v1.16.1
	if terwayVersion != "" {
		version := terwayVersion
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		if semver.IsValid(version) && semver.Compare(version, "v1.16.1") < 0 {
			t.Skipf("TestPodNetworking_MultiNetwork requires terway version >= v1.16.1, current: %s", terwayVersion)
			return
		}
	}

	var feats []features.Feature

	// Test with Trunk mode and custom config
	trunkFeat := createMultiNetworkTestWithCustomConfig("Trunk", networkv1beta1.ENIOptionTypeTrunk, config)
	feats = append(feats, trunkFeat)

	// Test with exclusive ENI mode and custom config
	exclusiveFeat := createMultiNetworkTestWithCustomConfig("ExclusiveENI", networkv1beta1.ENIOptionTypeENI, config)
	feats = append(feats, exclusiveFeat)

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// createMultiNetworkTest creates a multi-network test feature for a specific ENI mode
func createMultiNetworkTest(eniModeName string, eniType networkv1beta1.ENIAttachType) features.Feature {
	pnPrimary := fmt.Sprintf("multi-net-primary-%s", strings.ToLower(eniModeName))
	pnSecondary := fmt.Sprintf("multi-net-secondary-%s", strings.ToLower(eniModeName))
	podName := fmt.Sprintf("multi-net-pod-%s", strings.ToLower(eniModeName))

	return features.New(fmt.Sprintf("PodNetworking/MultiNetwork/%s", eniModeName)).
		WithLabel("eni-type", string(eniType)).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Check required ENI resources
			eniInfo := checkENIResourcesBeforeTest(t)
			if eniType == networkv1beta1.ENIOptionTypeTrunk {
				if !eniInfo.HasMemberENIResource() {
					t.Skipf("Trunk mode requires aliyun/member-eni resource, but no nodes have it")
				}
			} else {
				if !eniInfo.HasENIResource() {
					t.Skipf("Exclusive ENI mode requires aliyun/eni resource, but no nodes have it")
				}
			}

			// Create primary PodNetworking
			pn1 := NewPodNetworking(pnPrimary).
				WithENIAttachType(eniType)
			err := CreatePodNetworkingAndWaitReady(ctx, config.Client(), pn1.PodNetworking)
			if err != nil {
				t.Fatalf("create and wait podNetworking %s failed: %v", pnPrimary, err)
			}
			ctx = SaveResources(ctx, pn1.PodNetworking)
			t.Logf("Created primary PodNetworking: %s (eniType=%s)", pnPrimary, eniType)

			// Create secondary PodNetworking
			pn2 := NewPodNetworking(pnSecondary).
				WithENIAttachType(eniType)
			err = CreatePodNetworkingAndWaitReady(ctx, config.Client(), pn2.PodNetworking)
			if err != nil {
				t.Fatalf("create and wait podNetworking %s failed: %v", pnSecondary, err)
			}
			ctx = SaveResources(ctx, pn2.PodNetworking)
			t.Logf("Created secondary PodNetworking: %s (eniType=%s)", pnSecondary, eniType)

			return ctx
		}).
		Assess("both networks should be ready", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			err := WaitPodNetworkingReady(pnPrimary, config.Client())
			assert.NoError(t, err, "primary network should be ready")

			err = WaitPodNetworkingReady(pnSecondary, config.Client())
			assert.NoError(t, err, "secondary network should be ready")
			return ctx
		}).
		Assess("pod with multi-network should be running", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Create pod with multi-network annotation
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

			err := config.Client().Resources().Create(ctx, pod.Pod)
			if err != nil {
				t.Fatalf("create pod %s failed: %v", podName, err)
			}
			ctx = SaveResources(ctx, pod.Pod)

			// Wait for pod to be ready
			err = wait.For(conditions.New(config.Client().Resources()).PodReady(pod.Pod),
				wait.WithTimeout(parsedTimeout),
				wait.WithInterval(1*time.Second))
			if err != nil {
				t.Fatalf("wait pod ready failed: %v", err)
			}

			t.Logf("Pod %s is running with multi-network annotation (eniType=%s)", podName, eniType)
			return ctx
		}).
		Assess("pod should have multiple network interfaces", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: config.Namespace()},
			}
			err := config.Client().Resources().Get(ctx, podName, config.Namespace(), pod)
			if err != nil {
				t.Fatalf("get pod failed: %v", err)
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

			t.Logf("Pod %s has multiple interfaces: eth0, eth1 (eniType=%s)", podName, eniType)
			return MarkTestSuccess(ctx)
		}).
		Feature()
}

// createMultiNetworkTestWithCustomConfig creates a multi-network test with custom vswitches and security groups
func createMultiNetworkTestWithCustomConfig(eniModeName string, eniType networkv1beta1.ENIAttachType, testConfig *MultiNetworkConfig) features.Feature {
	pnPrimary := fmt.Sprintf("multi-net-custom-primary-%s", strings.ToLower(eniModeName))
	pnSecondary := fmt.Sprintf("multi-net-custom-secondary-%s", strings.ToLower(eniModeName))
	podName := fmt.Sprintf("multi-net-custom-pod-%s", strings.ToLower(eniModeName))

	return features.New(fmt.Sprintf("PodNetworking/MultiNetwork/Custom/%s", eniModeName)).
		WithLabel("eni-type", string(eniType)).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Check required ENI resources
			eniInfo := checkENIResourcesBeforeTest(t)
			if eniType == networkv1beta1.ENIOptionTypeTrunk {
				if !eniInfo.HasMemberENIResource() {
					t.Skipf("Trunk mode requires aliyun/member-eni resource, but no nodes have it")
				}
			} else {
				if !eniInfo.HasENIResource() {
					t.Skipf("Exclusive ENI mode requires aliyun/eni resource, but no nodes have it")
				}
			}

			// Create primary PodNetworking with default config
			pn1 := NewPodNetworking(pnPrimary).
				WithENIAttachType(eniType)
			err := CreatePodNetworkingAndWaitReady(ctx, config.Client(), pn1.PodNetworking)
			if err != nil {
				t.Fatalf("create and wait podNetworking %s failed: %v", pnPrimary, err)
			}
			ctx = SaveResources(ctx, pn1.PodNetworking)
			t.Logf("Created primary PodNetworking: %s (eniType=%s)", pnPrimary, eniType)

			// Create secondary PodNetworking with custom config
			pn2 := NewPodNetworking(pnSecondary).
				WithENIAttachType(eniType).
				WithVSwitches(testConfig.CustomVSwitches).
				WithSecurityGroups(testConfig.CustomSecGroups)
			err = CreatePodNetworkingAndWaitReady(ctx, config.Client(), pn2.PodNetworking)
			if err != nil {
				t.Fatalf("create and wait podNetworking %s failed: %v", pnSecondary, err)
			}
			ctx = SaveResources(ctx, pn2.PodNetworking)
			t.Logf("Created secondary PodNetworking: %s with custom config (vswitches=%v, sg=%v)",
				pnSecondary, testConfig.CustomVSwitches, testConfig.CustomSecGroups)

			return ctx
		}).
		Assess("both networks should be ready", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			err := WaitPodNetworkingReady(pnPrimary, config.Client())
			assert.NoError(t, err, "primary network should be ready")

			err = WaitPodNetworkingReady(pnSecondary, config.Client())
			assert.NoError(t, err, "secondary network should be ready")
			return ctx
		}).
		Assess("pod with multi-network should be running", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Create pod with multi-network annotation
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
				WithLabels(map[string]string{"app": "multi-net-custom-test"}).
				WithContainer("server", nginxImage, nil).
				WithMultiNetworkAnnotation(networks)

			err := config.Client().Resources().Create(ctx, pod.Pod)
			if err != nil {
				t.Fatalf("create pod %s failed: %v", podName, err)
			}
			ctx = SaveResources(ctx, pod.Pod)

			// Wait for pod to be ready
			err = wait.For(conditions.New(config.Client().Resources()).PodReady(pod.Pod),
				wait.WithTimeout(parsedTimeout),
				wait.WithInterval(1*time.Second))
			if err != nil {
				t.Fatalf("wait pod ready failed: %v", err)
			}

			t.Logf("Pod %s is running with custom multi-network annotation (eniType=%s)", podName, eniType)
			return ctx
		}).
		Assess("pod should have multiple network interfaces", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: config.Namespace()},
			}
			err := config.Client().Resources().Get(ctx, podName, config.Namespace(), pod)
			if err != nil {
				t.Fatalf("get pod failed: %v", err)
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

			t.Logf("Pod %s has multiple interfaces with custom config: eth0, eth1 (eniType=%s)", podName, eniType)
			return MarkTestSuccess(ctx)
		}).
		Feature()
}
