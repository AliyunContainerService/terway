package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/AliyunContainerService/terway/types"
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
