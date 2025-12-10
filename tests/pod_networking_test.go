//go:build e2e

package tests

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	terwayTypes "github.com/AliyunContainerService/terway/types"
)

// =============================================================================
// Environment Information Collection
// =============================================================================

// ClusterEnvInfo contains cluster environment information
type ClusterEnvInfo struct {
	NodeInfo    *NodeTypeInfo
	ENIInfo     *ENIResourceInfo
	NodeDetails []NodeDetail
}

// NodeDetail contains detailed information about a node
type NodeDetail struct {
	Name        string
	MachineType string // "ECS" or "EFLO"
	ENIMode     string // "Shared" or "Exclusive"
	NodeType    NodeType
}

// collectClusterEnvInfo collects and logs cluster environment information
func collectClusterEnvInfo(t *testing.T) *ClusterEnvInfo {
	ctx := context.Background()

	// Discover node types
	nodeInfo, err := DiscoverNodeTypes(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("failed to discover node types: %v", err)
	}

	// Discover ENI resources
	eniInfo, err := CheckENIResourcesForPodNetworking(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("failed to check ENI resources: %v", err)
	}

	// Build node details
	var nodeDetails []NodeDetail
	for _, node := range nodeInfo.AllNodes {
		detail := NodeDetail{
			Name:     node.Name,
			NodeType: classifyNode(node),
		}

		// Determine machine type
		if isLingjunNode(&node) {
			detail.MachineType = "EFLO"
		} else {
			detail.MachineType = "ECS"
		}

		// Determine ENI mode
		if isExclusiveENINode(&node) {
			detail.ENIMode = "Exclusive"
		} else {
			detail.ENIMode = "Shared"
		}

		nodeDetails = append(nodeDetails, detail)
	}

	envInfo := &ClusterEnvInfo{
		NodeInfo:    nodeInfo,
		ENIInfo:     eniInfo,
		NodeDetails: nodeDetails,
	}

	// Log environment information
	t.Log("=== Cluster Environment Information ===")
	t.Logf("Total Nodes: %d", len(nodeInfo.AllNodes))
	t.Logf("  - ECS Shared ENI: %d nodes", len(nodeInfo.ECSSharedENINodes))
	t.Logf("  - ECS Exclusive ENI: %d nodes", len(nodeInfo.ECSExclusiveENINodes))
	t.Logf("  - EFLO Shared ENI: %d nodes", len(nodeInfo.LingjunSharedENINodes))
	t.Logf("  - EFLO Exclusive ENI: %d nodes", len(nodeInfo.LingjunExclusiveENINodes))
	t.Logf("ENI Resources:")
	t.Logf("  - aliyun/eni (Exclusive): %d", eniInfo.TotalExclusiveENI)
	t.Logf("  - aliyun/member-eni (Trunk): %d", eniInfo.TotalMemberENI)
	t.Log("")
	t.Log("Node Details:")
	for _, detail := range nodeDetails {
		t.Logf("  - %s: %s/%s", detail.Name, detail.MachineType, detail.ENIMode)
	}
	t.Log("=========================================")

	return envInfo
}

// checkENIResourcesBeforeTest checks if the cluster has sufficient ENI resources for PodNetworking tests
func checkENIResourcesBeforeTest(t *testing.T) *ENIResourceInfo {
	ctx := context.Background()
	eniInfo, err := CheckENIResourcesForPodNetworking(ctx, testenv.EnvConf().Client())
	if err != nil {
		t.Fatalf("failed to check ENI resources: %v", err)
	}

	t.Logf("ENI Resources - Exclusive ENI (aliyun/eni): %d, Member ENI (aliyun/member-eni): %d",
		eniInfo.TotalExclusiveENI, eniInfo.TotalMemberENI)

	return eniInfo
}

// =============================================================================
// PodNetworking ENI Mode Tests
// =============================================================================

