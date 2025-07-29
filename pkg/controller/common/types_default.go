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

	corev1 "k8s.io/api/core/v1"

	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"
)

func NewNodeInfo(node *corev1.Node) (*NodeInfo, error) {
	res := &NodeInfo{NodeName: node.Name}

	if utils.ISLinJunNode(node.Labels) {
		res.InstanceID = node.Spec.ProviderID
	} else {
		ids := strings.Split(node.Spec.ProviderID, ".")
		if len(ids) < 2 {
			return nil, fmt.Errorf("error parse providerID %s", node.Spec.ProviderID)
		}
		res.InstanceID = ids[1]
	}
	if res.InstanceID == "" {
		return nil, fmt.Errorf("can not found instanceID from node %s", node.Name)
	}

	res.TrunkENIID = node.GetAnnotations()[types.TrunkOn]

	res.RegionID = node.Labels[corev1.LabelTopologyRegion]
	if res.RegionID == "" {
		return nil, fmt.Errorf("can not found regionID from node %s", node.Name)
	}

	res.InstanceType = node.Labels[corev1.LabelInstanceTypeStable]
	if res.InstanceType == "" {
		return nil, fmt.Errorf("can not found instance type from node %s", node.Name)
	}

	zone, ok := node.GetLabels()[corev1.LabelTopologyZone]
	if ok {
		res.ZoneID = zone
		return res, nil
	}
	zone, ok = node.GetLabels()[corev1.LabelZoneFailureDomain]
	if ok {
		res.ZoneID = zone
		return res, nil
	}
	return nil, fmt.Errorf("can not found zone label from node %s", node.Name)
}
