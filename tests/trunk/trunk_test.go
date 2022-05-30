//go:build e2e

package trunk

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/tests/utils"
	terwayTypes "github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/intstr"

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
			pn := newPodNetworking(defaultPodNetworkingName, nil, nil, &metav1.LabelSelector{
				MatchLabels: map[string]string{"trunking-pod": "true"},
			}, nil)
			if err := config.Client().Resources().Create(ctx, pn); err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Assess("with default config", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			var pn v1beta1.PodNetworking
			err := config.Client().Resources().Get(ctx, defaultPodNetworkingName, config.Namespace(), &pn)
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
				ObjectMeta: metav1.ObjectMeta{Name: defaultPodNetworkingName, Namespace: config.Namespace()},
			}
			_ = config.Client().Resources().Delete(ctx, pn)
			return ctx
		}).
		Feature()

	defaultVSwitch := features.New("PodNetworking/DefaultVSwitch").WithLabel("env", "trunking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pn := newPodNetworking(defaultPodNetworkingName, []string{"foo"}, nil, &metav1.LabelSelector{
				MatchLabels: map[string]string{"trunking-pod": "true"},
			}, nil)
			if err := config.Client().Resources().Create(ctx, pn); err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Assess("vSwitchOptions is foo", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			var pn v1beta1.PodNetworking
			err := config.Client().Resources().Get(ctx, defaultPodNetworkingName, config.Namespace(), &pn)
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
				ObjectMeta: metav1.ObjectMeta{Name: defaultPodNetworkingName, Namespace: config.Namespace()},
			}
			_ = config.Client().Resources().Delete(ctx, pn)
			return ctx
		}).
		Feature()

	defaultSecurityGroup := features.New("PodNetworking/DefaultSecurityGroup").WithLabel("env", "trunking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pn := newPodNetworking(defaultPodNetworkingName, nil, []string{"foo"}, &metav1.LabelSelector{
				MatchLabels: map[string]string{"trunking-pod": "true"},
			}, nil)
			if err := config.Client().Resources().Create(ctx, pn); err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Assess("SecurityGroup is foo", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			var pn v1beta1.PodNetworking
			err := config.Client().Resources().Get(ctx, defaultPodNetworkingName, config.Namespace(), &pn)
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
				ObjectMeta: metav1.ObjectMeta{Name: defaultPodNetworkingName, Namespace: config.Namespace()},
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
			pn := newPodNetworking(defaultPodNetworkingName, nil, nil, &metav1.LabelSelector{
				MatchLabels: map[string]string{"trunking-pod": "true"},
			}, nil)
			if err := config.Client().Resources().Create(ctx, pn); err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Assess("podNetworking status ready", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			err := WaitPodNetworkingReady(defaultPodNetworkingName, config.Client())
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
				if p.Annotations[terwayTypes.PodNetworking] != defaultPodNetworkingName {
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
				ObjectMeta: metav1.ObjectMeta{Name: defaultPodNetworkingName, Namespace: config.Namespace()},
			}
			_ = config.Client().Resources().Delete(ctx, pn)
			return ctx
		}).
		Feature()

	nsSelector := features.New("PodNetworking/NamespaceSelector").WithLabel("env", "trunking").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pn := newPodNetworking(defaultPodNetworkingName, nil, nil, nil, &metav1.LabelSelector{
				MatchLabels: map[string]string{"trunking-pod": "true"},
			})
			if err := config.Client().Resources().Create(ctx, pn); err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Assess("podNetworking status ready", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			err := WaitPodNetworkingReady(defaultPodNetworkingName, config.Client())
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
			err := WaitPodHaveValidateConfig(config.Namespace(), "any-pod", config.Client(), defaultPodNetworkingName)
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
				ObjectMeta: metav1.ObjectMeta{Name: defaultPodNetworkingName, Namespace: config.Namespace()},
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

			pn := newPodNetworking(defaultPodNetworkingName, nil, nil, &metav1.LabelSelector{
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
			err := WaitPodNetworkingReady(defaultPodNetworkingName, config.Client())
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("podNetworking %s status is ready", defaultPodNetworkingName)
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
				if p.Annotations[terwayTypes.PodNetworking] != defaultPodNetworkingName {
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
				ObjectMeta: metav1.ObjectMeta{Name: defaultPodNetworkingName, Namespace: config.Namespace()},
			})
			_ = config.Client().Resources().Delete(ctx, &v1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: config.Namespace()},
			})

			return ctx
		}).
		Feature()
	testenv.Test(t, zoneLimit)
}

