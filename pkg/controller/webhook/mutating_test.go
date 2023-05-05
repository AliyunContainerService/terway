package webhook

import (
	"reflect"
	"strconv"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_setResourceRequest(t *testing.T) {
	type args struct {
		pod     *corev1.Pod
		resName string
		count   int
	}
	tests := []struct {
		name string
		args args
		want *corev1.Pod
	}{
		{
			name: "ignore count=0",
			args: args{
				pod:   &corev1.Pod{},
				count: 0,
			},
			want: &corev1.Pod{},
		}, {
			name: "request one",
			args: args{
				pod: &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
					{
						Name: "a",
						Resources: corev1.ResourceRequirements{
							Limits: map[corev1.ResourceName]resource.Quantity{
								"bar": resource.MustParse(strconv.Itoa(100)),
							},
						},
					}, {
						Name: "b",
					},
				}}},
				resName: "foo",
				count:   1,
			},
			want: &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
				{
					Name: "a",
					Resources: corev1.ResourceRequirements{
						Limits: map[corev1.ResourceName]resource.Quantity{
							"bar": resource.MustParse(strconv.Itoa(100)),
							"foo": resource.MustParse(strconv.Itoa(1)),
						},
						Requests: map[corev1.ResourceName]resource.Quantity{
							"foo": resource.MustParse(strconv.Itoa(1)),
						},
					},
				}, {
					Name: "b",
				},
			}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setResourceRequest(tt.args.pod, tt.args.resName, tt.args.count)
			if !reflect.DeepEqual(tt.args.pod, tt.want) {
				t.Errorf("setResourceRequest() = %v, want %v", tt.args.pod, tt.want)
			}
		})
	}
}

func Test_setNodeAffinityByZones(t *testing.T) {
	type args struct {
		pod       *corev1.Pod
		zones     []string
		prevZones []string
	}
	tests := []struct {
		name string
		args args
		want *corev1.Pod
	}{
		{
			name: "ds pod should ignore",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						OwnerReferences: []metav1.OwnerReference{
							{
								Kind: "DaemonSet",
							},
						},
					},
				},
				zones: []string{"foo", "bar"},
			},
			want: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind: "DaemonSet",
						},
					},
				},
			},
		}, {
			name: "pod with no affinity",
			args: args{
				pod:   &corev1.Pod{},
				zones: []string{"foo", "bar"},
			},
			want: &corev1.Pod{
				Spec: corev1.PodSpec{Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      corev1.LabelTopologyZone,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"foo", "bar"},
								},
							},
						},
					},
				}}}},
			},
		}, {
			name: "pod with exist affinity",
			args: args{
				pod: &corev1.Pod{Spec: corev1.PodSpec{Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      corev1.LabelTopologyZone,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"exist"},
								},
							},
							MatchFields: []corev1.NodeSelectorRequirement{
								{
									Key:      "metadata.name",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"foo"},
								},
							},
						},
						{
							MatchFields: []corev1.NodeSelectorRequirement{
								{
									Key:      "metadata.name",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"foo"},
								},
							},
						},
					},
				}}}}},
				zones: []string{"foo", "bar"},
			},
			want: &corev1.Pod{Spec: corev1.PodSpec{Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"exist"},
							},
							{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"foo", "bar"},
							},
						},
						MatchFields: []corev1.NodeSelectorRequirement{
							{
								Key:      "metadata.name",
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"foo"},
							},
						},
					},
					{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"foo", "bar"},
							},
						},
						MatchFields: []corev1.NodeSelectorRequirement{
							{
								Key:      "metadata.name",
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"foo"},
							},
						},
					},
				},
			}}}}},
		}, {
			name: "pod with multi zone set",
			args: args{
				pod: &corev1.Pod{Spec: corev1.PodSpec{Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      corev1.LabelTopologyZone,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"exist"},
								},
							},
							MatchFields: []corev1.NodeSelectorRequirement{
								{
									Key:      "metadata.name",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"foo"},
								},
							},
						},
						{
							MatchFields: []corev1.NodeSelectorRequirement{
								{
									Key:      "metadata.name",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"foo"},
								},
							},
						},
					},
				}}}}},
				zones:     []string{"foo", "bar"},
				prevZones: []string{"foo", "bar"},
			},
			want: &corev1.Pod{Spec: corev1.PodSpec{Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"exist"},
							},
							{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"foo", "bar"},
							},
							{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"foo", "bar"},
							},
						},
						MatchFields: []corev1.NodeSelectorRequirement{
							{
								Key:      "metadata.name",
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"foo"},
							},
						},
					},
					{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"foo", "bar"},
							},
							{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"foo", "bar"},
							},
						},
						MatchFields: []corev1.NodeSelectorRequirement{
							{
								Key:      "metadata.name",
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"foo"},
							},
						},
					},
				},
			}}}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setNodeAffinityByZones(tt.args.pod, tt.args.zones, tt.args.prevZones)
			if !reflect.DeepEqual(tt.args.pod, tt.want) {
				t.Errorf("setNodeAffinityByZones() = %v, want %v", tt.args.pod, tt.want)
			}
		})
	}
}
