package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
)

func TestExclusiveModeNewNode(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	labels := map[string]string{"k8s.aliyun.com/exclusive-mode-eni-type": "eniOnly"}
	cniPath := tempFile.Name() + "_cni_config"

	err = setExclusiveMode(store, labels, cniPath)
	assert.NoError(t, err)
	assert.Equal(t, "eniOnly", store.Get(nodecap.NodeCapabilityExclusiveENI))
}

func TestExclusiveModeDoesNotChangeWhenAlreadySet(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	err = os.WriteFile(tempFile.Name(), []byte("cni_exclusive_eni = eniOnly"), 0644)
	assert.NoError(t, err)

	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	labels := map[string]string{"k8s.aliyun.com/exclusive-mode-eni-type": "eniOnly"}
	cniPath := tempFile.Name() + "_cni_config"

	err = setExclusiveMode(store, labels, cniPath)
	assert.NoError(t, err)
	assert.Equal(t, "eniOnly", store.Get(nodecap.NodeCapabilityExclusiveENI))
}

func TestExclusiveModeFailsWhenChangedFromExclusive(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	err = os.WriteFile(tempFile.Name(), []byte("cni_exclusive_eni = eniOnly"), 0644)
	assert.NoError(t, err)

	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	labels := map[string]string{"k8s.aliyun.com/exclusive-mode-eni-type": "default"}
	cniPath := tempFile.Name() + "_cni_config"

	err = setExclusiveMode(store, labels, cniPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exclusive eni mode changed")
}

func TestExclusiveModeWritesCNIConfigWhenSet(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	labels := map[string]string{"k8s.aliyun.com/exclusive-mode-eni-type": "eniOnly"}
	cniPath := tempFile.Name() + "_cni_config"
	defer os.Remove(cniPath)

	err = setExclusiveMode(store, labels, cniPath)
	assert.NoError(t, err)

	content, err := os.ReadFile(cniPath)
	assert.NoError(t, err)
	assert.Contains(t, string(content), `"type": "terway"`)
}

func TestExclusiveModeDoesNotWriteCNIConfigWhenNotSet(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	labels := map[string]string{"k8s.aliyun.com/exclusive-mode-eni-type": "default"}
	cniPath := tempFile.Name() + "_cni_config"
	defer os.Remove(cniPath)

	err = setExclusiveMode(store, labels, cniPath)
	assert.NoError(t, err)

	_, err = os.ReadFile(cniPath)
	assert.Error(t, err)
	assert.True(t, os.IsNotExist(err))
}
