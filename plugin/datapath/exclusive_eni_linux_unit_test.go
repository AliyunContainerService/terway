//go:build linux

package datapath

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"

	"github.com/AliyunContainerService/terway/plugin/driver/types"
	terwayTypes "github.com/AliyunContainerService/terway/types"
)

func TestGenerateHostSlaveCfgPreferredSource(t *testing.T) {
	link := &netlink.Veth{LinkAttrs: netlink.LinkAttrs{Index: 10, Name: "calixxx"}}
	containerIPv4 := net.ParseIP("10.0.0.2")
	containerIPv6 := net.ParseIP("fd00::2")
	hostIPv4 := net.ParseIP("10.0.0.1")
	hostIPv6 := net.ParseIP("fd00::1")

	cfg := &types.SetupConfig{
		HostVETHName: "calixxx",
		ContainerIPNet: &terwayTypes.IPNetSet{
			IPv4: &net.IPNet{IP: containerIPv4, Mask: net.CIDRMask(32, 32)},
			IPv6: &net.IPNet{IP: containerIPv6, Mask: net.CIDRMask(128, 128)},
		},
		HostIPSet: &terwayTypes.IPNetSet{
			IPv4: &net.IPNet{IP: hostIPv4, Mask: net.CIDRMask(32, 32)},
			IPv6: &net.IPNet{IP: hostIPv6, Mask: net.CIDRMask(128, 128)},
		},
	}

	conf := generateHostSlaveCfg(cfg, link)
	require.Len(t, conf.Routes, 2)
	assert.Equal(t, hostIPv4, conf.Routes[0].Src)
	assert.Equal(t, hostIPv6, conf.Routes[1].Src)
	assert.Equal(t, netlink.SCOPE_LINK, conf.Routes[0].Scope)
	assert.Equal(t, netlink.SCOPE_UNIVERSE, conf.Routes[1].Scope)
	assert.Equal(t, "10.0.0.2/32", conf.Routes[0].Dst.String())
	assert.Equal(t, "fd00::2/128", conf.Routes[1].Dst.String())

	cfg.HostIPSet = nil
	conf = generateHostSlaveCfg(cfg, link)
	require.Len(t, conf.Routes, 2)
	assert.Nil(t, conf.Routes[0].Src)
	assert.Nil(t, conf.Routes[1].Src)
}
