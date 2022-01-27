//go:build e2e
// +build e2e

package trunk

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	terwayTypes "github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

func TestDefaultConfigPodNetworking(t *testing.T) {
	defaultConfig := features.New("PodNetworking/DefaultConfig").WithLabel("env", "trunking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pn := newPodNetworking(pnName, nil, nil, &metav1.LabelSelector{
				MatchLabels: map[string]string{"trunking-pod": "true"},
			}, nil)
			if err := config.Client().Resources().Create(ctx, pn); err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Assess("with default config", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			var pn v1beta1.PodNetworking
			err := config.Client().Resources().Get(ctx, pnName, config.Namespace(), &pn)
			if err != nil {
				t.Fatal(err)
			}
			if len(pn.Spec.VSwitchOptions) == 0 {
				t.Errorf("vSwitchOptions not set")
			}
			if len(pn.Spec.SecurityGroupIDs) == 0 {
				t.Errorf("securityGroupIDs not set")
			}
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pn := &v1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{Name: pnName, Namespace: config.Namespace()},
			}
			_ = config.Client().Resources().Delete(ctx, pn)
			return ctx
		}).
		Feature()

	defaultVSwitch := features.New("PodNetworking/DefaultVSwitch").WithLabel("env", "trunking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pn := newPodNetworking(pnName, []string{"foo"}, nil, &metav1.LabelSelector{
				MatchLabels: map[string]string{"trunking-pod": "true"},
			}, nil)
			if err := config.Client().Resources().Create(ctx, pn); err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Assess("vSwitchOptions is foo", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			var pn v1beta1.PodNetworking
			err := config.Client().Resources().Get(ctx, pnName, config.Namespace(), &pn)
			if err != nil {
				t.Fatal(err)
			}
			if len(pn.Spec.VSwitchOptions) == 0 {
				t.Errorf("vSwitchOptions not set")
			}
			if pn.Spec.VSwitchOptions[0] != "foo" {
				t.Errorf("vSwitchOptions not equal foo")
			}
			if len(pn.Spec.SecurityGroupIDs) == 0 {
				t.Errorf("securityGroupIDs not set")
			}
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pn := &v1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{Name: pnName, Namespace: config.Namespace()},
			}
			_ = config.Client().Resources().Delete(ctx, pn)
			return ctx
		}).
		Feature()

	defaultSecurityGroup := features.New("PodNetworking/DefaultSecurityGroup").WithLabel("env", "trunking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pn := newPodNetworking(pnName, nil, []string{"foo"}, &metav1.LabelSelector{
				MatchLabels: map[string]string{"trunking-pod": "true"},
			}, nil)
			if err := config.Client().Resources().Create(ctx, pn); err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Assess("SecurityGroup is foo", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			var pn v1beta1.PodNetworking
			err := config.Client().Resources().Get(ctx, pnName, config.Namespace(), &pn)
			if err != nil {
				t.Fatal(err)
			}
			if len(pn.Spec.VSwitchOptions) == 0 {
				t.Errorf("vSwitchOptions not set")
			}
			if len(pn.Spec.SecurityGroupIDs) == 0 {
				t.Errorf("securityGroupIDs not set")
			}
			if pn.Spec.SecurityGroupIDs[0] != "foo" {
				t.Errorf("securityGroupIDs not equal foo")
			}
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pn := &v1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{Name: pnName, Namespace: config.Namespace()},
			}
			_ = config.Client().Resources().Delete(ctx, pn)
			return ctx
		}).
		Feature()

	defaultAnnotationConfig := features.New("Annotation/DefaultAnnoConfig").WithLabel("env", "trunking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			p := newPod(config.Namespace(), podName, nil, map[string]string{terwayTypes.PodENI: "true"})
			err := config.Client().Resources().Create(ctx, p)
			if err != nil {
				t.Error(err)
			}
			return ctx
		}).
		Assess("with default config", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			var p corev1.Pod
			err := config.Client().Resources().Get(ctx, podName, config.Namespace(), &p)
			if err != nil {
				t.Fatal(err)
			}
			pn, err := controlplane.ParsePodNetworksFromAnnotation(&p)
			if err != nil {
				t.Fatal(err)
			}
			if len(pn.PodNetworks) != 1 {
				t.Errorf("annotation have invalid config")
			}
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: config.Namespace()},
			}
			_ = config.Client().Resources().Delete(ctx, pod)
			return ctx
		}).
		Feature()

	defaultVSwitchAnnotationConfig := features.New("Annotation/DefaultVSwitchAnnoConfig").WithLabel("env", "trunking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			p := newPod(config.Namespace(), podName, nil, map[string]string{terwayTypes.PodENI: "true", terwayTypes.PodNetworks: "{\"podNetworks\":[{\"vSwitchOptions\":[\"foo\"],\"interface\":\"eth0\"}]}"})
			err := config.Client().Resources().Create(ctx, p)
			if err != nil {
				t.Error(err)
			}
			return ctx
		}).
		Assess("vSwitchOptions is foo", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			var p corev1.Pod
			err := config.Client().Resources().Get(ctx, podName, config.Namespace(), &p)
			if err != nil {
				t.Fatal(err)
			}
			pn, err := controlplane.ParsePodNetworksFromAnnotation(&p)
			if err != nil {
				t.Fatal(err)
			}
			if len(pn.PodNetworks) != 1 {
				t.Errorf("annotation have invalid config")
			}
			if len(pn.PodNetworks[0].VSwitchOptions) != 1 {
				t.Errorf("annotation have invalid config")
			}
			if pn.PodNetworks[0].VSwitchOptions[0] != "foo" {
				t.Errorf("VSwitchOptions not equal foo")
			}
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: config.Namespace()},
			}
			_ = config.Client().Resources().Delete(ctx, pod)
			return ctx
		}).
		Feature()

	testenv.Test(t, defaultConfig, defaultVSwitch, defaultSecurityGroup, defaultAnnotationConfig, defaultVSwitchAnnotationConfig)
}

