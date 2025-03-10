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

package controlplane

import (
	"encoding/json"
	"fmt"

	terwayTypes "github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/route"

	corev1 "k8s.io/api/core/v1"
)

type PodNetworksAnnotation struct {
	PodNetworks []PodNetworks `json:"podNetworks"`
}

type PodNetworkRef struct {
	InterfaceName string        `json:"interfaceName"`
	Network       string        `json:"network"`
	DefaultRoute  bool          `json:"defaultRoute,omitempty"`
	Routes        []route.Route `json:"routes,omitempty"`
}

// ParsePodNetworksFromAnnotation parse annotation and convert to PodNetworksAnnotation
func ParsePodNetworksFromAnnotation(pod *corev1.Pod) (*PodNetworksAnnotation, error) {
	v, ok := pod.GetAnnotations()[terwayTypes.PodNetworks]
	if !ok {
		return &PodNetworksAnnotation{}, nil
	}

	var annoConf PodNetworksAnnotation
	err := json.Unmarshal([]byte(v), &annoConf)
	if err != nil {
		return nil, fmt.Errorf("parse %s from pod annotataion, %w", terwayTypes.PodNetworks, err)
	}
	return &annoConf, nil
}

func ParsePodNetworksFromRequest(anno map[string]string) ([]PodNetworkRef, error) {
	v, ok := anno[terwayTypes.PodNetworksRequest]
	if !ok {
		return nil, nil
	}

	var annoConf []PodNetworkRef
	err := json.Unmarshal([]byte(v), &annoConf)
	if err != nil {
		return nil, fmt.Errorf("parse %s from pod annotataion, %w", terwayTypes.PodNetworksRequest, err)
	}
	return annoConf, nil
}
