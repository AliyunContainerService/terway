//go:build e2e

package tests

import (
	"context"
	"fmt"
	"net"
	"strings"
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

type PodConfig struct {
	name    string
	podFunc func(pod *Pod) *Pod
}

// generatePodConfigs generates pod configurations with proper node affinity to avoid exclusive ENI nodes
func generatePodConfigs(testName string) []PodConfig {
	// Get node affinity exclude labels to avoid scheduling on exclusive ENI nodes
	nodeAffinityExclude := GetNodeAffinityExcludeForType(NodeTypeNormal)

	var mutateConfig []PodConfig
	if affinityLabel == "" {
		mutateConfig = []PodConfig{
			{
				name: "normal config",
				podFunc: func(pod *Pod) *Pod {
					return pod.WithNodeAffinityExclude(nodeAffinityExclude)
				},
			},
		}
		if testTrunk {
			mutateConfig = append(mutateConfig, PodConfig{
				name: "trunk pod",
				podFunc: func(pod *Pod) *Pod {
					return pod.WithLabels(map[string]string{"netplan": "default"}).WithNodeAffinityExclude(nodeAffinityExclude)
				},
			})
		}
	} else {
		labelArr := strings.Split(affinityLabel, ":")
		if len(labelArr) != 2 {
			// This will be handled by the calling function
			return nil
		}
		mutateConfig = []PodConfig{
			{
				name: fmt.Sprintf("normal_%s", labelArr[0]),
				podFunc: func(pod *Pod) *Pod {
					return pod.WithNodeAffinity(map[string]string{labelArr[0]: labelArr[1]}).WithNodeAffinityExclude(nodeAffinityExclude)
				},
			},
		}
		if testTrunk {
			mutateConfig = append(mutateConfig, PodConfig{
				name: fmt.Sprintf("trunk_%s", labelArr[0]),
				podFunc: func(pod *Pod) *Pod {
					return pod.WithLabels(map[string]string{"netplan": "default"}).WithNodeAffinity(map[string]string{labelArr[0]: labelArr[1]}).WithNodeAffinityExclude(nodeAffinityExclude)
				},
			})
		}
	}
	return mutateConfig
}

// TestNormal_HostNetworkConnectivity tests connectivity from host network pods to regular pods
// Note: Basic connectivity, hairpin, and cross-node tests have been migrated to connectivity_scenarios_test.go
func TestNormal_HostNetworkConnectivity(t *testing.T) {
	var feats []features.Feature

	type PodConfig struct {
		name    string
		podFunc func(pod *Pod) *Pod
	}
	mutateConfig := []PodConfig{}
	if affinityLabel == "" {
		mutateConfig = []PodConfig{{
			name: "normal config",
			podFunc: func(pod *Pod) *Pod {
				return pod
			},
		}}
	} else {
		labelArr := strings.Split(affinityLabel, ":")
		if len(labelArr) != 2 {
			t.Fatal("affinityLabel is not valid")
		}
		mutateConfig = []PodConfig{
			{
				name: fmt.Sprintf("normal_%s", labelArr[0]),
				podFunc: func(pod *Pod) *Pod {
					return pod.WithNodeAffinity(map[string]string{labelArr[0]: labelArr[1]})
				},
			},
		}
	}

	for i := range mutateConfig {
		name := mutateConfig[i].name
		fn := mutateConfig[i].podFunc

		// Host network to pod connectivity test (unique, not covered by new tests)
		hostToPodSameNode := features.New(fmt.Sprintf("PodConnective/hostToSameNode-%s", name)).
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				var objs []k8s.Object

				server := fn(NewPod("server", config.Namespace()).
					WithLabels(map[string]string{"app": "server"}).
					WithContainer("server", nginxImage, nil))

				err := config.Client().Resources().Create(ctx, server.Pod)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				client := fn(NewPod("client", config.Namespace()).
					WithLabels(map[string]string{"app": "client"}).
					WithContainer("client", nginxImage, nil)).
					WithPodAffinity(map[string]string{"app": "server"}).
					WithDNSPolicy(corev1.DNSClusterFirstWithHostNet).
					WithHostNetwork()

				err = config.Client().Resources().Create(ctx, client.Pod)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				objs = append(objs, server.Pod, client.Pod)

				for _, stack := range getStack() {
					svc := NewService("server-"+stack, config.Namespace(), map[string]string{"app": "server"}).
						ExposePort(80, "http").
						WithIPFamily(stack)

					err = config.Client().Resources().Create(ctx, svc.Service)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
					objs = append(objs, svc.Service)
				}
				ctx = SaveResources(ctx, objs...)

				return ctx
			}).
			Assess("Pod can access server", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				server := fn(NewPod("server", config.Namespace()).
					WithLabels(map[string]string{"app": "server"}).
					WithContainer("server", nginxImage, nil))

				client := fn(NewPod("client", config.Namespace()).
					WithLabels(map[string]string{"app": "client"}).
					WithContainer("client", nginxImage, nil)).
					WithPodAffinity(map[string]string{"app": "server"})

				err := wait.For(conditions.New(config.Client().Resources()).PodReady(client.Pod),
					wait.WithTimeout(parsedTimeout),
					wait.WithInterval(1*time.Second))
				if err != nil {
					t.Error(err)
					t.FailNow()
				}
				err = wait.For(conditions.New(config.Client().Resources()).PodReady(server.Pod),
					wait.WithTimeout(parsedTimeout),
					wait.WithInterval(1*time.Second))
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				for _, stack := range getStack() {
					_, err = Pull(config.Client(), client.Namespace, client.Name, "client", "server-"+stack, t)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
				}

				return MarkTestSuccess(ctx)
			}).
			Feature()

		feats = append(feats, hostToPodSameNode)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

