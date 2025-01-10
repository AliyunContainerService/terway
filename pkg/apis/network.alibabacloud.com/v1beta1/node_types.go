/*
Copyright 2024 Terway Authors.

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

// NetworkInterfaceTrafficMode represents the traffic mode of the network interface.
// +kubebuilder:validation:Enum=Standard;HighPerformance
type NetworkInterfaceTrafficMode string

const (
	NetworkInterfaceTrafficModeStandard        NetworkInterfaceTrafficMode = "Standard"
	NetworkInterfaceTrafficModeHighPerformance NetworkInterfaceTrafficMode = "HighPerformance"
)

// IPStatus representing the status of an IP address.
// +kubebuilder:validation:Enum=Valid;Deleting
type IPStatus string

const (
	IPStatusValid    IPStatus = "Valid"
	IPStatusDeleting IPStatus = "Deleting"
)

// +kubebuilder:validation:Enum=ordered;random;most
type SelectionPolicy string

// VSwitch Selection Policy
const (
	VSwitchSelectionPolicyOrdered SelectionPolicy = "ordered"
	VSwitchSelectionPolicyRandom  SelectionPolicy = "random"
	VSwitchSelectionPolicyMost    SelectionPolicy = "most"
)

type NodeMetadata struct {
	// +kubebuilder:validation:Required
	RegionID string `json:"regionID,omitempty"`
	// +kubebuilder:validation:Required
	InstanceType string `json:"instanceType,omitempty"`
	// +kubebuilder:validation:Required
	InstanceID string `json:"instanceID,omitempty"`
	// +kubebuilder:validation:Required
	ZoneID string `json:"zoneID,omitempty"`
}

type NodeCap struct {
	Adapters              int `json:"adapters,omitempty"`
	EriQuantity           int `json:"eriQuantity,omitempty"`
	TotalAdapters         int `json:"totalAdapters,omitempty"`
	IPv4PerAdapter        int `json:"ipv4PerAdapter,omitempty"`
	IPv6PerAdapter        int `json:"ipv6PerAdapter,omitempty"`
	MemberAdapterLimit    int `json:"memberAdapterLimit,omitempty"`
	MaxMemberAdapterLimit int `json:"maxMemberAdapterLimit,omitempty"`
	InstanceBandwidthRx   int `json:"instanceBandwidthRx,omitempty"`
	InstanceBandwidthTx   int `json:"instanceBandwidthTx,omitempty"`
}

type ENISpec struct {
	Tag       map[string]string `json:"tag,omitempty"`
	TagFilter map[string]string `json:"tagFilter,omitempty"`
	// +kubebuilder:validation:Required
	VSwitchOptions []string `json:"vSwitchOptions,omitempty"`
	// +kubebuilder:validation:Required
	SecurityGroupIDs []string `json:"securityGroupIDs,omitempty"`
	ResourceGroupID  string   `json:"resourceGroupID,omitempty"`
	EnableIPv4       bool     `json:"enableIPv4,omitempty"`
	EnableIPv6       bool     `json:"enableIPv6,omitempty"`
	EnableERDMA      bool     `json:"enableERDMA,omitempty"`
	EnableTrunk      bool     `json:"enableTrunk,omitempty"`
	// +kubebuilder:validation:Required
	// +kubebuilder:default:most
	VSwitchSelectPolicy SelectionPolicy `json:"vSwitchSelectPolicy,omitempty"`
}

type PoolSpec struct {
	// +kubebuilder:validation:Minimum=0
	MaxPoolSize int `json:"maxPoolSize,omitempty"`
	// +kubebuilder:validation:Minimum=0
	MinPoolSize int `json:"minPoolSize,omitempty"`
}

type Flavor struct {
	NetworkInterfaceType        ENIType                     `json:"networkInterfaceType"`
	NetworkInterfaceTrafficMode NetworkInterfaceTrafficMode `json:"networkInterfaceTrafficMode"`

	// +kubebuilder:validation:Minimum=0
	Count int `json:"count"`
}

// NodeSpec defines the desired state of Node
type NodeSpec struct {
	NodeMetadata NodeMetadata `json:"nodeMetadata,omitempty"`
	NodeCap      NodeCap      `json:"nodeCap,omitempty"`
	ENISpec      *ENISpec     `json:"eni,omitempty"`
	Pool         *PoolSpec    `json:"pool,omitempty"`
	// Flavor guide the controller to generate eni as expected
	Flavor []Flavor `json:"flavor,omitempty"`
}

type IP struct {
	// +kubebuilder:validation:Required
	IP string `json:"ip"`
	// +kubebuilder:validation:Required
	Primary bool `json:"primary"`
	// +kubebuilder:validation:Required
	Status IPStatus `json:"status"`
	// Add the pod ID
	PodID string `json:"podID,omitempty"`
	// Add pod UID for validate
	PodUID string `json:"podUID,omitempty"`
}

type IPMap map[string]*IP

type NetworkInterface struct {
	// +kubebuilder:validation:Required
	ID string `json:"id"`
	// +kubebuilder:validation:Required
	Status string `json:"status"`
	// +kubebuilder:validation:Required
	MacAddress string `json:"macAddress"`
	// +kubebuilder:validation:Required
	VSwitchID string `json:"vSwitchID"`
	// +kubebuilder:validation:Required
	SecurityGroupIDs []string `json:"securityGroupIDs"`
	// +kubebuilder:validation:Required
	PrimaryIPAddress string `json:"primaryIPAddress"`
	// +kubebuilder:validation:Required
	NetworkInterfaceTrafficMode NetworkInterfaceTrafficMode `json:"networkInterfaceTrafficMode"`
	// +kubebuilder:validation:Required
	NetworkInterfaceType ENIType `json:"networkInterfaceType"`
	// +kubebuilder:validation:Required
	IPv4 map[string]*IP `json:"ipv4,omitempty"`
	IPv6 map[string]*IP `json:"ipv6,omitempty"`
	// +kubebuilder:validation:Required
	IPv4CIDR string `json:"ipv4CIDR"`
	// +kubebuilder:validation:Required
	IPv6CIDR string `json:"ipv6CIDR"`

	Conditions map[string]Condition `json:"conditions,omitempty"`
}

type Condition struct {
	ObservedTime metav1.Time `json:"observedTime,omitempty"`
	Message      string      `json:"message,omitempty"`
}

// NodeStatus defines the observed state of Node
type NodeStatus struct {
	NextSyncOpenAPITime metav1.Time                  `json:"nextSyncOpenAPITime,omitempty"`
	LastSyncOpenAPITime metav1.Time                  `json:"lastSyncOpenAPITime,omitempty"`
	NetworkInterfaces   map[string]*NetworkInterface `json:"networkInterfaces,omitempty"`
}

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// Node is the Schema for the nodes API
type Node struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeSpec   `json:"spec,omitempty"`
	Status NodeStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// NodeList contains a list of Node
type NodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Node `json:"items"`
}
