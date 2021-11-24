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
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"

	corev1 "k8s.io/api/core/v1"
)

type PodNetworksAnnotation struct {
	PodNetworks []PodNetworkAnnotation `json:"podNetworks"`
	Zone        string                 `json:"zone"`
}

type PodNetworkAnnotation struct {
	RoleName         string   `json:"roleName"`
	Interface        string   `json:"interface"`
	UserID           string   `json:"userID"`
	SwitchID         string   `json:"switchID"`
	SecurityGroupIDs []string `json:"securityGroupIDs"`
	ResourceGroupID  string   `json:"resourceGroupID"`
	DefaultRoute     bool     `json:"defaultRoute"`
	ExtraRoutes      []Route  `json:"extraRoutes"`
}

type Route struct {
	Dst string `json:"dst"`
}

// ParsePodNetworksFromAnnotation parse annotation and convert to []v1beta1.Allocation
func ParsePodNetworksFromAnnotation(pod *corev1.Pod) ([]*v1beta1.Allocation, string, error) {
	return nil, "", nil
}

// ParsePodIPTypeFromAnnotation parse annotation and convert to v1beta1.IPType
func ParsePodIPTypeFromAnnotation(pod *corev1.Pod) (*v1beta1.AllocationType, error) {
	return &v1beta1.AllocationType{
		Type: v1beta1.IPAllocTypeElastic,
	}, nil
}
