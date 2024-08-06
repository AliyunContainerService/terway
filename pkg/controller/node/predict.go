/*
Copyright 2024 Terway Authors.

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
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/AliyunContainerService/terway/pkg/utils"
)

type predicateForNodeEvent struct {
	predicate.Funcs
}

// Create returns true if the Create event should be processed
func (p *predicateForNodeEvent) Create(e event.CreateEvent) bool {
	node, ok := e.Object.(*corev1.Node)
	if !ok {
		return false
	}
	return isECSNode(node)
}

// Delete returns true if the Delete event should be processed
func (p *predicateForNodeEvent) Delete(e event.DeleteEvent) bool {
	node, ok := e.Object.(*corev1.Node)
	if !ok {
		return false
	}
	return isECSNode(node)
}

// Update returns true if the Update event should be processed
func (p *predicateForNodeEvent) Update(e event.UpdateEvent) bool {
	node, ok := e.ObjectNew.(*corev1.Node)
	if !ok {
		return false
	}
	return isECSNode(node)
}

// Generic returns true if the Generic event should be processed
func (p *predicateForNodeEvent) Generic(e event.GenericEvent) bool {
	node, ok := e.Object.(*corev1.Node)
	if !ok {
		return false
	}
	return isECSNode(node)
}

func isECSNode(node *corev1.Node) bool {
	if utils.ISLinJunNode(node) {
		return false
	}
	if utils.ISVKNode(node) {
		return false
	}
	return true
}
