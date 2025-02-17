package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/instance"
	"github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types/daemon"
)

type Mock struct {
	regionID     string
	zoneID       string
	vSwitchID    string
	primaryMAC   string
	instanceID   string
	instanceType string
}

func (m *Mock) GetRegionID() (string, error) {
	return m.regionID, nil
}

func (m *Mock) GetZoneID() (string, error) {
	return m.zoneID, nil
}

func (m *Mock) GetVSwitchID() (string, error) {
	return m.vSwitchID, nil
}

func (m *Mock) GetPrimaryMAC() (string, error) {
	return m.primaryMAC, nil
}

func (m *Mock) GetInstanceID() (string, error) {
	return m.instanceID, nil
}

func (m *Mock) GetInstanceType() (string, error) {
	return m.instanceType, nil
}

func TestGetPoolConfigWithENIMultiIPMode(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
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
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
	cfg := &daemon.Config{
		ENITags:                map[string]string{"aa": "bb"},
		SecurityGroups:         []string{"sg1", "sg2"},
		VSwitchSelectionPolicy: "ordered",
		EniSelectionPolicy:     "most_ips",
		ResourceGroupID:        "rgID",
		EnableENITrunking:      true,
		EnableERDMA:            true,
		VSwitches: map[string][]string{
			"zoneID": {"vswitch1", "vswitch2"},
		},
	}

	eniConfig := getENIConfig(cfg)

	assert.Equal(t, 1, len(eniConfig.ENITags))
	assert.Equal(t, []string{"sg1", "sg2"}, eniConfig.SecurityGroupIDs)
	assert.Equal(t, vswitch.VSwitchSelectionPolicyMost, eniConfig.VSwitchSelectionPolicy)
	assert.Equal(t, daemon.EniSelectionPolicyMostIPs, eniConfig.EniSelectionPolicy)
	assert.Equal(t, "rgID", eniConfig.ResourceGroupID)
	assert.Equal(t, daemon.Feat(3), eniConfig.EniTypeAttr)
}
