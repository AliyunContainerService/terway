package webhook

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/types/controlplane"
	"github.com/AliyunContainerService/terway/types/daemon"
	v1 "k8s.io/api/admission/v1"
)

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

func Test_setResourceRequest1(t *testing.T) {
	type args struct {
		pod         *corev1.Pod
		podNetworks []controlplane.PodNetworks
		enableTrunk bool
	}
	tests := []struct {
		name string
		args args
		want *corev1.Pod
	}{
		{
			name: "ignore count=0",
			args: args{
				pod:         &corev1.Pod{},
				podNetworks: nil,
				enableTrunk: false,
			},
			want: &corev1.Pod{},
		},
		{
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
				podNetworks: []controlplane.PodNetworks{
					{},
				},
				enableTrunk: true,
			},
			want: &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
				{
					Name: "a",
					Resources: corev1.ResourceRequirements{
						Limits: map[corev1.ResourceName]resource.Quantity{
							"bar":               resource.MustParse(strconv.Itoa(100)),
							"aliyun/member-eni": resource.MustParse(strconv.Itoa(1)),
						},
						Requests: map[corev1.ResourceName]resource.Quantity{
							"aliyun/member-eni": resource.MustParse(strconv.Itoa(1)),
						},
					},
				}, {
					Name: "b",
				},
			}}},
		},
		{
			name: "eni only",
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
				podNetworks: []controlplane.PodNetworks{
					{},
				},
				enableTrunk: false,
			},
			want: &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
				{
					Name: "a",
					Resources: corev1.ResourceRequirements{
						Limits: map[corev1.ResourceName]resource.Quantity{
							"bar":        resource.MustParse(strconv.Itoa(100)),
							"aliyun/eni": resource.MustParse(strconv.Itoa(1)),
						},
						Requests: map[corev1.ResourceName]resource.Quantity{
							"aliyun/eni": resource.MustParse(strconv.Itoa(1)),
						},
					},
				}, {
					Name: "b",
				},
			}}},
		},
		{
			name: "allow override to aliyun/eni",
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
				podNetworks: []controlplane.PodNetworks{
					{
						ENIOptions: v1beta1.ENIOptions{
							ENIAttachType: "ENI",
						},
					},
				},
				enableTrunk: true,
			},
			want: &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
				{
					Name: "a",
					Resources: corev1.ResourceRequirements{
						Limits: map[corev1.ResourceName]resource.Quantity{
							"bar":        resource.MustParse(strconv.Itoa(100)),
							"aliyun/eni": resource.MustParse(strconv.Itoa(1)),
						},
						Requests: map[corev1.ResourceName]resource.Quantity{
							"aliyun/eni": resource.MustParse(strconv.Itoa(1)),
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
			setResourceRequest(tt.args.pod, tt.args.podNetworks, tt.args.enableTrunk)
			assert.Equal(t, tt.want, tt.args.pod)
		})
	}
}

func TestPodMatchSelectorReturnsTrueWhenLabelsMatch(t *testing.T) {
	labelSelector := &metav1.LabelSelector{
		MatchLabels: map[string]string{"key": "value"},
	}
	labelsSet := labels.Set{"key": "value"}

	result, err := PodMatchSelector(labelSelector, labelsSet)
	assert.NoError(t, err)
	assert.True(t, result)
}

func TestPodMatchSelectorReturnsFalseWhenLabelsDoNotMatch(t *testing.T) {
	labelSelector := &metav1.LabelSelector{
		MatchLabels: map[string]string{"key": "value"},
	}
	labelsSet := labels.Set{"key": "different"}

	result, err := PodMatchSelector(labelSelector, labelsSet)
	assert.NoError(t, err)
	assert.False(t, result)
}

func TestPodMatchSelectorReturnsErrorForInvalidLabelSelector(t *testing.T) {
	labelSelector := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      "key",
				Operator: "InvalidOperator",
				Values:   []string{"value"},
			},
		},
	}
	labelsSet := labels.Set{"key": "value"}

	result, err := PodMatchSelector(labelSelector, labelsSet)
	assert.Error(t, err)
	assert.False(t, result)
}

func TestPreviousZoneReturnsEmptyWhenPodIsNotFixedName(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "ReplicaSet",
				},
			},
		},
	}

	zone, err := getPreviousZone(context.Background(), fakeClient, pod)
	assert.NoError(t, err)
	assert.Equal(t, "", zone)
}

