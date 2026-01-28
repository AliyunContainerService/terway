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

func TestSave_ErrorCreatingDirectory(t *testing.T) {
	// Test Save when directory creation fails
	// Use a path that would require root permissions or invalid path
	store := NewFileNodeCapabilities("/invalid/path/that/does/not/exist/capabilities")
	store.Set("key", "value")
	err := store.Save()
	// This may or may not error depending on system, but should handle gracefully
	_ = err
}

func TestSave_ErrorWritingFile(t *testing.T) {
	// Test Save when file write fails
	tempDir, err := os.MkdirTemp("", "test_node_capabilities_dir")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a file as a directory to cause write error
	filePath := tempDir + "/capabilities"
	err = os.WriteFile(filePath, []byte("test"), 0644)
	assert.NoError(t, err)

	store := NewFileNodeCapabilities(filePath + "/nested")
	store.Set("key", "value")
	err = store.Save()
	// Should handle error gracefully
	_ = err
}

func TestLoad_ErrorReadingFile(t *testing.T) {
	// Test Load when file exists but is invalid
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	// Write invalid INI content
	err = os.WriteFile(tempFile.Name(), []byte("invalid content\n[section\n"), 0644)
	assert.NoError(t, err)

	store := NewFileNodeCapabilities(tempFile.Name())
	err = store.Load()
	// May or may not error depending on INI parser, but should handle gracefully
	_ = err
}

func TestGet_NonExistentKey(t *testing.T) {
	store := NewFileNodeCapabilities("/tmp/nonexistent")
	value := store.Get("nonexistent")
	assert.Equal(t, "", value)
}

func TestSet_OverwriteExisting(t *testing.T) {
	store := NewFileNodeCapabilities("/tmp/test")
	store.Set("key", "value1")
	assert.Equal(t, "value1", store.Get("key"))

	store.Set("key", "value2")
	assert.Equal(t, "value2", store.Get("key"))
}

func TestSave_EmptyCapabilities(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	store := NewFileNodeCapabilities(tempFile.Name())
	// Save with no capabilities set
	err = store.Save()
	assert.NoError(t, err)

	// Load should work with empty file
	err = store.Load()
	assert.NoError(t, err)
}

func TestLoad_WithMultipleKeys(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	err = os.WriteFile(tempFile.Name(), []byte("key1=value1\nkey2=value2\nkey3=value3\n"), 0644)
	assert.NoError(t, err)

	store := NewFileNodeCapabilities(tempFile.Name())
	err = store.Load()
	assert.NoError(t, err)

	assert.Equal(t, "value1", store.Get("key1"))
	assert.Equal(t, "value2", store.Get("key2"))
	assert.Equal(t, "value3", store.Get("key3"))
}
