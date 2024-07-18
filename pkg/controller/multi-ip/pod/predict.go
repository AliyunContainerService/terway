/*
Copyright 2022 Terway Authors.

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
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type predicateForPodEvent struct {
	predicate.Funcs
}

func (p *predicateForPodEvent) Create(e event.CreateEvent) bool {
	return needProcess(e.Object)
}

func (p *predicateForPodEvent) Update(e event.UpdateEvent) bool {
	return needProcess(e.ObjectNew)
}

func (p *predicateForPodEvent) Delete(e event.DeleteEvent) bool {
	return needProcess(e.Object)
}

func (p *predicateForPodEvent) Generic(e event.GenericEvent) bool {
	return needProcess(e.Object)
}

// needProcess filter pod which is ready to process
func needProcess(obj interface{}) bool {
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

	if types.PodUseENI(pod) {
		return false
	}

	if !utils.PodSandboxExited(pod) && pod.Status.PodIP != "" {
		return false
	}
	return true
}
