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

	eniConfig := getENIConfig(cfg, "zoneID")

	assert.Equal(t, 1, len(eniConfig.ENITags))
	assert.Equal(t, []string{"sg1", "sg2"}, eniConfig.SecurityGroupIDs)
	assert.Equal(t, vswitch.VSwitchSelectionPolicyMost, eniConfig.VSwitchSelectionPolicy)
	assert.Equal(t, daemon.EniSelectionPolicyMostIPs, eniConfig.EniSelectionPolicy)
	assert.Equal(t, "rgID", eniConfig.ResourceGroupID)
	assert.Equal(t, daemon.Feat(3), eniConfig.EniTypeAttr)
	assert.Equal(t, []string{"vswitch1", "vswitch2"}, eniConfig.VSwitchOptions)
}

// TestGetENIConfigWithDefaultVSwitchSelectionPolicy tests getENIConfig with default vswitch selection policy
func TestGetENIConfigWithDefaultVSwitchSelectionPolicy(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
	cfg := &daemon.Config{
		ENITags:        map[string]string{"key": "value"},
		SecurityGroups: []string{"sg1"},
		// VSwitchSelectionPolicy not set (default)
		EniSelectionPolicy: "most_ips",
		ResourceGroupID:    "rgID",
	}

	eniConfig := getENIConfig(cfg, "zoneID")

	// Default vswitch selection policy should be random
	assert.Equal(t, vswitch.VSwitchSelectionPolicyRandom, eniConfig.VSwitchSelectionPolicy)
	assert.Equal(t, daemon.EniSelectionPolicyMostIPs, eniConfig.EniSelectionPolicy)
	assert.Equal(t, "zoneID", eniConfig.ZoneID)
}

// TestGetENIConfigWithLeastIPsEniSelectionPolicy tests getENIConfig with least_ips eni selection policy
func TestGetENIConfigWithLeastIPsEniSelectionPolicy(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
	cfg := &daemon.Config{
		ENITags:                map[string]string{"key": "value"},
		SecurityGroups:         []string{"sg1"},
		VSwitchSelectionPolicy: "random",
		EniSelectionPolicy:     "least_ips",
		ResourceGroupID:        "rgID",
	}

	eniConfig := getENIConfig(cfg, "zoneID")

	assert.Equal(t, vswitch.VSwitchSelectionPolicyRandom, eniConfig.VSwitchSelectionPolicy)
	assert.Equal(t, daemon.EniSelectionPolicyLeastIPs, eniConfig.EniSelectionPolicy)
}

// TestGetENIConfigWithDefaultEniSelectionPolicy tests getENIConfig with default eni selection policy
func TestGetENIConfigWithDefaultEniSelectionPolicy(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
	cfg := &daemon.Config{
		ENITags:                map[string]string{"key": "value"},
		SecurityGroups:         []string{"sg1"},
		VSwitchSelectionPolicy: "ordered",
		// EniSelectionPolicy not set (default)
		ResourceGroupID: "rgID",
	}

	eniConfig := getENIConfig(cfg, "zoneID")

	// Default eni selection policy should be most_ips
	assert.Equal(t, daemon.EniSelectionPolicyMostIPs, eniConfig.EniSelectionPolicy)
}

// TestGetENIConfigWithNoVSwitches tests getENIConfig when no VSwitches are configured for the zone
func TestGetENIConfigWithNoVSwitches(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
	cfg := &daemon.Config{
		ENITags:        map[string]string{"key": "value"},
		SecurityGroups: []string{"sg1"},
		// No VSwitches configured
		VSwitches: nil,
	}

	eniConfig := getENIConfig(cfg, "zoneID")

	assert.Nil(t, eniConfig.VSwitchOptions)
}

// TestGetENIConfigWithEmptyVSwitchesForZone tests getENIConfig when VSwitches map exists but no entries for the zone
func TestGetENIConfigWithEmptyVSwitchesForZone(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
	cfg := &daemon.Config{
		ENITags:        map[string]string{"key": "value"},
		SecurityGroups: []string{"sg1"},
		VSwitches: map[string][]string{
			"otherZoneID": {"vswitch1", "vswitch2"}, // Different zone
		},
	}

	eniConfig := getENIConfig(cfg, "zoneID")

	assert.Nil(t, eniConfig.VSwitchOptions)
}

