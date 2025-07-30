package main

import (
	"encoding/json"
	"net"
	"os"
	"runtime"
	"testing"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"

	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	"github.com/AliyunContainerService/terway/types/daemon"
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

func TestGetENIConfig(t *testing.T) {
	// Create a temporary directory for the ENI configuration
	tempDir, err := os.MkdirTemp("", "test_eni_config")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create the required ENI configuration files
	eniConf := `{
		"ip_stack": "dual",
		"eni_tags": {
			"key1": "value1",
			"key2": "value2"
		},
		"max_pool_size": 10,
		"min_pool_size": 2
	}`

	// Write the ENI configuration file
	err = os.WriteFile(tempDir+"/eni_conf", []byte(eniConf), 0644)
	assert.NoError(t, err)

	// Write the required 10-terway.conf file
	terwayConf := `{
		"cniVersion": "0.4.0",
		"name": "terway",
		"type": "terway"
	}`
	err = os.WriteFile(tempDir+"/10-terway.conf", []byte(terwayConf), 0644)
	assert.NoError(t, err)

	// Test the getAllConfig function directly with the temp directory
	cfg, err := getAllConfig(tempDir)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.NotNil(t, cfg.eniConfig)

	// Parse the ENI config
	var daemonCfg daemon.Config
	err = json.Unmarshal(cfg.eniConfig, &daemonCfg)
	assert.NoError(t, err)
	assert.Equal(t, "dual", daemonCfg.IPStack)
}

func TestGetENIConfigMissingFiles(t *testing.T) {
	// Create a temporary directory without required files
	tempDir, err := os.MkdirTemp("", "test_eni_config_missing")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Test getAllConfig with missing files
	_, err = getAllConfig(tempDir)
	assert.Error(t, err)
}

func TestDualStack(t *testing.T) {
	// Create a temporary file for node capabilities
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	// Test dual stack with "dual" IP stack
	eniCfg = &daemon.Config{IPStack: "dual"}

	cmd := &cobra.Command{}
	args := []string{}

	// Create a test function that uses the temp file
	testDualStack := func(cmd *cobra.Command, args []string) error {
		store := nodecap.NewFileNodeCapabilities(tempFile.Name())

		val := ""
		switch eniCfg.IPStack {
		case "dual", "ipv6":
			val = True
		default:
			val = False
		}

		err := store.Load()
		if err != nil {
			return err
		}

		store.Set(nodecap.NodeCapabilityIPv6, val)
		return store.Save()
	}

	err = testDualStack(cmd, args)
	assert.NoError(t, err)

	// Verify the capability was set correctly
	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	err = store.Load()
	assert.NoError(t, err)
	assert.Equal(t, True, store.Get(nodecap.NodeCapabilityIPv6))
}

func TestDualStackIPv6(t *testing.T) {
	// Create a temporary file for node capabilities
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	// Test dual stack with "ipv6" IP stack
	eniCfg = &daemon.Config{IPStack: "ipv6"}

	cmd := &cobra.Command{}
	args := []string{}

	// Create a test function that uses the temp file
	testDualStack := func(cmd *cobra.Command, args []string) error {
		store := nodecap.NewFileNodeCapabilities(tempFile.Name())

		val := ""
		switch eniCfg.IPStack {
		case "dual", "ipv6":
			val = True
		default:
			val = False
		}

		err := store.Load()
		if err != nil {
			return err
		}

		store.Set(nodecap.NodeCapabilityIPv6, val)
		return store.Save()
	}

	err = testDualStack(cmd, args)
	assert.NoError(t, err)

	// Verify the capability was set correctly
	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	err = store.Load()
	assert.NoError(t, err)
	assert.Equal(t, True, store.Get(nodecap.NodeCapabilityIPv6))
}

func TestDualStackIPv4(t *testing.T) {
	// Create a temporary file for node capabilities
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	// Test dual stack with "ipv4" IP stack
	eniCfg = &daemon.Config{IPStack: "ipv4"}

	cmd := &cobra.Command{}
	args := []string{}

	// Create a test function that uses the temp file
	testDualStack := func(cmd *cobra.Command, args []string) error {
		store := nodecap.NewFileNodeCapabilities(tempFile.Name())

		val := ""
		switch eniCfg.IPStack {
		case "dual", "ipv6":
			val = True
		default:
			val = False
		}

		err := store.Load()
		if err != nil {
			return err
		}

		store.Set(nodecap.NodeCapabilityIPv6, val)
		return store.Save()
	}

	err = testDualStack(cmd, args)
	assert.NoError(t, err)

	// Verify the capability was set correctly
	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	err = store.Load()
	assert.NoError(t, err)
	assert.Equal(t, False, store.Get(nodecap.NodeCapabilityIPv6))
}

func TestDualStackOther(t *testing.T) {
	// Create a temporary file for node capabilities
	tempFile, err := os.CreateTemp("", "test_node_capabilities")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())

	// Test dual stack with other IP stack value
	eniCfg = &daemon.Config{IPStack: "other"}

	cmd := &cobra.Command{}
	args := []string{}

	// Create a test function that uses the temp file
	testDualStack := func(cmd *cobra.Command, args []string) error {
		store := nodecap.NewFileNodeCapabilities(tempFile.Name())

		val := ""
		switch eniCfg.IPStack {
		case "dual", "ipv6":
			val = True
		default:
			val = False
		}

		err := store.Load()
		if err != nil {
			return err
		}

		store.Set(nodecap.NodeCapabilityIPv6, val)
		return store.Save()
	}

	err = testDualStack(cmd, args)
	assert.NoError(t, err)

	// Verify the capability was set correctly
	store := nodecap.NewFileNodeCapabilities(tempFile.Name())
	err = store.Load()
	assert.NoError(t, err)
	assert.Equal(t, False, store.Get(nodecap.NodeCapabilityIPv6))
}

