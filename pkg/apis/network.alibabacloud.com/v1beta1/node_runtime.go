package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RuntimePodSpec struct {
	UID       string `json:"uid"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	IPv4      string `json:"ipv4"`
	IPv6      string `json:"ipv6"`
}

// +kubebuilder:validation:Enum=initial;deleted
type CNIStatus string

const (
	CNIStatusInitial CNIStatus = "initial"
	CNIStatusDeleted CNIStatus = "deleted"
)

type CNIStatusInfo struct {
	LastUpdateTime metav1.Time `json:"lastUpdateTime"`
}

type RuntimePodStatus struct {
	PodID string `json:"podID"`
	// when pod is added
	Status map[CNIStatus]*CNIStatusInfo `json:"status"`
}

type NodeRuntimeSpec struct {
}

type NodeRuntimeStatus struct {
	// runtime status, indexed by pod uid
	Pods map[string]*RuntimePodStatus `json:"pods,omitempty"`
}

// +genclient
// +genclient:nonNamespaced
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// NodeRuntime is the Schema for the per node runtime API
type NodeRuntime struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeRuntimeSpec   `json:"spec,omitempty"`
	Status NodeRuntimeStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// NodeRuntimeList contains a list of Node
type NodeRuntimeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeRuntime `json:"items"`
}
