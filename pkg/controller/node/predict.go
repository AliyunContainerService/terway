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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"
)

type predicateForNodeEvent struct {
	predicate.Funcs

	supportEFLO        bool
	nodeLabelWhiteList map[string]string
}

// Create returns true if the Create event should be processed
func (p *predicateForNodeEvent) Create(e event.CreateEvent) bool {
	return p.predicateNode(e.Object)
}

// Delete returns true if the Delete event should be processed
func (p *predicateForNodeEvent) Delete(e event.DeleteEvent) bool {
	return p.predicateNode(e.Object)
}

// Update returns true if the Update event should be processed
func (p *predicateForNodeEvent) Update(e event.UpdateEvent) bool {
	return p.predicateNode(e.ObjectNew)
}

// Generic returns true if the Generic event should be processed
func (p *predicateForNodeEvent) Generic(e event.GenericEvent) bool {
	return p.predicateNode(e.Object)
}

func (p *predicateForNodeEvent) predicateNode(o client.Object) bool {
	node, ok := o.(*corev1.Node)
	if !ok {
		return false
	}

	if p.nodeLabelWhiteList != nil {
		if !utils.ContainsAll(node.Labels, p.nodeLabelWhiteList) {
			return false
		}
	}

	if node.Labels[corev1.LabelTopologyRegion] == "" {
		return false
	}

	if types.IgnoredByTerway(node.Labels) {
		return false
	}

	if !p.supportEFLO && utils.ISLinJunNode(node.Labels) {
		return false
	}

	if utils.ISVKNode(node) {
		return false
	}

	return true
}
