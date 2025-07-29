package main

import (
	"testing"

	"github.com/AliyunContainerService/terway/rpc"
	"github.com/stretchr/testify/assert"
)

func TestTreeAddLeaf(t *testing.T) {
	t.Run("add single leaf", func(t *testing.T) {
		tree := &tree{}
		tree.addLeaf([]string{"key1", "key2", "key3"}, "value")

		assert.Len(t, tree.Leaves, 1)
		assert.Equal(t, "key1", tree.Leaves[0].Key)
		assert.False(t, tree.Leaves[0].IsLeaf)

		level2 := tree.Leaves[0].Leaves
		assert.Len(t, level2, 1)
		assert.Equal(t, "key2", level2[0].Key)
		assert.False(t, level2[0].IsLeaf)

		level3 := level2[0].Leaves
		assert.Len(t, level3, 1)
		assert.Equal(t, "key3", level3[0].Key)
		assert.True(t, level3[0].IsLeaf)
		assert.Equal(t, "value", level3[0].Value)
	})

	t.Run("add multiple leaves", func(t *testing.T) {
		tree := &tree{}
		tree.addLeaf([]string{"a", "b", "c"}, "value1")
		tree.addLeaf([]string{"a", "b", "d"}, "value2")

		assert.Len(t, tree.Leaves, 1)
		assert.Equal(t, "a", tree.Leaves[0].Key)

		level2 := tree.Leaves[0].Leaves
		assert.Len(t, level2, 1)
		assert.Equal(t, "b", level2[0].Key)

		level3 := level2[0].Leaves
		assert.Len(t, level3, 2)

		keys := make(map[string]string)
		for _, leaf := range level3 {
			keys[leaf.Key] = leaf.Value
		}
		assert.Equal(t, "value1", keys["c"])
		assert.Equal(t, "value2", keys["d"])
	})

	t.Run("add leaf to empty path", func(t *testing.T) {
		tree := &tree{}
		tree.addLeaf([]string{}, "value")
		assert.Len(t, tree.Leaves, 0)
	})
}

func TestPrintPTermTree(t *testing.T) {
	entries := []*rpc.MapKeyValueEntry{
		{
			Key:   "network/interface/eth0",
			Value: "attached",
		},
		{
			Key:   "network/interface/eth1",
			Value: "detached",
		},
		{
			Key:   "storage/disk/sda",
			Value: "active",
		},
	}

	err := printPTermTree(entries)
	assert.NoError(t, err)
}

func TestPrintPTermTreeEmpty(t *testing.T) {
	entries := []*rpc.MapKeyValueEntry{}
	err := printPTermTree(entries)
	assert.NoError(t, err)
}
