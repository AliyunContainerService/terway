package types_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
