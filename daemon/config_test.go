package daemon

import (
	"context"
	"testing"
	"time"

	k8smocks "github.com/AliyunContainerService/terway/pkg/k8s/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/instance"
	"github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types"
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

// TestGetPoolConfigWithIPReclaimConfig tests getPoolConfig with valid IP reclaim configurations
func TestGetPoolConfigWithIPReclaimConfig(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})

	reclaimInterval := "5m"
	reclaimAfter := "30m"
	reclaimFactor := "0.2"

	cfg := &daemon.Config{
		MaxPoolSize:               10,
		MinPoolSize:               2,
		EniCapRatio:               1,
		IdleIPReclaimBatchSize:    3,
		IdleIPReclaimInterval:     &reclaimInterval,
		IdleIPReclaimAfter:        &reclaimAfter,
		IdleIPReclaimJitterFactor: &reclaimFactor,
	}

	limit := &client.Limits{
		Adapters:           10,
		IPv4PerAdapter:     5,
		MemberAdapterLimit: 5,
	}

	poolConfig, err := getPoolConfig(cfg, daemon.ModeENIMultiIP, limit)
	assert.NoError(t, err)
	assert.Equal(t, 3, poolConfig.ReclaimBatchSize)
	assert.Equal(t, 5*time.Minute, poolConfig.ReclaimInterval)
	assert.Equal(t, 30*time.Minute, poolConfig.ReclaimAfter)
	assert.Equal(t, 0.2, poolConfig.ReclaimFactor)
}

// TestGetPoolConfigWithInvalidReclaimInterval tests getPoolConfig with invalid reclaim interval
func TestGetPoolConfigWithInvalidReclaimInterval(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})

	invalidInterval := "invalid-duration"
	cfg := &daemon.Config{
		MaxPoolSize:            10,
		MinPoolSize:            2,
		EniCapRatio:            1,
		IdleIPReclaimBatchSize: 3,
		IdleIPReclaimInterval:  &invalidInterval,
	}

	limit := &client.Limits{
		Adapters:           10,
		IPv4PerAdapter:     5,
		MemberAdapterLimit: 5,
	}

	_, err := getPoolConfig(cfg, daemon.ModeENIMultiIP, limit)
	assert.Error(t, err)
}

// TestGetPoolConfigWithInvalidReclaimAfter tests getPoolConfig with invalid reclaim after
func TestGetPoolConfigWithInvalidReclaimAfter(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})

	invalidAfter := "not-a-duration"
	cfg := &daemon.Config{
		MaxPoolSize:            10,
		MinPoolSize:            2,
		EniCapRatio:            1,
		IdleIPReclaimBatchSize: 3,
		IdleIPReclaimAfter:     &invalidAfter,
	}

	limit := &client.Limits{
		Adapters:           10,
		IPv4PerAdapter:     5,
		MemberAdapterLimit: 5,
	}

	_, err := getPoolConfig(cfg, daemon.ModeENIMultiIP, limit)
	assert.Error(t, err)
}

// TestGetPoolConfigWithInvalidReclaimFactor tests getPoolConfig with invalid reclaim jitter factor
func TestGetPoolConfigWithInvalidReclaimFactor(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})

	invalidFactor := "not-a-number"
	cfg := &daemon.Config{
		MaxPoolSize:               10,
		MinPoolSize:               2,
		EniCapRatio:               1,
		IdleIPReclaimBatchSize:    3,
		IdleIPReclaimJitterFactor: &invalidFactor,
	}

	limit := &client.Limits{
		Adapters:           10,
		IPv4PerAdapter:     5,
		MemberAdapterLimit: 5,
	}

	_, err := getPoolConfig(cfg, daemon.ModeENIMultiIP, limit)
	assert.Error(t, err)
}

// TestGetPoolConfigWithPartialIPReclaimConfig tests getPoolConfig with partial IP reclaim configurations
func TestGetPoolConfigWithPartialIPReclaimConfig(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})

	reclaimInterval := "10m"
	cfg := &daemon.Config{
		MaxPoolSize:            10,
		MinPoolSize:            2,
		EniCapRatio:            1,
		IdleIPReclaimBatchSize: 5,
		IdleIPReclaimInterval:  &reclaimInterval,
		// IdleIPReclaimAfter and IdleIPReclaimJitterFactor are nil
	}

	limit := &client.Limits{
		Adapters:           10,
		IPv4PerAdapter:     5,
		MemberAdapterLimit: 5,
	}

	poolConfig, err := getPoolConfig(cfg, daemon.ModeENIMultiIP, limit)
	assert.NoError(t, err)
	assert.Equal(t, 5, poolConfig.ReclaimBatchSize)
	assert.Equal(t, 10*time.Minute, poolConfig.ReclaimInterval)
	assert.Equal(t, time.Duration(0), poolConfig.ReclaimAfter)
	assert.Equal(t, 0.0, poolConfig.ReclaimFactor)
}

