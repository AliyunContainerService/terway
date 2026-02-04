package node

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
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

func TestPredicateForNodeEvent_CreateDeleteUpdateGeneric(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"topology.kubernetes.io/region": "region"},
		},
	}
	p := &predicateForNodeEvent{supportEFLO: false}

	t.Run("Create", func(t *testing.T) {
		e := event.CreateEvent{Object: node}
		assert.True(t, p.Create(e))
	})

	t.Run("Delete", func(t *testing.T) {
		e := event.DeleteEvent{Object: node}
		assert.True(t, p.Delete(e))
	})

	t.Run("Update", func(t *testing.T) {
		e := event.UpdateEvent{ObjectNew: node}
		assert.True(t, p.Update(e))
	})

	t.Run("Generic", func(t *testing.T) {
		e := event.GenericEvent{Object: node}
		assert.True(t, p.Generic(e))
	})

	t.Run("Create with non-Node object returns false", func(t *testing.T) {
		e := event.CreateEvent{Object: &corev1.Pod{}}
		assert.False(t, p.Create(e))
	})
}
