//go:build e2e

package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	terwayTypes "github.com/AliyunContainerService/terway/types"
)

// TestPodNetworking_TrunkMode tests Trunk ENI attach type
func TestPodNetworking_TrunkMode(t *testing.T) {
	config := DefaultPodNetworkingTestConfig().
		WithENIAttachType(networkv1beta1.ENIOptionTypeTrunk).
		WithExpectedENIType(networkv1beta1.ENITypeMember)

	RunPodNetworkingTestSuite(t, config)
}

// TestPodNetworking_ExclusiveENIMode tests Exclusive ENI attach type
func TestPodNetworking_ExclusiveENIMode(t *testing.T) {
	config := DefaultPodNetworkingTestConfig().
		WithENIAttachType(networkv1beta1.ENIOptionTypeENI).
		WithExpectedENIType(networkv1beta1.ENITypeSecondary)

	RunPodNetworkingTestSuite(t, config)
}

// TestPodNetworking_DefaultMode tests Default ENI attach type (auto-selection)
func TestPodNetworking_DefaultMode(t *testing.T) {
	config := DefaultPodNetworkingTestConfig().
		WithENIAttachType(networkv1beta1.ENIOptionTypeDefault)
	// No expected ENI type for default mode as it auto-selects

	RunPodNetworkingTestSuite(t, config)
}

// TestPodNetworking_PodSelector tests pod selector matching
func TestPodNetworking_PodSelector(t *testing.T) {
	pnName := "pod-selector-pn"

	feature := features.New("PodNetworking/PodSelector").
		WithLabel("env", "pod-networking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Create PodNetworking with podSelector
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

			// Create pod WITH matching label
			matchingPod := NewPod("matching-pod", config.Namespace()).
				WithLabels(map[string]string{"network": "custom"}).
				WithContainer("nginx", nginxImage, nil)

			err = config.Client().Resources().Create(ctx, matchingPod.Pod)
			if err != nil {
				t.Fatalf("create matching pod failed: %v", err)
			}

			// Create pod WITHOUT matching label
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

			// Refresh pod to get latest annotations
			err = config.Client().Resources().Get(ctx, matchingPod.Name, matchingPod.Namespace, matchingPod)
			if err != nil {
				t.Fatalf("get matching pod failed: %v", err)
			}

			// Validate it has the PodNetworking annotation
			if err := ValidatePodHasPodNetworking(matchingPod, pnName); err != nil {
				t.Fatalf("matching pod should use PodNetworking: %v", err)
			}
			t.Logf("✓ Pod with matching selector uses PodNetworking %s", pnName)

			return ctx
		}).
		Assess("non-matching pod should NOT use PodNetworking", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nonMatchingPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "non-matching-pod", Namespace: config.Namespace()}}

			err := wait.For(conditions.New(config.Client().Resources()).PodReady(nonMatchingPod),
				wait.WithTimeout(parsedTimeout))
			if err != nil {
				t.Fatalf("non-matching pod not ready: %v", err)
			}

			// Refresh pod to get latest annotations
			err = config.Client().Resources().Get(ctx, nonMatchingPod.Name, nonMatchingPod.Namespace, nonMatchingPod)
			if err != nil {
				t.Fatalf("get non-matching pod failed: %v", err)
			}

			// Validate it does NOT have the PodNetworking annotation
			if nonMatchingPod.Annotations[terwayTypes.PodNetworking] == pnName {
				t.Fatalf("non-matching pod should NOT use PodNetworking %s", pnName)
			}
			t.Logf("✓ Pod without matching selector does NOT use PodNetworking %s", pnName)

			return MarkTestSuccess(ctx)
		}).
		Feature()

	testenv.Test(t, feature)
}