// TestGetPoolConfigWithIPAMTypeCRD tests getPoolConfig when IPAMType is CRD (MaxPoolSize/MinPoolSize zeroed)
func TestGetPoolConfigWithIPAMTypeCRD(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})

	cfg := &daemon.Config{
		MaxPoolSize: 10,
		MinPoolSize: 2,
		EniCapRatio: 1,
		IPAMType:    types.IPAMTypeCRD,
	}

	limit := &client.Limits{
		Adapters:           10,
		IPv4PerAdapter:     5,
		MemberAdapterLimit: 5,
	}

	poolConfig, err := getPoolConfig(cfg, daemon.ModeENIMultiIP, limit)
	assert.NoError(t, err)
	assert.Equal(t, 0, poolConfig.MaxPoolSize)
	assert.Equal(t, 0, poolConfig.MinPoolSize)
	assert.Equal(t, 45, poolConfig.Capacity) // maxENI * ipPerENI before CRD zeroing
}

// TestGetPoolConfigWithNilIPReclaimConfig tests getPoolConfig without IP reclaim configurations
func TestGetPoolConfigWithNilIPReclaimConfig(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})

	cfg := &daemon.Config{
		MaxPoolSize: 10,
		MinPoolSize: 2,
		EniCapRatio: 1,
		// All IP reclaim config fields are default (nil or 0)
	}

	limit := &client.Limits{
		Adapters:           10,
		IPv4PerAdapter:     5,
		MemberAdapterLimit: 5,
	}

	poolConfig, err := getPoolConfig(cfg, daemon.ModeENIMultiIP, limit)
	assert.NoError(t, err)
	assert.Equal(t, 0, poolConfig.ReclaimBatchSize)
	assert.Equal(t, time.Duration(0), poolConfig.ReclaimInterval)
	assert.Equal(t, time.Duration(0), poolConfig.ReclaimAfter)
	assert.Equal(t, 0.0, poolConfig.ReclaimFactor)
}

// TestGetPoolConfigWithZeroReclaimBatchSize tests getPoolConfig with zero reclaim batch size
func TestGetPoolConfigWithZeroReclaimBatchSize(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})

	reclaimInterval := "5m"
	cfg := &daemon.Config{
		MaxPoolSize:            10,
		MinPoolSize:            2,
		EniCapRatio:            1,
		IdleIPReclaimBatchSize: 0, // explicitly set to 0
		IdleIPReclaimInterval:  &reclaimInterval,
	}

	limit := &client.Limits{
		Adapters:           10,
		IPv4PerAdapter:     5,
		MemberAdapterLimit: 5,
	}

	poolConfig, err := getPoolConfig(cfg, daemon.ModeENIMultiIP, limit)
	assert.NoError(t, err)
	assert.Equal(t, 0, poolConfig.ReclaimBatchSize)
	assert.Equal(t, 5*time.Minute, poolConfig.ReclaimInterval)
}

// TestGetPoolConfigWithIPReclaimConfigENIOnly tests getPoolConfig with IP reclaim in ENIOnly mode
func TestGetPoolConfigWithIPReclaimConfigENIOnly(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})

	reclaimInterval := "3m"
	reclaimAfter := "15m"
	reclaimFactor := "0.15"

	cfg := &daemon.Config{
		MaxPoolSize:               10,
		MinPoolSize:               2,
		EniCapRatio:               1,
		IdleIPReclaimBatchSize:    2,
		IdleIPReclaimInterval:     &reclaimInterval,
		IdleIPReclaimAfter:        &reclaimAfter,
		IdleIPReclaimJitterFactor: &reclaimFactor,
	}

	limit := &client.Limits{
		Adapters:           10,
		IPv4PerAdapter:     5,
		MemberAdapterLimit: 5,
	}

	poolConfig, err := getPoolConfig(cfg, daemon.ModeENIOnly, limit)
	assert.NoError(t, err)
	assert.Equal(t, 2, poolConfig.ReclaimBatchSize)
	assert.Equal(t, 3*time.Minute, poolConfig.ReclaimInterval)
	assert.Equal(t, 15*time.Minute, poolConfig.ReclaimAfter)
	assert.Equal(t, 0.15, poolConfig.ReclaimFactor)
	// Verify ENIOnly mode specific settings
	assert.Equal(t, 1, poolConfig.MaxIPPerENI)
	assert.Equal(t, 0, poolConfig.MaxPoolSize)
	assert.Equal(t, 0, poolConfig.MinPoolSize)
}