func TestNormal_NetworkPolicy(t *testing.T) {
	if !testNetworkPolicy {
		t.Log("Skip networkPolicy tests")
		return
	}
	var feats []features.Feature

	mutateConfig := generatePodConfigs("NetworkPolicy")
	if mutateConfig == nil {
		labelArr := strings.Split(affinityLabel, ":")
		if len(labelArr) != 2 {
			t.Fatal("affinityLabel is not valid")
		}
	}
	for i := range mutateConfig {
		name := mutateConfig[i].name
		fn := mutateConfig[i].podFunc

		healthCheck := features.New(fmt.Sprintf("NetworkPolicy/PodHealthCheck-%s", name)).
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				var objs []k8s.Object

				policy := NewNetworkPolicy("default-deny-ingress", config.Namespace()).
					WithPolicyType(networkingv1.PolicyTypeIngress)

				server := fn(NewPod("server", config.Namespace()).
					WithLabels(map[string]string{"app": "server"}).
					WithContainer("server", nginxImage, nil).
					WithHealthCheck(80))

				objs = append(objs, policy.NetworkPolicy, server.Pod)

				for _, obj := range objs {
					err := config.Client().Resources().Create(ctx, obj)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
				}

				ctx = SaveResources(ctx, objs...)

				return ctx
			}).
			Assess("Pod should ready", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: config.Namespace()}}
				err := waitPodsReady(config.Client(), server)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				return MarkTestSuccess(ctx)
			}).Feature()

		denyIngressSameNode := features.New(fmt.Sprintf("NetworkPolicy/DenyIngressSameNode-%s", name)).
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				var objs []k8s.Object

				policy := NewNetworkPolicy("deny-ingress", config.Namespace()).
					WithPolicyType(networkingv1.PolicyTypeIngress).
					WithPodSelector(map[string]string{"app": "deny-ingress"})

				client := fn(NewPod("client", config.Namespace()).
					WithLabels(map[string]string{"app": "client"}).
					WithContainer("client", nginxImage, nil)).
					WithPodAffinity(map[string]string{"app": "server"})

				serverDenyIngress := fn(NewPod("server-deny-ingress", config.Namespace()).
					WithLabels(map[string]string{"app": "deny-ingress"}).
					WithContainer("server", nginxImage, nil))

				server := fn(NewPod("server", config.Namespace()).
					WithLabels(map[string]string{"app": "server"}).
					WithContainer("server", nginxImage, nil).
					WithPodAffinity(map[string]string{"app": "deny-ingress"}))

				objs = append(objs, policy.NetworkPolicy, client.Pod, serverDenyIngress.Pod, server.Pod)

				for _, stack := range getStack() {

					denySvc := NewService("server-deny-ingress-"+stack, config.Namespace(), map[string]string{"app": "deny-ingress"}).WithIPFamily(stack).ExposePort(80, "http")

					normalSvc := NewService("server-"+stack, config.Namespace(), map[string]string{"app": "server"}).WithIPFamily(stack).ExposePort(80, "http")

					objs = append(objs, denySvc.Service, normalSvc.Service)
				}

				for _, obj := range objs {
					err := config.Client().Resources().Create(ctx, obj)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
				}

				ctx = SaveResources(ctx, objs...)

				return ctx
			}).
			Assess("Check ingress policy", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "client", Namespace: config.Namespace()}}
				server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: config.Namespace()}}
				serverDenyIngress := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server-deny-ingress", Namespace: config.Namespace()}}
				err := waitPodsReady(config.Client(), client, server, serverDenyIngress)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				for _, stack := range getStack() {
					t.Logf("Testing %s: client should be able to access server (normal traffic, no policy applied)", stack)
					_, err = Pull(config.Client(), client.Namespace, client.Name, "client", "server-"+stack, t)
					if err != nil {
						t.Errorf("Expected: client can access server (%s), but failed: %v", stack, err)
						t.FailNow()
					}

					t.Logf("Testing %s: client should NOT be able to access server-deny-ingress (ingress policy denies traffic to deny-ingress pods)", stack)
					_, err = PullFail(config.Client(), client.Namespace, client.Name, "client", "server-deny-ingress-"+stack, t)
					if err != nil {
						t.Errorf("Expected: client cannot access server-deny-ingress (%s), but connection succeeded: %v", stack, err)
						t.FailNow()
					}
				}
				return MarkTestSuccess(ctx)
			}).Feature()

		denyIngressotherNode := features.New(fmt.Sprintf("NetworkPolicy/denyIngressotherNode-%s", name)).
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				var objs []k8s.Object

				policy := NewNetworkPolicy("deny-ingress-other", config.Namespace()).
					WithPolicyType(networkingv1.PolicyTypeIngress).
					WithPodSelector(map[string]string{"app": "deny-ingress-other"})

				client := fn(NewPod("client-other", config.Namespace()).
					WithLabels(map[string]string{"app": "client-other"}).
					WithContainer("client", nginxImage, nil)).
					WithPodAntiAffinity(map[string]string{"app": "server-other", "applabel": "deny-ingress-other"})

				serverDenyIngress := fn(NewPod("server-deny-ingress-other", config.Namespace()).
					WithLabels(map[string]string{"applabel": "deny-ingress-other"}).
					WithContainer("server", nginxImage, nil))

				server := fn(NewPod("server-other", config.Namespace()).
					WithLabels(map[string]string{"app": "server-other"}).
					WithContainer("server", nginxImage, nil).
					WithPodAffinity(map[string]string{"applabel": "deny-ingress-other"}))

				objs = append(objs, policy.NetworkPolicy, client.Pod, serverDenyIngress.Pod, server.Pod)

				for _, stack := range getStack() {

					denySvc := NewService("server-deny-ingress-"+stack, config.Namespace(), map[string]string{"app": "deny-ingress-other"}).WithIPFamily(stack).ExposePort(80, "http")

					normalSvc := NewService("server-"+stack, config.Namespace(), map[string]string{"app": "server-other"}).WithIPFamily(stack).ExposePort(80, "http")

					objs = append(objs, denySvc.Service, normalSvc.Service)
				}

				for _, obj := range objs {
					err := config.Client().Resources().Create(ctx, obj)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
				}

				ctx = SaveResources(ctx, objs...)

				return ctx
			}).
			Assess("Check ingress policy", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "client-other", Namespace: config.Namespace()}}
				server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server-other", Namespace: config.Namespace()}}
				serverDenyIngress := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server-deny-ingress-other", Namespace: config.Namespace()}}
				err := waitPodsReady(config.Client(), client, server, serverDenyIngress)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				for _, stack := range getStack() {
					t.Logf("Testing %s: client-other should be able to access server-other (normal traffic, no policy applied)", stack)
					_, err = Pull(config.Client(), client.Namespace, client.Name, "client", "server-"+stack, t)
					if err != nil {
						t.Errorf("Expected: client-other can access server-other (%s), but failed: %v", stack, err)
						t.FailNow()
					}

					t.Logf("Testing %s: client-other should NOT be able to access server-deny-ingress-other (ingress policy denies traffic to deny-ingress-other pods)", stack)
					_, err = PullFail(config.Client(), client.Namespace, client.Name, "client", "server-deny-ingress-"+stack, t)
					if err != nil {
						t.Errorf("Expected: client-other cannot access server-deny-ingress-other (%s), but connection succeeded: %v", stack, err)
						t.FailNow()
					}
				}
				return MarkTestSuccess(ctx)
			}).Feature()

		denyEgressSameNode := features.New(fmt.Sprintf("NetworkPolicy/denyEgressSameNode-%s", name)).
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				var objs []k8s.Object

				policy := NewNetworkPolicy("deny-egress", config.Namespace()).
					WithPolicyType(networkingv1.PolicyTypeEgress).
					WithPodSelector(map[string]string{"app": "client"})

				client := fn(NewPod("client", config.Namespace()).
					WithLabels(map[string]string{"app": "client"}).
					WithContainer("client", nginxImage, nil)).
					WithPodAffinity(map[string]string{"app": "server"})

				server := fn(NewPod("server", config.Namespace()).
					WithLabels(map[string]string{"app": "server"}).
					WithContainer("server", nginxImage, nil))

				objs = append(objs, policy.NetworkPolicy, client.Pod, server.Pod)

				for _, stack := range getStack() {

					normalSvc := NewService("server-"+stack, config.Namespace(), map[string]string{"app": "server"}).WithIPFamily(stack).ExposePort(80, "http")

					objs = append(objs, normalSvc.Service)
				}

				for _, obj := range objs {
					err := config.Client().Resources().Create(ctx, obj)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
				}

				ctx = SaveResources(ctx, objs...)
				return ctx
			}).
			Assess("Check egress policy", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "client", Namespace: config.Namespace()}}
				server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: config.Namespace()}}
				err := waitPodsReady(config.Client(), client, server)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				for _, stack := range getStack() {
					t.Logf("Testing %s: client should NOT be able to access server (egress policy denies all outbound traffic from client)", stack)
					_, err = PullFail(config.Client(), client.Namespace, client.Name, "client", "server-"+stack, t)
					if err != nil {
						t.Errorf("Expected: client cannot access server (%s), but connection succeeded: %v", stack, err)
						t.FailNow()
					}
				}
				return MarkTestSuccess(ctx)
			}).Feature()

		denyEgressOtherNode := features.New(fmt.Sprintf("NetworkPolicy/denyEgressOtherNode-%s", name)).
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				var objs []k8s.Object

				policy := NewNetworkPolicy("deny-egress", config.Namespace()).
					WithPolicyType(networkingv1.PolicyTypeEgress).
					WithPodSelector(map[string]string{"app": "client"})

				client := fn(NewPod("client", config.Namespace()).
					WithLabels(map[string]string{"app": "client"}).
					WithContainer("client", nginxImage, nil)).
					WithPodAntiAffinity(map[string]string{"app": "server"})

				server := fn(NewPod("server", config.Namespace()).
					WithLabels(map[string]string{"app": "server"}).
					WithContainer("server", nginxImage, nil))

				objs = append(objs, policy.NetworkPolicy, client.Pod, server.Pod)

				for _, stack := range getStack() {

					normalSvc := NewService("server-"+stack, config.Namespace(), map[string]string{"app": "server"}).WithIPFamily(stack).ExposePort(80, "http")

					objs = append(objs, normalSvc.Service)
				}

				for _, obj := range objs {
					err := config.Client().Resources().Create(ctx, obj)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
				}

				ctx = SaveResources(ctx, objs...)
				return ctx
			}).
			Assess("Check egress policy", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "client", Namespace: config.Namespace()}}
				server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: config.Namespace()}}
				err := waitPodsReady(config.Client(), client, server)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				for _, stack := range getStack() {
					t.Logf("Testing %s: client should NOT be able to access server (egress policy denies all outbound traffic from client)", stack)
					_, err = PullFail(config.Client(), client.Namespace, client.Name, "client", "server-"+stack, t)
					if err != nil {
						t.Errorf("Expected: client cannot access server (%s), but connection succeeded: %v", stack, err)
						t.FailNow()
					}
				}
				return MarkTestSuccess(ctx)
			}).Feature()

		feats = append(feats, healthCheck, denyIngressSameNode, denyIngressotherNode, denyEgressSameNode, denyEgressOtherNode)
	}

	testenv.Test(t, feats...)
	if t.Failed() {
		isFailed.Store(true)
	}
}