// TestPodNetworking_TrunkMode tests Trunk ENI attach type
// Pods will be automatically scheduled to nodes with aliyun/member-eni resource.
func TestPodNetworking_TrunkMode(t *testing.T) {
	envInfo := collectClusterEnvInfo(t)
	if terway != "terway-eniip" {
		t.Skipf("TestPodNetworking_ExclusiveENIMode requires terway-eniip, but terway is %s", terway)
	}
	if !envInfo.ENIInfo.HasMemberENIResource() {
		t.Skipf("TestPodNetworking_TrunkMode requires aliyun/member-eni resource, but no nodes have it (total: %d)", envInfo.ENIInfo.TotalMemberENI)
	}

	config := DefaultPodNetworkingTestConfig().
		WithENIAttachType(networkv1beta1.ENIOptionTypeTrunk).
		WithExpectedENIType(networkv1beta1.ENITypeMember)

	RunPodNetworkingTestSuite(t, config)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestPodNetworking_ExclusiveENIMode tests Exclusive ENI attach type
// Pods will be automatically scheduled to nodes with aliyun/eni resource.
func TestPodNetworking_ExclusiveENIMode(t *testing.T) {
	envInfo := collectClusterEnvInfo(t)
	if terway != "terway-eniip" {
		t.Skipf("TestPodNetworking_ExclusiveENIMode requires terway-eniip, but terway is %s", terway)
	}
	if !envInfo.ENIInfo.HasExclusiveENIResource() {
		t.Skipf("TestPodNetworking_ExclusiveENIMode requires aliyun/eni resource, but no nodes have it (total: %d)", envInfo.ENIInfo.TotalExclusiveENI)
	}

	config := DefaultPodNetworkingTestConfig().
		WithENIAttachType(networkv1beta1.ENIOptionTypeENI).
		WithExpectedENIType(networkv1beta1.ENITypeSecondary)

	RunPodNetworkingTestSuite(t, config)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestPodNetworking_DefaultMode tests Default ENI attach type (auto-selection)
// The system will automatically select the appropriate ENI type based on available resources.
func TestPodNetworking_DefaultMode(t *testing.T) {
	envInfo := collectClusterEnvInfo(t)
	if terway != "terway-eniip" {
		t.Skipf("TestPodNetworking_ExclusiveENIMode requires terway-eniip, but terway is %s", terway)
	}
	if !envInfo.ENIInfo.HasExclusiveENIResource() && !envInfo.ENIInfo.HasMemberENIResource() {
		t.Skipf("TestPodNetworking_DefaultMode requires either aliyun/eni or aliyun/member-eni resource, but no nodes have them (exclusive: %d, member: %d)",
			envInfo.ENIInfo.TotalExclusiveENI, envInfo.ENIInfo.TotalMemberENI)
	}

	config := DefaultPodNetworkingTestConfig().
		WithENIAttachType(networkv1beta1.ENIOptionTypeDefault)

	RunPodNetworkingTestSuite(t, config)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// =============================================================================
// PodNetworking Selector Tests
// =============================================================================

// TestPodNetworking_PodSelector tests pod selector matching
// Pods with matching labels will use the PodNetworking configuration.
func TestPodNetworking_PodSelector(t *testing.T) {
	envInfo := collectClusterEnvInfo(t)
	if terway != "terway-eniip" {
		t.Skipf("TestPodNetworking_ExclusiveENIMode requires terway-eniip, but terway is %s", terway)
	}
	if !envInfo.ENIInfo.HasMemberENIResource() {
		t.Skipf("TestPodNetworking_PodSelector requires aliyun/member-eni resource, but no nodes have it (total: %d)", envInfo.ENIInfo.TotalMemberENI)
	}

	pnName := "pod-selector-pn"

	feature := features.New("PodNetworking/PodSelector").
		WithLabel("env", "pod-networking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pn := NewPodNetworking(pnName).
				WithPodSelector(&metav1.LabelSelector{
					MatchLabels: map[string]string{"network": "custom"},
				}).
				WithENIAttachType(networkv1beta1.ENIOptionTypeTrunk)

			err := CreatePodNetworkingAndWaitReady(ctx, config.Client(), pn.PodNetworking)
			if err != nil {
				t.Fatalf("create PodNetworking failed: %v", err)
			}
			ctx = SaveResources(ctx, pn.PodNetworking)

			matchingPod := NewPod("matching-pod", config.Namespace()).
				WithLabels(map[string]string{"network": "custom"}).
				WithContainer("nginx", nginxImage, nil)

			err = config.Client().Resources().Create(ctx, matchingPod.Pod)
			if err != nil {
				t.Fatalf("create matching pod failed: %v", err)
			}

			nonMatchingPod := NewPod("non-matching-pod", config.Namespace()).
				WithLabels(map[string]string{"network": "default"}).
				WithContainer("nginx", nginxImage, nil)

			err = config.Client().Resources().Create(ctx, nonMatchingPod.Pod)
			if err != nil {
				t.Fatalf("create non-matching pod failed: %v", err)
			}

			ctx = SaveResources(ctx, matchingPod.Pod, nonMatchingPod.Pod)
			return ctx
		}).
		Assess("matching pod should use PodNetworking", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			matchingPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "matching-pod", Namespace: config.Namespace()}}

			err := wait.For(conditions.New(config.Client().Resources()).PodReady(matchingPod),
				wait.WithTimeout(parsedTimeout))
			if err != nil {
				t.Fatalf("matching pod not ready: %v", err)
			}

			err = config.Client().Resources().Get(ctx, matchingPod.Name, matchingPod.Namespace, matchingPod)
			if err != nil {
				t.Fatalf("get matching pod failed: %v", err)
			}

			if err := ValidatePodHasPodNetworking(matchingPod, pnName); err != nil {
				t.Fatalf("matching pod should use PodNetworking: %v", err)
			}
			t.Logf("Pod with matching selector uses PodNetworking %s", pnName)

			return ctx
		}).
		Assess("non-matching pod should NOT use PodNetworking", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nonMatchingPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "non-matching-pod", Namespace: config.Namespace()}}

			err := wait.For(conditions.New(config.Client().Resources()).PodReady(nonMatchingPod),
				wait.WithTimeout(parsedTimeout))
			if err != nil {
				t.Fatalf("non-matching pod not ready: %v", err)
			}

			err = config.Client().Resources().Get(ctx, nonMatchingPod.Name, nonMatchingPod.Namespace, nonMatchingPod)
			if err != nil {
				t.Fatalf("get non-matching pod failed: %v", err)
			}

			if nonMatchingPod.Annotations[terwayTypes.PodNetworking] == pnName {
				t.Fatalf("non-matching pod should NOT use PodNetworking %s", pnName)
			}
			t.Logf("Pod without matching selector does NOT use PodNetworking %s", pnName)

			return MarkTestSuccess(ctx)
		}).
		Feature()

	testenv.Test(t, feature)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// TestPodNetworking_NamespaceSelector tests namespace selector matching