func TestPreviousZoneReturnsEmptyWhenPodENINotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "StatefulSet",
				},
			},
		},
	}

	zone, err := getPreviousZone(context.Background(), fakeClient, pod)
	assert.NoError(t, err)
	assert.Equal(t, "", zone)
}

func TestPreviousZoneReturnsEmptyWhenPodENIHasDeletionTimestamp(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
		},
	}
	podENI := &v1beta1.PodENI{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         "default",
			Name:              "test-pod",
			DeletionTimestamp: &metav1.Time{Time: time.Now()},
		},
	}

	_ = fakeClient.Create(context.Background(), podENI)

	zone, err := getPreviousZone(context.Background(), fakeClient, pod)
	assert.NoError(t, err)
	assert.Equal(t, "", zone)
}

func TestPreviousZoneReturnsEmptyWhenPodENIHasNoAllocations(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
		},
	}
	podENI := &v1beta1.PodENI{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
		},
		Spec: v1beta1.PodENISpec{
			Allocations: []v1beta1.Allocation{},
		},
	}

	_ = fakeClient.Create(context.Background(), podENI)

	zone, err := getPreviousZone(context.Background(), fakeClient, pod)
	assert.NoError(t, err)
	assert.Equal(t, "", zone)
}

func TestPreviousZoneReturnsZoneWhenPodENIHasAllocations(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
		},
	}
	podENI := &v1beta1.PodENI{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
		},
		Spec: v1beta1.PodENISpec{
			Zone: "aa-1a",
			Allocations: []v1beta1.Allocation{
				{},
			},
		},
	}

	_ = fakeClient.Create(context.Background(), podENI)

	zone, err := getPreviousZone(context.Background(), fakeClient, pod)
	assert.NoError(t, err)
	assert.Equal(t, "aa-1a", zone)
}

func TestMatchOnePodNetworkingReturnsNilWhenNoPodNetworkings(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
		},
	}

	podNetworking, err := matchOnePodNetworking(context.Background(), "default", fakeClient, pod)
	assert.NoError(t, err)
	assert.Nil(t, podNetworking)
}

func TestMatchOnePodNetworkingReturnsNilWhenNoMatchingPodNetworking(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "default",
			},
		},
		&v1beta1.PodNetworking{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-podnetworking",
			},
			Spec: v1beta1.PodNetworkingSpec{
				Selector: v1beta1.Selector{
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"key": "different"},
					},
				},
			},
			Status: v1beta1.PodNetworkingStatus{
				Status: v1beta1.NetworkingStatusReady,
			},
		},
	).Build()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			Labels:    map[string]string{"key": "value"},
		},
	}

	result, err := matchOnePodNetworking(context.Background(), "default", fakeClient, pod)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestMatchOnePodNetworkingReturnsPodNetworkingWhenMatchingPodSelector(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "default",
			},
		},
		&v1beta1.PodNetworking{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-podnetworking",
			},
			Spec: v1beta1.PodNetworkingSpec{
				Selector: v1beta1.Selector{
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"key": "value"},
					},
				},
			},
			Status: v1beta1.PodNetworkingStatus{
				Status: v1beta1.NetworkingStatusReady,
			},
		},
	).Build()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			Labels:    map[string]string{"key": "value"},
		},
	}

	result, err := matchOnePodNetworking(context.Background(), "default", fakeClient, pod)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "test-podnetworking", result.Name)
}

func TestMatchOnePodNetworkingReturnsPodNetworkingWhenMatchingNamespaceSelector(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "default",
				Labels: map[string]string{"key": "value"},
			},
		},
		&v1beta1.PodNetworking{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-podnetworking",
			},
			Spec: v1beta1.PodNetworkingSpec{
				Selector: v1beta1.Selector{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"key": "value"},
					},
				},
			},
			Status: v1beta1.PodNetworkingStatus{
				Status: v1beta1.NetworkingStatusReady,
			},
		},
	).Build()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
		},
	}

	result, err := matchOnePodNetworking(context.Background(), "default", fakeClient, pod)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "test-podnetworking", result.Name)
}

