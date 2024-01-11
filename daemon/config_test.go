package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/AliyunContainerService/terway/pkg/aliyun/instance"
	"github.com/AliyunContainerService/terway/types/daemon"
)

func init() {
	instance.Test = true
}

func TestGetPoolConfigWithVPCMode(t *testing.T) {
	cfg := &daemon.Config{
		MaxPoolSize: 5,
		MinPoolSize: 1,
		EniCapRatio: 1,
		RegionID:    "foo",
	}
	limit := &instance.Limits{
		Adapters:           10,
		MemberAdapterLimit: 5,
	}
	poolConfig, err := getPoolConfig(cfg, "VPC", limit)
	assert.NoError(t, err)
	assert.Equal(t, 5, poolConfig.MaxPoolSize)
	assert.Equal(t, 1, poolConfig.MinPoolSize)
}

func TestGetPoolConfigWithENIOnlyMode(t *testing.T) {
	cfg := &daemon.Config{
		MaxPoolSize: 5,
		MinPoolSize: 1,
		EniCapRatio: 1,
		RegionID:    "foo",
	}
	limit := &instance.Limits{
		Adapters:           10,
		MemberAdapterLimit: 5,
	}
	poolConfig, err := getPoolConfig(cfg, "ENIOnly", limit)
	assert.NoError(t, err)
	assert.Equal(t, 5, poolConfig.MaxPoolSize)
	assert.Equal(t, 1, poolConfig.MinPoolSize)
}

func TestGetPoolConfigWithENIMultiIPMode(t *testing.T) {
	cfg := &daemon.Config{
		MaxPoolSize: 5,
		MinPoolSize: 1,
		EniCapRatio: 1,
		RegionID:    "foo",
	}
	limit := &instance.Limits{
		Adapters:           10,
		IPv4PerAdapter:     5,
		MemberAdapterLimit: 5,
	}
	poolConfig, err := getPoolConfig(cfg, "ENIMultiIP", limit)
	assert.NoError(t, err)
	assert.Equal(t, 5, poolConfig.MaxPoolSize)
	assert.Equal(t, 1, poolConfig.MinPoolSize)
	assert.Equal(t, 5, poolConfig.MaxIPPerENI)
}
