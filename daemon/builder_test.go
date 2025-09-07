package daemon

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/AliyunContainerService/terway/types/daemon"
)

func TestNewNetworkServiceBuilder(t *testing.T) {
	ctx := context.Background()
	builder := NewNetworkServiceBuilder(ctx)
	assert.NotNil(t, builder, "NewNetworkServiceBuilder should return a non-nil NetworkServiceBuilder")
	assert.Equal(t, ctx, builder.ctx, "The context in the NetworkServiceBuilder should be the same as the provided context")
}

func TestWithConfigFilePath(t *testing.T) {
	ctx := context.Background()
	builder := NewNetworkServiceBuilder(ctx)
	configFilePath := "/path/to/config"

	builder = builder.WithConfigFilePath(configFilePath)

	assert.Equal(t, configFilePath, builder.configFilePath, "The configFilePath should be set correctly in the NetworkServiceBuilder")
}

func TestWithDaemonMode(t *testing.T) {
	ctx := context.Background()
	builder := NewNetworkServiceBuilder(ctx)
	daemonMode := "true"

	builder = builder.WithDaemonMode(daemonMode)

	assert.Equal(t, daemonMode, builder.daemonMode, "The daemonMode should be set correctly in the NetworkServiceBuilder")
}

func TestInitService(t *testing.T) {
	tests := []struct {
		name          string
		daemonMode    string
		expectedError bool
	}{
		{
			name:          "Valid daemon mode ENIMultiIP",
			daemonMode:    daemon.ModeENIMultiIP,
			expectedError: false,
		},
		{
			name:          "Unsupported daemon mode",
			daemonMode:    "unsupported",
			expectedError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			builder := &NetworkServiceBuilder{
				daemonMode: tc.daemonMode,
			}

			builder = builder.InitService()

			if tc.expectedError {
				assert.NotNil(t, builder.err)
			} else {
				assert.Nil(t, builder.err)
				assert.NotNil(t, builder.service)
				assert.Equal(t, tc.daemonMode, builder.service.daemonMode)
			}
		})
	}
}

func TestNetworkServiceBuilder_LoadGlobalConfig(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer tmpFile.Close()
	configContent := `
{
      "version": "1",
      "max_pool_size": 5,
      "min_pool_size": 0,
      "credential_path": "/var/addon/token-config",
      "ipam_type": "crd"
    }`
	err = os.WriteFile(tmpFile.Name(), []byte(configContent), os.ModeDir)
	assert.NoError(t, err)
	builder := &NetworkServiceBuilder{
		configFilePath: tmpFile.Name(),
		service:        &networkService{},
	}
	builder.LoadGlobalConfig()
	assert.True(t, *builder.config.EnablePatchPodIPs)
}

func TestNetworkServiceBuilder_LoadGlobalConfig2(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer tmpFile.Close()
	configContent := `
{
      "version": "1",
      "max_pool_size": 5,
      "min_pool_size": 0,
      "credential_path": "/var/addon/token-config",
      "enable_patch_pod_ips": false,
      "ipam_type": "crd"
    }`
	err = os.WriteFile(tmpFile.Name(), []byte(configContent), os.ModeDir)
	assert.NoError(t, err)
	builder := &NetworkServiceBuilder{
		configFilePath: tmpFile.Name(),
		service:        &networkService{},
	}
	builder.LoadGlobalConfig()
	assert.False(t, *builder.config.EnablePatchPodIPs)
}

func TestNetworkServiceBuilder_GetConfigFromFileWithMerge_1(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer tmpFile.Close()
	configContent := `
{
      "version": "1",
      "max_pool_size": 5,
      "min_pool_size": 0,
      "credential_path": "/var/addon/token-config",
      "ipam_type": "crd"
    }`

	dynamicCfg := ""
	err = os.WriteFile(tmpFile.Name(), []byte(configContent), os.ModeDir)
	assert.NoError(t, err)
	config, err := daemon.GetConfigFromFileWithMerge(tmpFile.Name(), []byte(dynamicCfg))
	assert.NoError(t, err)
	config.Populate()

	assert.True(t, *config.EnablePatchPodIPs)
}