// TestGetENIConfigWithEmptyVSwitchListForZone tests getENIConfig when VSwitches exist for zone but list is empty
func TestGetENIConfigWithEmptyVSwitchListForZone(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
	cfg := &daemon.Config{
		ENITags:        map[string]string{"key": "value"},
		SecurityGroups: []string{"sg1"},
		VSwitches: map[string][]string{
			"zoneID": {}, // Empty list for the zone
		},
	}

	eniConfig := getENIConfig(cfg, "zoneID")

	assert.Nil(t, eniConfig.VSwitchOptions)
}

// TestGetENIConfigWithOnlyENITrunking tests getENIConfig with only ENI trunking enabled
func TestGetENIConfigWithOnlyENITrunking(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
	cfg := &daemon.Config{
		ENITags:           map[string]string{"key": "value"},
		SecurityGroups:    []string{"sg1"},
		EnableENITrunking: true,
		EnableERDMA:       false,
	}

	eniConfig := getENIConfig(cfg, "zoneID")

	// Should have only trunk feature enabled
	assert.Equal(t, daemon.Feat(1), eniConfig.EniTypeAttr) // FeatTrunk = 1
}

// TestGetENIConfigWithOnlyERDMA tests getENIConfig with only ERDMA enabled
func TestGetENIConfigWithOnlyERDMA(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
	cfg := &daemon.Config{
		ENITags:           map[string]string{"key": "value"},
		SecurityGroups:    []string{"sg1"},
		EnableENITrunking: false,
		EnableERDMA:       true,
	}

	eniConfig := getENIConfig(cfg, "zoneID")

	// Should have only ERDMA feature enabled
	assert.Equal(t, daemon.Feat(2), eniConfig.EniTypeAttr) // FeatERDMA = 2
}

// TestGetENIConfigWithNoFeatures tests getENIConfig with no features enabled
func TestGetENIConfigWithNoFeatures(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
	cfg := &daemon.Config{
		ENITags:           map[string]string{"key": "value"},
		SecurityGroups:    []string{"sg1"},
		EnableENITrunking: false,
		EnableERDMA:       false,
	}

	eniConfig := getENIConfig(cfg, "zoneID")

	// Should have no features enabled
	assert.Equal(t, daemon.Feat(0), eniConfig.EniTypeAttr)
}

// TestGetENIConfigWithNilENITags tests getENIConfig with nil ENI tags
func TestGetENIConfigWithNilENITags(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
	cfg := &daemon.Config{
		ENITags:        nil, // Nil ENI tags
		SecurityGroups: []string{"sg1"},
	}

	eniConfig := getENIConfig(cfg, "zoneID")

	assert.Nil(t, eniConfig.ENITags)
}

// TestGetENIConfigWithEmptyENITags tests getENIConfig with empty ENI tags
func TestGetENIConfigWithEmptyENITags(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
	cfg := &daemon.Config{
		ENITags:        map[string]string{}, // Empty ENI tags
		SecurityGroups: []string{"sg1"},
	}

	eniConfig := getENIConfig(cfg, "zoneID")

	assert.Equal(t, 0, len(eniConfig.ENITags))
}

// TestGetENIConfigWithNilSecurityGroups tests getENIConfig with nil security groups
func TestGetENIConfigWithNilSecurityGroups(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
	cfg := &daemon.Config{
		ENITags:        map[string]string{"key": "value"},
		SecurityGroups: nil, // Nil security groups
	}

	eniConfig := getENIConfig(cfg, "zoneID")

	// GetSecurityGroups() should handle nil case
	assert.NotNil(t, eniConfig.SecurityGroupIDs)
}

// TestGetENIConfigWithEmptySecurityGroups tests getENIConfig with empty security groups
func TestGetENIConfigWithEmptySecurityGroups(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
	cfg := &daemon.Config{
		ENITags:        map[string]string{"key": "value"},
		SecurityGroups: []string{}, // Empty security groups
	}

	eniConfig := getENIConfig(cfg, "zoneID")

	assert.Equal(t, 0, len(eniConfig.SecurityGroupIDs))
}

// TestGetENIConfigWithTagFilter tests getENIConfig with ENI tag filter
func TestGetENIConfigWithTagFilter(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})
	cfg := &daemon.Config{
		ENITags:        map[string]string{"key": "value"},
		SecurityGroups: []string{"sg1"},
		ENITagFilter:   map[string]string{"filter-key": "filter-value"},
	}

	eniConfig := getENIConfig(cfg, "zoneID")

	assert.Equal(t, map[string]string{"filter-key": "filter-value"}, eniConfig.TagFilter)
}
