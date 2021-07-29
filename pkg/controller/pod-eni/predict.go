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
	"strconv"

	"github.com/AliyunContainerService/terway/types"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type predicateForPodEvent struct {
	predicate.Funcs
}

func (p *predicateForPodEvent) Create(e event.CreateEvent) bool {
	return OKToProcess(e.Object)
}

func (p *predicateForPodEvent) Update(e event.UpdateEvent) bool {
	return OKToProcess(e.ObjectNew)
}

func (p *predicateForPodEvent) Delete(e event.DeleteEvent) bool {
	return OKToProcess(e.Object)
}

func (p *predicateForPodEvent) Generic(e event.GenericEvent) bool {
	return OKToProcess(e.Object)
}

// OKToProcess filter pod which is ready to process
func OKToProcess(obj interface{}) bool {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return false
	}
	if pod.Spec.NodeName == "" {
		return false
	}
	key, ok := pod.GetAnnotations()[types.PodENI]
	if !ok {
		return false
	}
	v, err := strconv.ParseBool(key)
	if err != nil {
		return false
	}
	return v
}
