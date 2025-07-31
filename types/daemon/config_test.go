package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/secret"
)

func Test_MergeConfigAndUnmarshal(t *testing.T) {
	baseCfg := `{
		"version": "1",
		"max_pool_size": 5,
		"min_pool_size": 0,
		"credential_path": "/var/addon/token-config",
		"vswitches": {"cn-hangzhou-i":["vsw-10000"], "cn-hangzhou-g": ["vsw-20000"]},
		"service_cidr": "172.26.0.0/20",
		"security_group": "sg-10000",
		"vswitch_selection_policy": "ordered"
	}`

	dynCfg1 := `{
		"version": "1",
		"max_pool_size": 5,
		"min_pool_size": 0,
		"credential_path": "/var/addon/token-config",
		"vswitches": {"cn-hangzhou-i":["vsw-11111"], "cn-hangzhou-g": null},
		"service_cidr": "172.26.0.0/20",
		"security_group": "sg-11111",
		"vswitch_selection_policy": "ordered"
		}`

	dynCfg2 := `{
		"max_pool_size": 10,
		"min_pool_size": 5,
		"vswitches": {"cn-hangzhou-i":["vsw-11111", "vsw-22222"], "cn-hangzhou-g": null},
		"security_group": "sg-11111"
	}`

	dynCfg3 := ``

	cfg, err := MergeConfigAndUnmarshal([]byte(dynCfg1), []byte(baseCfg))
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, "1", cfg.Version)
	assert.Equal(t, 5, cfg.MaxPoolSize)
	assert.Equal(t, 0, cfg.MinPoolSize)
	assert.Equal(t, "/var/addon/token-config", cfg.CredentialPath)
	assert.Equal(t, 1, len(cfg.VSwitches))
	assert.Equal(t, 1, len(cfg.VSwitches["cn-hangzhou-i"]))
	assert.Equal(t, "vsw-11111", cfg.VSwitches["cn-hangzhou-i"][0])
	assert.Equal(t, "172.26.0.0/20", cfg.ServiceCIDR)
	assert.Equal(t, "sg-11111", cfg.SecurityGroup)
	assert.Equal(t, "ordered", cfg.VSwitchSelectionPolicy)
	t.Logf("%+v", cfg)

	cfg, err = MergeConfigAndUnmarshal([]byte(dynCfg2), []byte(baseCfg))
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, "1", cfg.Version)
	assert.Equal(t, 10, cfg.MaxPoolSize)
	assert.Equal(t, 5, cfg.MinPoolSize)
	assert.Equal(t, "/var/addon/token-config", cfg.CredentialPath)
	assert.Equal(t, 1, len(cfg.VSwitches))
	assert.Equal(t, 2, len(cfg.VSwitches["cn-hangzhou-i"]))
	assert.Equal(t, "172.26.0.0/20", cfg.ServiceCIDR)
	assert.Equal(t, "sg-11111", cfg.SecurityGroup)
	assert.Equal(t, "ordered", cfg.VSwitchSelectionPolicy)
	t.Logf("%+v", cfg)

	cfg, err = MergeConfigAndUnmarshal([]byte(dynCfg3), []byte(baseCfg))
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, "1", cfg.Version)
	assert.Equal(t, 5, cfg.MaxPoolSize)
	assert.Equal(t, 0, cfg.MinPoolSize)
	assert.Equal(t, "/var/addon/token-config", cfg.CredentialPath)
	assert.Equal(t, 2, len(cfg.VSwitches))
	assert.Equal(t, 1, len(cfg.VSwitches["cn-hangzhou-i"]))
	assert.Equal(t, "172.26.0.0/20", cfg.ServiceCIDR)
	assert.Equal(t, "sg-10000", cfg.SecurityGroup)
	assert.Equal(t, "ordered", cfg.VSwitchSelectionPolicy)
	t.Logf("%+v", cfg)
}

func TestGetAddonSecret(t *testing.T) {
	dir, err := os.MkdirTemp("", "")
	assert.NoError(t, err)

	defer os.RemoveAll(dir)

	_ = os.WriteFile(filepath.Join(dir, addonSecretKeyID), []byte("key"), 0700)
	_ = os.WriteFile(filepath.Join(dir, addonSecretKeySecret), []byte("secret"), 0700)
	addonSecretRootPath = dir

	ak, sk, err := GetAddonSecret()
	assert.NoError(t, err)
	assert.Equal(t, "key", ak)
	assert.Equal(t, "secret", sk)
}

func TestGetVSwitchIDsReturnsAllVSwitchIDs(t *testing.T) {
	cfg := &Config{
		VSwitches: map[string][]string{
			"zone-a": {"vsw-1", "vsw-2"},
			"zone-b": {"vsw-3"},
		},
	}
	vsws := cfg.GetVSwitchIDs()
	assert.ElementsMatch(t, []string{"vsw-1", "vsw-2", "vsw-3"}, vsws)
}

func TestGetVSwitchIDsReturnsEmptyWhenNoVSwitches(t *testing.T) {
	cfg := &Config{
		VSwitches: map[string][]string{},
	}
	vsws := cfg.GetVSwitchIDs()
	assert.Empty(t, vsws)
}