func TestMatchOnePodNetworkingReturnsNilWhenPodNetworkingNotReady(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "default",
			},
		},
		&v1beta1.PodNetworking{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-podnetworking",
			},
			Spec: v1beta1.PodNetworkingSpec{
				Selector: v1beta1.Selector{
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"key": "value"},
					},
				},
			},
			Status: v1beta1.PodNetworkingStatus{
				Status: v1beta1.NetworkingStatusFail,
			},
		},
	).Build()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			Labels:    map[string]string{"key": "value"},
		},
	}

	result, err := matchOnePodNetworking(context.Background(), "default", fakeClient, pod)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func Test_getPodNetworkRequests(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1beta1.AddToScheme(scheme)

	type args struct {
		ctx    context.Context
		client client.Client
		anno   map[string]string
	}
	tests := []struct {
		name    string
		args    args
		want    []controlplane.PodNetworks
		want1   []string
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "returns error when annotation is invalid",
			args: args{
				ctx:    context.Background(),
				client: fake.NewClientBuilder().Build(),
				anno:   map[string]string{"k8s.aliyun.com/pod-networks-request": "annotation"},
			},
			want:    nil,
			want1:   nil,
			wantErr: assert.Error,
		},
		{
			name: "returns nil when no pod network refs",
			args: args{
				ctx:    context.Background(),
				client: fake.NewClientBuilder().Build(),
				anno:   map[string]string{},
			},
			want:    nil,
			want1:   nil,
			wantErr: assert.NoError,
		},
		{
			name: "returns pod networks and zones",
			args: args{
				ctx: context.Background(),
				client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
					&v1beta1.PodNetworking{
						ObjectMeta: metav1.ObjectMeta{Name: "test-network"},
						Spec: v1beta1.PodNetworkingSpec{
							ENIOptions: v1beta1.ENIOptions{},
							AllocationType: v1beta1.AllocationType{
								Type: v1beta1.IPAllocTypeElastic,
							},
							Selector:         v1beta1.Selector{},
							SecurityGroupIDs: []string{"sg-1"},
							VSwitchOptions:   []string{"vsw-a", "vsw-b"},
							VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
								VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyMost,
							}},
						Status: v1beta1.PodNetworkingStatus{
							Status: v1beta1.NetworkingStatusReady,
							VSwitches: []v1beta1.VSwitch{
								{Zone: "zone-a", ID: "vsw-a"},
								{Zone: "zone-b", ID: "vsw-b"},
							},
						},
					},
				).Build(),
				anno: map[string]string{
					"k8s.aliyun.com/pod-networks-request": `[{"network": "test-network"}]`,
				},
			},
			want: []controlplane.PodNetworks{
				{
					Interface:        "eth0",
					VSwitchOptions:   []string{"vsw-a", "vsw-b"},
					SecurityGroupIDs: []string{"sg-1"},
					ENIOptions:       v1beta1.ENIOptions{},
					VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
						VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyMost,
					},
					AllocationType: &v1beta1.AllocationType{Type: v1beta1.IPAllocTypeElastic},
				},
			},
			want1:   []string{"zone-a", "zone-b"},
			wantErr: assert.NoError,
		},
		{
			name: "returns error when pod networking not ready",
			args: args{
				ctx: context.Background(),
				client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
					&v1beta1.PodNetworking{
						ObjectMeta: metav1.ObjectMeta{Name: "test-network"},
						Status:     v1beta1.PodNetworkingStatus{Status: v1beta1.NetworkingStatusFail},
					},
				).Build(),
				anno: map[string]string{
					"k8s.aliyun.com/pod-networks-request": `[{"network": "test-network"}]`,
				},
			},
			want:    nil,
			want1:   nil,
			wantErr: assert.Error,
		},
		{
			name: "returns intersection of zones",
			args: args{
				ctx: context.Background(),
				client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
					&v1beta1.PodNetworking{
						ObjectMeta: metav1.ObjectMeta{Name: "network-1"},
						Spec: v1beta1.PodNetworkingSpec{
							ENIOptions: v1beta1.ENIOptions{},
							AllocationType: v1beta1.AllocationType{
								Type: v1beta1.IPAllocTypeElastic,
							},
							Selector:         v1beta1.Selector{},
							SecurityGroupIDs: []string{"sg-1"},
							VSwitchOptions:   []string{"vsw-a", "vsw-b"},
							VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
								VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyMost,
							}},
						Status: v1beta1.PodNetworkingStatus{
							Status: v1beta1.NetworkingStatusReady,
							VSwitches: []v1beta1.VSwitch{
								{Zone: "zone-a", ID: "vsw-a"},
								{Zone: "zone-b", ID: "vsw-b"},
							},
						},
					},
					&v1beta1.PodNetworking{
						ObjectMeta: metav1.ObjectMeta{Name: "network-2"},
						Spec: v1beta1.PodNetworkingSpec{
							ENIOptions: v1beta1.ENIOptions{},
							AllocationType: v1beta1.AllocationType{
								Type: v1beta1.IPAllocTypeElastic,
							},
							Selector:         v1beta1.Selector{},
							SecurityGroupIDs: []string{"sg-1"},
							VSwitchOptions:   []string{"vsw-b", "vsw-c"},
							VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
								VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyMost,
							}},
						Status: v1beta1.PodNetworkingStatus{
							Status: v1beta1.NetworkingStatusReady,
							VSwitches: []v1beta1.VSwitch{
								{Zone: "zone-b", ID: "vsw-b"},
								{Zone: "zone-c", ID: "vsw-c"},
							},
						},
					},
				).Build(),
				anno: map[string]string{
					"k8s.aliyun.com/pod-networks-request": `[{"network": "network-1","interfaceName":"eth0"}, {"network": "network-2","interfaceName":"eth1","defaultRoute": true}]`,
				},
			},
			want: []controlplane.PodNetworks{
				{
					Interface:        "eth0",
					VSwitchOptions:   []string{"vsw-a", "vsw-b"},
					SecurityGroupIDs: []string{"sg-1"},
					ENIOptions:       v1beta1.ENIOptions{},
					VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
						VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyMost,
					},
					AllocationType: &v1beta1.AllocationType{
						Type:            v1beta1.IPAllocTypeElastic,
						ReleaseStrategy: "",
						ReleaseAfter:    "",
					},
				},
				{
					Interface:        "eth1",
					VSwitchOptions:   []string{"vsw-b", "vsw-c"},
					SecurityGroupIDs: []string{"sg-1"},
					ENIOptions:       v1beta1.ENIOptions{},
					VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
						VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyMost,
					},
					DefaultRoute: true,
					AllocationType: &v1beta1.AllocationType{
						Type:            v1beta1.IPAllocTypeElastic,
						ReleaseStrategy: "",
						ReleaseAfter:    "",
					},
				},
			},
			want1:   []string{"zone-b"},
			wantErr: assert.NoError,
		},
		{
			name: "returns error when pod networkings have different ENI attach types",
			args: args{
				ctx: context.Background(),
				client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
					&v1beta1.PodNetworking{
						ObjectMeta: metav1.ObjectMeta{Name: "network-1"},
						Spec: v1beta1.PodNetworkingSpec{
							ENIOptions: v1beta1.ENIOptions{
								ENIAttachType: v1beta1.ENIOptionTypeDefault,
							},
							AllocationType: v1beta1.AllocationType{
								Type: v1beta1.IPAllocTypeElastic,
							},
							Selector:         v1beta1.Selector{},
							SecurityGroupIDs: []string{"sg-1"},
							VSwitchOptions:   []string{"vsw-a"},
							VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
								VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyRandom,
							}},
						Status: v1beta1.PodNetworkingStatus{
							Status: v1beta1.NetworkingStatusReady,
							VSwitches: []v1beta1.VSwitch{
								{Zone: "zone-a", ID: "vsw-a"},
							},
						},
					},
					&v1beta1.PodNetworking{
						ObjectMeta: metav1.ObjectMeta{Name: "network-2"},
						Spec: v1beta1.PodNetworkingSpec{
							ENIOptions: v1beta1.ENIOptions{
								ENIAttachType: v1beta1.ENIOptionTypeENI, // Different from network-1
							},
							AllocationType: v1beta1.AllocationType{
								Type: v1beta1.IPAllocTypeElastic,
							},
							Selector:         v1beta1.Selector{},
							SecurityGroupIDs: []string{"sg-1"},
							VSwitchOptions:   []string{"vsw-b"},
							VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
								VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyRandom,
							}},
						Status: v1beta1.PodNetworkingStatus{
							Status: v1beta1.NetworkingStatusReady,
							VSwitches: []v1beta1.VSwitch{
								{Zone: "zone-b", ID: "vsw-b"},
							},
						},
					},
				).Build(),
				anno: map[string]string{
					"k8s.aliyun.com/pod-networks-request": `[{"network": "network-1"}, {"network": "network-2"}]`,
				},
			},
			want:    nil,
			want1:   nil,
			wantErr: assert.Error,
		},
		{
			name: "allows multiple pod networkings with same ENI attach type",
			args: args{
				ctx: context.Background(),
				client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
					&v1beta1.PodNetworking{
						ObjectMeta: metav1.ObjectMeta{Name: "network-1"},
						Spec: v1beta1.PodNetworkingSpec{
							ENIOptions: v1beta1.ENIOptions{
								ENIAttachType: v1beta1.ENIOptionTypeENI,
							},
							AllocationType: v1beta1.AllocationType{
								Type: v1beta1.IPAllocTypeElastic,
							},
							Selector:         v1beta1.Selector{},
							SecurityGroupIDs: []string{"sg-1"},
							VSwitchOptions:   []string{"vsw-a"},
							VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
								VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyRandom,
							}},
						Status: v1beta1.PodNetworkingStatus{
							Status: v1beta1.NetworkingStatusReady,
							VSwitches: []v1beta1.VSwitch{
								{Zone: "zone-a", ID: "vsw-a"},
								{Zone: "zone-b", ID: "vsw-b"}, // Add common zone
							},
						},
					},
					&v1beta1.PodNetworking{
						ObjectMeta: metav1.ObjectMeta{Name: "network-2"},
						Spec: v1beta1.PodNetworkingSpec{
							ENIOptions: v1beta1.ENIOptions{
								ENIAttachType: v1beta1.ENIOptionTypeENI, // Same as network-1
							},
							AllocationType: v1beta1.AllocationType{
								Type: v1beta1.IPAllocTypeElastic,
							},
							Selector:         v1beta1.Selector{},
							SecurityGroupIDs: []string{"sg-1"},
							VSwitchOptions:   []string{"vsw-b"},
							VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
								VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyRandom,
							}},
						Status: v1beta1.PodNetworkingStatus{
							Status: v1beta1.NetworkingStatusReady,
							VSwitches: []v1beta1.VSwitch{
								{Zone: "zone-a", ID: "vsw-a"}, // Add common zone
								{Zone: "zone-b", ID: "vsw-b"},
							},
						},
					},
				).Build(),
				anno: map[string]string{
					"k8s.aliyun.com/pod-networks-request": `[{"network": "network-1"}, {"network": "network-2"}]`,
				},
			},
			want: []controlplane.PodNetworks{
				{
					Interface:        "eth0",
					VSwitchOptions:   []string{"vsw-a"},
					SecurityGroupIDs: []string{"sg-1"},
					ENIOptions: v1beta1.ENIOptions{
						ENIAttachType: v1beta1.ENIOptionTypeENI,
					},
					VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
						VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyRandom,
					},
					AllocationType: &v1beta1.AllocationType{
						Type:            v1beta1.IPAllocTypeElastic,
						ReleaseStrategy: "",
						ReleaseAfter:    "",
					},
				},
				{
					Interface:        "eth0",
					VSwitchOptions:   []string{"vsw-b"},
					SecurityGroupIDs: []string{"sg-1"},
					ENIOptions: v1beta1.ENIOptions{
						ENIAttachType: v1beta1.ENIOptionTypeENI,
					},
					VSwitchSelectOptions: v1beta1.VSwitchSelectOptions{
						VSwitchSelectionPolicy: v1beta1.VSwitchSelectionPolicyRandom,
					},
					AllocationType: &v1beta1.AllocationType{
						Type:            v1beta1.IPAllocTypeElastic,
						ReleaseStrategy: "",
						ReleaseAfter:    "",
					},
				},
			},
			want1:   []string{"zone-a", "zone-b"},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1, err := getPodNetworkRequests(tt.args.ctx, tt.args.client, tt.args.anno)
			if !tt.wantErr(t, err, fmt.Sprintf("getPodNetworkRequests(%v, %v, %v)", tt.args.ctx, tt.args.client, tt.args.anno)) {
				return
			}
			assert.Equalf(t, tt.want, got, "getPodNetworkRequests(%v, %v, %v)", tt.args.ctx, tt.args.client, tt.args.anno)
			assert.Equalf(t, tt.want1, got1, "getPodNetworkRequests(%v, %v, %v)", tt.args.ctx, tt.args.client, tt.args.anno)
		})
	}
}

