package main

import (
	"os"
	"testing"

	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	"github.com/Jeffail/gabs/v2"
	"github.com/stretchr/testify/assert"
)

func Test_parseRelease(t *testing.T) {
	type args struct {
		rel string
	}
	tests := []struct {
		name      string
		args      args
		wantMajor int
		wantMinor int
		wantPatch int
		wantOk    bool
	}{
		{
			name: "test1",
			args: args{
				rel: "6.8.0-51-generic",
			},
			wantMajor: 6,
			wantMinor: 8,
			wantPatch: 0,
			wantOk:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMajor, gotMinor, gotPatch, gotOk := parseRelease(tt.args.rel)
			assert.Equalf(t, tt.wantMajor, gotMajor, "parseRelease(%v)", tt.args.rel)
			assert.Equalf(t, tt.wantMinor, gotMinor, "parseRelease(%v)", tt.args.rel)
			assert.Equalf(t, tt.wantPatch, gotPatch, "parseRelease(%v)", tt.args.rel)
			assert.Equalf(t, tt.wantOk, gotOk, "parseRelease(%v)", tt.args.rel)
		})
	}
}

func Test_storeRuntimeConfig_with_cilium_plugin(t *testing.T) {
	// Create a temporary file for node capabilities
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	// Create JSON with cilium-cni plugin
	container, err := gabs.ParseJSON([]byte(`{
		"plugins": [
			{
				"type": "cilium-cni"
			}
		]
	}`))
	assert.NoError(t, err)

	err = storeRuntimeConfig(tempFile.Name(), container)
	assert.NoError(t, err)

	// Verify capabilities were stored correctly
	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	err = store.Load()
	assert.NoError(t, err)
	assert.Equal(t, True, store.Get(nodecap.NodeCapabilityHasCiliumChainer))
}

func Test_storeRuntimeConfig_with_terway_plugin_network_policy(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	container, err := gabs.ParseJSON([]byte(`{
		"plugins": [
			{
				"type": "terway",
				"network_policy_provider": "ebpf"
			}
		]
	}`))
	assert.NoError(t, err)

	err = storeRuntimeConfig(tempFile.Name(), container)
	assert.NoError(t, err)

	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	err = store.Load()
	assert.NoError(t, err)
	assert.Equal(t, "ebpf", store.Get(nodecap.NodeCapabilityNetworkPolicyProvider))
	assert.Equal(t, False, store.Get(nodecap.NodeCapabilityHasCiliumChainer))
}

func Test_storeRuntimeConfig_with_terway_plugin_datapath(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	container, err := gabs.ParseJSON([]byte(`{
		"plugins": [
			{
				"type": "terway",
				"eniip_virtual_type": "datapathv2"
			}
		]
	}`))
	assert.NoError(t, err)

	err = storeRuntimeConfig(tempFile.Name(), container)
	assert.NoError(t, err)

	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	err = store.Load()
	assert.NoError(t, err)
	assert.Equal(t, "datapathv2", store.Get(nodecap.NodeCapabilityDataPath))
	assert.Equal(t, False, store.Get(nodecap.NodeCapabilityHasCiliumChainer))
}

func Test_storeRuntimeConfig_with_both_plugins(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	container, err := gabs.ParseJSON([]byte(`{
		"plugins": [
			{
				"type": "terway",
				"network_policy_provider": "iptables",
				"eniip_virtual_type": "veth"
			},
			{
				"type": "cilium-cni"
			}
		]
	}`))
	assert.NoError(t, err)

	err = storeRuntimeConfig(tempFile.Name(), container)
	assert.NoError(t, err)

	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	err = store.Load()
	assert.NoError(t, err)
	assert.Equal(t, "iptables", store.Get(nodecap.NodeCapabilityNetworkPolicyProvider))
	assert.Equal(t, "veth", store.Get(nodecap.NodeCapabilityDataPath))
	assert.Equal(t, True, store.Get(nodecap.NodeCapabilityHasCiliumChainer))
}

func Test_storeRuntimeConfig_with_no_relevant_plugins(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	container, err := gabs.ParseJSON([]byte(`{
		"plugins": [
			{
				"type": "portmap"
			}
		]
	}`))
	assert.NoError(t, err)

	err = storeRuntimeConfig(tempFile.Name(), container)
	assert.NoError(t, err)

	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	err = store.Load()
	assert.NoError(t, err)
	assert.Equal(t, False, store.Get(nodecap.NodeCapabilityHasCiliumChainer))
}

func Test_storeRuntimeConfig_invalid_plugin_type(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	container, err := gabs.ParseJSON([]byte(`{
		"plugins": [
			{
				"type": 123
			}
		]
	}`))
	assert.NoError(t, err)

	err = storeRuntimeConfig(tempFile.Name(), container)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "type must be string")
}

func Test_storeRuntimeConfig_missing_type(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	container, err := gabs.ParseJSON([]byte(`{
		"plugins": [
			{
				"name": "test"
			}
		]
	}`))
	assert.NoError(t, err)

	err = storeRuntimeConfig(tempFile.Name(), container)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "type must be string")
}

func Test_storeRuntimeConfig_empty_plugins(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	container, err := gabs.ParseJSON([]byte(`{
		"plugins": []
	}`))
	assert.NoError(t, err)

	err = storeRuntimeConfig(tempFile.Name(), container)
	assert.NoError(t, err)

	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	err = store.Load()
	assert.NoError(t, err)
	assert.Equal(t, False, store.Get(nodecap.NodeCapabilityHasCiliumChainer))
}

func Test_storeRuntimeConfig_terway_without_optional_fields(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	container, err := gabs.ParseJSON([]byte(`{
		"plugins": [
			{
				"type": "terway"
			}
		]
	}`))
	assert.NoError(t, err)

	err = storeRuntimeConfig(tempFile.Name(), container)
	assert.NoError(t, err)

	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	err = store.Load()
	assert.NoError(t, err)
	assert.Equal(t, "", store.Get(nodecap.NodeCapabilityNetworkPolicyProvider))
	assert.Equal(t, "", store.Get(nodecap.NodeCapabilityDataPath))
	assert.Equal(t, False, store.Get(nodecap.NodeCapabilityHasCiliumChainer))
}

func Test_storeRuntimeConfig_store_load_error(t *testing.T) {
	// Use an invalid file path to trigger load error
	container, err := gabs.ParseJSON([]byte(`{
		"plugins": [
			{
				"type": "terway"
			}
		]
	}`))
	assert.NoError(t, err)

	// Use a directory path instead of file path to cause save error
	err = storeRuntimeConfig("/", container)
	assert.Error(t, err)
}

func Test_isMounted(t *testing.T) {
	_, err := isMounted("/sys/fs/cgroup")
	assert.NoError(t, err)
}

func Test_allowEBPFNetworkPolicy(t *testing.T) {

}