// Pods in namespaces with matching labels will use the PodNetworking configuration.
func TestPodNetworking_NamespaceSelector(t *testing.T) {
	envInfo := collectClusterEnvInfo(t)
	if terway != "terway-eniip" {
		t.Skipf("TestPodNetworking_ExclusiveENIMode requires terway-eniip, but terway is %s", terway)
	}
	if !envInfo.ENIInfo.HasMemberENIResource() {
		t.Skipf("TestPodNetworking_NamespaceSelector requires aliyun/member-eni resource, but no nodes have it (total: %d)", envInfo.ENIInfo.TotalMemberENI)
	}

	pnName := "namespace-selector-pn"
	matchingNs := "matching-namespace"
	nonMatchingNs := "non-matching-namespace"

	feature := features.New("PodNetworking/NamespaceSelector").
		WithLabel("env", "pod-networking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pn := NewPodNetworking(pnName).
				WithNamespaceSelector(&metav1.LabelSelector{
					MatchLabels: map[string]string{"network-type": "custom"},
				}).
				WithENIAttachType(networkv1beta1.ENIOptionTypeTrunk)

			err := CreatePodNetworkingAndWaitReady(ctx, config.Client(), pn.PodNetworking)
			if err != nil {
				t.Fatalf("create PodNetworking failed: %v", err)
			}
			ctx = SaveResources(ctx, pn.PodNetworking)

			matchingNsObj := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: matchingNs,
					Labels: map[string]string{
						"network-type":              "custom",
						"k8s.aliyun.com/terway-e2e": "true",
					},
				},
			}
			err = config.Client().Resources().Create(ctx, matchingNsObj)
			if err != nil {
				t.Fatalf("create matching namespace failed: %v", err)
			}

			nonMatchingNsObj := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nonMatchingNs,
					Labels: map[string]string{
						"network-type":              "default",
						"k8s.aliyun.com/terway-e2e": "true",
					},
				},
			}
			err = config.Client().Resources().Create(ctx, nonMatchingNsObj)
			if err != nil {
				t.Fatalf("create non-matching namespace failed: %v", err)
			}

			matchingPod := NewPod("pod-in-matching-ns", matchingNs).
				WithContainer("nginx", nginxImage, nil)
			err = config.Client().Resources().Create(ctx, matchingPod.Pod)
			if err != nil {
				t.Fatalf("create pod in matching namespace failed: %v", err)
			}

			nonMatchingPod := NewPod("pod-in-non-matching-ns", nonMatchingNs).
				WithContainer("nginx", nginxImage, nil)
			err = config.Client().Resources().Create(ctx, nonMatchingPod.Pod)
			if err != nil {
				t.Fatalf("create pod in non-matching namespace failed: %v", err)
			}

			ctx = SaveResources(ctx, matchingNsObj, nonMatchingNsObj, matchingPod.Pod, nonMatchingPod.Pod)
			return ctx
		}).
		Assess("pod in matching namespace should use PodNetworking", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			matchingPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-in-matching-ns", Namespace: matchingNs}}

			err := wait.For(conditions.New(config.Client().Resources()).PodReady(matchingPod),
				wait.WithTimeout(parsedTimeout))
			if err != nil {
				t.Fatalf("pod in matching namespace not ready: %v", err)
			}

			err = config.Client().Resources().Get(ctx, matchingPod.Name, matchingPod.Namespace, matchingPod)
			if err != nil {
				t.Fatalf("get pod in matching namespace failed: %v", err)
			}

			if err := ValidatePodHasPodNetworking(matchingPod, pnName); err != nil {
				t.Fatalf("pod in matching namespace should use PodNetworking: %v", err)
			}
			t.Logf("Pod in namespace with matching selector uses PodNetworking %s", pnName)

			return ctx
		}).
		Assess("pod in non-matching namespace should NOT use PodNetworking", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nonMatchingPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-in-non-matching-ns", Namespace: nonMatchingNs}}

			err := wait.For(conditions.New(config.Client().Resources()).PodReady(nonMatchingPod),
				wait.WithTimeout(parsedTimeout))
			if err != nil {
				t.Fatalf("pod in non-matching namespace not ready: %v", err)
			}

			err = config.Client().Resources().Get(ctx, nonMatchingPod.Name, nonMatchingPod.Namespace, nonMatchingPod)
			if err != nil {
				t.Fatalf("get pod in non-matching namespace failed: %v", err)
			}

			if nonMatchingPod.Annotations[terwayTypes.PodNetworking] == pnName {
				t.Fatalf("pod in non-matching namespace should NOT use PodNetworking %s", pnName)
			}
			t.Logf("Pod in namespace without matching selector does NOT use PodNetworking %s", pnName)

			return MarkTestSuccess(ctx)
		}).
		Feature()

	testenv.Test(t, feature)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// =============================================================================
