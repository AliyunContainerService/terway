package main

import (
	"errors"
	"os"
	"testing"

	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	"github.com/Jeffail/gabs/v2"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func Test_mergeConfigList(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return false
	}
	out, err := mergeConfigList([][]byte{
		[]byte(`{
            "type":"terway",
            "foo":"bar"
        }`), []byte(`{
            "type":"portmap",
            "capabilities":{
                "portMappings":true
            },
            "externalSetMarkChain":"KUBE-MARK-MASQ"
        }`)}, &feature{})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, "bar", g.Path("plugins.0.foo").Data())
}

func Test_mergeConfigList_ipvl(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return false
	}
	out, err := mergeConfigList([][]byte{
		[]byte(`{
            "type":"terway",
            "foo":"bar",
            "eniip_virtual_type": "ipvlan"
        }`), []byte(`{
            "type":"portmap",
            "capabilities":{
                "portMappings":true
            },
            "externalSetMarkChain":"KUBE-MARK-MASQ"
        }`)}, &feature{
		EBPF: true,
		EDT:  true,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, "bar", g.Path("plugins.0.foo").Data())
	assert.Equal(t, "cilium-cni", g.Path("plugins.2.type").Data())
	assert.Equal(t, "ipvlan", g.Path("plugins.0.eniip_virtual_type").Data())
}

func Test_mergeConfigList_ipvl_exist(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return false
	}
	out, err := mergeConfigList([][]byte{
		[]byte(`{
            "type":"terway",
            "foo":"bar",
            "eniip_virtual_type": "ipvlan"
        }`),
		[]byte(`{
            "type":"cilium-cni"
        }`),
		[]byte(`{
            "type":"portmap",
            "capabilities":{
                "portMappings":true
            },
            "externalSetMarkChain":"KUBE-MARK-MASQ"
        }`)}, &feature{
		EBPF: true,
		EDT:  true,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, "edt", g.Path("plugins.0.bandwidth_mode").Data())
	assert.Equal(t, "bar", g.Path("plugins.0.foo").Data())
	assert.Equal(t, "cilium-cni", g.Path("plugins.1.type").Data())
	assert.Equal(t, "ipvlan", g.Path("plugins.1.datapath").Data())
}

func Test_mergeConfigList_ipvl_unsupport(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return false
	}
	out, err := mergeConfigList([][]byte{
		[]byte(`{
            "type":"terway",
            "foo":"bar",
            "eniip_virtual_type": "ipvlan"
        }`),
		[]byte(`{
            "type":"cilium-cni"
        }`),
		[]byte(`{
            "type":"portmap",
            "capabilities":{
                "portMappings":true
            },
            "externalSetMarkChain":"KUBE-MARK-MASQ"
        }`)}, &feature{
		EBPF: false,
		EDT:  false,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, false, g.ExistsP("plugins.0.eniip_virtual_type"))
	assert.Equal(t, "portmap", g.Path("plugins.1.type").Data())
}

func Test_mergeConfigList_migrate_datapathv2(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return true
	}
	out, err := mergeConfigList([][]byte{
		[]byte(`{
            "type":"terway",
            "foo":"bar",
            "eniip_virtual_type": "ipvlan"
        }`),
		[]byte(`{
            "type":"cilium-cni"
        }`),
		[]byte(`{
            "type":"portmap",
            "capabilities":{
                "portMappings":true
            },
            "externalSetMarkChain":"KUBE-MARK-MASQ"
        }`)}, &feature{
		EBPF: true,
		EDT:  true,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, "datapathv2", g.Path("plugins.0.eniip_virtual_type").Data())
	assert.Equal(t, "cilium-cni", g.Path("plugins.1.type").Data())
	assert.Equal(t, "datapathv2", g.Path("plugins.1.datapath").Data())
	assert.Equal(t, "portmap", g.Path("plugins.2.type").Data())
}