func TestSymmetricRouting(t *testing.T) {
	// Create a temporary directory for the CNI configuration
	tempDir, err := os.MkdirTemp("", "test_cni_config")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create the CNI configuration file path
	cniFilePath := tempDir + "/10-terway.conflist"

	// CNI configuration content with symmetric_routing enabled
	cniConfig := `{
		"plugins": [
			{
				"cniVersion": "0.4.0",
				"name": "terway",
				"type": "terway",
				"capabilities": {"bandwidth": true},
				"symmetric_routing": true,
				"symmetric_routing_config": {
					"mark": 32,
					"mask": 32,
					"table_id": 100,
					"rule_priority": 600,
					"comment": "custom-terway-symmetric"
				}
			}
		]
	}`

	// Write the CNI configuration to the file
	err = os.WriteFile(cniFilePath, []byte(cniConfig), 0644)
	assert.NoError(t, err)

	eniCfg = nil
	err = setSymmetricRouting(cniFilePath)
	require.Error(t, err, "missing eni_config")
}

func TestSymmetricRoutingEmptyNetns(t *testing.T) {
	// Create a temporary directory for the CNI configuration
	tempDir, err := os.MkdirTemp("", "test_cni_config")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create the CNI configuration file path
	cniFilePath := tempDir + "/10-terway.conflist"

	// CNI configuration content with symmetric_routing enabled
	cniConfig := `{
		"plugins": [
			{
				"cniVersion": "0.4.0",
				"name": "terway",
				"type": "terway",
				"capabilities": {"bandwidth": true},
				"symmetric_routing": true,
				"symmetric_routing_config": {
					"mark": 32,
					"mask": 32,
					"table_id": 100,
					"rule_priority": 600,
					"comment": "custom-terway-symmetric"
				}
			}
		]
	}`

	// Write the CNI configuration to the file
	err = os.WriteFile(cniFilePath, []byte(cniConfig), 0644)
	assert.NoError(t, err)

	if os.Geteuid() != 0 {
		t.Skip("This test requires root privileges")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	containerNS, err := testutils.NewNS()
	require.NoError(t, err)
	defer func() {
		err := containerNS.Close()
		require.NoError(t, err)

		err = testutils.UnmountNS(containerNS)
		require.NoError(t, err)
	}()
	_ = containerNS.Do(func(ns ns.NetNS) error {
		eniCfg = &daemon.Config{IPStack: "dual"}
		err = setSymmetricRouting(cniFilePath)
		require.Error(t, err)
		return nil
	})
}

func TestSymmetricRoutingNoRouteTest(t *testing.T) {
	// Create a temporary directory for the CNI configuration
	tempDir, err := os.MkdirTemp("", "test_cni_config")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create the CNI configuration file path
	cniFilePath := tempDir + "/10-terway.conflist"

	// CNI configuration content with symmetric_routing enabled
	cniConfig := `{
		"plugins": [
			{
				"cniVersion": "0.4.0",
				"name": "terway",
				"type": "terway",
				"capabilities": {"bandwidth": true},
				"symmetric_routing": true,
				"symmetric_routing_config": {
					"mark": 32,
					"mask": 32,
					"table_id": 100,
					"rule_priority": 600,
					"comment": "custom-terway-symmetric"
				}
			}
		]
	}`

	// Write the CNI configuration to the file
	err = os.WriteFile(cniFilePath, []byte(cniConfig), 0644)
	assert.NoError(t, err)

	if os.Geteuid() != 0 {
		t.Skip("This test requires root privileges")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	containerNS, err := testutils.NewNS()
	require.NoError(t, err)
	defer func() {
		err := containerNS.Close()
		require.NoError(t, err)

		err = testutils.UnmountNS(containerNS)
		require.NoError(t, err)
	}()
	_ = containerNS.Do(func(ns ns.NetNS) error {
		dummy := netlink.Dummy{}
		dummy.Name = "eth0"
		err := netlink.LinkAdd(&dummy)
		require.NoError(t, err)

		dummyLink, err := netlink.LinkByName("eth0")
		require.NoError(t, err)

		err = netlink.AddrAdd(dummyLink, &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   net.ParseIP("192.168.1.2"),
				Mask: net.IPv4Mask(255, 255, 255, 255),
			},
		})
		require.NoError(t, err)

		err = netlink.LinkSetUp(dummyLink)
		require.NoError(t, err)

		eniCfg = &daemon.Config{IPStack: "dual"}
		err = setSymmetricRouting(cniFilePath)
		require.Error(t, err)
		return nil
	})
}

