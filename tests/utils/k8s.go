package utils

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient"
)

// GetAvailableContainerNetworkPods return number of container pods can create in this cluster
// not very accurate but is enough for tests
func GetAvailableContainerNetworkPods(ctx context.Context, client klient.Client) (int, error) {
	var nodes corev1.NodeList
	err := client.Resources().List(ctx, &nodes)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, node := range nodes.Items {
		count += int(node.Status.Allocatable.Pods().Value())
	}
	var pods corev1.PodList
	err = client.Resources().List(ctx, &pods)
	if err != nil {
		return 0, err
	}
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			count--
		}
	}
	return count, nil
}

func DeploymentPause(name, namespace string, count int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RecreateDeploymentStrategyType,
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": name,
				},
			},
			Replicas: func(a int32) *int32 { return &a }(count),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": name,
					},
				},
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{
						{
							Key:      "node-role.kubernetes.io/master",
							Operator: corev1.TolerationOpExists,
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
					TerminationGracePeriodSeconds: func(a int64) *int64 { return &a }(0),
					Containers: []corev1.Container{
						{
							Name:            "pause",
							Image:           "registry.cn-hangzhou.aliyuncs.com/acs/pause:3.2",
							Command:         []string{"/pause"},
							ImagePullPolicy: corev1.PullIfNotPresent,
						},
					},
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "type",
												Operator: corev1.NodeSelectorOpNotIn,
												Values: []string{
													"virtual-kubelet",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

type Pod struct {
	Pod *corev1.Pod
}

// NewPod create pod with custom group id, pod is anti-affinity with in same group
func NewPod(name, namespace, group, image string) *Pod {
	var zero int64
	return &Pod{Pod: &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app":   name,
				"group": group,
			},
			Annotations: map[string]string{},
		},
		Spec: corev1.PodSpec{
			Affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{
										Key:      "type",
										Operator: corev1.NodeSelectorOpNotIn,
										Values:   []string{"virtual-kubelet"},
									}, {
										Key:      "kubernetes.io/arch",
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"amd64", "arm64"},
									}, {
										Key:      "kubernetes.io/os",
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"linux"},
									},
								},
							},
						},
					},
				},
				PodAntiAffinity: &corev1.PodAntiAffinity{RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"group": group,
							},
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				}},
			},
			Containers: []corev1.Container{
				{
					Name:            name,
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
				},
			},
			TerminationGracePeriodSeconds: &zero,
		},
	}}
}

func (p *Pod) Expose(svcType string) *corev1.Service {
	policy := corev1.IPFamilyPolicyPreferDualStack
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        p.Pod.Name,
			Namespace:   p.Pod.Namespace,
			Labels:      p.Pod.Labels,
			Annotations: map[string]string{},
		},
		Spec: corev1.ServiceSpec{
			Ports:          nil,
			Selector:       p.Pod.Labels,
			IPFamilyPolicy: &policy,
		},
	}
	switch svcType {
	case "headless":
		svc.Spec.ClusterIP = corev1.ClusterIPNone
	case "nodePort":
		svc.Spec.Type = corev1.ServiceTypeNodePort
	case "loadBalancer":
		svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	default:
		// default clusterIP
	}
	return svc
}

type Sts struct {
	Sts *appsv1.StatefulSet
}

func NewSts(name, namespace, group, image string, replicas int) *Sts {
	var zero int64
	return &Sts{Sts: &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app":   name,
				"group": group,
			},
			Annotations: map[string]string{},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: func(r int) *int32 {
				a := int32(r)
				return &a
			}(replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":   name,
					"group": group,
				}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
					"app":   name,
					"group": group,
				}},
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "type",
												Operator: corev1.NodeSelectorOpNotIn,
												Values:   []string{"virtual-kubelet"},
											}, {
												Key:      "kubernetes.io/arch",
												Operator: corev1.NodeSelectorOpIn,
												Values:   []string{"amd64", "arm64"},
											}, {
												Key:      "kubernetes.io/os",
												Operator: corev1.NodeSelectorOpIn,
												Values:   []string{"linux"},
											},
										},
									},
								},
							},
						},
						PodAntiAffinity: &corev1.PodAntiAffinity{RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"group": group,
									},
								},
								TopologyKey: "kubernetes.io/hostname",
							},
						}},
					},
					Containers: []corev1.Container{
						{
							Name:            name,
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
						},
					},
					TerminationGracePeriodSeconds: &zero,
				},
			},
			ServiceName: name,
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.OnDeleteStatefulSetStrategyType,
			},
		},
	}}
}

func (p *Sts) Expose(svcType string) *corev1.Service {
	policy := corev1.IPFamilyPolicyPreferDualStack
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        p.Sts.Name,
			Namespace:   p.Sts.Namespace,
			Labels:      p.Sts.Labels,
			Annotations: map[string]string{},
		},
		Spec: corev1.ServiceSpec{
			Ports:          nil,
			Selector:       p.Sts.Labels,
			IPFamilyPolicy: &policy,
		},
	}
	switch svcType {
	case "headless":
		svc.Spec.ClusterIP = corev1.ClusterIPNone
	case "nodePort":
		svc.Spec.Type = corev1.ServiceTypeNodePort
	case "loadBalancer":
		svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	default:
		// default clusterIP
	}
	return svc
}
