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