// PodNetworking Fixed IP Tests
// =============================================================================

// TestPodNetworking_FixedIP tests fixed IP allocation for both Trunk and Exclusive ENI modes.
// Pods with fixed IP will retain their IP across restarts within the TTL period.
func TestPodNetworking_FixedIP(t *testing.T) {
	if terway != "terway-eniip" {
		t.Skipf("TestPodNetworking_ExclusiveENIMode requires terway-eniip, but terway is %s", terway)
	}
	envInfo := collectClusterEnvInfo(t)

	var feats []features.Feature

	// Test with Trunk mode if member-eni resource is available
	if envInfo.ENIInfo.HasMemberENIResource() {
		trunkFeat := createFixedIPTest("Trunk", networkv1beta1.ENIOptionTypeTrunk)
		feats = append(feats, trunkFeat)
	} else {
		t.Logf("Skipping Trunk mode FixedIP test: no aliyun/member-eni resource available")
	}

	// Test with Exclusive ENI mode if eni resource is available
	if envInfo.ENIInfo.HasExclusiveENIResource() {
		exclusiveFeat := createFixedIPTest("ExclusiveENI", networkv1beta1.ENIOptionTypeENI)
		feats = append(feats, exclusiveFeat)
	} else {
		t.Logf("Skipping ExclusiveENI mode FixedIP test: no aliyun/eni resource available")
	}

	if len(feats) == 0 {
		t.Skipf("TestPodNetworking_FixedIP requires aliyun/member-eni or aliyun/eni resource, but no nodes have them")
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

func createFixedIPTest(eniModeName string, eniType networkv1beta1.ENIAttachType) features.Feature {
	pnName := fmt.Sprintf("fixed-ip-%s", strings.ToLower(eniModeName))
	podName := fmt.Sprintf("fixed-ip-pod-%s", strings.ToLower(eniModeName))

	return features.New(fmt.Sprintf("PodNetworking/FixedIP/%s", eniModeName)).
		WithLabel("eni-type", string(eniType)).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pn := NewPodNetworking(pnName).
				WithPodSelector(&metav1.LabelSelector{
					MatchLabels: map[string]string{"netplan": pnName},
				}).
				WithENIAttachType(eniType).
				WithAllocationType(networkv1beta1.AllocationType{
					Type:            networkv1beta1.IPAllocTypeFixed,
					ReleaseStrategy: networkv1beta1.ReleaseStrategyTTL,
					ReleaseAfter:    "10m",
				})

			err := CreatePodNetworkingAndWaitReady(ctx, config.Client(), pn.PodNetworking)
			if err != nil {
				t.Fatalf("create PodNetworking failed: %v", err)
			}
			ctx = SaveResources(ctx, pn.PodNetworking)

			pod := NewPod(podName, config.Namespace()).
				WithLabels(map[string]string{"netplan": pnName}).
				WithContainer("nginx", nginxImage, nil)

			err = config.Client().Resources().Create(ctx, pod.Pod)
			if err != nil {
				t.Fatalf("create pod failed: %v", err)
			}
			ctx = SaveResources(ctx, pod.Pod)

			return ctx
		}).
		Assess("pod should get fixed IP", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: config.Namespace()}}

			err := wait.For(conditions.New(config.Client().Resources()).PodReady(pod),
				wait.WithTimeout(parsedTimeout))
			if err != nil {
				t.Fatalf("pod not ready: %v", err)
			}

			err = config.Client().Resources().Get(ctx, pod.Name, pod.Namespace, pod)
			if err != nil {
				t.Fatalf("get pod failed: %v", err)
			}
			originalIP := pod.Status.PodIP
			t.Logf("Pod original IP: %s (eniType=%s)", originalIP, eniType)

			return context.WithValue(ctx, "originalIP", originalIP)
		}).
		Assess("recreated pod should get same IP", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			originalIP := ctx.Value("originalIP").(string)

			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: config.Namespace()}}
			err := config.Client().Resources().Delete(ctx, pod)
			if err != nil {
				t.Fatalf("delete pod failed: %v", err)
			}

			err = wait.For(conditions.New(config.Client().Resources()).ResourceDeleted(pod),
				wait.WithTimeout(30*time.Second))
			if err != nil {
				t.Fatalf("wait for pod deletion failed: %v", err)
			}

			newPod := NewPod(podName, config.Namespace()).
				WithLabels(map[string]string{"netplan": pnName}).
				WithContainer("nginx", nginxImage, nil)

			err = config.Client().Resources().Create(ctx, newPod.Pod)
			if err != nil {
				t.Fatalf("recreate pod failed: %v", err)
			}

			err = wait.For(conditions.New(config.Client().Resources()).PodReady(newPod.Pod),
				wait.WithTimeout(parsedTimeout))
			if err != nil {
				t.Fatalf("recreated pod not ready: %v", err)
			}

			err = config.Client().Resources().Get(ctx, newPod.Name, newPod.Namespace, newPod.Pod)
			if err != nil {
				t.Fatalf("get recreated pod failed: %v", err)
			}

			if newPod.Status.PodIP != originalIP {
				t.Errorf("expected fixed IP %s, got %s (eniType=%s)", originalIP, newPod.Status.PodIP, eniType)
			} else {
				t.Logf("Fixed IP verified: %s (eniType=%s)", newPod.Status.PodIP, eniType)
			}

			return MarkTestSuccess(ctx)
		}).
		Feature()
}