// ==============================================================================
// podNetworkingWebhook Tests (Lines 259-284)
// ==============================================================================

func TestPodNetworkingWebhook_ConfigFromConfigMap_NotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	podNetworking := &v1beta1.PodNetworking{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-podnetworking",
		},
		Spec: v1beta1.PodNetworkingSpec{
			SecurityGroupIDs: []string{},
			VSwitchOptions:   []string{},
		},
	}
	original, _ := json.Marshal(podNetworking)

	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Kind: "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: original,
			},
		},
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock ConfigFromConfigMap to return NotFound error
	patches.ApplyFunc(daemon.ConfigFromConfigMap, func(ctx context.Context, client client.Client, nodeName string) (*daemon.Config, error) {
		return nil, k8sErr.NewNotFound(schema.GroupResource{Resource: "configmaps"}, "eni-config")
	})

	ctx := context.Background()
	resp := podNetworkingWebhook(ctx, req, fakeClient)

	assert.True(t, resp.Allowed)
	assert.Equal(t, "no terway eni-config found", resp.Result.Message)
}

func TestPodNetworkingWebhook_ConfigFromConfigMap_OtherError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	podNetworking := &v1beta1.PodNetworking{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-podnetworking",
		},
		Spec: v1beta1.PodNetworkingSpec{
			SecurityGroupIDs: []string{},
			VSwitchOptions:   []string{},
		},
	}
	original, _ := json.Marshal(podNetworking)

	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Kind: "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: original,
			},
		},
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock ConfigFromConfigMap to return other error
	patches.ApplyFunc(daemon.ConfigFromConfigMap, func(ctx context.Context, client client.Client, nodeName string) (*daemon.Config, error) {
		return nil, errors.New("internal server error")
	})

	ctx := context.Background()
	resp := podNetworkingWebhook(ctx, req, fakeClient)

	assert.False(t, resp.Allowed)
	assert.Equal(t, int32(1), resp.Result.Code)
}

