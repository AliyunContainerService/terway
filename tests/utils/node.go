package utils

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	// LingjunTaintKey is the taint key for Lingjun nodes
	LingjunTaintKey = "node-role.alibabacloud.com/lingjun"

	// LingjunWorkerLabelKey is the label key for Lingjun worker nodes
	LingjunWorkerLabelKey = "alibabacloud.com/lingjun-worker"

	// ExclusiveENILabelKey is the label key for exclusive ENI mode nodes
	ExclusiveENILabelKey = "k8s.aliyun.com/exclusive-mode-eni-type"

	// ExclusiveENILabelValue is the label value for exclusive ENI mode nodes
	ExclusiveENILabelValue = "eniOnly"
)

// LingjunToleration returns the toleration for Lingjun nodes
func LingjunToleration() corev1.Toleration {
	return corev1.Toleration{
		Key:      LingjunTaintKey,
		Operator: corev1.TolerationOpExists,
	}
}

// LingjunTolerations returns a slice containing the Lingjun toleration
func LingjunTolerations() []corev1.Toleration {
	return []corev1.Toleration{LingjunToleration()}
}

// IsLingjunNodeType checks if the given node type string represents a Lingjun node
func IsLingjunNodeType(nodeType string) bool {
	return nodeType == "lingjun-shared-eni" || nodeType == "lingjun-exclusive-eni"
}
