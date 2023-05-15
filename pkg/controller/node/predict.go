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

package node

import (
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type predicateForNodeEvent struct {
	predicate.Funcs
}

func (p *predicateForNodeEvent) Create(e event.CreateEvent) bool {
	node, ok := e.Object.(*corev1.Node)
	if !ok {
		return false
	}

	if types.IgnoredByTerway(node.Labels) {
		return false
	}

	return !utils.ISVKNode(node)
}

func (p *predicateForNodeEvent) Update(e event.UpdateEvent) bool {
	node, ok := e.ObjectNew.(*corev1.Node)
	if !ok {
		return false
	}

	if types.IgnoredByTerway(node.Labels) {
		return false
	}

	return !utils.ISVKNode(node)
}

func (p *predicateForNodeEvent) Delete(e event.DeleteEvent) bool {
	node, ok := e.Object.(*corev1.Node)
	if !ok {
		return false
	}

	if types.IgnoredByTerway(node.Labels) {
		return false
	}

	return !utils.ISVKNode(node)
}
