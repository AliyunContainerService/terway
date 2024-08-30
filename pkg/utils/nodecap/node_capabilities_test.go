package nodecap

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCreatesNewFileNodeCapabilitiesInstance(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	store := NewFileNodeCapabilities(tempFile.Name())
	assert.NotNil(t, store)
	assert.Equal(t, tempFile.Name(), store.filePath)
	assert.Empty(t, store.capabilities)
}

func TestLoadsCapabilitiesFromFile(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	err = os.WriteFile(tempFile.Name(), []byte("key=value\n"), 0644)
	assert.NoError(t, err)

	store := NewFileNodeCapabilities(tempFile.Name())
	err = store.Load()
	assert.NoError(t, err)
	assert.Equal(t, "value", store.Get("key"))
}

func TestReturnsEmptyMapWhenFileDoesNotExist(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	os.Remove(tempFile.Name())

	store := NewFileNodeCapabilities(tempFile.Name())
	err = store.Load()
	assert.NoError(t, err)
	assert.Empty(t, store.capabilities)
}

func TestSavesCapabilitiesToFile(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	store := NewFileNodeCapabilities(tempFile.Name())
	store.Set("key", "value")
	err = store.Save()
	assert.NoError(t, err)

	content, err := os.ReadFile(tempFile.Name())
	assert.NoError(t, err)
	assert.Contains(t, string(content), "key = value")
}

func TestOverwritesExistingCapabilitiesInFile(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	err = os.WriteFile(tempFile.Name(), []byte("oldKey=oldValue\n"), 0644)
	assert.NoError(t, err)

	store := NewFileNodeCapabilities(tempFile.Name())
	store.Set("newKey", "newValue")
	err = store.Save()
	assert.NoError(t, err)

	content, err := os.ReadFile(tempFile.Name())
	assert.NoError(t, err)
	assert.Contains(t, string(content), "newKey = newValue")
	assert.NotContains(t, string(content), "oldKey = oldValue")
}

func TestLoadShouldCleanCurrentCache(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	store := NewFileNodeCapabilities(tempFile.Name())
	store.Set("newKey", "newValue")
	err = store.Load()
	assert.NoError(t, err)

	assert.Equal(t, "", store.Get("newKey"))
}