func Test_mergeConfigList_datapathv2(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return true
	}
	out, err := mergeConfigList([][]byte{
		[]byte(`{
			"type":"terway",
			"foo":"bar",
			"eniip_virtual_type": "datapathv2"
		}`), []byte(`{
            "type":"cilium-cni"
        }`),
		[]byte(`{
			"type":"portmap",
			"capabilities":{
				"portMappings":true
			},
			"externalSetMarkChain":"KUBE-MARK-MASQ"
		}`)}, &feature{
		EBPF: true,
		EDT:  true,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, "datapathv2", g.Path("plugins.0.eniip_virtual_type").Data())
	assert.Equal(t, "cilium-cni", g.Path("plugins.1.type").Data())
	assert.Equal(t, "datapathv2", g.Path("plugins.1.datapath").Data())
	assert.Equal(t, "portmap", g.Path("plugins.2.type").Data())
}

func TestVeth(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return true
	}

	_ = os.Remove(nodeCapabilitiesFile)
	out, err := mergeConfigList([][]byte{
		[]byte(`{
			"type":"terway",
			"foo":"bar"
		}`)}, &feature{
		EBPF:                true,
		EDT:                 true,
		EnableNetworkPolicy: true,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, "veth", g.Path("plugins.0.eniip_virtual_type").Data())
	assert.Equal(t, 1, len(g.Path("plugins").Children()))
}

func TestVethWithNoPolicy(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return true
	}

	_ = os.Remove(nodeCapabilitiesFile)
	out, err := mergeConfigList([][]byte{
		[]byte(`{
			"type":"terway",
			"foo":"bar",
            "network_policy_provider": "ebpf"
		}`)}, &feature{
		EBPF:                true,
		EDT:                 true,
		EnableNetworkPolicy: false,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, "veth", g.Path("plugins.0.eniip_virtual_type").Data())
	assert.Equal(t, 1, len(g.Path("plugins").Children()))
}

func TestVethToDatapathV2(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return true
	}

	_ = os.Remove(nodeCapabilitiesFile)
	out, err := mergeConfigList([][]byte{
		[]byte(`{
			"type":"terway",
			"foo":"bar",
            "network_policy_provider": "ebpf"
		}`)}, &feature{
		EBPF:                true,
		EDT:                 true,
		EnableNetworkPolicy: true,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, 2, len(g.Path("plugins").Children()))
	assert.Equal(t, "datapathv2", g.Path("plugins.0.eniip_virtual_type").Data())
	assert.Equal(t, "cilium-cni", g.Path("plugins.1.type").Data())
}

func TestVethNotAllowToSwitch(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return true
	}

	_ = os.Remove(nodeCapabilitiesFile)
	store := nodecap.NewFileNodeCapabilities(nodeCapabilitiesFile)
	store.Set(nodecap.NodeCapabilityHasCiliumChainer, False)
	err := store.Save()
	assert.NoError(t, err)

	out, err := mergeConfigList([][]byte{
		[]byte(`{
			"type":"terway",
			"foo":"bar",
            "network_policy_provider": "ebpf"
		}`)}, &feature{
		EBPF:                true,
		EDT:                 true,
		EnableNetworkPolicy: true,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, 1, len(g.Path("plugins").Children()))
	assert.Equal(t, "veth", g.Path("plugins.0.eniip_virtual_type").Data())
}

