//go:build e2e

package tests

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

var resourceKey struct{}

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

func TestConnective(t *testing.T) {
	var feats []features.Feature

	mutateConfig := []struct {
		name    string
		podFunc func(pod *Pod) *Pod
	}{
		{
			name: "normal config",
			podFunc: func(pod *Pod) *Pod {
				return pod
			},
		},
		{
			name: "alinux2 node",
			podFunc: func(pod *Pod) *Pod {
				return pod.WithNodeAffinity(map[string]string{"e2e-os": "alinux2"})
			},
		},
		{
			name: "alinux3 node",
			podFunc: func(pod *Pod) *Pod {
				return pod.WithNodeAffinity(map[string]string{"e2e-os": "alinux3"})
			},
		},
		{
			name: "trunk pod",
			podFunc: func(pod *Pod) *Pod {
				return pod.WithLabels(map[string]string{"trunk": "enable"})
			},
		},
		{
			name: "trunk pod alinux2",
			podFunc: func(pod *Pod) *Pod {
				return pod.WithLabels(map[string]string{"trunk": "enable"}).WithNodeAffinity(map[string]string{"e2e-os": "alinux2"})
			},
		},
		{
			name: "trunk pod alinux3",
			podFunc: func(pod *Pod) *Pod {
				return pod.WithLabels(map[string]string{"trunk": "enable"}).WithNodeAffinity(map[string]string{"e2e-os": "alinux3"})
			},
		},
	}

	for i := range mutateConfig {
		name := mutateConfig[i].name
		fn := mutateConfig[i].podFunc

		hairpin := features.New(fmt.Sprintf("PodConnective/hairpin-%s", name)).
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

				objs = append(objs, server.Pod)

				for _, stack := range getStack() {
					svc := NewService("server-"+stack, config.Namespace(), map[string]string{"app": "server"}).
						ExposePort(80, "http").WithIPFamily(stack)
					err = config.Client().Resources().Create(ctx, svc.Service)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
					objs = append(objs, svc.Service)
				}
				ctx = context.WithValue(ctx, resourceKey, objs)
				return ctx
			}).
			Assess("Pod can access own service", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				server := fn(NewPod("server", config.Namespace()).
					WithLabels(map[string]string{"app": "server"}).
					WithContainer("server", nginxImage, nil))

				err := wait.For(conditions.New(config.Client().Resources()).PodReady(server.Pod),
					wait.WithTimeout(parsedTimeout),
					wait.WithInterval(1*time.Second))
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				for _, stack := range getStack() {
					// https://github.com/cilium/cilium/issues/13891 cilium not support ipv6 hairpin
					if stack == "ipv6" && ipvlan {
						continue
					}

					err = pull(config.Client(), server.Namespace, server.Name, "server", "server-"+stack)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
				}

				return ctx
			}).
			Feature()

		podSameNode := features.New(fmt.Sprintf("PodConnective/podSameNode-%s", name)).
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
					WithPodAffinity(map[string]string{"app": "server"})

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
				ctx = context.WithValue(ctx, resourceKey, objs)

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
					err = pull(config.Client(), client.Namespace, client.Name, "client", "server-"+stack)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
				}

				return ctx
			}).
			Feature()

		podDifferentNode := features.New(fmt.Sprintf("PodConnective/podDifferentNode-%s", name)).
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
					WithPodAntiAffinity(map[string]string{"app": "server"})

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
				ctx = context.WithValue(ctx, resourceKey, objs)

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
					err = pull(config.Client(), client.Namespace, client.Name, "client", "server-"+stack)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
				}

				return ctx
			}).
			Feature()

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
				ctx = context.WithValue(ctx, resourceKey, objs)

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
					err = pull(config.Client(), client.Namespace, client.Name, "client", "server-"+stack)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
				}

				return ctx
			}).
			Feature()

		feats = append(feats, hairpin, podSameNode, podDifferentNode, hostToPodSameNode)
	}

	testenv.Test(t, feats...)

	if t.Failed() {
		isFailed.Store(true)
	}
}