func TestPodNetworkingWebhook_FillSecurityGroupIDs(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	podNetworking := &v1beta1.PodNetworking{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-podnetworking",
		},
		Spec: v1beta1.PodNetworkingSpec{
			SecurityGroupIDs: []string{}, // Empty
			VSwitchOptions:   []string{"vsw-1"},
		},
	}
	original, _ := json.Marshal(podNetworking)

	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Kind: "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: original,
			},
		},
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock ConfigFromConfigMap to return config
	mockConfig := &daemon.Config{
		SecurityGroups: []string{"sg-1", "sg-2"},
		VSwitches: map[string][]string{
			"zone-1": {"vsw-1"},
		},
	}
	patches.ApplyFunc(daemon.ConfigFromConfigMap, func(ctx context.Context, client client.Client, nodeName string) (*daemon.Config, error) {
		return mockConfig, nil
	})

	ctx := context.Background()
	resp := podNetworkingWebhook(ctx, req, fakeClient)

	assert.True(t, resp.Allowed)
	assert.NotEmpty(t, resp.Patches)
}

func TestPodNetworkingWebhook_FillVSwitchOptions(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	podNetworking := &v1beta1.PodNetworking{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-podnetworking",
		},
		Spec: v1beta1.PodNetworkingSpec{
			SecurityGroupIDs: []string{"sg-1"},
			VSwitchOptions:   []string{}, // Empty
		},
	}
	original, _ := json.Marshal(podNetworking)

	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Kind: "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: original,
			},
		},
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock ConfigFromConfigMap to return config
	mockConfig := &daemon.Config{
		SecurityGroups: []string{"sg-1"},
		VSwitches: map[string][]string{
			"zone-1": {"vsw-1", "vsw-2"},
			"zone-2": {"vsw-3"},
		},
	}
	patches.ApplyFunc(daemon.ConfigFromConfigMap, func(ctx context.Context, client client.Client, nodeName string) (*daemon.Config, error) {
		return mockConfig, nil
	})

	ctx := context.Background()
	resp := podNetworkingWebhook(ctx, req, fakeClient)

	assert.True(t, resp.Allowed)
	assert.NotEmpty(t, resp.Patches)
}

