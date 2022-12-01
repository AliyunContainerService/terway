package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
