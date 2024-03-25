package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/instance"
	"github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

func init() {
	instance.SetPopulateFunc(func() *instance.Instance {
		return &instance.Instance{
			RegionID:     "regionID",
			ZoneID:       "zoneID",
			VPCID:        "vpc",
			VSwitchID:    "vsw",
			PrimaryMAC:   "",
			InstanceID:   "instanceID",
			InstanceType: "",
		}
	})
}

func TestGetPoolConfigWithVPCMode(t *testing.T) {
	cfg := &daemon.Config{
		MaxPoolSize: 5,
		MinPoolSize: 1,
		EniCapRatio: 1,
		RegionID:    "foo",
	}
	limit := &client.Limits{
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
	limit := &client.Limits{
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
	limit := &client.Limits{
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

func TestGetENIConfig(t *testing.T) {
	cfg := &daemon.Config{
		ENITags:                map[string]string{"aa": "bb"},
		SecurityGroups:         []string{"sg1", "sg2"},
		VSwitchSelectionPolicy: "ordered",
		ResourceGroupID:        "rgID",
		EnableENITrunking:      true,
		EnableERDMA:            true,
		VSwitches: map[string][]string{
			"zoneID": {"vswitch1", "vswitch2"},
		},
	}

	eniConfig := getENIConfig(cfg)

	assert.Equal(t, "zoneID", eniConfig.ZoneID)
	assert.Equal(t, []string{"vswitch1", "vswitch2"}, eniConfig.VSwitchOptions)
	assert.Equal(t, 1, len(eniConfig.ENITags))
	assert.Equal(t, []string{"sg1", "sg2"}, eniConfig.SecurityGroupIDs)
	assert.Equal(t, "instanceID", eniConfig.InstanceID)
	assert.Equal(t, vswitch.VSwitchSelectionPolicyMost, eniConfig.VSwitchSelectionPolicy)
	assert.Equal(t, "rgID", eniConfig.ResourceGroupID)
	assert.Equal(t, types.Feat(3), eniConfig.EniTypeAttr)
}