func TestSelector(t *testing.T) {
	podSelector := features.New("PodNetworking/PodSelector").WithLabel("env", "trunking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pn := newPodNetworking(pnName, nil, nil, &metav1.LabelSelector{
				MatchLabels: map[string]string{"trunking-pod": "true"},
			}, nil)
			if err := config.Client().Resources().Create(ctx, pn); err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Assess("podNetworking status ready", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			err := WaitPodNetworkingReady(pnName, config.Client())
			if err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			p := newPod("default", "pod-use-trunking", map[string]string{"trunking-pod": "true"}, nil)
			err := config.Client().Resources().Create(ctx, p)
			if err != nil {
				t.Error(err)
			}
			return ctx
		}).
		Assess("pod have trunking config", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pod := corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-use-trunking", Namespace: "default"},
			}
			err := wait.For(conditions.New(config.Client().Resources()).ResourceMatch(&pod, func(object k8s.Object) bool {
				p := object.(*corev1.Pod)
				if !terwayTypes.PodUseENI(p) {
					return false
				}
				if p.Annotations[terwayTypes.PodNetworking] != pnName {
					return false
				}

				return true
			}), wait.WithTimeout(time.Second*5))
			if err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			p := newPod("default", podName, map[string]string{"trunking-pod": "true"}, nil)
			_ = config.Client().Resources().Delete(ctx, p)
			pn := &v1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{Name: pnName, Namespace: config.Namespace()},
			}
			_ = config.Client().Resources().Delete(ctx, pn)
			return ctx
		}).
		Feature()

	nsSelector := features.New("PodNetworking/NamespaceSelector").WithLabel("env", "trunking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pn := newPodNetworking(pnName, nil, nil, nil, &metav1.LabelSelector{
				MatchLabels: map[string]string{"trunking-pod": "true"},
			})
			if err := config.Client().Resources().Create(ctx, pn); err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Assess("podNetworking status ready", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			err := WaitPodNetworkingReady(pnName, config.Client())
			if err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			p := newPod(config.Namespace(), "any-pod", nil, nil)
			err := config.Client().Resources().Create(ctx, p)
			if err != nil {
				t.Error(err)
			}
			return ctx
		}).
		Assess("pod have trunking config", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			err := WaitPodHaveValidateConfig(config.Namespace(), "any-pod", config.Client(), pnName)
			if err != nil {
				t.Error(err)
			}
			return ctx
		}).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			p := newPod("default", "different-ns", nil, nil)
			err := config.Client().Resources().Create(ctx, p)
			if err != nil {
				t.Error(err)
			}
			return ctx
		}).
		Assess("default ns pod should not using trunking", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			var pod corev1.Pod
			err := config.Client().Resources().Get(ctx, "different-ns", "default", &pod)
			if err != nil {
				t.Error(err)
			}
			if terwayTypes.PodUseENI(&pod) {
				t.Error(fmt.Errorf("pod in namespace default should not use trunking"))
			}
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			p1 := newPod(config.Namespace(), "any-pod", nil, nil)
			p2 := newPod("default", "different-ns", nil, nil)
			_ = config.Client().Resources().Delete(ctx, p1)
			_ = config.Client().Resources().Delete(ctx, p2)
			pn := &v1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{Name: pnName, Namespace: config.Namespace()},
			}
			_ = config.Client().Resources().Delete(ctx, pn)
			return ctx
		}).
		Feature()
	testenv.Test(t, podSelector, nsSelector)
}

