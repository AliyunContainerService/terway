//go:build e2e

package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/tests/utils"
	terwayTypes "github.com/AliyunContainerService/terway/types"
)

func TestCreatePodNetworking(t *testing.T) {
	defaultConfig := func() features.Feature {
		pnName := "empty-pn"

		return features.New("PodNetworking/DefaultConfig").
			WithLabel("env", "trunking").
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				err := WaitPodNetworkingDeleted(pnName, config.Client())
				if err != nil {
					t.Fatalf("delete podNetworking %s failed, %v", pnName, err)
				}
				pn := newPodNetworking(pnName, nil, nil,
					&metav1.LabelSelector{
						MatchLabels: map[string]string{"match": "nothing"},
					}, nil)
				if err = config.Client().Resources().Create(ctx, pn); err != nil {
					t.Fatalf("create podNetworking %s failed, %v", pnName, err)
				}
				ctx = SaveResources(ctx, pn)
				return ctx
			}).
			Assess("podNetworking should have default config", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				err := WaitPodNetworkingReady(pnName, config.Client())
				assert.NoError(t, err)
				return ctx
			}).Feature()
	}()

	defaultVSwitch := func() features.Feature {
		pnName := "default-vswitch"
		return features.New("PodNetworking/DefaultVSwitch").
			WithLabel("env", "trunking").
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				err := WaitPodNetworkingDeleted(pnName, config.Client())
				if err != nil {
					t.Fatalf("delete podNetworking %s failed, %v", pnName, err)
				}
				pn := newPodNetworking(pnName, []string{"foo"}, nil, &metav1.LabelSelector{
					MatchLabels: map[string]string{"match": "nothing"},
				}, nil)
				if err = config.Client().Resources().Create(ctx, pn); err != nil {
					t.Fatalf("create podNetworking %s failed, %v", pnName, err)
				}
				ctx = SaveResources(ctx, pn)
				return ctx
			}).
			Assess("vSwitchOptions is foo", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				var pn v1beta1.PodNetworking
				err := config.Client().Resources().Get(ctx, pnName, config.Namespace(), &pn)
				if err != nil {
					t.Fatal(err)
				}
				assert.Equal(t, []string{"foo"}, pn.Spec.VSwitchOptions)

				return ctx
			}).Feature()
	}()

	defaultSecurityGroup := func() features.Feature {
		pnName := "default-security-group"
		return features.New("PodNetworking/DefaultSecurityGroup").
			WithLabel("env", "trunking").
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				err := WaitPodNetworkingDeleted(pnName, config.Client())
				if err != nil {
					t.Fatalf("delete podNetworking %s failed, %v", pnName, err)
				}
				pn := newPodNetworking(pnName, nil, []string{"foo"}, &metav1.LabelSelector{
					MatchLabels: map[string]string{"match": "nothing"},
				}, nil)
				if err = config.Client().Resources().Create(ctx, pn); err != nil {
					t.Fatalf("create podNetworking %s failed, %v", pnName, err)
				}
				ctx = SaveResources(ctx, pn)
				return ctx
			}).
			Assess("SecurityGroup is foo", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				var pn v1beta1.PodNetworking
				err := config.Client().Resources().Get(ctx, pnName, config.Namespace(), &pn)
				if err != nil {
					t.Fatalf("create podNetworking %s failed, %v", pnName, err)
				}
				assert.Equal(t, []string{"foo"}, pn.Spec.SecurityGroupIDs)
				return ctx
			}).Feature()
	}()

	testenv.TestInParallel(t, defaultConfig, defaultVSwitch, defaultSecurityGroup)
}

