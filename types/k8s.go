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

package types

import (
	"strconv"

	corev1 "k8s.io/api/core/v1"
)

// AnnotationPrefix is the annotation prefix
const AnnotationPrefix = "k8s.aliyun.com/"

// annotations used by terway
const (
	// TrunkOn is the key for eni
	TrunkOn = AnnotationPrefix + "trunk-on"

	// PodENI whether pod is using podENI cr resource
	PodENI        = AnnotationPrefix + "pod-eni"
	PodNetworking = AnnotationPrefix + "pod-networking"

	// PodIPReservation whether pod's IP will be reserved for a reuse
	PodIPReservation = AnnotationPrefix + "pod-ip-reservation"

	// PodNetworks for additional net config
	PodNetworks = AnnotationPrefix + "pod-networks"
)

// FinalizerPodENI finalizer for podENI resource
const FinalizerPodENI = "pod-eni"

// events for control plane
const (
	EventCreateENISucceed = "CreateENISucceed"

	EventCreateENIFailed  = "CreateENIFailed"
	EventAttachENISucceed = "AttachENISucceed"
	EventAttachENIFailed  = "AttachENIFailed"
	EventDetachENISucceed = "DetachENISucceed"
	EventDetachENIFailed  = "DetachENIFailed"
	EventDeleteENISucceed = "DeleteENISucceed"
	EventDeleteENIFailed  = "DeleteENIFailed"

	EventUpdatePodENIFailed = "UpdatePodENIFailed"

	EventSyncPodNetworkingSucceed = "SyncPodNetworkingSucceed"
	EventSyncPodNetworkingFailed  = "SyncPodNetworkingFailed"
)

// PodUseENI whether pod is use podENI cr res
func PodUseENI(pod *corev1.Pod) bool {
	key, ok := pod.GetAnnotations()[PodENI]
	if !ok {
		return false
	}
	v, err := strconv.ParseBool(key)
	if err != nil {
		return false
	}
	return v
}