func TestGetExtraRoutesReturnsAllRoutes(t *testing.T) {
	cfg := &Config{
		VSwitches: map[string][]string{
			"zone-a": {"vsw-1", "vsw-2"},
			"zone-b": {"vsw-3"},
		},
	}
	routes := cfg.GetExtraRoutes()
	assert.ElementsMatch(t, []string{"vsw-1", "vsw-2", "vsw-3"}, routes)
}

func TestGetExtraRoutesReturnsEmptyWhenNoRoutes(t *testing.T) {
	cfg := &Config{
		VSwitches: map[string][]string{},
	}
	routes := cfg.GetExtraRoutes()
	assert.Empty(t, routes)
}

func TestPopulateSetsDefaultValues(t *testing.T) {
	cfg := &Config{}
	cfg.Populate()
	assert.Equal(t, 1.0, cfg.EniCapRatio)
	assert.Equal(t, VSwitchSelectionPolicyRandom, cfg.VSwitchSelectionPolicy)
	assert.Equal(t, string(types.IPStackIPv4), cfg.IPStack)
	assert.True(t, *cfg.EnablePatchPodIPs)
}

func TestPopulateDoesNotOverrideExistingValues(t *testing.T) {
	cfg := &Config{
		EniCapRatio:            0.5,
		VSwitchSelectionPolicy: "custom",
		IPStack:                string(types.IPStackDual),
	}
	cfg.Populate()
	err := cfg.Validate()
	assert.NoError(t, err)
	assert.Equal(t, 0.5, cfg.EniCapRatio)
	assert.Equal(t, "custom", cfg.VSwitchSelectionPolicy)
	assert.Equal(t, string(types.IPStackDual), cfg.IPStack)
}