func TestPodNetworkingWebhook_FillBothSecurityGroupIDsAndVSwitchOptions(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	podNetworking := &v1beta1.PodNetworking{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-podnetworking",
		},
		Spec: v1beta1.PodNetworkingSpec{
			SecurityGroupIDs: []string{}, // Empty
			VSwitchOptions:   []string{}, // Empty
		},
	}
	original, _ := json.Marshal(podNetworking)

	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Kind: "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: original,
			},
		},
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock ConfigFromConfigMap to return config
	mockConfig := &daemon.Config{
		SecurityGroups: []string{"sg-1", "sg-2"},
		VSwitches: map[string][]string{
			"zone-1": {"vsw-1", "vsw-2"},
		},
	}
	patches.ApplyFunc(daemon.ConfigFromConfigMap, func(ctx context.Context, client client.Client, nodeName string) (*daemon.Config, error) {
		return mockConfig, nil
	})

	ctx := context.Background()
	resp := podNetworkingWebhook(ctx, req, fakeClient)

	assert.True(t, resp.Allowed)
	assert.NotEmpty(t, resp.Patches)
}

func TestPodNetworkingWebhook_JsonMarshalError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	podNetworking := &v1beta1.PodNetworking{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-podnetworking",
		},
		Spec: v1beta1.PodNetworkingSpec{
			SecurityGroupIDs: []string{},
			VSwitchOptions:   []string{},
		},
	}
	original, _ := json.Marshal(podNetworking)

	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Kind: "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: original,
			},
		},
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock ConfigFromConfigMap to return config
	mockConfig := &daemon.Config{
		SecurityGroups: []string{"sg-1"},
		VSwitches: map[string][]string{
			"zone-1": {"vsw-1"},
		},
	}
	patches.ApplyFunc(daemon.ConfigFromConfigMap, func(ctx context.Context, client client.Client, nodeName string) (*daemon.Config, error) {
		return mockConfig, nil
	})

	// Mock json.Marshal to return error
	patches.ApplyFunc(json.Marshal, func(v interface{}) ([]byte, error) {
		// Only fail for PodNetworking type
		if _, ok := v.(*v1beta1.PodNetworking); ok {
			return nil, errors.New("marshal error")
		}
		// Use original implementation for other types
		return json.Marshal(v)
	})

	ctx := context.Background()
	resp := podNetworkingWebhook(ctx, req, fakeClient)

	assert.False(t, resp.Allowed)
	assert.Equal(t, int32(1), resp.Result.Code)
}

