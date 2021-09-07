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
	"reflect"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type predicateForPodENIEvent struct {
	predicate.Funcs
}

func (p *predicateForPodENIEvent) Update(e event.UpdateEvent) bool {
	oldPodENI, ok := e.ObjectOld.(*v1beta1.PodENI)
	if !ok {
		return false
	}
	newPodENI, ok := e.ObjectNew.(*v1beta1.PodENI)
	if !ok {
		return false
	}

	oldPodENICopy := oldPodENI.DeepCopy()
	newPodENICopy := newPodENI.DeepCopy()

	oldPodENICopy.ResourceVersion = ""
	newPodENICopy.ResourceVersion = ""
	oldPodENICopy.Status.PodLastSeen = metav1.Unix(0, 0)
	newPodENICopy.Status.PodLastSeen = metav1.Unix(0, 0)

	return !reflect.DeepEqual(&oldPodENICopy, &newPodENICopy)
}