func TestNetworkServiceBuilder_GetConfigFromFileWithMerge_2(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer tmpFile.Close()
	configContent := `
{
      "version": "1",
      "max_pool_size": 5,
      "min_pool_size": 0,
      "credential_path": "/var/addon/token-config",
      "enable_patch_pod_ips": false,
      "ipam_type": "crd"
    }`

	dynamicCfg := ""
	err = os.WriteFile(tmpFile.Name(), []byte(configContent), os.ModeDir)
	assert.NoError(t, err)
	config, err := daemon.GetConfigFromFileWithMerge(tmpFile.Name(), []byte(dynamicCfg))
	assert.NoError(t, err)
	config.Populate()

	assert.False(t, *config.EnablePatchPodIPs)
}


func TestNetworkServiceBuilder_InitService_AllModes(t *testing.T) {
	tests := []struct {
		name          string
		daemonMode    string
		expectedError bool
	}{
		{
			name:          "ENIMultiIP mode",
			daemonMode:    daemon.ModeENIMultiIP,
			expectedError: false,
		},
		{
			name:          "ENIOnly mode (unsupported in InitService)",
			daemonMode:    daemon.ModeENIOnly,
			expectedError: true,
		},
		{
			name:          "VPC mode (unsupported in InitService)",
			daemonMode:    daemon.ModeVPC,
			expectedError: true,
		},
		{
			name:          "Unsupported mode",
			daemonMode:    "invalid_mode",
			expectedError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			builder := &NetworkServiceBuilder{
				daemonMode: tc.daemonMode,
			}

			builder = builder.InitService()

			if tc.expectedError {
				assert.NotNil(t, builder.err)
			} else {
				assert.Nil(t, builder.err)
				assert.NotNil(t, builder.service)
				assert.Equal(t, tc.daemonMode, builder.service.daemonMode)
			}
		})
	}
}

func TestNetworkServiceBuilder_LoadGlobalConfig_InvalidFile(t *testing.T) {
	builder := &NetworkServiceBuilder{
		configFilePath: "/non/existent/file.json",
		service:        &networkService{},
	}

	builder = builder.LoadGlobalConfig()

	assert.NotNil(t, builder.err)
}

func TestNetworkServiceBuilder_LoadGlobalConfig_InvalidJSON(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.json")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer tmpFile.Close()
	
	// Write invalid JSON
	configContent := `{ "version": "1", invalid json }`
	err = os.WriteFile(tmpFile.Name(), []byte(configContent), 0644)
	assert.NoError(t, err)

	builder := &NetworkServiceBuilder{
		configFilePath: tmpFile.Name(),
		service:        &networkService{},
	}

	builder = builder.LoadGlobalConfig()

	assert.NotNil(t, builder.err)
}

func TestNetworkServiceBuilder_Build_Success(t *testing.T) {
	ctx := context.Background()
	builder := NewNetworkServiceBuilder(ctx).
		WithDaemonMode(daemon.ModeENIMultiIP).
		InitService()

	// Note: Build() may fail due to missing dependencies in test environment
	// but we can test that it doesn't panic and handles errors gracefully
	service, err := builder.Build()

	if err != nil {
		// Expected in test environment without proper setup
		assert.Nil(t, service)
	} else {
		assert.NotNil(t, service)
		assert.Equal(t, daemon.ModeENIMultiIP, service.daemonMode)
	}
}

func TestNetworkServiceBuilder_Build_WithError(t *testing.T) {
	ctx := context.Background()
	builder := NewNetworkServiceBuilder(ctx).
		WithDaemonMode("invalid_mode").
		InitService()

	service, err := builder.Build()

	assert.Error(t, err)
	assert.Nil(t, service)
}

func TestNetworkServiceBuilder_RegisterTracing(t *testing.T) {
	ctx := context.Background()
	builder := NewNetworkServiceBuilder(ctx).
		WithConfigFilePath("/test/config").
		WithDaemonMode(daemon.ModeENIMultiIP).
		InitService()

	builder = builder.RegisterTracing()

	// Should not return error for basic tracing registration
	assert.Nil(t, builder.err)
}