// =============================================================================
// PodNetworking vSwitch and Security Group Tests
// =============================================================================

// TestPodNetworking_VSwitchAndSG tests vSwitch and security group configuration
// with both default and custom configurations.
func TestPodNetworking_VSwitchAndSG(t *testing.T) {
	if terway != "terway-eniip" {
		t.Skipf("TestPodNetworking_ExclusiveENIMode requires terway-eniip, but terway is %s", terway)
	}
	collectClusterEnvInfo(t)

	var feats []features.Feature

	// Test with default vSwitch and security groups
	defaultFeat := createVSwitchSGTest("Default", nil, nil)
	feats = append(feats, defaultFeat)

	// Test with custom vSwitch and security groups if provided
	if vSwitchIDs != "" && securityGroupIDs != "" {
		customVSwitchIDs := strings.Split(vSwitchIDs, ",")
		customSecurityGroupIDs := strings.Split(securityGroupIDs, ",")
		customFeat := createVSwitchSGTest("Custom", customVSwitchIDs, customSecurityGroupIDs)
		feats = append(feats, customFeat)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

func createVSwitchSGTest(configType string, vswitches, securityGroups []string) features.Feature {
	pnName := "vswitch-sg-" + strings.ToLower(configType)
	podName := "vswitch-sg-pod-" + strings.ToLower(configType)

	return features.New("PodNetworking/VSwitchAndSG/"+configType).
		WithLabel("env", "pod-networking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			eniInfo := checkENIResourcesBeforeTest(t)
			if !eniInfo.HasMemberENIResource() {
				t.Skipf("VSwitchAndSG test requires aliyun/member-eni resource, but no nodes have it (total: %d)", eniInfo.TotalMemberENI)
			}

			pn := NewPodNetworking(pnName).
				WithPodSelector(&metav1.LabelSelector{
					MatchLabels: map[string]string{"netplan": pnName},
				}).
				WithENIAttachType(networkv1beta1.ENIOptionTypeTrunk)

			if len(vswitches) > 0 {
				pn = pn.WithVSwitches(vswitches)
				t.Logf("Using custom vSwitches: %v", vswitches)
			}
			if len(securityGroups) > 0 {
				pn = pn.WithSecurityGroups(securityGroups)
				t.Logf("Using custom security groups: %v", securityGroups)
			}

			err := CreatePodNetworkingAndWaitReady(ctx, config.Client(), pn.PodNetworking)
			if err != nil {
				t.Fatalf("create PodNetworking failed: %v", err)
			}
			ctx = SaveResources(ctx, pn.PodNetworking)

			pod := NewPod(podName, config.Namespace()).
				WithLabels(map[string]string{"netplan": pnName}).
				WithContainer("nginx", nginxImage, nil)

			err = config.Client().Resources().Create(ctx, pod.Pod)
			if err != nil {
				t.Fatalf("create pod failed: %v", err)
			}
			ctx = SaveResources(ctx, pod.Pod)

			return ctx
		}).
		Assess("pod should be running with PodNetworking", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: config.Namespace()}}

			err := wait.For(conditions.New(config.Client().Resources()).PodReady(pod),
				wait.WithTimeout(parsedTimeout))
			if err != nil {
				t.Fatalf("pod not ready: %v", err)
			}

			err = config.Client().Resources().Get(ctx, pod.Name, pod.Namespace, pod)
			if err != nil {
				t.Fatalf("get pod failed: %v", err)
			}

			if err := ValidatePodHasPodNetworking(pod, pnName); err != nil {
				t.Fatalf("pod should use PodNetworking: %v", err)
			}
			t.Logf("Pod is running with PodNetworking %s (config: %s)", pnName, configType)

			return MarkTestSuccess(ctx)
		}).
		Feature()
}