func TestNormal_HostPort(t *testing.T) {
	var feats []features.Feature

	mutateConfig := generatePodConfigs("HostPort")
	if mutateConfig == nil {
		labelArr := strings.Split(affinityLabel, ":")
		if len(labelArr) != 2 {
			t.Fatal("affinityLabel is not valid")
		}
	}

	for i := range mutateConfig {
		name := mutateConfig[i].name
		fn := mutateConfig[i].podFunc

		// Case 1: Node access node IP + port
		hostPortNodeIP := features.New(fmt.Sprintf("HostPort/NodeIP-%s", name)).
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				var objs []k8s.Object

				// Create server pod with hostPort
				server := fn(NewPod("server-hostport", config.Namespace()).
					WithLabels(map[string]string{"app": "server-hostport"}).
					WithContainer("server", nginxImage, nil).
					WithHostPort(80, 8080))

				err := config.Client().Resources().Create(ctx, server.Pod)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				// Create client pod on different node
				client := fn(NewPod("client-hostport", config.Namespace()).
					WithLabels(map[string]string{"app": "client-hostport"}).
					WithContainer("client", nginxImage, nil)).
					WithPodAntiAffinity(map[string]string{"app": "server-hostport"})

				err = config.Client().Resources().Create(ctx, client.Pod)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				objs = append(objs, server.Pod, client.Pod)
				ctx = SaveResources(ctx, objs...)
				return ctx
			}).
			Assess("Client can access server via node IP + hostPort", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server-hostport", Namespace: config.Namespace()}}
				client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "client-hostport", Namespace: config.Namespace()}}

				// Wait for pods to be ready
				err := waitPodsReady(config.Client(), server, client)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				// Get server pod to find its node
				err = config.Client().Resources().Get(ctx, "server-hostport", config.Namespace(), server)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				// Get node to find its IPs
				node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: server.Spec.NodeName}}
				err = config.Client().Resources().Get(ctx, server.Spec.NodeName, "", node)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				// Test for each IP stack
				for _, stack := range getStack() {
					isIPv6 := stack == "ipv6"

					internalIP, _ := getNodeIPs(node, isIPv6)
					if internalIP == "" {
						t.Logf("No internal %s address found, skipping %s test", stack, stack)
						continue
					}

					// Test connectivity via node IP + hostPort using net.JoinHostPort
					target := net.JoinHostPort(internalIP, "8080")
					t.Logf("Testing %s connectivity to %s", stack, target)
					if isIPv6 {
						_, err = PullWithIPv6(config.Client(), client.Namespace, client.Name, "client", target, t)
					} else {
						_, err = Pull(config.Client(), client.Namespace, client.Name, "client", target, t)
					}
					if err != nil {
						t.Errorf("Failed to connect via %s: %v", stack, err)
						t.FailNow()
					}
				}

				return MarkTestSuccess(ctx)
			}).
			Feature()

		// Case 2: Node access node external IP + port
		hostPortExternalIP := features.New(fmt.Sprintf("HostPort/ExternalIP-%s", name)).
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				var objs []k8s.Object

				// Create server pod with hostPort on different port
				server := fn(NewPod("server-hostport-ext", config.Namespace()).
					WithLabels(map[string]string{"app": "server-hostport-ext"}).
					WithContainer("server", nginxImage, nil).
					WithHostPort(80, 8081))

				err := config.Client().Resources().Create(ctx, server.Pod)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				// Create client pod on different node
				client := fn(NewPod("client-hostport-ext", config.Namespace()).
					WithLabels(map[string]string{"app": "client-hostport-ext"}).
					WithContainer("client", nginxImage, nil)).
					WithPodAntiAffinity(map[string]string{"app": "server-hostport-ext"})

				err = config.Client().Resources().Create(ctx, client.Pod)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				objs = append(objs, server.Pod, client.Pod)
				ctx = SaveResources(ctx, objs...)
				return ctx
			}).
			Assess("Client can access server via node external IP + hostPort", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server-hostport-ext", Namespace: config.Namespace()}}
				client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "client-hostport-ext", Namespace: config.Namespace()}}

				// Wait for pods to be ready
				err := waitPodsReady(config.Client(), server, client)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				// Get server pod to find its node
				err = config.Client().Resources().Get(ctx, "server-hostport-ext", config.Namespace(), server)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				// Get node to find its external IPs
				node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: server.Spec.NodeName}}
				err = config.Client().Resources().Get(ctx, server.Spec.NodeName, "", node)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				// Test for each IP stack
				for _, stack := range getStack() {
					isIPv6 := stack == "ipv6"

					_, externalIP := getNodeIPs(node, isIPv6)
					if externalIP == "" {
						t.Logf("No external %s address found, skipping %s test", stack, stack)
						continue
					}

					// Test connectivity via node external IP + hostPort using net.JoinHostPort
					target := net.JoinHostPort(externalIP, "8081")
					t.Logf("Testing %s connectivity to external IP %s", stack, target)
					if isIPv6 {
						_, err = PullWithIPv6(config.Client(), client.Namespace, client.Name, "client", target, t)
					} else {
						_, err = Pull(config.Client(), client.Namespace, client.Name, "client", target, t)
					}
					if err != nil {
						t.Errorf("Failed to connect via %s: %v", stack, err)
						t.FailNow()
					}
				}

				return MarkTestSuccess(ctx)
			}).
			Feature()

		feats = append(feats, hostPortNodeIP, hostPortExternalIP)
	}

	testenv.Test(t, feats...)
	if t.Failed() {
		isFailed.Store(true)
	}
}
