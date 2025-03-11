package node

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPredicateNode(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node

		predicate *predicateForNodeEvent
		expected  bool
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
			predicate: &predicateForNodeEvent{
				supportEFLO: false,
			},
			expected: false,
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
			predicate: &predicateForNodeEvent{
				supportEFLO: false,
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
			predicate: &predicateForNodeEvent{
				supportEFLO: false,
			},
			expected: false,
		},
		{
			name: "Lunjun worker",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"topology.kubernetes.io/region":   "region",
						"alibabacloud.com/lingjun-worker": "true",
					},
				},
			},
			predicate: &predicateForNodeEvent{
				supportEFLO: true,
			},
			expected: true,
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
			predicate: &predicateForNodeEvent{
				supportEFLO: false,
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
			predicate: &predicateForNodeEvent{
				supportEFLO: false,
			},
			expected: true,
		},
		{
			name: "Normal with label whitelist",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"topology.kubernetes.io/region": "region",
					},
				},
			},
			predicate: &predicateForNodeEvent{
				supportEFLO: false,
				nodeLabelWhiteList: map[string]string{
					"kind": "ecs",
					"zone": "1",
				},
			},
			expected: false,
		},
		{
			name: "Normal with label whitelist",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"topology.kubernetes.io/region": "region",
						"kind":                          "ecs",
						"aa":                            "bb",
					},
				},
			},
			predicate: &predicateForNodeEvent{
				supportEFLO: false,
				nodeLabelWhiteList: map[string]string{
					"kind":                          "ecs",
					"topology.kubernetes.io/region": "region",
				},
			},
			expected: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.predicate.predicateNode(test.node)
			assert.Equal(t, test.expected, result)
		})
	}
}