func TestSelector(t *testing.T) {
	podSelector := func() features.Feature {
		pnName := "pn-pod-selector"
		podName := "pod-selector"
		return features.New("PodNetworking/PodSelector").WithLabel("env", "trunking").
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				err := WaitPodNetworkingDeleted(pnName, config.Client())
				if err != nil {
					t.Fatalf("delete podNetworking %s failed, %v", pnName, err)
				}
				pn := newPodNetworking(pnName, nil, nil, &metav1.LabelSelector{
					MatchLabels: map[string]string{"netplan": pnName},
				}, nil)
				if err := config.Client().Resources().Create(ctx, pn); err != nil {
					t.Fatal(err)
				}
				t.Logf("podNetworking %s %s", pnName, pn.UID)

				err = WaitPodNetworkingReady(pnName, config.Client())
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("podNetworking %s status ready", pnName)

				ctx = SaveResources(ctx, pn)
				return ctx
			}).
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				pod := newPod(config.Namespace(), podName, map[string]string{"netplan": pnName}, nil)
				err := config.Client().Resources().Create(ctx, pod)
				if err != nil {
					t.Error(err)
				}
				t.Logf("pod %s %s created", pod.Name, pod.UID)

				ctx = SaveResources(ctx, pod)
				return ctx
			}).
			Assess("pod have trunking config", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				err := WaitPodHaveValidateConfig(config.Namespace(), podName, config.Client(), pnName)
				if err != nil {
					t.Fatal(err)
				}
				return ctx
			}).Feature()
	}()

	nsSelector := func() features.Feature {
		pnName := "pn-ns-selector"
		podName := "ns-selector"
		return features.New("PodNetworking/NamespaceSelector").WithLabel("env", "trunking").
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				err := WaitPodNetworkingDeleted(pnName, config.Client())
				if err != nil {
					t.Fatalf("delete podNetworking %s failed, %v", pnName, err)
				}

				ns := &corev1.Namespace{}
				err = config.Client().Resources().Get(ctx, config.Namespace(), "", ns)
				if err != nil {
					t.Fatal(err)
				}
				if ns.Labels == nil {
					ns.Labels = make(map[string]string)
				}
				ns.Labels["netplan"] = pnName
				err = config.Client().Resources().Update(ctx, ns)
				if err != nil {
					t.Fatal(err)
				}

				pn := newPodNetworking(pnName, nil, nil, nil, &metav1.LabelSelector{
					MatchLabels: map[string]string{"netplan": pnName},
				})
				if err = config.Client().Resources().Create(ctx, pn); err != nil {
					t.Fatal(err)
				}
				err = WaitPodNetworkingReady(pnName, config.Client())
				if err != nil {
					t.Fatal(err)
				}
				ctx = SaveResources(ctx, pn)
				return ctx
			}).
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				pod := newPod(config.Namespace(), podName, nil, nil)
				err := config.Client().Resources().Create(ctx, pod)
				if err != nil {
					t.Error(err)
				}
				ctx = SaveResources(ctx, pod)
				return ctx
			}).
			Assess("pod have trunking config", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				err := WaitPodHaveValidateConfig(config.Namespace(), podName, config.Client(), pnName)
				if err != nil {
					t.Fatal(err)
				}
				return ctx
			}).
			Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				ns := &corev1.Namespace{}
				err := config.Client().Resources().Get(ctx, config.Namespace(), "", ns)
				if err != nil {
					t.Fatal(err)
				}
				delete(ns.Labels, "netplan")
				err = config.Client().Resources().Update(ctx, ns)
				if err != nil {
					t.Fatal(err)
				}
				return ctx
			}).
			Feature()
	}()

	testenv.TestInParallel(t, podSelector, nsSelector)
}

func TestZoneLimit(t *testing.T) {
	zoneLimit := func() features.Feature {
		pnName := "zone-limit"
		podName := "zone-limit"
		return features.New("PodNetworking/ZoneLimit").WithLabel("env", "trunking").
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

				err := WaitPodNetworkingDeleted(pnName, config.Client())
				if err != nil {
					t.Fatalf("delete podNetworking %s failed, %v", pnName, err)
				}
				pn := newPodNetworking(pnName, nil, nil, &metav1.LabelSelector{
					MatchLabels: map[string]string{"netplan": pnName},
				}, nil)
				pn.Spec.AllocationType = v1beta1.AllocationType{
					Type:            v1beta1.IPAllocTypeFixed,
					ReleaseStrategy: v1beta1.ReleaseStrategyNever,
				}
				if err := config.Client().Resources().Create(ctx, pn); err != nil {
					t.Fatal(err)
				}
				t.Logf("podNetworking created %#v", pn)

				err = WaitPodNetworkingReady(pnName, config.Client())
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("podNetworking %s status is ready", pnName)

				ctx = SaveResources(ctx, podENI, pn)
				return ctx
			}).
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				pod := newPod(config.Namespace(), podName, map[string]string{"netplan": pnName}, nil)
				pod.OwnerReferences = append(pod.OwnerReferences, metav1.OwnerReference{
					Kind:       "StatefulSet",
					Name:       "foo",
					UID:        "foo",
					APIVersion: "foo",
				})
				pod.Spec.Affinity = &corev1.Affinity{
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

				err := config.Client().Resources().Create(ctx, pod)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("pod created %#v", pod)

				ctx = SaveResources(ctx, pod)
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
				}), wait.WithTimeout(time.Second*50))
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
			}).Feature()
	}()

	testenv.Test(t, zoneLimit)
}