// TestGetPoolConfigWithDurationFormats tests various valid duration formats
func TestGetPoolConfigWithDurationFormats(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})

	testCases := []struct {
		name             string
		interval         string
		after            string
		expectedInterval time.Duration
		expectedAfter    time.Duration
	}{
		{
			name:             "seconds",
			interval:         "30s",
			after:            "60s",
			expectedInterval: 30 * time.Second,
			expectedAfter:    60 * time.Second,
		},
		{
			name:             "minutes",
			interval:         "5m",
			after:            "10m",
			expectedInterval: 5 * time.Minute,
			expectedAfter:    10 * time.Minute,
		},
		{
			name:             "hours",
			interval:         "1h",
			after:            "2h",
			expectedInterval: 1 * time.Hour,
			expectedAfter:    2 * time.Hour,
		},
		{
			name:             "mixed",
			interval:         "1h30m",
			after:            "2h45m30s",
			expectedInterval: 1*time.Hour + 30*time.Minute,
			expectedAfter:    2*time.Hour + 45*time.Minute + 30*time.Second,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			interval := tc.interval
			after := tc.after
			cfg := &daemon.Config{
				MaxPoolSize:            10,
				MinPoolSize:            2,
				EniCapRatio:            1,
				IdleIPReclaimBatchSize: 5,
				IdleIPReclaimInterval:  &interval,
				IdleIPReclaimAfter:     &after,
			}

			limit := &client.Limits{
				Adapters:           10,
				IPv4PerAdapter:     5,
				MemberAdapterLimit: 5,
			}

			poolConfig, err := getPoolConfig(cfg, daemon.ModeENIMultiIP, limit)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedInterval, poolConfig.ReclaimInterval)
			assert.Equal(t, tc.expectedAfter, poolConfig.ReclaimAfter)
		})
	}
}

// TestGetPoolConfigWithFloatJitterFactor tests various float values for jitter factor
func TestGetPoolConfigWithFloatJitterFactor(t *testing.T) {
	instance.Init(&Mock{
		regionID:     "regionID",
		zoneID:       "zoneID",
		vSwitchID:    "vsw",
		primaryMAC:   "",
		instanceID:   "instanceID",
		instanceType: "",
	})

	testCases := []struct {
		name           string
		factor         string
		expectedFactor float64
	}{
		{"zero", "0", 0.0},
		{"small", "0.1", 0.1},
		{"medium", "0.5", 0.5},
		{"large", "1.0", 1.0},
		{"decimal", "0.25", 0.25},
		{"many_decimals", "0.123456", 0.123456},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			factor := tc.factor
			cfg := &daemon.Config{
				MaxPoolSize:               10,
				MinPoolSize:               2,
				EniCapRatio:               1,
				IdleIPReclaimBatchSize:    5,
				IdleIPReclaimJitterFactor: &factor,
			}

			limit := &client.Limits{
				Adapters:           10,
				IPv4PerAdapter:     5,
				MemberAdapterLimit: 5,
			}

			poolConfig, err := getPoolConfig(cfg, daemon.ModeENIMultiIP, limit)
			assert.NoError(t, err)
			assert.InDelta(t, tc.expectedFactor, poolConfig.ReclaimFactor, 0.000001)
		})
	}
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

func TestGetDynamicConfig_NoLabel(t *testing.T) {
	m := k8smocks.NewKubernetes(t)
	m.On("GetNodeDynamicConfigLabel").Return("")
	cfg, label, err := getDynamicConfig(context.Background(), m)
	assert.Empty(t, cfg)
	assert.Empty(t, label)
	assert.Nil(t, err)
	m.AssertNotCalled(t, "GetDynamicConfigWithName")
}

func TestDetDynamicConfig(t *testing.T) {
	m := k8smocks.NewKubernetes(t)
	m.On("GetDynamicConfigWithName", mock.Anything, mock.Anything).Return("", nil)
	m.On("GetNodeDynamicConfigLabel").Return("foo")
	cfg, label, err := getDynamicConfig(context.Background(), m)
	assert.Empty(t, cfg)
	assert.Equal(t, "foo", label)
	assert.Nil(t, err)
}