func TestFixedIP(t *testing.T) {
	fixedIP := features.New("FixedIP").WithLabel("env", "trunking").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			_ = cfg.Client().Resources().Delete(ctx, &v1beta1.PodNetworking{ObjectMeta: metav1.ObjectMeta{Name: "fixed-ip"}})
			pn := newPodNetworking("fixed-ip", nil, nil, &metav1.LabelSelector{
				MatchLabels: map[string]string{"fixed-ip": "true"},
			}, nil)
			pn.Spec.AllocationType = v1beta1.AllocationType{
				Type:            v1beta1.IPAllocTypeFixed,
				ReleaseStrategy: v1beta1.ReleaseStrategyTTL,
				ReleaseAfter:    "10m",
			}
			if err := cfg.Client().Resources().Create(ctx, pn); err != nil {
				t.Error(err)
			}
			err := WaitPodNetworkingReady("fixed-ip", cfg.Client())
			if err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			ports := []corev1.ServicePort{
				{
					Name:       "http",
					Protocol:   corev1.ProtocolTCP,
					Port:       int32(80),
					TargetPort: intstr.FromInt(80),
				},
			}
			for _, args := range [][]interface{}{
				{
					"sts-1",
					"connective-test",
					"l1b0k/echo:v0.0.1",
				},
				{
					"sts-2",
					"connective-test",
					"l1b0k/echo:v0.0.1",
				},
			} {
				sts := utils.NewSts(args[0].(string), cfg.Namespace(), args[1].(string), args[2].(string), 1)
				sts.Sts.Spec.Template.Labels["fixed-ip"] = "true"
				err := cfg.Client().Resources().Create(ctx, sts.Sts)
				if err != nil {
					t.Fatal(err)
				}
				svc := sts.Expose("")
				svc.Spec.Ports = ports
				err = cfg.Client().Resources().Create(ctx, svc)
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
					if p.Annotations[terwayTypes.PodNetworking] != "fixed-ip" {
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
		Assess("test connective", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			pod := utils.NewPod("client", cfg.Namespace(), "client", "l1b0k/echo:v0.0.1")
			pod.Pod.Spec.Containers[0].Command = []string{"/usr/bin/echo", "-mode", "client", "-cases", "dns://aliyun.com,http://sts-1,http://sts-2,tcp://100.100.100.200:80"}
			pod.Pod.Labels["fixed-ip"] = "true"
			pod.Pod.Spec.RestartPolicy = corev1.RestartPolicyNever
			err := cfg.Client().Resources().Create(ctx, pod.Pod)
			if err != nil {
				t.Fatal(err)
			}
			p := corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "client", Namespace: cfg.Namespace()},
			}
			stopChan := make(chan struct{})
			err = wait.For(conditions.New(cfg.Client().Resources()).ResourceMatch(&p, func(object k8s.Object) bool {
				p := object.(*corev1.Pod)
				switch p.Status.Phase {
				case corev1.PodSucceeded:
					return true
				case corev1.PodFailed:
					stopChan <- struct{}{}
					t.Fatal("pod status failed")
				}
				return false
			}), wait.WithInterval(time.Second), wait.WithTimeout(time.Minute*2), wait.WithStopChannel(stopChan))
			if err != nil {
				t.Fatal(err)
			}
			err = cfg.Client().Resources().Delete(ctx, pod.Pod)
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
					if p.Annotations[terwayTypes.PodNetworking] != "fixed-ip" {
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
		}).
		Assess("re-test connective", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			pod := utils.NewPod("client", cfg.Namespace(), "client", "l1b0k/echo:v0.0.1")
			pod.Pod.Spec.Containers[0].Command = []string{"/usr/bin/echo", "-mode", "client", "-cases", "dns://aliyun.com,http://sts-1,http://sts-2,tcp://100.100.100.200:80"}
			pod.Pod.Labels["fixed-ip"] = "true"
			pod.Pod.Spec.RestartPolicy = corev1.RestartPolicyNever
			err := cfg.Client().Resources().Create(ctx, pod.Pod)
			if err != nil {
				t.Fatal(err)
			}
			p := corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "client", Namespace: cfg.Namespace()},
			}
			stopChan := make(chan struct{})
			err = wait.For(conditions.New(cfg.Client().Resources()).ResourceMatch(&p, func(object k8s.Object) bool {
				p := object.(*corev1.Pod)
				switch p.Status.Phase {
				case corev1.PodSucceeded:
					return true
				case corev1.PodFailed:
					stopChan <- struct{}{}
					t.Fatal("pod status failed")
				}
				return false
			}), wait.WithInterval(time.Second), wait.WithTimeout(time.Minute*2), wait.WithStopChannel(stopChan))
			if err != nil {
				t.Fatal(err)
			}
			err = cfg.Client().Resources().Delete(ctx, pod.Pod)
			if err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			_ = config.Client().Resources().Delete(ctx, &v1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{Name: "fixed-ip"},
			})
			for _, arg := range []string{"sts-1", "sts-2"} {
				_ = config.Client().Resources().Delete(ctx, &appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{Name: arg, Namespace: config.Namespace()},
				})
				_ = config.Client().Resources().Delete(ctx, &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: arg, Namespace: config.Namespace()},
				})
			}
			return ctx
		}).Feature()

	testenv.Test(t, fixedIP)
}

