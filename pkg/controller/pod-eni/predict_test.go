/*
Copyright 2021 Terway Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package podeni

import (
	"testing"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

func TestUpdateFunc(t *testing.T) {
	t.Run("same content after normalizing returns false", func(t *testing.T) {
		oldObj := &v1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-eni-1", ResourceVersion: "100"},
			Spec:       v1beta1.PodENISpec{Zone: "zone-a"},
			Status: v1beta1.PodENIStatus{
				Phase:       v1beta1.ENIPhaseBind,
				PodLastSeen: metav1.Unix(12345, 0),
			},
		}
		newObj := oldObj.DeepCopy()
		newObj.ResourceVersion = "101"
		newObj.Status.PodLastSeen = metav1.Unix(99999, 0)

		e := event.TypedUpdateEvent[*v1beta1.PodENI]{
			ObjectOld: oldObj,
			ObjectNew: newObj,
		}
		result := updateFunc(e)
		assert.False(t, result)
	})

	t.Run("different spec returns true", func(t *testing.T) {
		oldObj := &v1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-eni-1", ResourceVersion: "100"},
			Spec:       v1beta1.PodENISpec{Zone: "zone-a"},
			Status:     v1beta1.PodENIStatus{Phase: v1beta1.ENIPhaseBind},
		}
		newObj := oldObj.DeepCopy()
		newObj.Spec.Zone = "zone-b"

		e := event.TypedUpdateEvent[*v1beta1.PodENI]{
			ObjectOld: oldObj,
			ObjectNew: newObj,
		}
		result := updateFunc(e)
		assert.True(t, result)
	})

	t.Run("different status phase returns true", func(t *testing.T) {
		oldObj := &v1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-eni-1"},
			Spec:       v1beta1.PodENISpec{Zone: "zone-a"},
			Status:     v1beta1.PodENIStatus{Phase: v1beta1.ENIPhaseBind},
		}
		newObj := oldObj.DeepCopy()
		newObj.Status.Phase = v1beta1.ENIPhaseBinding

		e := event.TypedUpdateEvent[*v1beta1.PodENI]{
			ObjectOld: oldObj,
			ObjectNew: newObj,
		}
		result := updateFunc(e)
		assert.True(t, result)
	})
}

func TestPodRef(t *testing.T) {
	ref := podRef("default", "my-pod")
	assert.Equal(t, "Pod", ref.Kind)
	assert.Equal(t, "default", ref.Namespace)
	assert.Equal(t, "my-pod", ref.Name)
}

func TestAllocIDs(t *testing.T) {
	tests := []struct {
		name   string
		podENI *v1beta1.PodENI
		want   []string
	}{
		{
			name:   "nil allocations",
			podENI: &v1beta1.PodENI{},
			want:   nil,
		},
		{
			name: "single allocation",
			podENI: &v1beta1.PodENI{
				Spec: v1beta1.PodENISpec{
					Allocations: []v1beta1.Allocation{
						{ENI: v1beta1.ENI{ID: "eni-1"}},
					},
				},
			},
			want: []string{"eni-1"},
		},
		{
			name: "multiple allocations",
			podENI: &v1beta1.PodENI{
				Spec: v1beta1.PodENISpec{
					Allocations: []v1beta1.Allocation{
						{ENI: v1beta1.ENI{ID: "eni-1"}},
						{ENI: v1beta1.ENI{ID: "eni-2"}},
					},
				},
			},
			want: []string{"eni-1", "eni-2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := allocIDs(tt.podENI)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPodNumaHints(t *testing.T) {
	tests := []struct {
		name string
		anno map[string]string
		want []int
	}{
		{
			name: "empty annotation",
			anno: map[string]string{},
			want: nil,
		},
		{
			name: "missing cpuSet key",
			anno: map[string]string{"other": "value"},
			want: nil,
		},
		{
			name: "invalid json",
			anno: map[string]string{"cpuSet": "invalid-json"},
			want: nil,
		},
		{
			name: "valid numa json",
			anno: map[string]string{
				"cpuSet": `{"container1": {"0": true, "1": false}}`,
			},
			want: []int{0, 1},
		},
		{
			name: "empty cpuSet value",
			anno: map[string]string{"cpuSet": ""},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := podNumaHints(tt.anno)
			assert.ElementsMatch(t, tt.want, got)
		})
	}
}

func TestEniFilter(t *testing.T) {
	m := &ReconcilePodENI{}

	tests := []struct {
		name   string
		eni    *aliyunClient.NetworkInterface
		filter map[string]string
		want   bool
	}{
		{
			name:   "empty filter always matches",
			eni:    &aliyunClient.NetworkInterface{},
			filter: map[string]string{},
			want:   true,
		},
		{
			name: "filter key not found in tags",
			eni: &aliyunClient.NetworkInterface{
				Tags: []ecs.Tag{{TagKey: "other", TagValue: "val"}},
			},
			filter: map[string]string{"missing-key": "val"},
			want:   false,
		},
		{
			name: "filter key found but wrong value",
			eni: &aliyunClient.NetworkInterface{
				Tags: []ecs.Tag{{TagKey: "env", TagValue: "prod"}},
			},
			filter: map[string]string{"env": "staging"},
			want:   false,
		},
		{
			name: "all filter tags match",
			eni: &aliyunClient.NetworkInterface{
				Tags: []ecs.Tag{
					{TagKey: "env", TagValue: "prod"},
					{TagKey: "team", TagValue: "infra"},
				},
			},
			filter: map[string]string{"env": "prod", "team": "infra"},
			want:   true,
		},
		{
			name: "partial match - one tag matches but another missing",
			eni: &aliyunClient.NetworkInterface{
				Tags: []ecs.Tag{{TagKey: "env", TagValue: "prod"}},
			},
			filter: map[string]string{"env": "prod", "team": "infra"},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.eniFilter(tt.eni, tt.filter)
			assert.Equal(t, tt.want, got)
		})
	}
}