// =============================================================================
// PodNetworking Connectivity Tests
// =============================================================================

// TestPodNetworking_Connectivity tests connectivity for PodNetworking pods (same-node, cross-node, and cross-zone)
// for both Trunk and Exclusive ENI modes.
func TestPodNetworking_Connectivity(t *testing.T) {
	if terway != "terway-eniip" {
		t.Skipf("TestPodNetworking_Connectivity requires terway-eniip, but terway is %s", terway)
	}
	envInfo := collectClusterEnvInfo(t)

	var feats []features.Feature

	// Test with Trunk mode if member-eni resource is available
	if envInfo.ENIInfo.HasMemberENIResource() {
		sameNodeTrunk := createPodNetworkingConnectivityTest("Trunk", networkv1beta1.ENIOptionTypeTrunk, "SameNode", "same-node")
		crossNodeTrunk := createPodNetworkingConnectivityTest("Trunk", networkv1beta1.ENIOptionTypeTrunk, "CrossNode", "cross-node")
		crossZoneTrunk := createPodNetworkingConnectivityTest("Trunk", networkv1beta1.ENIOptionTypeTrunk, "CrossZone", "cross-zone")
		feats = append(feats, sameNodeTrunk, crossNodeTrunk, crossZoneTrunk)
	} else {
		t.Logf("Skipping Trunk mode connectivity test: no aliyun/member-eni resource available")
	}

	// Test with Exclusive ENI mode if eni resource is available
	if envInfo.ENIInfo.HasExclusiveENIResource() {
		sameNodeExclusive := createPodNetworkingConnectivityTest("ExclusiveENI", networkv1beta1.ENIOptionTypeENI, "SameNode", "same-node")
		crossNodeExclusive := createPodNetworkingConnectivityTest("ExclusiveENI", networkv1beta1.ENIOptionTypeENI, "CrossNode", "cross-node")
		crossZoneExclusive := createPodNetworkingConnectivityTest("ExclusiveENI", networkv1beta1.ENIOptionTypeENI, "CrossZone", "cross-zone")
		feats = append(feats, sameNodeExclusive, crossNodeExclusive, crossZoneExclusive)
	} else {
		t.Logf("Skipping ExclusiveENI mode connectivity test: no aliyun/eni resource available")
	}

	if len(feats) == 0 {
		t.Skipf("TestPodNetworking_Connectivity requires aliyun/member-eni or aliyun/eni resource, but no nodes have them")
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

// affinityMode can be "same-node", "cross-node", or "cross-zone"
func createPodNetworkingConnectivityTest(eniModeName string, eniType networkv1beta1.ENIAttachType, scenario string, affinityMode string) features.Feature {
	pnName := fmt.Sprintf("connectivity-%s-%s", strings.ToLower(eniModeName), strings.ToLower(scenario))
	serverName := fmt.Sprintf("server-%s-%s", strings.ToLower(eniModeName), strings.ToLower(scenario))
	clientName := fmt.Sprintf("client-%s-%s", strings.ToLower(eniModeName), strings.ToLower(scenario))

	return features.New(fmt.Sprintf("PodNetworking/Connectivity/%s/%s", eniModeName, scenario)).
		WithLabel("eni-type", string(eniType)).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// For cross-zone, check if we have nodes in multiple zones
			if affinityMode == "cross-zone" {
				nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
				if err != nil {
					t.Fatalf("Failed to discover node types: %v", err)
				}

				// Check if any node type has multiple zones
				hasMultiZone := false
				for _, nodeType := range GetAllNodeTypes() {
					if nodeInfo.HasMultipleZones(nodeType) {
						hasMultiZone = true
						break
					}
				}
				if !hasMultiZone {
					t.Skipf("Need nodes in at least 2 different zones for cross-zone test")
				}
			}

			var objs []k8s.Object

			// Create PodNetworking
			pn := NewPodNetworking(pnName).
				WithPodSelector(&metav1.LabelSelector{
					MatchLabels: map[string]string{"netplan": pnName},
				}).
				WithENIAttachType(eniType)

			err := CreatePodNetworkingAndWaitReady(ctx, config.Client(), pn.PodNetworking)
			if err != nil {
				t.Fatalf("create PodNetworking failed: %v", err)
			}
			objs = append(objs, pn.PodNetworking)

			// Create server pod
			server := NewPod(serverName, config.Namespace()).
				WithLabels(map[string]string{
					"app":     "server",
					"netplan": pnName,
				}).
				WithContainer("server", nginxImage, nil)

			err = config.Client().Resources().Create(ctx, server.Pod)
			if err != nil {
				t.Fatalf("create server pod failed: %v", err)
			}
			objs = append(objs, server.Pod)

			// Create client pod with appropriate affinity
			client := NewPod(clientName, config.Namespace()).
				WithLabels(map[string]string{
					"app":     "client",
					"netplan": pnName,
				}).
				WithContainer("client", nginxImage, nil)

			switch affinityMode {
			case "same-node":
				client = client.WithPodAffinity(map[string]string{"app": "server", "netplan": pnName})
			case "cross-node":
				client = client.WithPodAntiAffinity(map[string]string{"app": "server", "netplan": pnName})
			case "cross-zone":
				client = client.WithPodAntiAffinityByZone(map[string]string{"app": "server", "netplan": pnName})
			}

			err = config.Client().Resources().Create(ctx, client.Pod)
			if err != nil {
				t.Fatalf("create client pod failed: %v", err)
			}
			objs = append(objs, client.Pod)

			// Create services
			for _, stack := range getStack() {
				svc := NewService(serverName+"-"+stack, config.Namespace(),
					map[string]string{"app": "server", "netplan": pnName}).
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
		Assess("pods should be ready and using PodNetworking", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
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
			err = config.Client().Resources().Get(ctx, clientName, config.Namespace(), client)
			if err != nil {
				t.Fatalf("get client pod failed: %v", err)
			}

			switch affinityMode {
			case "same-node":
				if server.Spec.NodeName != client.Spec.NodeName {
					t.Fatalf("expected pods on same node, but server on %s, client on %s",
						server.Spec.NodeName, client.Spec.NodeName)
				}
				t.Logf("Both pods are on same node: %s (eniType=%s)", server.Spec.NodeName, eniType)
			case "cross-node":
				if server.Spec.NodeName == client.Spec.NodeName {
					t.Fatalf("expected pods on different nodes, but both on %s", server.Spec.NodeName)
				}
				t.Logf("Pods on different nodes: server=%s, client=%s (eniType=%s)",
					server.Spec.NodeName, client.Spec.NodeName, eniType)
			case "cross-zone":
				if server.Spec.NodeName == client.Spec.NodeName {
					t.Fatalf("expected pods on different nodes, but both on %s", server.Spec.NodeName)
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
					t.Fatalf("expected pods in different zones, but both in %s", serverZone)
				}
				t.Logf("Pods in different zones: server=%s (zone=%s), client=%s (zone=%s) (eniType=%s)",
					server.Spec.NodeName, serverZone, client.Spec.NodeName, clientZone, eniType)
			}

			if err := ValidatePodHasPodNetworking(server, pnName); err != nil {
				t.Fatalf("server pod should use PodNetworking: %v", err)
			}
			if err := ValidatePodHasPodNetworking(client, pnName); err != nil {
				t.Fatalf("client pod should use PodNetworking: %v", err)
			}
			t.Logf("Both pods are using PodNetworking: %s", pnName)

			return ctx
		}).
		Assess("connectivity should work", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			for _, stack := range getStack() {
				serviceName := serverName + "-" + stack
				_, err := Pull(config.Client(), config.Namespace(), clientName, "client", serviceName, t)
				if err != nil {
					t.Errorf("connectivity failed for %s (%s/%s): %v", stack, eniModeName, scenario, err)
				}
			}
			return ctx
		}).
		Assess("hairpin connectivity should work", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			for _, stack := range getStack() {
				// Skip IPv6 hairpin test for ipvlan mode (known limitation)
				if stack == "ipv6" && ipvlan {
					t.Logf("Skipping IPv6 hairpin test for ipvlan mode")
					continue
				}
				serviceName := serverName + "-" + stack
				_, err := Pull(config.Client(), config.Namespace(), serverName, "server", serviceName, t)
				if err != nil {
					t.Errorf("hairpin connectivity failed for %s (%s/%s): %v", stack, eniModeName, scenario, err)
				}
			}
			return MarkTestSuccess(ctx)
		}).
		Feature()
}