// TestPodNetworking_NamespaceSelector tests namespace selector matching
func TestPodNetworking_NamespaceSelector(t *testing.T) {
	pnName := "namespace-selector-pn"
	matchingNs := "matching-namespace"
	nonMatchingNs := "non-matching-namespace"

	feature := features.New("PodNetworking/NamespaceSelector").
		WithLabel("env", "pod-networking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Create PodNetworking with namespaceSelector
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

			// Create namespace WITH matching label
			matchingNsObj := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   matchingNs,
					Labels: map[string]string{"network-type": "custom"},
				},
			}
			err = config.Client().Resources().Create(ctx, matchingNsObj)
			if err != nil {
				t.Fatalf("create matching namespace failed: %v", err)
			}

			// Create namespace WITHOUT matching label
			nonMatchingNsObj := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   nonMatchingNs,
					Labels: map[string]string{"network-type": "default"},
				},
			}
			err = config.Client().Resources().Create(ctx, nonMatchingNsObj)
			if err != nil {
				t.Fatalf("create non-matching namespace failed: %v", err)
			}

			// Create pod in matching namespace
			matchingPod := NewPod("pod-in-matching-ns", matchingNs).
				WithContainer("nginx", nginxImage, nil)
			err = config.Client().Resources().Create(ctx, matchingPod.Pod)
			if err != nil {
				t.Fatalf("create pod in matching namespace failed: %v", err)
			}

			// Create pod in non-matching namespace
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

			// Refresh pod to get latest annotations
			err = config.Client().Resources().Get(ctx, matchingPod.Name, matchingPod.Namespace, matchingPod)
			if err != nil {
				t.Fatalf("get pod in matching namespace failed: %v", err)
			}

			// Validate it has the PodNetworking annotation
			if err := ValidatePodHasPodNetworking(matchingPod, pnName); err != nil {
				t.Fatalf("pod in matching namespace should use PodNetworking: %v", err)
			}
			t.Logf("✓ Pod in namespace with matching selector uses PodNetworking %s", pnName)

			return ctx
		}).
		Assess("pod in non-matching namespace should NOT use PodNetworking", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			nonMatchingPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-in-non-matching-ns", Namespace: nonMatchingNs}}

			err := wait.For(conditions.New(config.Client().Resources()).PodReady(nonMatchingPod),
				wait.WithTimeout(parsedTimeout))
			if err != nil {
				t.Fatalf("pod in non-matching namespace not ready: %v", err)
			}

			// Refresh pod to get latest annotations
			err = config.Client().Resources().Get(ctx, nonMatchingPod.Name, nonMatchingPod.Namespace, nonMatchingPod)
			if err != nil {
				t.Fatalf("get pod in non-matching namespace failed: %v", err)
			}

			// Validate it does NOT have the PodNetworking annotation
			if nonMatchingPod.Annotations[terwayTypes.PodNetworking] == pnName {
				t.Fatalf("pod in non-matching namespace should NOT use PodNetworking %s", pnName)
			}
			t.Logf("✓ Pod in namespace without matching selector does NOT use PodNetworking %s", pnName)

			return MarkTestSuccess(ctx)
		}).
		Feature()

	testenv.Test(t, feature)
}

// TestPodNetworking_FixedIP tests fixed IP allocation
func TestPodNetworking_FixedIP(t *testing.T) {
	pnName := "fixed-ip-pn"
	podName := "fixed-ip-pod"

	feature := features.New("PodNetworking/FixedIP").
		WithLabel("env", "pod-networking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			// Create PodNetworking with fixed IP allocation
			pn := NewPodNetworking(pnName).
				WithPodSelector(&metav1.LabelSelector{
					MatchLabels: map[string]string{"netplan": pnName},
				}).
				WithENIAttachType(networkv1beta1.ENIOptionTypeTrunk).
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

			// Create pod
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

			// Get and save the IP
			err = config.Client().Resources().Get(ctx, pod.Name, pod.Namespace, pod)
			if err != nil {
				t.Fatalf("get pod failed: %v", err)
			}
			originalIP := pod.Status.PodIP
			t.Logf("Pod original IP: %s", originalIP)

			// Store IP in context for next phase
			return context.WithValue(ctx, "originalIP", originalIP)
		}).
		Assess("recreated pod should get same IP", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			originalIP := ctx.Value("originalIP").(string)

			// Delete pod
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: config.Namespace()}}
			err := config.Client().Resources().Delete(ctx, pod)
			if err != nil {
				t.Fatalf("delete pod failed: %v", err)
			}

			// Wait for deletion
			err = wait.For(conditions.New(config.Client().Resources()).ResourceDeleted(pod),
				wait.WithTimeout(30*time.Second))
			if err != nil {
				t.Fatalf("wait for pod deletion failed: %v", err)
			}

			// Recreate pod with same name and labels
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

			// Refresh to get the IP
			err = config.Client().Resources().Get(ctx, newPod.Name, newPod.Namespace, newPod.Pod)
			if err != nil {
				t.Fatalf("get recreated pod failed: %v", err)
			}

			// Verify IP is the same
			if newPod.Status.PodIP != originalIP {
				t.Errorf("expected fixed IP %s, got %s", originalIP, newPod.Status.PodIP)
			} else {
				t.Logf("✓ Fixed IP verified: %s", newPod.Status.PodIP)
			}

			return MarkTestSuccess(ctx)
		}).
		Feature()

	testenv.Test(t, feature)
}

// TestPodNetworking_VSwitchAndSG_Default tests using cluster default vSwitch and security groups
func TestPodNetworking_VSwitchAndSG_Default(t *testing.T) {
	config := DefaultPodNetworkingTestConfig().
		WithENIAttachType(networkv1beta1.ENIOptionTypeTrunk).
		WithoutConnectivity() // This test focuses on vSwitch validation, not connectivity

	RunPodNetworkingTestSuite(t, config)
}

// TestPodNetworking_VSwitchAndSG_Custom tests using custom vSwitch and security groups
func TestPodNetworking_VSwitchAndSG_Custom(t *testing.T) {
	// Skip if no custom vSwitch and security group provided
	if vSwitchIDs == "" || securityGroupIDs == "" {
		t.Skip("Skip custom vSwitch/SG test: vSwitchIDs and securityGroupIDs are required")
	}

	customVSwitchIDs := strings.Split(vSwitchIDs, ",")
	customSecurityGroupIDs := strings.Split(securityGroupIDs, ",")

	config := DefaultPodNetworkingTestConfig().
		WithENIAttachType(networkv1beta1.ENIOptionTypeTrunk).
		WithVSwitches(customVSwitchIDs).
		WithSecurityGroups(customSecurityGroupIDs).
		WithoutConnectivity() // This test focuses on custom vSwitch/SG validation

	RunPodNetworkingTestSuite(t, config)
}
