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
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
)

// AnnotationPrefix is the annotation prefix
const AnnotationPrefix = "k8s.aliyun.com/"
const LabelPrefix = "k8s.aliyun.com/"

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

	// PodAllocType for additional net config
	PodAllocType = AnnotationPrefix + "pod-alloc-type"

	// PodENIAllocated pod annotation for allocated eni
	PodENIAllocated = AnnotationPrefix + "allocated"

	// PodUID store pod uid
	PodUID = AnnotationPrefix + "pod-uid"

	// NetworkPriority set pod network priority
	NetworkPriority = AnnotationPrefix + "network-priority"

	ENIAllocFromPool   = AnnotationPrefix + "eni-alloc-from-pool"
	ENIRelatedNodeName = AnnotationPrefix + "node"

	PodIPs = AnnotationPrefix + "pod-ips"

	// IgnoreByTerway if the label exist , terway will not handle this kind of res
	IgnoreByTerway = LabelPrefix + "ignore-by-terway"
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

// IgnoredByTerway for both pods and nodes
func IgnoredByTerway(labels map[string]string) bool {
	return labels[IgnoreByTerway] == "true"
}

// NetworkPrio network priority for pod
type NetworkPrio string

// NetworkPrio val
const (
	NetworkPrioBestEffort NetworkPrio = "best-effort"
	NetworkPrioBurstable  NetworkPrio = "burstable"
	NetworkPrioGuaranteed NetworkPrio = "guaranteed"
)

// PodIPTypeIPs Pod IP address type
type PodIPTypeIPs string

// PodIPTypeIPs val
const (
	NormalIPTypeIPs    PodIPTypeIPs = AnnotationPrefix + "max-available-ip"
	MemberENIIPTypeIPs PodIPTypeIPs = AnnotationPrefix + "max-member-eni-ip"
	ERDMAIPTypeIPs     PodIPTypeIPs = AnnotationPrefix + "max-erdma-ip"
)

// SufficientIPCondition definitions
const (
	SufficientIPCondition   corev1.NodeConditionType = "SufficientIP"
	IPResInsufficientReason string                   = "InsufficientIP"
	IPResSufficientReason   string                   = "SufficientIP"
)

type IPInsufficientError struct {
	Err    error
	Reason string
}

func (e *IPInsufficientError) Error() string {
	return fmt.Sprintf("ip insufficient error: %v with reason: %s", e.Err, e.Reason)
}