func TestFixedIP(t *testing.T) {
	fixedIP := func() features.Feature {
		pnName := "fixed-ip"
		return features.New("FixedIP").WithLabel("env", "trunking").
			Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				err := WaitPodNetworkingDeleted(pnName, cfg.Client())
				if err != nil {
					t.Fatal(err)
				}
				pn := newPodNetworking(pnName, nil, nil, &metav1.LabelSelector{
					MatchLabels: map[string]string{"netplan": pnName},
				}, nil)
				pn.Spec.AllocationType = v1beta1.AllocationType{
					Type:            v1beta1.IPAllocTypeFixed,
					ReleaseStrategy: v1beta1.ReleaseStrategyTTL,
					ReleaseAfter:    "10m",
				}
				if err := cfg.Client().Resources().Create(ctx, pn); err != nil {
					t.Error(err)
				}
				err = WaitPodNetworkingReady(pnName, cfg.Client())
				if err != nil {
					t.Fatal(err)
				}
				ctx = SaveResources(ctx, pn)
				return ctx
			}).
			Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {

				for _, args := range [][]interface{}{
					{
						"sts-1",
						"connective-test",
						nginxImage,
					},
					{
						"sts-2",
						"connective-test",
						nginxImage,
					},
				} {
					sts := utils.NewSts(args[0].(string), cfg.Namespace(), args[1].(string), args[2].(string), 1)
					sts.Sts.Spec.Template.Labels["netplan"] = pnName
					err := cfg.Client().Resources().Create(ctx, sts.Sts)
					if err != nil {
						t.Fatal(err)
					}
					ctx = SaveResources(ctx, sts.Sts)
				}

				return ctx
			}).
			Assess("wait for pod ready", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				for _, name := range []string{"sts-1-0", "sts-2-0"} {
					pod := corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cfg.Namespace()},
					}
					err := wait.For(conditions.New(cfg.Client().Resources()).ResourceMatch(&pod, func(object k8s.Object) bool {
						p := object.(*corev1.Pod)
						if !terwayTypes.PodUseENI(p) {
							return false
						}
						if p.Annotations[terwayTypes.PodNetworking] != pnName {
							return false
						}
						if p.Status.Phase != corev1.PodRunning {
							return false
						}
						ctx = context.WithValue(ctx, fmt.Sprintf("%s-ip", pod.Name), pod.Status.PodIP)
						return true
					}), wait.WithInterval(time.Second), wait.WithTimeout(time.Minute*2))
					if err != nil {
						t.Fatal(err)
					}
				}
				return ctx
			}).
			Assess("recreate pod", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				for _, name := range []string{"sts-1-0", "sts-2-0"} {
					pod := &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cfg.Namespace()},
					}
					err := cfg.Client().Resources().Delete(ctx, pod)
					if err != nil {
						t.Fatal(err)
					}
				}
				return ctx
			}).
			Assess("wait for pod ready", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				for _, name := range []string{"sts-1-0", "sts-2-0"} {
					pod := corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cfg.Namespace()},
					}
					err := wait.For(conditions.New(cfg.Client().Resources()).ResourceMatch(&pod, func(object k8s.Object) bool {
						p := object.(*corev1.Pod)
						if !terwayTypes.PodUseENI(p) {
							return false
						}
						if !p.DeletionTimestamp.IsZero() {
							return false
						}
						if p.Annotations[terwayTypes.PodNetworking] != pnName {
							return false
						}
						if p.Status.Phase != corev1.PodRunning {
							return false
						}
						ip := ctx.Value(fmt.Sprintf("%s-ip", pod.Name)).(string)
						if ip != pod.Status.PodIP {
							t.Fatalf("pod ip changed from %s to %s", ip, pod.Status.PodIP)
						}
						return true
					}), wait.WithInterval(time.Second), wait.WithTimeout(time.Minute*2))
					if err != nil {
						t.Fatal(err)
					}
				}
				return ctx
			}).Feature()
	}()

	testenv.Test(t, fixedIP)
}

