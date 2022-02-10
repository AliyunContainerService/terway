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

package podnetworking

import (
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type predicateForPodnetwokringEvent struct {
	predicate.Funcs
}

func (p *predicateForPodnetwokringEvent) Update(e event.UpdateEvent) bool {
	newPodNetworking, ok := e.ObjectNew.(*v1beta1.PodNetworking)
	if !ok {
		return false
	}

	if newPodNetworking.Status.Status == "" {
		return true
	}

	return false
}
