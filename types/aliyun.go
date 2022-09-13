package types

// this keys is used in alibabacloud resource
const (
	TagKeyClusterID = "ack.aliyun.com"

	// NetworkInterfaceTagCreatorKey denotes the creator tag's key of network interface
	NetworkInterfaceTagCreatorKey = "creator"
	// NetworkInterfaceTagCreatorValue denotes the creator tag's value of network interface
	NetworkInterfaceTagCreatorValue = "terway"

	// TagTerwayController terway controller
	TagTerwayController = "terway-controller"

	TagENIAllocPolicy = "eni-alloc-policy"

	TagK8SNodeName = "node-name"

	TagKubernetesPodName      = "k8s_pod_name"
	TagKubernetesPodNamespace = "k8s_pod_namespace"
)
