package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateKey(t *testing.T) {
	generator := NewIdempotentKeyGenerator()

	// Test case 1: paramHash does not exist in cache
	paramHash1 := "paramHash1"
	key1 := generator.GenerateKey(paramHash1)
	assert.NotEmpty(t, key1)

	// Test case 2: paramHash exists in cache with multiple UUIDs
	paramHash2 := "paramHash2"

	uuids := []string{"uuid1", "uuid2", "uuid3"}
	generator.cache.Add(paramHash2, uuids)

	assert.Equal(t, "uuid3", generator.GenerateKey(paramHash2))
	uuids2, ok := generator.cache.Get(paramHash2)
	assert.True(t, ok)
	assert.Len(t, uuids2.([]string), 2)

	assert.Equal(t, "uuid2", generator.GenerateKey(paramHash2))
	uuids2, ok = generator.cache.Get(paramHash2)
	assert.True(t, ok)
	assert.Len(t, uuids2.([]string), 1)
}

func TestPutBack(t *testing.T) {
	// Create a new instance of SimpleIdempotentKeyGenerator
	generator := NewIdempotentKeyGenerator()

	// Generate a paramHash and uuid for testing
	paramHash := "testParamHash"
	uuid1 := "testUUID1"
	uuid2 := "testUUID2"

	// Call PutBack method
	generator.PutBack(paramHash, uuid1)
	generator.PutBack(paramHash, uuid2)

	// Retrieve the value associated with the paramHash from the cache
	value, ok := generator.cache.Get(paramHash)
	assert.True(t, ok)

	// Check if the retrieved value is a slice of strings
	uuids, ok := value.([]string)
	assert.True(t, ok)
	assert.Len(t, uuids, 2)

	assert.Equal(t, uuids, []string{uuid1, uuid2})
}