// TestConnective run test cover several cases.
// pod to pod ,pod to service, dns resolve
func TestConnective(t *testing.T) {
	// this test requires at least 2 vSwitches 2 nodes
	vsw := strings.Split(vSwitchIDs, ",")
	if len(vsw) < 2 {
		return
	}
	crossVSwitch := features.New("Connective/MultiVSwitch").WithLabel("env", "trunking").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			for _, args := range [][]interface{}{
				{
					"use-vsw-1",
					[]string{vsw[0]},
				},
				{
					"use-vsw-2",
					[]string{vsw[1]},
				},
			} {
				pn := newPodNetworking(args[0].(string), args[1].([]string), nil, &metav1.LabelSelector{
					MatchLabels: map[string]string{args[0].(string): ""},
				}, nil)
				err := cfg.Client().Resources().Create(ctx, pn)
				if err != nil && !errors.IsAlreadyExists(err) {
					t.Fatal(err)
				}
			}
			return ctx
		}).
		Assess("podNetworking status ready", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			for _, arg := range []string{"use-vsw-1", "use-vsw-2"} {
				err := WaitPodNetworkingReady(arg, cfg.Client())
				if err != nil {
					t.Fatal(err)
				}
			}
			return ctx
		}).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			ports := []corev1.ServicePort{
				{
					Name:       "http",
					Protocol:   corev1.ProtocolTCP,
					Port:       int32(80),
					TargetPort: intstr.FromInt(80),
				},
			}
			for _, args := range [][]interface{}{
				{
					"pod-1",
					"connective-test",
					"l1b0k/echo:v0.0.1",
					"use-vsw-1",
				},
				{
					"pod-2",
					"connective-test",
					"l1b0k/echo:v0.0.1",
					"use-vsw-2",
				},
			} {
				pod := utils.NewPod(args[0].(string), cfg.Namespace(), args[1].(string), args[2].(string))
				pod.Pod.Labels[args[3].(string)] = ""
				err := cfg.Client().Resources().Create(ctx, pod.Pod)
				if err != nil {
					t.Error(err)
				}
				svc := pod.Expose("")
				svc.Spec.Ports = ports
				err = cfg.Client().Resources().Create(ctx, svc)
				if err != nil {
					t.Error(err)
				}
			}
			return ctx
		}).
		Assess("wait for pod ready", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			for _, name := range []string{"pod-1", "pod-2"} {
				pod := corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cfg.Namespace()},
				}
				err := wait.For(conditions.New(cfg.Client().Resources()).ResourceMatch(&pod, func(object k8s.Object) bool {
					p := object.(*corev1.Pod)
					if !terwayTypes.PodUseENI(p) {
						return false
					}
					return p.Status.Phase == corev1.PodRunning
				}), wait.WithInterval(time.Second), wait.WithTimeout(time.Minute*2))
				if err != nil {
					t.Fatal(err)
				}
			}
			return ctx
		}).
		Assess("test connective", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			pod := utils.NewPod("client", cfg.Namespace(), "client", "l1b0k/echo:v0.0.1")
			pod.Pod.Spec.Containers[0].Command = []string{"/usr/bin/echo", "-mode", "client", "-cases", "dns://aliyun.com,http://pod-1,http://pod-2,tcp://100.100.100.200:80"}
			pod.Pod.Labels["use-vsw-1"] = ""
			pod.Pod.Spec.RestartPolicy = corev1.RestartPolicyNever
			err := cfg.Client().Resources().Create(ctx, pod.Pod)
			if err != nil {
				t.Fatal(err)
			}
			p := corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "client", Namespace: cfg.Namespace()},
			}
			err = wait.For(conditions.New(cfg.Client().Resources()).ResourceMatch(&p, func(object k8s.Object) bool {
				p := object.(*corev1.Pod)
				return p.Status.Phase == corev1.PodSucceeded
			}), wait.WithInterval(time.Second), wait.WithTimeout(time.Minute*2))
			if err != nil {
				t.Fatal(err)
			}
			err = cfg.Client().Resources().Delete(ctx, pod.Pod)
			if err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			for _, arg := range []string{"use-vsw-1", "use-vsw-2"} {
				pn1 := &v1beta1.PodNetworking{
					ObjectMeta: metav1.ObjectMeta{Name: arg},
				}
				_ = config.Client().Resources().Delete(ctx, pn1)
			}
			return ctx
		}).Feature()

	testenv.Test(t, crossVSwitch)
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
	}), wait.WithTimeout(time.Minute*1))
	time.Sleep(5 * time.Second)
	return err
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
					Name:            "foo",
					Image:           "registry.cn-hangzhou.aliyuncs.com/acs/pause:3.2",
					ImagePullPolicy: corev1.PullIfNotPresent,
					Command:         []string{"/pause"},
				},
			},
			TerminationGracePeriodSeconds: func(a int64) *int64 { return &a }(0),
		},
	}
}
