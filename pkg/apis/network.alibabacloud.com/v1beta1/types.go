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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:openapi-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PodENI is the Schema for the podenis API
type PodENI struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PodENISpec   `json:"spec,omitempty"`
	Status PodENIStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// PodENIList contains a list of PodENI
type PodENIList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PodENI `json:"items"`
}

// PodENISpec defines the desired state of PodENI
type PodENISpec struct {
	// Allocation store the state for eni
	Allocations []Allocation `json:"allocations,omitempty"`
	// Zone zone
	Zone string `json:"zone,omitempty"`
}

// PodENIStatus defines the observed state of PodENI
type PodENIStatus struct {
	// Phase is the status for the eni binding
	Phase Phase `json:"phase,omitempty"`
	// InstanceID for ecs
	InstanceID string `json:"instanceID,omitempty"`
	// TrunkENIID is the trunk eni id
	TrunkENIID string `json:"trunkENIID,omitempty"`
	// Msg additional info
	Msg string `json:"msg,omitempty"`
	// PodLastSeen is the timestamp when pod resource last seen
	PodLastSeen metav1.Time `json:"podLastSeen,omitempty"`
	// ENIInfos is the status after eni is attached, it is indexed by eni id
	ENIInfos map[string]ENIInfo `json:"eniInfos,omitempty"`
}

// Allocation for eni record
type Allocation struct {
	AllocationType AllocationType    `json:"allocationType,omitempty"`
	ENI            ENI               `json:"eni,omitempty"`
	IPv4           string            `json:"ipv4,omitempty"`
	IPv6           string            `json:"ipv6,omitempty"`
	IPv4CIDR       string            `json:"ipv4CIDR,omitempty"`
	IPv6CIDR       string            `json:"ipv6CIDR,omitempty"`
	Interface      string            `json:"interface,omitempty"`
	DefaultRoute   bool              `json:"defaultRoute,omitempty"`
	ExtraRoutes    []Route           `json:"extraRoutes,omitempty"`
	ExtraConfig    map[string]string `json:"extraConfig,omitempty"`
}

type Route struct {
	Dst string `json:"dst,omitempty"`
}

// ENI eni info
type ENI struct {
	ID               string   `json:"id,omitempty"`
	MAC              string   `json:"mac,omitempty"`
	Zone             string   `json:"zone,omitempty"`
	VSwitchID        string   `json:"vSwitchID,omitempty"`
	ResourceGroupID  string   `json:"resourceGroupID,omitempty"`
	SecurityGroupIDs []string `json:"securityGroupIDs,omitempty"`
}

// AllocationType ip type and release strategy
type AllocationType struct {
	// +kubebuilder:default:=Elastic
	Type            IPAllocType     `json:"type,omitempty"`
	ReleaseStrategy ReleaseStrategy `json:"releaseStrategy,omitempty"`
	ReleaseAfter    string          `json:"releaseAfter,omitempty"` // go type 5m0s
}

type Phase string

//	pod create
//	    |
//	    |   podENI create
//	    |
//
// ENIPhaseInitial
//
//	      |
//	      |   bind eni
//	ENIPhaseBind        <-----   ENIPhaseBinding  <----- sts pod recreate
//	      |                               |
//	      |                               |
//	      |                               |              gc reserved resource for sts pods
//	      |                         ENIPhaseUnbind    ---------------
//	      |                               |                     |    |
//	      |   sts pod delete              |                     |    |
//	      |-----------------------> ENIPhaseDetaching           |    |
//	      |                                                     |    |
//	      |   stateless pod delete                              |    |
//	      |        <---------------------------------------------    |
//	      |                                                          |
//
// ENIPhaseDeleting <------------------------------------------------|
//
//	      |
//	del podENI
const (
	// ENIPhaseInitial the status when pod first created
	ENIPhaseInitial = ""
	// ENIPhaseBind the status ENI is bind to ECS
	ENIPhaseBind = "Bind"
	// ENIPhaseBinding the status ENI need to bind to ECS, usually when sts pod require the previous ENI
	ENIPhaseBinding = "Binding"
	// ENIPhaseUnbind the status ENI is not bind to ECS
	ENIPhaseUnbind = "Unbind"
	// ENIPhaseDetaching the status when need to detach ENI from ECS
	ENIPhaseDetaching = "Detaching"
	// ENIPhaseDeleting the status when CR is removing
	ENIPhaseDeleting = "Deleting"
)

// ENIBindStatus is the current status for the eni
type ENIBindStatus string

const (
	// ENIStatusBind eni is bind
	ENIStatusBind ENIBindStatus = "Bind"
	// ENIStatusUnBind eni is not attached
	ENIStatusUnBind ENIBindStatus = "Unbind"
	// ENIStatusDeleted eni is already deleted
	ENIStatusDeleted ENIBindStatus = "Deleted"
)

type ENIInfo struct {
	ID     string        `json:"id,omitempty"`
	Type   ENIType       `json:"type,omitempty"`
	Vid    int           `json:"vid,omitempty"`    // vlan id for trunk
	Status ENIBindStatus `json:"status,omitempty"` // the status for operate the eni
}

// ENIType for this eni, only Secondary and Member is supported
type ENIType string

// status for ENIType
const (
	ENITypePrimary   ENIType = "Primary"
	ENITypeSecondary ENIType = "Secondary"
	ENITypeTrunk     ENIType = "Trunk"
	ENITypeMember    ENIType = "Member"
)

// +kubebuilder:validation:Enum=Elastic;Fixed

// IPAllocType is the type for ip alloc strategy
type IPAllocType string

// IPAllocType
const (
	IPAllocTypeElastic = "Elastic"
	IPAllocTypeFixed   = "Fixed"
)

// +kubebuilder:validation:Enum=TTL;Never

// ReleaseStrategy is the type for ip release strategy
type ReleaseStrategy string

// ReleaseStrategy
const (
	ReleaseStrategyTTL   = "TTL"
	ReleaseStrategyNever = "Never"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:openapi-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// PodNetworking is the Schema for the PodNetworking API
type PodNetworking struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PodNetworkingSpec   `json:"spec,omitempty"`
	Status PodNetworkingStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// PodNetworkingList contains a list of PodNetworking
type PodNetworkingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PodNetworking `json:"items"`
}

// PodNetworkingSpec defines the desired state of PodNetworking
type PodNetworkingSpec struct {
	AllocationType AllocationType `json:"allocationType,omitempty"`

	Selector Selector `json:"selector,omitempty"`

	SecurityGroupIDs []string `json:"securityGroupIDs,omitempty"`
	VSwitchOptions   []string `json:"vSwitchOptions,omitempty"`
}

// PodNetworkingStatus defines the observed state of PodNetworking
type PodNetworkingStatus struct {
	// Status is the status for crd
	Status NetworkingStatus `json:"status,omitempty"`
	// vSwitches list for vSwitches
	VSwitches []VSwitch `json:"vSwitches,omitempty"`
	// UpdateAt the time status updated
	UpdateAt metav1.Time `json:"updateAt,omitempty"`
	// Message for the status
	Message string `json:"message,omitempty"`
}

// VSwitch VSwitch info
type VSwitch struct {
	ID   string `json:"id,omitempty"`
	Zone string `json:"zone,omitempty"`
}

// NetworkingStatus the status for the resource
type NetworkingStatus string

// NetworkingStatus
const (
	NetworkingStatusReady NetworkingStatus = "Ready"
	NetworkingStatusFail  NetworkingStatus = "Fail"
)

// Selector is for pod or namespace
type Selector struct {
	PodSelector       *metav1.LabelSelector `json:"podSelector,omitempty"`
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`
}
