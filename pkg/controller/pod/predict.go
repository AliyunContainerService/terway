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

package pod

import (
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/AliyunContainerService/terway/types"
)

type predicateForPodEvent struct {
	predicate.Funcs

	crdMode bool
}

func (p *predicateForPodEvent) Create(e event.CreateEvent) bool {
	return OKToProcess(e.Object, p.crdMode)
}

func (p *predicateForPodEvent) Update(e event.UpdateEvent) bool {
	oldPod, ok := e.ObjectOld.(*corev1.Pod)
	if !ok {
		return false
	}
	newPod, ok := e.ObjectNew.(*corev1.Pod)
	if !ok {
		return false
	}

	if newPod.Spec.HostNetwork {
		return false
	}
	if newPod.Spec.NodeName == "" {
		return false
	}

	if types.IgnoredByTerway(newPod.Labels) {
		return false
	}

	if !p.crdMode {
		if !types.PodUseENI(oldPod) {
			return false
		}
		if !types.PodUseENI(newPod) {
			return false
		}
	}

	oldPodCopy := oldPod.DeepCopy()
	newPodCopy := newPod.DeepCopy()

	oldPodCopy.ResourceVersion = ""
	newPodCopy.ResourceVersion = ""

	oldPodCopy.Status = corev1.PodStatus{Phase: oldPod.Status.Phase}
	newPodCopy.Status = corev1.PodStatus{Phase: newPod.Status.Phase}

	return !reflect.DeepEqual(&oldPodCopy, &newPodCopy)
}

func (p *predicateForPodEvent) Delete(e event.DeleteEvent) bool {
	return OKToProcess(e.Object, p.crdMode)
}

func (p *predicateForPodEvent) Generic(e event.GenericEvent) bool {
	return OKToProcess(e.Object, p.crdMode)
}

// OKToProcess filter pod which is ready to process
func OKToProcess(obj interface{}, crdMode bool) bool {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return false
	}
	if pod.Spec.NodeName == "" {
		return false
	}

	if pod.Spec.HostNetwork {
		return false
	}

	if types.IgnoredByTerway(pod.Labels) {
		return false
	}

	if crdMode {
		return true
	}

	// 1. process pods only enable trunk
	// 2. if pod turn from trunk to normal pod, assume delete is called
	// 3. podENI will do remain GC if resource is leaked
	return types.PodUseENI(pod)
}