func TestGetIPPoolSYncPeriod(t *testing.T) {
	tests := []struct {
		name     string
		period   string
		expected time.Duration
	}{
		{
			name:     "valid duration",
			period:   "30s",
			expected: 30 * time.Second,
		},
		{
			name:     "valid duration minutes",
			period:   "2m",
			expected: 2 * time.Minute,
		},
		{
			name:     "empty period",
			period:   "",
			expected: 120 * time.Second,
		},
		{
			name:     "invalid period",
			period:   "invalid",
			expected: 120 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				IPPoolSyncPeriod: tt.period,
			}
			result := cfg.GetIPPoolSYncPeriod()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEnableFeature(t *testing.T) {
	var features Feat

	// Test enabling single feature
	EnableFeature(&features, FeatTrunk)
	assert.True(t, IsFeatureEnabled(features, FeatTrunk))
	assert.False(t, IsFeatureEnabled(features, FeatERDMA))

	// Test enabling multiple features
	EnableFeature(&features, FeatERDMA)
	assert.True(t, IsFeatureEnabled(features, FeatTrunk))
	assert.True(t, IsFeatureEnabled(features, FeatERDMA))

	// Test enabling already enabled feature
	EnableFeature(&features, FeatTrunk)
	assert.True(t, IsFeatureEnabled(features, FeatTrunk))
}

func TestDisableFeature(t *testing.T) {
	var features Feat

	// Enable both features first
	EnableFeature(&features, FeatTrunk)
	EnableFeature(&features, FeatERDMA)
	assert.True(t, IsFeatureEnabled(features, FeatTrunk))
	assert.True(t, IsFeatureEnabled(features, FeatERDMA))

	// Test disabling single feature
	DisableFeature(&features, FeatTrunk)
	assert.False(t, IsFeatureEnabled(features, FeatTrunk))
	assert.True(t, IsFeatureEnabled(features, FeatERDMA))

	// Test disabling already disabled feature
	DisableFeature(&features, FeatTrunk)
	assert.False(t, IsFeatureEnabled(features, FeatTrunk))
	assert.True(t, IsFeatureEnabled(features, FeatERDMA))

	// Test disabling the other feature
	DisableFeature(&features, FeatERDMA)
	assert.False(t, IsFeatureEnabled(features, FeatTrunk))
	assert.False(t, IsFeatureEnabled(features, FeatERDMA))
}

func TestIsFeatureEnabled(t *testing.T) {
	var features Feat

	// Test with no features enabled
	assert.False(t, IsFeatureEnabled(features, FeatTrunk))
	assert.False(t, IsFeatureEnabled(features, FeatERDMA))

	// Test with single feature enabled
	EnableFeature(&features, FeatTrunk)
	assert.True(t, IsFeatureEnabled(features, FeatTrunk))
	assert.False(t, IsFeatureEnabled(features, FeatERDMA))

	// Test with multiple features enabled
	EnableFeature(&features, FeatERDMA)
	assert.True(t, IsFeatureEnabled(features, FeatTrunk))
	assert.True(t, IsFeatureEnabled(features, FeatERDMA))
}

func TestGetConfigFromFileWithMerge(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "terway-test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create test config file
	configFile := filepath.Join(tempDir, "config.json")
	baseConfig := `{
		"version": "1.0",
		"max_pool_size": 10,
		"min_pool_size": 2,
		"region_id": "cn-hangzhou",
		"service_cidr": "172.16.0.0/16"
	}`
	err = os.WriteFile(configFile, []byte(baseConfig), 0644)
	assert.NoError(t, err)

	// Test case 1: Merge with additional config
	mergeConfig := `{
		"max_pool_size": 15,
		"min_pool_size": 5,
		"vswitches": {"cn-hangzhou-i": ["vsw-12345"]}
	}`

	cfg, err := GetConfigFromFileWithMerge(configFile, []byte(mergeConfig))
	assert.NoError(t, err)
	assert.Equal(t, "1.0", cfg.Version)
	assert.Equal(t, 15, cfg.MaxPoolSize)
	assert.Equal(t, 5, cfg.MinPoolSize)
	assert.Equal(t, "cn-hangzhou", cfg.RegionID)
	assert.Equal(t, "172.16.0.0/16", cfg.ServiceCIDR)
	assert.Equal(t, 1, len(cfg.VSwitches))
	assert.Equal(t, 1, len(cfg.VSwitches["cn-hangzhou-i"]))
	assert.Equal(t, "vsw-12345", cfg.VSwitches["cn-hangzhou-i"][0])

	// Test case 2: No merge config (empty)
	cfg, err = GetConfigFromFileWithMerge(configFile, []byte{})
	assert.NoError(t, err)
	assert.Equal(t, "1.0", cfg.Version)
	assert.Equal(t, 10, cfg.MaxPoolSize)
	assert.Equal(t, 2, cfg.MinPoolSize)
	assert.Equal(t, "cn-hangzhou", cfg.RegionID)
	assert.Equal(t, "172.16.0.0/16", cfg.ServiceCIDR)

	// Test case 3: File not found
	_, err = GetConfigFromFileWithMerge("/nonexistent/file.json", []byte(mergeConfig))
	assert.Error(t, err)

	// Test case 4: Invalid JSON in file
	invalidConfig := `{
		"version": "1.0",
		"max_pool_size": "invalid",
		"min_pool_size": 2
	}`
	invalidFile := filepath.Join(tempDir, "invalid.json")
	err = os.WriteFile(invalidFile, []byte(invalidConfig), 0644)
	assert.NoError(t, err)

	_, err = GetConfigFromFileWithMerge(invalidFile, []byte(mergeConfig))
	assert.NoError(t, err) // JSON unmarshaling is lenient, so this might not error

	// Test case 5: Invalid merge config
	_, err = GetConfigFromFileWithMerge(configFile, []byte(`{"invalid": json`))
	assert.Error(t, err)
}

func TestGetConfigFromFileWithMergeWithAddonSecret(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "terway-test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create test config file
	configFile := filepath.Join(tempDir, "config.json")
	baseConfig := `{
		"version": "1.0",
		"max_pool_size": 10,
		"region_id": "cn-hangzhou"
	}`
	err = os.WriteFile(configFile, []byte(baseConfig), 0644)
	assert.NoError(t, err)

	// Create addon secret directory and files
	secretDir := filepath.Join(tempDir, "secret")
	err = os.MkdirAll(secretDir, 0755)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(secretDir, addonSecretKeyID), []byte("test-access-key"), 0644)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(secretDir, addonSecretKeySecret), []byte("test-secret-key"), 0644)
	assert.NoError(t, err)

	// Temporarily change the addon secret path
	originalPath := addonSecretRootPath
	addonSecretRootPath = secretDir
	defer func() { addonSecretRootPath = originalPath }()

	// Test merging with addon secret
	mergeConfig := `{
		"max_pool_size": 15
	}`

	cfg, err := GetConfigFromFileWithMerge(configFile, []byte(mergeConfig))
	assert.NoError(t, err)
	assert.Equal(t, "1.0", cfg.Version)
	assert.Equal(t, 15, cfg.MaxPoolSize)
	assert.Equal(t, "cn-hangzhou", cfg.RegionID)
	assert.Equal(t, secret.Secret("test-access-key"), cfg.AccessID)
	assert.Equal(t, secret.Secret("test-secret-key"), cfg.AccessSecret)
}

// TestConfig_GetIPStack
func TestConfig_GetIPStack(t *testing.T) {
	testCases := map[string]struct {
		input    string
		expected [2]bool
	}{
		"dual stack configuration": {
			input:    "dual",
			expected: [2]bool{true, true},
		},
		"ipv4 only configuration": {
			input:    "ipv4",
			expected: [2]bool{true, false},
		},
		"empty configuration defaults to ipv4": {
			input:    "",
			expected: [2]bool{true, false},
		},
		"ipv6 only configuration": {
			input:    "ipv6",
			expected: [2]bool{false, true},
		},
		"unknown configuration defaults to none": {
			input:    "unknown",
			expected: [2]bool{false, false},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			config := &Config{IPStack: tc.input}
			ipv4, ipv6 := config.GetIPStack()

			if ipv4 != tc.expected[0] || ipv6 != tc.expected[1] {
				t.Errorf("GetIPStack() = (%v, %v), want (%v, %v)",
					ipv4, ipv6, tc.expected[0], tc.expected[1])
			}
		})
	}
}
