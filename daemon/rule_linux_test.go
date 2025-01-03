//go:build linux

package daemon

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vishvananda/netlink"

	"github.com/AliyunContainerService/terway/plugin/datapath"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	terwayTypes "github.com/AliyunContainerService/terway/types"
)

func TestGenerateConfig(t *testing.T) {
	setUp := &types.SetupConfig{
		ContainerIPNet: &terwayTypes.IPNetSet{
			IPv4: &net.IPNet{
				IP:   net.ParseIP("10.0.0.2"),
				Mask: net.CIDRMask(32, 32),
			},
			IPv6: &net.IPNet{
				IP:   net.ParseIP("fd0::2"),
				Mask: net.CIDRMask(128, 128),
			},
		},
		GatewayIP: &terwayTypes.IPSet{
			IPv4: net.ParseIP("169.254.169.254"),
			IPv6: net.ParseIP("fd0::1"),
		},
		ENIIndex: 1,
	}
	eni := &netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Index: 1,
			Name:  "eth1",
		},
	}
	eniConf := datapath.GenerateENICfgForPolicy(setUp, eni, 1)

	assert.True(t, eniConf.Routes[0].Equal(netlink.Route{
		Dst: &net.IPNet{
			IP:   net.ParseIP("0.0.0.0"),
			Mask: net.CIDRMask(0, 0),
		},
		Gw:        net.ParseIP("169.254.169.254"),
		Table:     1,
		LinkIndex: 1,
		Scope:     netlink.SCOPE_UNIVERSE,
		Flags:     int(netlink.FLAG_ONLINK),
	}))
	assert.True(t, eniConf.Routes[2].Equal(netlink.Route{
		Dst: &net.IPNet{
			IP:   net.ParseIP("::"),
			Mask: net.CIDRMask(0, 0),
		},
		Gw:        net.ParseIP("fd0::1"),
		Table:     1,
		LinkIndex: 1,
		Scope:     netlink.SCOPE_UNIVERSE,
		Flags:     int(netlink.FLAG_ONLINK),
	}))
}

func TestGenerateHostPeerCfgForPolicy(t *testing.T) {
	setUp := &types.SetupConfig{
		ContainerIPNet: &terwayTypes.IPNetSet{
			IPv4: &net.IPNet{
				IP:   net.ParseIP("10.0.0.2"),
				Mask: net.CIDRMask(32, 32),
			},
			IPv6: &net.IPNet{
				IP:   net.ParseIP("fd0::2"),
				Mask: net.CIDRMask(128, 128),
			},
		},
		GatewayIP: &terwayTypes.IPSet{
			IPv4: net.ParseIP("169.254.169.254"),
			IPv6: net.ParseIP("fd0::1"),
		},
		ENIIndex: 1,
	}
	veth := &netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Index: 1,
			Name:  "calixxx",
		},
	}
	vethConf := datapath.GenerateHostPeerCfgForPolicy(setUp, veth, 1)
	assert.Equal(t, 2, len(vethConf.Routes))
	assert.Equal(t, 4, len(vethConf.Rules))
}