func TestNetworkPolicy(t *testing.T) {
	if !testNetworkPolicy {
		t.Log("Skip networkPolicy tests")
		return
	}
	var feats []features.Feature

	mutateConfig := []struct {
		name    string
		podFunc func(pod *Pod) *Pod
	}{
		{
			name: "normal config",
			podFunc: func(pod *Pod) *Pod {
				return pod
			},
		},
		{
			name: "alinux2 node",
			podFunc: func(pod *Pod) *Pod {
				return pod.WithNodeAffinity(map[string]string{"e2e-os": "alinux2"})
			},
		},
		{
			name: "alinux3 node",
			podFunc: func(pod *Pod) *Pod {
				return pod.WithNodeAffinity(map[string]string{"e2e-os": "alinux3"})
			},
		},
		{
			name: "trunk pod",
			podFunc: func(pod *Pod) *Pod {
				return pod.WithLabels(map[string]string{"trunk": "enable"})
			},
		},
		{
			name: "trunk pod alinux2",
			podFunc: func(pod *Pod) *Pod {
				return pod.WithLabels(map[string]string{"trunk": "enable"}).WithNodeAffinity(map[string]string{"e2e-os": "alinux2"})
			},
		},
		{
			name: "trunk pod alinux3",
			podFunc: func(pod *Pod) *Pod {
				return pod.WithLabels(map[string]string{"trunk": "enable"}).WithNodeAffinity(map[string]string{"e2e-os": "alinux3"})
			},
		},
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

				ctx = context.WithValue(ctx, resourceKey, objs)

				return ctx
			}).
			Assess("Pod should ready", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: config.Namespace()}}
				err := waitPodsReady(config.Client(), server)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				return ctx
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

				ctx = context.WithValue(ctx, resourceKey, objs)

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
					err = pull(config.Client(), client.Namespace, client.Name, "client", "server-"+stack)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}

					err = pullFail(config.Client(), client.Namespace, client.Name, "client", "server-deny-ingress-"+stack)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
				}
				return ctx
			}).Feature()

		denyIngressotherNode := features.New(fmt.Sprintf("NetworkPolicy/denyIngressotherNode-%s", name)).
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				var objs []k8s.Object

				policy := NewNetworkPolicy("deny-ingress", config.Namespace()).
					WithPolicyType(networkingv1.PolicyTypeIngress).
					WithPodSelector(map[string]string{"app": "deny-ingress"})

				client := fn(NewPod("client", config.Namespace()).
					WithLabels(map[string]string{"app": "client"}).
					WithContainer("client", nginxImage, nil)).
					WithPodAntiAffinity(map[string]string{"app": "server"})

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

				ctx = context.WithValue(ctx, resourceKey, objs)

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
					err = pull(config.Client(), client.Namespace, client.Name, "client", "server-"+stack)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}

					err = pullFail(config.Client(), client.Namespace, client.Name, "client", "server-deny-ingress-"+stack)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
				}
				return ctx
			}).Feature()

		denyEgressSameNode := features.New(fmt.Sprintf("NetworkPolicy/denyEgressSameNode-%s", name)).
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				var objs []k8s.Object

				policy := NewNetworkPolicy("deny-ingress", config.Namespace()).
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

				ctx = context.WithValue(ctx, resourceKey, objs)

				return ctx
			}).
			Assess("Check ingress policy", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "client", Namespace: config.Namespace()}}
				server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: config.Namespace()}}
				err := waitPodsReady(config.Client(), client, server)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				for _, stack := range getStack() {
					err = pullFail(config.Client(), client.Namespace, client.Name, "client", "server-"+stack)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
				}
				return ctx
			}).Feature()

		denyEgressOtherNode := features.New(fmt.Sprintf("NetworkPolicy/denyEgressOtherNode-%s", name)).
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				var objs []k8s.Object

				policy := NewNetworkPolicy("deny-ingress", config.Namespace()).
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

				ctx = context.WithValue(ctx, resourceKey, objs)

				return ctx
			}).
			Assess("Check ingress policy", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				client := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "client", Namespace: config.Namespace()}}
				server := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: config.Namespace()}}
				err := waitPodsReady(config.Client(), client, server)
				if err != nil {
					t.Error(err)
					t.FailNow()
				}

				for _, stack := range getStack() {
					err = pullFail(config.Client(), client.Namespace, client.Name, "client", "server-"+stack)
					if err != nil {
						t.Error(err)
						t.FailNow()
					}
				}
				return ctx
			}).Feature()

		feats = append(feats, healthCheck, denyIngressSameNode, denyIngressotherNode, denyEgressSameNode, denyEgressOtherNode)
	}

	testenv.Test(t, feats...)
	if t.Failed() {
		isFailed.Store(true)
	}
}

func pull(client klient.Client, namespace, name, container, target string) error {
	errors := []error{}

	err := wait.For(func(ctx context.Context) (done bool, err error) {
		var stdout, stderr bytes.Buffer
		cmd := []string{"curl", "-m", "2", "--retry", "3", "-I", target}
		err = client.Resources().ExecInPod(ctx, namespace, name, container, cmd, &stdout, &stderr)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed %s %w", cmd, err))
			return false, nil
		}
		httpStatus := strings.Split(stdout.String(), "\n")[0]
		if !strings.Contains(httpStatus, "200") {
			return false, nil
		}
		return true, nil
	},
		wait.WithTimeout(parsedTimeout),
		wait.WithInterval(1*time.Second))

	if err != nil {
		return utilerrors.NewAggregate(errors)
	}
	return nil
}

func pullFail(client klient.Client, namespace, name, container, target string) error {
	errors := []error{}

	err := wait.For(func(ctx context.Context) (done bool, err error) {
		var stdout, stderr bytes.Buffer
		cmd := []string{"curl", "-m", "2", "--retry", "3", "-I", target}

		err = client.Resources().ExecInPod(ctx, namespace, name, container, cmd, &stdout, &stderr)
		if err != nil {
			return true, nil
		}

		errors = append(errors, fmt.Errorf("connect success, expect fail %s %s", cmd, stdout.String()))

		return false, nil
	},
		wait.WithTimeout(7*time.Second),
		wait.WithInterval(1*time.Second))

	if err != nil {
		return utilerrors.NewAggregate(errors)
	}
	return nil
}

func waitPodsReady(client klient.Client, pods ...*corev1.Pod) error {
	for _, pod := range pods {
		err := wait.For(conditions.New(client.Resources()).PodReady(pod),
			wait.WithTimeout(parsedTimeout),
			wait.WithInterval(1*time.Second))
		if err != nil {
			return err
		}
	}
	return nil
}
