package types

import (
	"net"
	"testing"

	"github.com/AliyunContainerService/terway/plugin/terway/cni"
	cniTypes "github.com/containernetworking/cni/pkg/types"
	"github.com/stretchr/testify/assert"
)

func TestCNIConf_IPVlan(t *testing.T) {
	tests := []struct {
		name             string
		eniipVirtualType string
		expected         bool
	}{
		{
			name:             "lowercase ipvlan",
			eniipVirtualType: "ipvlan",
			expected:         true,
		},
		{
			name:             "uppercase IPVLAN",
			eniipVirtualType: "IPVLAN",
			expected:         true,
		},
		{
			name:             "mixed case IPVlan",
			eniipVirtualType: "IPVlan",
			expected:         true,
		},
		{
			name:             "empty string",
			eniipVirtualType: "",
			expected:         false,
		},
		{
			name:             "veth",
			eniipVirtualType: "veth",
			expected:         false,
		},
		{
			name:             "macvlan",
			eniipVirtualType: "macvlan",
			expected:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf := &CNIConf{
				ENIIPVirtualType: tt.eniipVirtualType,
			}
			result := conf.IPVlan()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestVlanStripTypeConstants(t *testing.T) {
	// Test VlanStripType constants
	assert.Equal(t, "filter", string(VlanStripTypeFilter))
	assert.Equal(t, "vlan", string(VlanStripTypeVlan))
}

func TestDataPathConstants(t *testing.T) {
	// Test DataPath constants
	assert.Equal(t, DataPath(0), VPCRoute)
	assert.Equal(t, DataPath(1), PolicyRoute)
	assert.Equal(t, DataPath(2), IPVlan)
	assert.Equal(t, DataPath(3), ExclusiveENI)
	assert.Equal(t, DataPath(4), Vlan)
}

func TestBandwidthModeConstants(t *testing.T) {
	// Test BandwidthMode constants
	assert.Equal(t, "edt", BandwidthModeEDT)
	assert.Equal(t, "tc", BandwidthModeTC)
}

func TestCNIConf(t *testing.T) {
	// Test CNIConf structure
	conf := &CNIConf{
		HostVethPrefix:        "veth",
		ENIIPVirtualType:      "ipvlan",
		HostStackCIDRs:        []string{"10.0.0.0/8"},
		DisableHostPeer:       false,
		VlanStripType:         VlanStripTypeFilter,
		MTU:                   1500,
		BandwidthMode:         BandwidthModeEDT,
		EnableNetworkPriority: true,
		Debug:                 true,
	}

	assert.Equal(t, "veth", conf.HostVethPrefix)
	assert.Equal(t, "ipvlan", conf.ENIIPVirtualType)
	assert.Len(t, conf.HostStackCIDRs, 1)
	assert.False(t, conf.DisableHostPeer)
	assert.Equal(t, VlanStripType("filter"), conf.VlanStripType)
	assert.Equal(t, 1500, conf.MTU)
	assert.True(t, conf.EnableNetworkPriority)
	assert.True(t, conf.Debug)
}

func TestSymmetricRoutingConfig(t *testing.T) {
	// Test SymmetricRoutingConfig structure
	config := &SymmetricRoutingConfig{
		Interface:    "eth0",
		Mark:         100,
		Mask:         255,
		TableID:      100,
		RulePriority: 1000,
		Comment:      "test comment",
	}

	assert.Equal(t, "eth0", config.Interface)
	assert.Equal(t, 100, config.Mark)
	assert.Equal(t, 255, config.Mask)
	assert.Equal(t, 100, config.TableID)
	assert.Equal(t, 1000, config.RulePriority)
	assert.Equal(t, "test comment", config.Comment)
}

func TestK8SArgs(t *testing.T) {
	// Test K8SArgs structure
	ip := net.ParseIP("10.0.0.1")
	args := &K8SArgs{
		IP:                         ip,
		K8S_POD_NAME:               cniTypes.UnmarshallableString("test-pod"),
		K8S_POD_NAMESPACE:          cniTypes.UnmarshallableString("default"),
		K8S_POD_INFRA_CONTAINER_ID: cniTypes.UnmarshallableString("container-id"),
	}

	assert.Equal(t, ip, args.IP)
	assert.Equal(t, "test-pod", string(args.K8S_POD_NAME))
	assert.Equal(t, "default", string(args.K8S_POD_NAMESPACE))
	assert.Equal(t, "container-id", string(args.K8S_POD_INFRA_CONTAINER_ID))
}

func TestSetupConfig(t *testing.T) {
	// Test SetupConfig structure
	config := &SetupConfig{
		DP:                    VPCRoute,
		HostVETHName:          "veth0",
		ContainerIfName:       "eth0",
		MTU:                   1500,
		ENIIndex:              1,
		ERDMA:                 false,
		DisableCreatePeer:     false,
		StripVlan:             false,
		Vid:                   0,
		DefaultRoute:          true,
		MultiNetwork:          false,
		BandwidthMode:         BandwidthModeEDT,
		Ingress:               1000000,
		Egress:                2000000,
		EnableNetworkPriority: true,
		NetworkPriority:       100,
	}

	assert.Equal(t, VPCRoute, config.DP)
	assert.Equal(t, "veth0", config.HostVETHName)
	assert.Equal(t, "eth0", config.ContainerIfName)
	assert.Equal(t, 1500, config.MTU)
	assert.Equal(t, 1, config.ENIIndex)
	assert.False(t, config.ERDMA)
	assert.True(t, config.DefaultRoute)
	assert.Equal(t, BandwidthModeEDT, config.BandwidthMode)
	assert.Equal(t, uint64(1000000), config.Ingress)
	assert.Equal(t, uint64(2000000), config.Egress)
	assert.True(t, config.EnableNetworkPriority)
	assert.Equal(t, uint32(100), config.NetworkPriority)
}

func TestTeardownCfg(t *testing.T) {
	// Test TeardownCfg structure
	cfg := &TeardownCfg{
		DP:                    IPVlan,
		HostVETHName:          "veth0",
		ENIIndex:              1,
		ContainerIfName:       "eth0",
		EnableNetworkPriority: true,
	}

	assert.Equal(t, IPVlan, cfg.DP)
	assert.Equal(t, "veth0", cfg.HostVETHName)
	assert.Equal(t, 1, cfg.ENIIndex)
	assert.Equal(t, "eth0", cfg.ContainerIfName)
	assert.True(t, cfg.EnableNetworkPriority)
}

func TestCNIConfWithSymmetricRouting(t *testing.T) {
	// Test CNIConf with SymmetricRoutingConfig
	conf := &CNIConf{
		SymmetricRoutingConfig: &SymmetricRoutingConfig{
			Interface: "eth0",
			Mark:      100,
			TableID:   100,
		},
	}

	assert.NotNil(t, conf.SymmetricRoutingConfig)
	assert.Equal(t, "eth0", conf.SymmetricRoutingConfig.Interface)
}

func TestCNIConfWithRuntimeConfig(t *testing.T) {
	// Test CNIConf with RuntimeConfig
	conf := &CNIConf{
		RuntimeConfig: cni.RuntimeConfig{
			DNS: cni.RuntimeDNS{
				Nameservers: []string{"8.8.8.8"},
			},
		},
	}

	assert.Len(t, conf.RuntimeConfig.DNS.Nameservers, 1)
	assert.Equal(t, "8.8.8.8", conf.RuntimeConfig.DNS.Nameservers[0])
}