func TestSymmetricRoutingIPv6Err(t *testing.T) {
	// Create a temporary directory for the CNI configuration
	tempDir, err := os.MkdirTemp("", "test_cni_config")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create the CNI configuration file path
	cniFilePath := tempDir + "/10-terway.conflist"

	// CNI configuration content with symmetric_routing enabled
	cniConfig := `{
		"plugins": [
			{
				"cniVersion": "0.4.0",
				"name": "terway",
				"type": "terway",
				"capabilities": {"bandwidth": true},
				"symmetric_routing": true,
				"symmetric_routing_config": {
					"mark": 32,
					"mask": 32,
					"table_id": 100,
					"rule_priority": 600,
					"comment": "custom-terway-symmetric"
				}
			}
		]
	}`

	// Write the CNI configuration to the file
	err = os.WriteFile(cniFilePath, []byte(cniConfig), 0644)
	assert.NoError(t, err)

	if os.Geteuid() != 0 {
		t.Skip("This test requires root privileges")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	containerNS, err := testutils.NewNS()
	require.NoError(t, err)
	defer func() {
		err := containerNS.Close()
		require.NoError(t, err)

		err = testutils.UnmountNS(containerNS)
		require.NoError(t, err)
	}()
	_ = containerNS.Do(func(ns ns.NetNS) error {
		dummy := netlink.Dummy{}
		dummy.Name = "eth0"
		err := netlink.LinkAdd(&dummy)
		require.NoError(t, err)

		dummyLink, err := netlink.LinkByName("eth0")
		require.NoError(t, err)

		err = netlink.AddrAdd(dummyLink, &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   net.ParseIP("192.168.1.2"),
				Mask: net.IPv4Mask(255, 255, 255, 255),
			},
		})
		require.NoError(t, err)

		err = netlink.LinkSetUp(dummyLink)
		require.NoError(t, err)

		eniCfg = &daemon.Config{IPStack: "ipv6"}
		err = setSymmetricRouting(cniFilePath)
		require.Error(t, err)
		return nil
	})
}
func TestSymmetricRoutingParseError(t *testing.T) {
	// Create a temporary directory for the CNI configuration
	tempDir, err := os.MkdirTemp("", "test_cni_config")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create the CNI configuration file path
	cniFilePath := tempDir + "/10-terway.conflist"

	// CNI configuration content with symmetric_routing enabled
	cniConfig := `{
		"plugins": [
			{
				"cniVersion": "0.4.0",
				"name": "terway",
				"type": "terway",
			}
		]
	}`

	// Write the CNI configuration to the file
	err = os.WriteFile(cniFilePath, []byte(cniConfig), 0644)
	assert.NoError(t, err)

	eniCfg = nil
	err = setSymmetricRouting(cniFilePath)
	require.Error(t, err, "parse error")
}
