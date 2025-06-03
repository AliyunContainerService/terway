package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:openapi-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:JSONPath=".status.phase",name=Phase,type=string
// +kubebuilder:printcolumn:JSONPath=".status.instanceID",name=InstanceID,type=string
// +kubebuilder:printcolumn:JSONPath=".status.trunkENIID",name=TrunkENIID,type=string
// +kubebuilder:printcolumn:JSONPath=".status.nodeName",name=NodeName,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.podENIRef.namespace",name=PodNamespace,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.podENIRef.name",name=PodName,type=string
// +kubebuilder:printcolumn:JSONPath=".status.eniInfo.type",name=Type,type=string

// NetworkInterface is the Schema for the NetworkInterface API
type NetworkInterface struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NetworkInterfaceSpec   `json:"spec,omitempty"`
	Status NetworkInterfaceStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// NetworkInterfaceList contains a list of NetworkInterface
type NetworkInterfaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkInterface `json:"items"`
}

type NetworkInterfaceSpec struct {
	ENI         ENI               `json:"eni,omitempty"`
	IPv4        string            `json:"ipv4,omitempty"`
	IPv6        string            `json:"ipv6,omitempty"`
	IPv4CIDR    string            `json:"ipv4CIDR,omitempty"`
	IPv6CIDR    string            `json:"ipv6CIDR,omitempty"`
	ExtraConfig map[string]string `json:"extraConfig,omitempty"`

	ManagePolicy ManagePolicy `json:"managePolicy,omitempty"`

	// PodENIRef only some filed is used, uid, name, namespace
	PodENIRef *corev1.ObjectReference `json:"podENIRef,omitempty"`
}

type ManagePolicy struct {
	// whether to cache on node
	Cache     bool `json:"cache,omitempty"`
	UnManaged bool `json:"unManaged,omitempty"`
}

type NetworkInterfaceStatus struct {
	Phase Phase `json:"phase"`

	ENIInfo ENIInfo `json:"eniInfo"`

	// InstanceID for ecs
	InstanceID string `json:"instanceID,omitempty"`
	// TrunkENIID is the trunk eni id
	TrunkENIID string `json:"trunkENIID,omitempty"`

	NetworkCardIndex *int   `json:"networkCardIndex,omitempty"`
	NodeName         string `json:"nodeName,omitempty"`
}
