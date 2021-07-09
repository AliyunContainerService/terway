package types

// AnnotationPrefix is the annotation prefix
const AnnotationPrefix = "k8s.aliyun.com/"

// annotations used by terway
const (
	// TrunkOn is the key for eni
	TrunkOn = AnnotationPrefix + "trunk-on"

	// PodENI whether pod is using eni (trunking mode)
	PodENI = AnnotationPrefix + "pod-eni"
)