// Test processCNIConfig function
func TestProcessCNIConfig_Success(t *testing.T) {
	// Create temporary directories and files for testing
	tempDir, err := os.MkdirTemp("", "cni_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Save original values
	originalOutPutPath := outPutPath
	originalCheckKernelVersion := _checkKernelVersion
	originalSwitchDataPathV2 := _switchDataPathV2
	defer func() {
		outPutPath = originalOutPutPath
		_checkKernelVersion = originalCheckKernelVersion
		_switchDataPathV2 = originalSwitchDataPathV2
	}()

	// Set up output path
	outPutPath = tempDir + "/10-terway.conflist"

	// Create test CNI config output file
	testCNIOutput := `{
		"cniVersion": "0.4.0",
		"name": "terway-chainer",
		"plugins": [
			{"type": "terway", "eniip_virtual_type": "veth"}
		]
	}`
	err = os.WriteFile(outPutPath, []byte(testCNIOutput), 0644)
	assert.NoError(t, err)

	// Patch getAllConfig
	patchGetAllConfig := gomonkey.ApplyFunc(getAllConfig, func(base string) (*TerwayConfig, error) {
		return &TerwayConfig{
			cniConfig:           []byte(`{"type":"terway","foo":"bar"}`),
			enableNetworkPolicy: true,
		}, nil
	})
	defer patchGetAllConfig.Reset()

	// Patch checkBpfFeature
	patchCheckBpfFeature := gomonkey.ApplyFunc(checkBpfFeature, func(key string) (bool, error) {
		return true, nil
	})
	defer patchCheckBpfFeature.Reset()

	// Patch storeRuntimeConfig
	patchStoreRuntimeConfig := gomonkey.ApplyFunc(storeRuntimeConfig, func(filePath string, container *gabs.Container) error {
		return nil
	})
	defer patchStoreRuntimeConfig.Reset()

	// Mock kernel version check to always return true
	_checkKernelVersion = func(major, minor, patch int) bool {
		return true
	}

	_switchDataPathV2 = func() bool {
		return false
	}

	// Patch os.WriteFile to succeed
	patchWriteFile := gomonkey.ApplyFunc(os.WriteFile, func(name string, data []byte, perm os.FileMode) error {
		return nil
	})
	defer patchWriteFile.Reset()

	cmd := &cobra.Command{}
	err = processCNIConfig(cmd, []string{})
	assert.NoError(t, err)
}

func TestProcessCNIConfig_GetAllConfigError(t *testing.T) {
	// Save original values
	originalCheckKernelVersion := _checkKernelVersion
	originalSwitchDataPathV2 := _switchDataPathV2
	defer func() {
		_checkKernelVersion = originalCheckKernelVersion
		_switchDataPathV2 = originalSwitchDataPathV2
	}()

	// Mock kernel version check
	_checkKernelVersion = func(major, minor, patch int) bool {
		return true
	}

	_switchDataPathV2 = func() bool {
		return false
	}

	// Patch getAllConfig to return error
	patchGetAllConfig := gomonkey.ApplyFunc(getAllConfig, func(base string) (*TerwayConfig, error) {
		return nil, errors.New("config file not found")
	})
	defer patchGetAllConfig.Reset()

	cmd := &cobra.Command{}
	err := processCNIConfig(cmd, []string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config file not found")
}

func TestProcessCNIConfig_KernelVersionError(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "cni_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Save original values
	originalOutPutPath := outPutPath
	defer func() {
		outPutPath = originalOutPutPath
	}()

	outPutPath = tempDir + "/output.conflist"

	// Patch checkKernelVersion to return false (unsupported kernel)
	// Note: processCNIConfig sets _checkKernelVersion = checkKernelVersion,
	// so we need to patch the actual function
	patchCheckKernelVersion := gomonkey.ApplyFunc(checkKernelVersion, func(major, minor, patch int) bool {
		return false
	})
	defer patchCheckKernelVersion.Reset()

	// Patch switchDataPathV2
	patchSwitchDataPathV2 := gomonkey.ApplyFunc(switchDataPathV2, func() bool {
		return false
	})
	defer patchSwitchDataPathV2.Reset()

	// Patch getAllConfig to return valid config
	patchGetAllConfig := gomonkey.ApplyFunc(getAllConfig, func(base string) (*TerwayConfig, error) {
		return &TerwayConfig{
			cniConfig:           []byte(`{"type":"terway"}`),
			enableNetworkPolicy: true,
		}, nil
	})
	defer patchGetAllConfig.Reset()

	cmd := &cobra.Command{}
	err = processCNIConfig(cmd, []string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupport kernel version")
}

func TestProcessCNIConfig_ReadFileError(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "cni_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Save original values
	originalOutPutPath := outPutPath
	originalCheckKernelVersion := _checkKernelVersion
	originalSwitchDataPathV2 := _switchDataPathV2
	defer func() {
		outPutPath = originalOutPutPath
		_checkKernelVersion = originalCheckKernelVersion
		_switchDataPathV2 = originalSwitchDataPathV2
	}()

	// Set output path to non-existent file
	outPutPath = tempDir + "/non_existent_file.conf"

	// Mock kernel version check
	_checkKernelVersion = func(major, minor, patch int) bool {
		return true
	}

	_switchDataPathV2 = func() bool {
		return false
	}

	// Patch getAllConfig to return valid config
	patchGetAllConfig := gomonkey.ApplyFunc(getAllConfig, func(base string) (*TerwayConfig, error) {
		return &TerwayConfig{
			cniConfig:           []byte(`{"type":"terway"}`),
			enableNetworkPolicy: true,
		}, nil
	})
	defer patchGetAllConfig.Reset()

	// Patch checkBpfFeature
	patchCheckBpfFeature := gomonkey.ApplyFunc(checkBpfFeature, func(key string) (bool, error) {
		return true, nil
	})
	defer patchCheckBpfFeature.Reset()

	// Patch os.WriteFile to succeed
	patchWriteFile := gomonkey.ApplyFunc(os.WriteFile, func(name string, data []byte, perm os.FileMode) error {
		return nil
	})
	defer patchWriteFile.Reset()

	// Don't create the output file - this will cause os.ReadFile to fail
	cmd := &cobra.Command{}
	err = processCNIConfig(cmd, []string{})
	assert.Error(t, err)
}

// Test processInput function
func TestProcessInput_Success(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "cni_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Save original values
	originalOutPutPath := outPutPath
	originalCheckKernelVersion := _checkKernelVersion
	originalSwitchDataPathV2 := _switchDataPathV2
	defer func() {
		outPutPath = originalOutPutPath
		_checkKernelVersion = originalCheckKernelVersion
		_switchDataPathV2 = originalSwitchDataPathV2
	}()

	outPutPath = tempDir + "/output.conflist"

	_checkKernelVersion = func(major, minor, patch int) bool {
		return true
	}

	_switchDataPathV2 = func() bool {
		return false
	}

	// Patch getAllConfig
	patchGetAllConfig := gomonkey.ApplyFunc(getAllConfig, func(base string) (*TerwayConfig, error) {
		return &TerwayConfig{
			cniConfig:           []byte(`{"type":"terway","foo":"bar"}`),
			enableNetworkPolicy: true,
		}, nil
	})
	defer patchGetAllConfig.Reset()

	// Patch checkBpfFeature
	patchCheckBpfFeature := gomonkey.ApplyFunc(checkBpfFeature, func(key string) (bool, error) {
		return true, nil
	})
	defer patchCheckBpfFeature.Reset()

	err = processInput()
	assert.NoError(t, err)

	// Verify output file was created
	_, err = os.Stat(outPutPath)
	assert.NoError(t, err)
}

func TestProcessInput_WithConfList(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "cni_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Save original values
	originalOutPutPath := outPutPath
	originalCheckKernelVersion := _checkKernelVersion
	originalSwitchDataPathV2 := _switchDataPathV2
	defer func() {
		outPutPath = originalOutPutPath
		_checkKernelVersion = originalCheckKernelVersion
		_switchDataPathV2 = originalSwitchDataPathV2
	}()

	outPutPath = tempDir + "/output.conflist"

	_checkKernelVersion = func(major, minor, patch int) bool {
		return true
	}

	_switchDataPathV2 = func() bool {
		return false
	}

	// Patch getAllConfig with conflist
	patchGetAllConfig := gomonkey.ApplyFunc(getAllConfig, func(base string) (*TerwayConfig, error) {
		return &TerwayConfig{
			cniConfig: []byte(`{"type":"terway"}`),
			cniConfigList: []byte(`{
				"cniVersion": "0.4.0",
				"name": "test",
				"plugins": [
					{"type":"terway"},
					{"type":"portmap"}
				]
			}`),
			enableNetworkPolicy: true,
		}, nil
	})
	defer patchGetAllConfig.Reset()

	// Patch checkBpfFeature
	patchCheckBpfFeature := gomonkey.ApplyFunc(checkBpfFeature, func(key string) (bool, error) {
		return true, nil
	})
	defer patchCheckBpfFeature.Reset()

	err = processInput()
	assert.NoError(t, err)
}

func TestProcessInput_InvalidJSON(t *testing.T) {
	// Save original values
	originalCheckKernelVersion := _checkKernelVersion
	originalSwitchDataPathV2 := _switchDataPathV2
	defer func() {
		_checkKernelVersion = originalCheckKernelVersion
		_switchDataPathV2 = originalSwitchDataPathV2
	}()

	_checkKernelVersion = func(major, minor, patch int) bool {
		return true
	}

	_switchDataPathV2 = func() bool {
		return false
	}

	// Patch getAllConfig with invalid JSON
	patchGetAllConfig := gomonkey.ApplyFunc(getAllConfig, func(base string) (*TerwayConfig, error) {
		return &TerwayConfig{
			cniConfig:           []byte(`{invalid json}`),
			enableNetworkPolicy: true,
		}, nil
	})
	defer patchGetAllConfig.Reset()

	err := processInput()
	assert.Error(t, err)
}

func TestProcessInput_CheckBpfFeatureError(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "cni_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Save original values
	originalOutPutPath := outPutPath
	originalCheckKernelVersion := _checkKernelVersion
	originalSwitchDataPathV2 := _switchDataPathV2
	defer func() {
		outPutPath = originalOutPutPath
		_checkKernelVersion = originalCheckKernelVersion
		_switchDataPathV2 = originalSwitchDataPathV2
	}()

	outPutPath = tempDir + "/output.conflist"

	_checkKernelVersion = func(major, minor, patch int) bool {
		return true
	}

	_switchDataPathV2 = func() bool {
		return false
	}

	// Patch getAllConfig
	patchGetAllConfig := gomonkey.ApplyFunc(getAllConfig, func(base string) (*TerwayConfig, error) {
		return &TerwayConfig{
			cniConfig:           []byte(`{"type":"terway"}`),
			enableNetworkPolicy: true,
		}, nil
	})
	defer patchGetAllConfig.Reset()

	// Patch checkBpfFeature to return error
	patchCheckBpfFeature := gomonkey.ApplyFunc(checkBpfFeature, func(key string) (bool, error) {
		return false, errors.New("bpftool not found")
	})
	defer patchCheckBpfFeature.Reset()

	err = processInput()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bpftool not found")
}

func TestProcessInput_WriteFileError(t *testing.T) {
	// Save original values
	originalOutPutPath := outPutPath
	originalCheckKernelVersion := _checkKernelVersion
	originalSwitchDataPathV2 := _switchDataPathV2
	defer func() {
		outPutPath = originalOutPutPath
		_checkKernelVersion = originalCheckKernelVersion
		_switchDataPathV2 = originalSwitchDataPathV2
	}()

	// Set output path to an invalid location
	outPutPath = "/nonexistent/path/output.conflist"

	_checkKernelVersion = func(major, minor, patch int) bool {
		return true
	}

	_switchDataPathV2 = func() bool {
		return false
	}

	// Patch getAllConfig
	patchGetAllConfig := gomonkey.ApplyFunc(getAllConfig, func(base string) (*TerwayConfig, error) {
		return &TerwayConfig{
			cniConfig:           []byte(`{"type":"terway"}`),
			enableNetworkPolicy: true,
		}, nil
	})
	defer patchGetAllConfig.Reset()

	// Patch checkBpfFeature
	patchCheckBpfFeature := gomonkey.ApplyFunc(checkBpfFeature, func(key string) (bool, error) {
		return true, nil
	})
	defer patchCheckBpfFeature.Reset()

	err := processInput()
	assert.Error(t, err)
}

// Test getAllConfig function
func TestGetAllConfig_Success(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "getAllConfig_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create required config files
	err = os.WriteFile(tempDir+"/10-terway.conf", []byte(`{"type":"terway"}`), 0644)
	assert.NoError(t, err)

	err = os.WriteFile(tempDir+"/10-terway.conflist", []byte(`{"plugins":[{"type":"terway"}]}`), 0644)
	assert.NoError(t, err)

	err = os.WriteFile(tempDir+"/eni_conf", []byte(`{"eni":"config"}`), 0644)
	assert.NoError(t, err)

	cfg, err := getAllConfig(tempDir)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.Equal(t, []byte(`{"plugins":[{"type":"terway"}]}`), cfg.cniConfigList)
}

func TestGetAllConfig_DisableNetworkPolicy(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "getAllConfig_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create required config files
	err = os.WriteFile(tempDir+"/10-terway.conf", []byte(`{"type":"terway"}`), 0644)
	assert.NoError(t, err)

	err = os.WriteFile(tempDir+"/eni_conf", []byte(`{"eni":"config"}`), 0644)
	assert.NoError(t, err)

	// Create disable_network_policy file with "true" value
	err = os.WriteFile(tempDir+"/disable_network_policy", []byte("true"), 0644)
	assert.NoError(t, err)

	cfg, err := getAllConfig(tempDir)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.False(t, cfg.enableNetworkPolicy)
}

func TestGetAllConfig_EnableNetworkPolicy(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "getAllConfig_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create required config files
	err = os.WriteFile(tempDir+"/10-terway.conf", []byte(`{"type":"terway"}`), 0644)
	assert.NoError(t, err)

	err = os.WriteFile(tempDir+"/eni_conf", []byte(`{"eni":"config"}`), 0644)
	assert.NoError(t, err)

	// Create disable_network_policy file with "false" value
	err = os.WriteFile(tempDir+"/disable_network_policy", []byte("false"), 0644)
	assert.NoError(t, err)

	cfg, err := getAllConfig(tempDir)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.True(t, cfg.enableNetworkPolicy)
}

func TestGetAllConfig_InClusterLB(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "getAllConfig_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create required config files
	err = os.WriteFile(tempDir+"/10-terway.conf", []byte(`{"type":"terway"}`), 0644)
	assert.NoError(t, err)

	err = os.WriteFile(tempDir+"/eni_conf", []byte(`{"eni":"config"}`), 0644)
	assert.NoError(t, err)

	// Create in_cluster_loadbalance file
	err = os.WriteFile(tempDir+"/in_cluster_loadbalance", []byte("true"), 0644)
	assert.NoError(t, err)

	cfg, err := getAllConfig(tempDir)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.True(t, cfg.enableInClusterLB)
}

func TestGetAllConfig_MissingCniConfig(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "getAllConfig_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Don't create 10-terway.conf file

	cfg, err := getAllConfig(tempDir)
	assert.Error(t, err)
	assert.Nil(t, cfg)
}

func TestGetAllConfig_MissingEniConf(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "getAllConfig_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create only cni config
	err = os.WriteFile(tempDir+"/10-terway.conf", []byte(`{"type":"terway"}`), 0644)
	assert.NoError(t, err)

	// Don't create eni_conf file

	cfg, err := getAllConfig(tempDir)
	assert.Error(t, err)
	assert.Nil(t, cfg)
}
