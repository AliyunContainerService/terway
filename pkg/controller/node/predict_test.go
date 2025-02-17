package node

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPredicateNode(t *testing.T) {
	tests := []struct {
		name        string
		node        *corev1.Node
		supportEFLO bool
		expected    bool
	}{
		{
			name: "SupportEFLOFalseAndNoRegionLabel",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"other-label": "value",
					},
				},
			},
			supportEFLO: false,
			expected:    false,
		},
		{
			name: "IgnoredByTerway",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"topology.kubernetes.io/region":   "region",
						"k8s.aliyun.com/ignore-by-terway": "true",
					},
				},
			},
			expected: false,
		},
		{
			name: "Ignore Lunjun worker",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"topology.kubernetes.io/region":   "region",
						"alibabacloud.com/lingjun-worker": "true",
					},
				},
			},
			supportEFLO: false,
			expected:    false,
		},
		{
			name: "Lunjun worker",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"alibabacloud.com/lingjun-worker": "true",
					},
				},
			},
			supportEFLO: true,
			expected:    true,
		},
		{
			name: "VKNode",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"topology.kubernetes.io/region": "region",
						"type":                          "virtual-kubelet",
					},
				},
			},
			expected: false,
		},
		{
			name: "AllConditionsSatisfied",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"topology.kubernetes.io/region": "region",
					},
				},
			},
			expected: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := predicateNode(test.node, test.supportEFLO)
			assert.Equal(t, test.expected, result)
		})
	}
}
