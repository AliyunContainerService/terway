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
	"fmt"
	"strings"
	"time"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/types"

	corev1 "k8s.io/api/core/v1"
)

const defaultReleaseAfter = 10 * time.Minute

// PodConf is the type describe the eni config for this pod
type PodConf struct {
	SecurityGroups []string
	VSwitchID      string
	UseFixedIP     bool

	IPAllocType         v1beta1.IPAllocType
	FixedIPReleaseAfter time.Duration
	ReleaseStrategy     v1beta1.ReleaseStrategy

	// from node
	InstanceID string
	Zone       string
	TrunkENIID string
}

// SetNodeConf parse from node
func (p *PodConf) SetNodeConf(node *corev1.Node) error {
	ids := strings.Split(node.Spec.ProviderID, ".")
	if len(ids) < 2 {
		return fmt.Errorf("error parse providerID %s", node.Spec.ProviderID)
	}
	p.InstanceID = ids[1]

	p.TrunkENIID = node.GetAnnotations()[types.TrunkOn]
	if p.TrunkENIID == "" {
		return fmt.Errorf("trunk eni id not found, this may dure to terway agent is not started")
	}

	zone, ok := node.GetLabels()[corev1.LabelZoneFailureDomainStable]
	if ok {
		p.Zone = zone
		return nil
	}
	zone, ok = node.GetLabels()[corev1.LabelZoneFailureDomain]
	if ok {
		p.Zone = zone
		return nil
	}
	return fmt.Errorf("cat not found zone label from node %s", node.Name)
}

// SetPodENIConf parse from podENI
func (p *PodConf) SetPodENIConf(podENI *v1beta1.PodENI) error {
	p.SecurityGroups = podENI.Spec.Allocation.ENI.SecurityGroupIDs
	p.VSwitchID = podENI.Spec.Allocation.ENI.VSwitchID
	switch podENI.Spec.Allocation.IPType.Type {
	case v1beta1.IPAllocTypeFixed:
		p.UseFixedIP = true
		p.FixedIPReleaseAfter = defaultReleaseAfter
		if podENI.Spec.Allocation.IPType.ReleaseAfter != "" {
			d, err := time.ParseDuration(podENI.Spec.Allocation.IPType.ReleaseAfter)
			if err != nil {
				return fmt.Errorf("error parse ReleaseAfter, %w", err)
			}
			p.FixedIPReleaseAfter = d
		}
		p.ReleaseStrategy = podENI.Spec.Allocation.IPType.ReleaseStrategy
	}

	return nil
}

// SetPodNetworkingConf SetPodNetworkingConf set config from podNetworking
func (p *PodConf) SetPodNetworkingConf(podNetworking *v1beta1.PodNetworking) error {
	switch podNetworking.Spec.IPType.Type {
	case v1beta1.IPAllocTypeElastic:
		p.UseFixedIP = false
		p.IPAllocType = v1beta1.IPAllocTypeElastic
	case v1beta1.IPAllocTypeFixed:
		p.UseFixedIP = true
		p.IPAllocType = v1beta1.IPAllocTypeFixed

		p.FixedIPReleaseAfter = defaultReleaseAfter
		if podNetworking.Spec.IPType.ReleaseAfter != "" {
			d, err := time.ParseDuration(podNetworking.Spec.IPType.ReleaseAfter)
			if err != nil {
				return fmt.Errorf("error parse ReleaseAfter, %w", err)
			}
			p.FixedIPReleaseAfter = d
		}
		p.ReleaseStrategy = v1beta1.ReleaseStrategyTTL
		if podNetworking.Spec.IPType.ReleaseStrategy != "" {
			p.ReleaseStrategy = podNetworking.Spec.IPType.ReleaseStrategy
		}
	}
	if len(podNetworking.Spec.SecurityGroupIDs) == 0 {
		return fmt.Errorf("podNetworking %s SecurityGroupIDs is empty", podNetworking.Name)
	}
	p.SecurityGroups = podNetworking.Spec.SecurityGroupIDs
	if len(podNetworking.Spec.VSwitchIDs) == 0 {
		return fmt.Errorf("podNetworking %s vSwitchIDs is empty", podNetworking.Name)
	}

	return nil
}
