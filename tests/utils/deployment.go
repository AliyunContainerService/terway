package utils

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Deployment is a builder for creating deployment configurations
type Deployment struct {
	*appsv1.Deployment
}

// NewDeployment creates a new deployment builder
func NewDeployment(name, namespace string, replicas int32) *Deployment {
	return &Deployment{
		Deployment: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"app": name,
					},
				},
				Strategy: appsv1.DeploymentStrategy{
					Type: appsv1.RollingUpdateDeploymentStrategyType,
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"app": name,
						},
					},
					Spec: corev1.PodSpec{
						TerminationGracePeriodSeconds: func() *int64 {
							i := int64(0)
							return &i
						}(),
						Containers: []corev1.Container{
							{
								Name:            "pause",
								Image:           "registry.cn-hangzhou.aliyuncs.com/acs/pause:3.2",
								Command:         []string{"/pause"},
								ImagePullPolicy: corev1.PullIfNotPresent,
							},
						},
					},
				},
			},
		},
	}
}

// WithNodeAffinity adds node affinity to the deployment
func (d *Deployment) WithNodeAffinity(labels map[string]string) *Deployment {
	var nodeSelectorTerms []corev1.NodeSelectorRequirement
	for k, v := range labels {
		nodeSelectorTerms = append(nodeSelectorTerms, corev1.NodeSelectorRequirement{
			Key:      k,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{v},
		})
	}

	if len(nodeSelectorTerms) == 0 {
		return d
	}

	if d.Spec.Template.Spec.Affinity == nil {
		d.Spec.Template.Spec.Affinity = &corev1.Affinity{}
	}
	if d.Spec.Template.Spec.Affinity.NodeAffinity == nil {
		d.Spec.Template.Spec.Affinity.NodeAffinity = &corev1.NodeAffinity{}
	}
	if d.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		d.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{}
	}
	if len(d.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms) == 0 {
		d.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = []corev1.NodeSelectorTerm{
			{MatchExpressions: nodeSelectorTerms},
		}
	} else {
		d.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions = append(
			d.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions,
			nodeSelectorTerms...,
		)
	}
	return d
}

// WithNodeAffinityExclude adds node affinity exclusion to the deployment
func (d *Deployment) WithNodeAffinityExclude(excludeLabels map[string]string) *Deployment {
	var nodeSelectorTerms []corev1.NodeSelectorRequirement
	for k, v := range excludeLabels {
		nodeSelectorTerms = append(nodeSelectorTerms, corev1.NodeSelectorRequirement{
			Key:      k,
			Operator: corev1.NodeSelectorOpNotIn,
			Values:   []string{v},
		})
	}

	// Always exclude virtual-kubelet nodes
	nodeSelectorTerms = append(nodeSelectorTerms, corev1.NodeSelectorRequirement{
		Key:      "type",
		Operator: corev1.NodeSelectorOpNotIn,
		Values:   []string{"virtual-kubelet"},
	})

	if d.Spec.Template.Spec.Affinity == nil {
		d.Spec.Template.Spec.Affinity = &corev1.Affinity{}
	}
	if d.Spec.Template.Spec.Affinity.NodeAffinity == nil {
		d.Spec.Template.Spec.Affinity.NodeAffinity = &corev1.NodeAffinity{}
	}
	if d.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		d.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{}
	}
	if len(d.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms) == 0 {
		d.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = []corev1.NodeSelectorTerm{
			{MatchExpressions: nodeSelectorTerms},
		}
	} else {
		d.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions = append(
			d.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions,
			nodeSelectorTerms...,
		)
	}
	return d
}

// WithTolerations adds tolerations to the deployment
func (d *Deployment) WithTolerations(tolerations []corev1.Toleration) *Deployment {
	d.Spec.Template.Spec.Tolerations = append(d.Spec.Template.Spec.Tolerations, tolerations...)
	return d
}

// WithLingjunToleration adds toleration for Lingjun nodes
func (d *Deployment) WithLingjunToleration() *Deployment {
	return d.WithTolerations(LingjunTolerations())
}

// WithLabels adds labels to the pod template
func (d *Deployment) WithLabels(labels map[string]string) *Deployment {
	if d.Spec.Template.Labels == nil {
		d.Spec.Template.Labels = make(map[string]string)
	}
	for k, v := range labels {
		d.Spec.Template.Labels[k] = v
	}
	return d
}

// WithAnnotations adds annotations to the pod template
func (d *Deployment) WithAnnotations(annotations map[string]string) *Deployment {
	if d.Spec.Template.Annotations == nil {
		d.Spec.Template.Annotations = make(map[string]string)
	}
	for k, v := range annotations {
		d.Spec.Template.Annotations[k] = v
	}
	return d
}

// WithPodAffinity adds pod affinity to schedule pods to the same node as pods with specified labels
func (d *Deployment) WithPodAffinity(labels map[string]string) *Deployment {
	if len(labels) == 0 {
		return d
	}

	if d.Spec.Template.Spec.Affinity == nil {
		d.Spec.Template.Spec.Affinity = &corev1.Affinity{}
	}
	if d.Spec.Template.Spec.Affinity.PodAffinity == nil {
		d.Spec.Template.Spec.Affinity.PodAffinity = &corev1.PodAffinity{}
	}

	d.Spec.Template.Spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(
		d.Spec.Template.Spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
		corev1.PodAffinityTerm{
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			TopologyKey: "kubernetes.io/hostname",
		},
	)
	return d
}
