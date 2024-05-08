package types

const (
	NodeTypeLabel = "alibabacloud.com/lingjun-worker"
)

func LingjunNodeTypeFromLables(labels map[string]string) bool {
	nodeType, ok := labels[NodeTypeLabel]
	if ok && nodeType == "true" {
		return true
	}
	return false
}
