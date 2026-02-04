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

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

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