func TestExclusiveENI(t *testing.T) {
	exclusiveENI := func() features.Feature {
		pnName := "exclusive-eni"
		podName := "exclusive-eni"
		return features.New("ExclusiveENI").WithLabel("env", "trunking").
			Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
				nodes := corev1.NodeList{}
				err := config.Client().Resources().List(ctx, &nodes)
				if err != nil {
					t.Fatal(err)
				}

				hasExclusiveENI := false
				for _, node := range nodes.Items {
					r := node.Status.Allocatable.Name("aliyun/eni", resource.DecimalSI)
					if r != nil && !r.IsZero() {
						hasExclusiveENI = true
						break
					}
				}
				if !hasExclusiveENI {
					t.Skip()
				}

				err = WaitPodNetworkingDeleted(pnName, config.Client())
				if err != nil {
					t.Fatal(err)
				}
				pn := &v1beta1.PodNetworking{
					ObjectMeta: metav1.ObjectMeta{
						Name: pnName,
					},
					Spec: v1beta1.PodNetworkingSpec{
						Selector: v1beta1.Selector{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"netplan": pnName},
							},
						},
						ENIOptions: v1beta1.ENIOptions{
							ENIAttachType: v1beta1.ENIOptionTypeENI,
						},
					},
				}
				err = config.Client().Resources().Create(ctx, pn)
				if err != nil {
					t.Fatal(err)
				}

				ctx = SaveResources(ctx, pn)

				err = WaitPodNetworkingReady(pnName, config.Client())
				if err != nil {
					t.Fatal(err)
				}

				return ctx
			}).
			Assess("wait for pod ready", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				pod := newPod(cfg.Namespace(), podName, map[string]string{"netplan": pnName}, nil)
				err := cfg.Client().Resources().Create(ctx, pod)
				if err != nil {
					t.Fatal(err)
				}
				ctx = SaveResources(ctx, pod)

				err = WaitPodHaveValidateConfig(cfg.Namespace(), pod.Name, cfg.Client(), pnName)
				if err != nil {
					t.Fatal(err)
				}

				// check pod have exclusive eni
				err = cfg.Client().Resources().Get(ctx, pod.Name, cfg.Namespace(), pod)
				if err != nil {
					t.Fatal(err)
				}

				pod, err = waitPodENIPod(cfg.Namespace(), pod.Name, cfg.Client())
				if err != nil {
					t.Fatalf("wait pod %s ready failed, %v", pod.Name, err)
				}

				assert.Equal(t, pnName, pod.Annotations[terwayTypes.PodNetworking])

				assert.True(t, pod.Spec.Containers[0].Resources.Limits[corev1.ResourceName("aliyun/eni")].Equal(resource.MustParse("1")))
				assert.True(t, pod.Spec.Containers[0].Resources.Requests[corev1.ResourceName("aliyun/eni")].Equal(resource.MustParse("1")))

				return ctx
			}).
			Feature()
	}()

	testenv.Test(t, exclusiveENI)
}

func WaitPodNetworkingDeleted(name string, client klient.Client) error {
	pn := v1beta1.PodNetworking{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	_ = client.Resources().Delete(context.Background(), &pn)

	err := wait.For(conditions.New(client.Resources()).ResourceDeleted(&pn),
		wait.WithImmediate(),
		wait.WithInterval(1*time.Second),
		wait.WithTimeout(30*time.Second),
	)
	return err
}

func WaitPodNetworkingReady(name string, client klient.Client) error {
	pn := v1beta1.PodNetworking{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	err := wait.For(conditions.New(client.Resources()).ResourceMatch(&pn, func(object k8s.Object) bool {
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
	}),
		wait.WithImmediate(),
		wait.WithInterval(1*time.Second),
		wait.WithTimeout(30*time.Second),
	)
	return err
}

func WaitPodHaveValidateConfig(namespace, name string, client klient.Client, podNetworkingName string) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	err := wait.For(conditions.New(client.Resources()).ResourceMatch(pod, func(object k8s.Object) bool {
		p := object.(*corev1.Pod)
		if !terwayTypes.PodUseENI(p) {
			return false
		}
		if p.Annotations[terwayTypes.PodNetworking] != podNetworkingName {
			return false
		}
		return true
	}),
		wait.WithImmediate(),
		wait.WithInterval(1*time.Second),
		wait.WithTimeout(10*time.Second),
	)

	if err != nil {
		if pod != nil {
			return fmt.Errorf("%w, Anno %#v, Labels %#v", err, pod.Annotations, pod.Labels)
		}
		return err
	}
	return nil
}

func waitPodENIPod(namespace, name string, client klient.Client) (*corev1.Pod, error) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	err := wait.For(conditions.New(client.Resources()).ResourceMatch(pod, func(object k8s.Object) bool {
		pod = object.(*corev1.Pod)
		if !terwayTypes.PodUseENI(pod) {
			return false
		}
		return true
	}),
		wait.WithImmediate(),
		wait.WithInterval(1*time.Second),
		wait.WithTimeout(10*time.Second),
	)

	return pod, err
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
			ENIOptions: v1beta1.ENIOptions{
				ENIAttachType: v1beta1.ENIOptionTypeTrunk,
			},
		},
	}
}

func newPod(namespace, name string, label, anno map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: label, Annotations: anno},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:            "foo",
					Image:           fmt.Sprintf("registry-%s-vpc.ack.aliyuncs.com/acs/pause:3.9", regionID),
					ImagePullPolicy: corev1.PullIfNotPresent,
					Command:         []string{"/pause"},
				},
			},
			TerminationGracePeriodSeconds: ptr.To(int64(0)),
		},
	}
}