func TestZoneLimit(t *testing.T) {
	zoneLimit := features.New("PodNetworking/ZoneLimit").WithLabel("env", "trunking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			podENI := &v1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: config.Namespace()},
				Spec: v1beta1.PodENISpec{
					Allocations: []v1beta1.Allocation{
						{
							AllocationType: v1beta1.AllocationType{
								Type:            v1beta1.IPAllocTypeFixed,
								ReleaseStrategy: v1beta1.ReleaseStrategyNever,
							},
							IPv4: "127.0.0.1",
						},
					},
					Zone: "foo",
				},
				Status: v1beta1.PodENIStatus{},
			}
			if err := config.Client().Resources().Create(ctx, podENI); err != nil {
				t.Fatal(err)
			}
			t.Logf("podENI created %#v", podENI)

			pn := newPodNetworking(pnName, nil, nil, &metav1.LabelSelector{
				MatchLabels: map[string]string{"trunking-pod": "true"},
			}, nil)
			pn.Spec.AllocationType = v1beta1.AllocationType{
				Type:            v1beta1.IPAllocTypeFixed,
				ReleaseStrategy: v1beta1.ReleaseStrategyNever,
			}
			if err := config.Client().Resources().Create(ctx, pn); err != nil {
				t.Fatal(err)
			}
			t.Logf("podNetworking created %#v", pn)
			return ctx
		}).
		Assess("podNetworking status ready", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			err := WaitPodNetworkingReady(pnName, config.Client())
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("podNetworking %s status is ready", pnName)
			return ctx
		}).
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			p := newPod(config.Namespace(), podName, map[string]string{"trunking-pod": "true"}, nil)
			p.OwnerReferences = append(p.OwnerReferences, metav1.OwnerReference{
				Kind:       "StatefulSet",
				Name:       "foo",
				UID:        "foo",
				APIVersion: "foo",
			})
			p.Spec.Affinity = &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{
										Key:      "user-config",
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"bar1", "bar2"},
									},
									{
										Key:      "topology.kubernetes.io/zone",
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"bar1"},
									},
								},
							},
							{
								MatchFields: []corev1.NodeSelectorRequirement{
									{
										Key:      "metadata.name",
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"bar1"},
									},
								},
							},
						},
					},
					PreferredDuringSchedulingIgnoredDuringExecution: nil,
				},
			}

			err := config.Client().Resources().Create(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("pod created %#v", p)
			return ctx
		}).
		Assess("pod have NodeSelectorTerms", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pod := corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: config.Namespace()},
			}
			err := wait.For(conditions.New(config.Client().Resources()).ResourceMatch(&pod, func(object k8s.Object) bool {
				p := object.(*corev1.Pod)
				if !terwayTypes.PodUseENI(p) {
					return false
				}
				if p.Annotations[terwayTypes.PodNetworking] != pnName {
					return false
				}

				return true
			}), wait.WithTimeout(time.Second*5))
			if err != nil {
				t.Fatal(err)
			}

			for _, term := range pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
				t.Logf("MatchFields %d MatchExpressions %d", len(term.MatchFields), len(term.MatchExpressions))
				found := false
				for _, match := range term.MatchExpressions {
					if match.Key == "topology.kubernetes.io/zone" && match.Values[0] == "foo" {
						found = true
					}
				}
				if !found {
					t.Errorf("node affinity config is not satisfy")
				}
			}
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			_ = config.Client().Resources().Delete(ctx, &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: config.Namespace()},
			})
			_ = config.Client().Resources().Delete(ctx, &v1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{Name: pnName, Namespace: config.Namespace()},
			})
			_ = config.Client().Resources().Delete(ctx, &v1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: config.Namespace()},
			})

			return ctx
		}).
		Feature()
	testenv.Test(t, zoneLimit)
}

func WaitPodNetworkingReady(name string, client klient.Client) error {
	pn := v1beta1.PodNetworking{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	return wait.For(conditions.New(client.Resources()).ResourceMatch(&pn, func(object k8s.Object) bool {
		p := object.(*v1beta1.PodNetworking)
		if len(p.Status.VSwitches) != len(p.Spec.VSwitchOptions) {
			return false
		}
		for _, s := range p.Status.VSwitches {
			if s.Zone == "" {
				return false
			}
		}
		return p.Status.Status == v1beta1.NetworkingStatusReady
	}), wait.WithTimeout(time.Minute*1))
}

func WaitPodHaveValidateConfig(namespace, name string, client klient.Client, podNetworkingName string) error {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	return wait.For(conditions.New(client.Resources()).ResourceMatch(&pod, func(object k8s.Object) bool {
		p := object.(*corev1.Pod)
		if !terwayTypes.PodUseENI(p) {
			return false
		}
		if p.Annotations[terwayTypes.PodNetworking] != podNetworkingName {
			return false
		}
		return true
	}), wait.WithTimeout(time.Second*5))
}

func newPodNetworking(name string, vSwitchOptions, securityGroupIDs []string, podSelector, namespaceSelector *metav1.LabelSelector) *v1beta1.PodNetworking {
	return &v1beta1.PodNetworking{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1beta1.PodNetworkingSpec{
			Selector: v1beta1.Selector{
				PodSelector:       podSelector,
				NamespaceSelector: namespaceSelector,
			},
			VSwitchOptions:   vSwitchOptions,
			SecurityGroupIDs: securityGroupIDs,
		},
	}
}

func newPod(namespace, name string, label, anno map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: label, Annotations: anno},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "foo",
					Image:   "registry.cn-hangzhou.aliyuncs.com/acs/pause:3.2",
					Command: []string{"/pause"},
				},
			},
			TerminationGracePeriodSeconds: func(a int64) *int64 { return &a }(0),
		},
	}
}
