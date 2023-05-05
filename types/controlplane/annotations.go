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
	"time"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	terwayTypes "github.com/AliyunContainerService/terway/types"

	corev1 "k8s.io/api/core/v1"
)

type PodNetworksAnnotation struct {
	PodNetworks []PodNetworks `json:"podNetworks"`
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

// ParsePodIPTypeFromAnnotation parse annotation and convert to v1beta1.AllocationType
func ParsePodIPTypeFromAnnotation(pod *corev1.Pod) (*v1beta1.AllocationType, error) {
	return ParsePodIPType(pod.GetAnnotations()[terwayTypes.PodAllocType])
}

func ParsePodIPType(str string) (*v1beta1.AllocationType, error) {
	if str == "" {
		return defaultAllocType(), nil
	}

	// will not be nil
	var annoConf v1beta1.AllocationType
	err := json.Unmarshal([]byte(str), &annoConf)
	if err != nil {
		return nil, err
	}
	return ParseAllocationType(&annoConf)
}

func defaultAllocType() *v1beta1.AllocationType {
	return &v1beta1.AllocationType{
		Type: v1beta1.IPAllocTypeElastic,
	}
}

// ParseAllocationType parse and set default val
func ParseAllocationType(old *v1beta1.AllocationType) (*v1beta1.AllocationType, error) {
	res := defaultAllocType()
	if old == nil {
		return res, nil
	}

	if old.Type == v1beta1.IPAllocTypeFixed {
		res.Type = v1beta1.IPAllocTypeFixed
		res.ReleaseStrategy = v1beta1.ReleaseStrategyTTL
		res.ReleaseAfter = "10m"

		if old.ReleaseStrategy == v1beta1.ReleaseStrategyNever {
			res.ReleaseStrategy = v1beta1.ReleaseStrategyNever
		}
		if old.ReleaseAfter != "" {
			_, err := time.ParseDuration(old.ReleaseAfter)
			if err != nil {
				return nil, fmt.Errorf("error parse ReleaseAfter, %w", err)
			}
			res.ReleaseAfter = old.ReleaseAfter
		}
	}
	return res, nil
}