func TestPodNetworkingWebhook_SuccessfulPatch(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	podNetworking := &v1beta1.PodNetworking{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-podnetworking",
		},
		Spec: v1beta1.PodNetworkingSpec{
			SecurityGroupIDs: []string{},
			VSwitchOptions:   []string{},
		},
	}
	original, _ := json.Marshal(podNetworking)

	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Kind: "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: original,
			},
		},
	}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock ConfigFromConfigMap to return config
	mockConfig := &daemon.Config{
		SecurityGroups: []string{"sg-1", "sg-2"},
		VSwitches: map[string][]string{
			"zone-1": {"vsw-1", "vsw-2"},
			"zone-2": {"vsw-3"},
		},
	}
	patches.ApplyFunc(daemon.ConfigFromConfigMap, func(ctx context.Context, client client.Client, nodeName string) (*daemon.Config, error) {
		return mockConfig, nil
	})

	ctx := context.Background()
	resp := podNetworkingWebhook(ctx, req, fakeClient)

	assert.True(t, resp.Allowed)
	assert.NotEmpty(t, resp.Patches)
	assert.Equal(t, "ok", resp.Result.Message)
}

func TestPodNetworkingWebhook_AllSet_NoPatch(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1beta1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	podNetworking := &v1beta1.PodNetworking{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-podnetworking",
		},
		Spec: v1beta1.PodNetworkingSpec{
			SecurityGroupIDs: []string{"sg-1"},
			VSwitchOptions:   []string{"vsw-1"},
		},
	}
	original, _ := json.Marshal(podNetworking)

	req := webhook.AdmissionRequest{
		AdmissionRequest: v1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Kind: "PodNetworking",
			},
			Object: runtime.RawExtension{
				Raw: original,
			},
		},
	}

	ctx := context.Background()
	resp := podNetworkingWebhook(ctx, req, fakeClient)

	assert.True(t, resp.Allowed)
	assert.Equal(t, "podNetworking all set", resp.Result.Message)
	assert.Empty(t, resp.Patches)
}
