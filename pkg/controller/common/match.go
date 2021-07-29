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

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/utils"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// MatchOnePodNetworking will range all podNetworking and try to found a matched podNetworking for this pod
// for stateless pod Fixed ip config is never matched
func MatchOnePodNetworking(pod *corev1.Pod, networkings []v1beta1.PodNetworking) (*v1beta1.PodNetworking, error) {
	l := labels.Set(pod.Labels)
	for _, podNetworking := range networkings {
		if podNetworking.Status.Status != v1beta1.NetworkingStatusReady {
			continue
		}
		if !utils.IsStsPod(pod) {
			// for fixed ip , only match sts pod
			if podNetworking.Spec.IPType.Type == v1beta1.IPAllocTypeFixed {
				continue
			}
		}

		matchOne := false
		if podNetworking.Spec.Selector.PodSelector != nil {
			ok, err := PodMatchSelector(podNetworking.Spec.Selector.PodSelector, l)
			if err != nil {
				return nil, fmt.Errorf("error match pod selector, %w", err)
			}
			if !ok {
				continue
			}
			matchOne = true
		}
		if podNetworking.Spec.Selector.NamespaceSelector != nil {
			ok, err := PodMatchSelector(podNetworking.Spec.Selector.NamespaceSelector, l)
			if err != nil {
				return nil, fmt.Errorf("error match namespace selector, %w", err)
			}
			if !ok {
				continue
			}
			matchOne = true
		}
		if matchOne {
			return &podNetworking, nil
		}
	}
	return nil, nil
}

// PodMatchSelector pod is selected by selector
func PodMatchSelector(labelSelector *metav1.LabelSelector, l labels.Set) (bool, error) {
	selector, err := metav1.LabelSelectorAsSelector(labelSelector)
	if err != nil {
		return false, err
	}
	return selector.Matches(l), nil
}
