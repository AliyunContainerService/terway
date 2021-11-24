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

package common

import (
	"fmt"
	"strings"

	"github.com/AliyunContainerService/terway/types"

	corev1 "k8s.io/api/core/v1"
)

// NodeInfo is the type describe the eni config for this pod
type NodeInfo struct {
	InstanceID string
	Zone       string
	TrunkENIID string
}

func NewNodeInfo(node *corev1.Node) (*NodeInfo, error) {
	res := &NodeInfo{}
	ids := strings.Split(node.Spec.ProviderID, ".")
	if len(ids) < 2 {
		return nil, fmt.Errorf("error parse providerID %s", node.Spec.ProviderID)
	}
	res.InstanceID = ids[1]

	res.TrunkENIID = node.GetAnnotations()[types.TrunkOn]

	zone, ok := node.GetLabels()[corev1.LabelZoneFailureDomainStable]
	if ok {
		res.Zone = zone
		return res, nil
	}
	zone, ok = node.GetLabels()[corev1.LabelZoneFailureDomain]
	if ok {
		res.Zone = zone
		return res, nil
	}
	return nil, fmt.Errorf("cat not found zone label from node %s", node.Name)
}
