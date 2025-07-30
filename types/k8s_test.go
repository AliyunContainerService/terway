package types_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/AliyunContainerService/terway/types"
)

func TestNodeExclusiveENIMode(t *testing.T) {
	t.Run("Returns default when label is missing", func(t *testing.T) {
		labels := map[string]string{}
		result := types.NodeExclusiveENIMode(labels)
		assert.Equal(t, types.ExclusiveDefault, result)
	})

	t.Run("Returns default when label is empty", func(t *testing.T) {
		labels := map[string]string{
			types.ExclusiveENIModeLabel: "",
		}
		result := types.NodeExclusiveENIMode(labels)
		assert.Equal(t, types.ExclusiveDefault, result)
	})

	t.Run("Returns ENI only when label is set", func(t *testing.T) {
		labels := map[string]string{
			types.ExclusiveENIModeLabel: "eniOnly",
		}
		result := types.NodeExclusiveENIMode(labels)
		assert.Equal(t, types.ExclusiveENIOnly, result)
	})

	t.Run("Is case insensitive", func(t *testing.T) {
		labels := map[string]string{
			types.ExclusiveENIModeLabel: "ENIONLY",
		}
		result := types.NodeExclusiveENIMode(labels)
		assert.Equal(t, types.ExclusiveENIOnly, result)
	})
}

func TestPodUseENI(t *testing.T) {
	t.Run("Returns false when annotation is missing", func(t *testing.T) {
		pod := &corev1.Pod{}
		result := types.PodUseENI(pod)
		assert.False(t, result)
	})

	t.Run("Returns false when annotation is empty", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					types.PodENI: "",
				},
			},
		}
		result := types.PodUseENI(pod)
		assert.False(t, result)
	})

	t.Run("Returns false when annotation is invalid boolean", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					types.PodENI: "invalid",
				},
			},
		}
		result := types.PodUseENI(pod)
		assert.False(t, result)
	})

	t.Run("Returns false when annotation is false", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					types.PodENI: "false",
				},
			},
		}
		result := types.PodUseENI(pod)
		assert.False(t, result)
	})

	t.Run("Returns true when annotation is true", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					types.PodENI: "true",
				},
			},
		}
		result := types.PodUseENI(pod)
		assert.True(t, result)
	})

	t.Run("Returns true when annotation is TRUE (case insensitive)", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					types.PodENI: "TRUE",
				},
			},
		}
		result := types.PodUseENI(pod)
		assert.True(t, result)
	})
}

func TestIgnoredByTerway(t *testing.T) {
	t.Run("Returns false when label is missing", func(t *testing.T) {
		labels := map[string]string{}
		result := types.IgnoredByTerway(labels)
		assert.False(t, result)
	})

	t.Run("Returns false when label is empty", func(t *testing.T) {
		labels := map[string]string{
			types.IgnoreByTerway: "",
		}
		result := types.IgnoredByTerway(labels)
		assert.False(t, result)
	})

	t.Run("Returns false when label is not true", func(t *testing.T) {
		labels := map[string]string{
			types.IgnoreByTerway: "false",
		}
		result := types.IgnoredByTerway(labels)
		assert.False(t, result)
	})

	t.Run("Returns true when label is true", func(t *testing.T) {
		labels := map[string]string{
			types.IgnoreByTerway: "true",
		}
		result := types.IgnoredByTerway(labels)
		assert.True(t, result)
	})

	t.Run("Returns true when label is TRUE (case sensitive)", func(t *testing.T) {
		labels := map[string]string{
			types.IgnoreByTerway: "TRUE",
		}
		result := types.IgnoredByTerway(labels)
		assert.False(t, result) // Should be false because it's case sensitive
	})

	t.Run("Returns false when other labels exist but ignore label is missing", func(t *testing.T) {
		labels := map[string]string{
			"other-label": "value",
			"another":     "test",
		}
		result := types.IgnoredByTerway(labels)
		assert.False(t, result)
	})
}
