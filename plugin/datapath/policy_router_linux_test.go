//go:build privileged

package datapath

import (
	"context"
	"net"
	"runtime"
	"testing"

	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	terwayTypes "github.com/AliyunContainerService/terway/types"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func TestDataPathPolicyRoute(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var err error
	hostNS, err := testutils.NewNS()
	assert.NoError(t, err)

	containerNS, err := testutils.NewNS()
	assert.NoError(t, err)

	err = hostNS.Set()
	assert.NoError(t, err)

	defer func() {
		err := containerNS.Close()
		assert.NoError(t, err)

		err = testutils.UnmountNS(containerNS)
		assert.NoError(t, err)

		err = hostNS.Close()
		assert.NoError(t, err)

		err = testutils.UnmountNS(hostNS)
		assert.NoError(t, err)
	}()

	err = netlink.LinkAdd(&netlink.Dummy{
		LinkAttrs: netlink.LinkAttrs{Name: "eni"},
	})
	assert.NoError(t, err)
	eni, err := netlink.LinkByName("eni")
	assert.NoError(t, err)

	cfg := &types.SetupConfig{
		HostVETHName:    "hostveth",
		ContainerIfName: "eth0",
		ContainerIPNet: &terwayTypes.IPNetSet{
			IPv4: containerIPNet,
			IPv6: containerIPNetIPv6,
		},
		GatewayIP: &terwayTypes.IPSet{
			IPv4: ipv4GW,
			IPv6: ipv6GW,
		},
		MTU:            1499,
		ENIIndex:       eni.Attrs().Index,
		StripVlan:      false,
		ExtraRoutes:    nil,
		ServiceCIDR:    nil,
		HostStackCIDRs: nil,
		Ingress:        0,
		Egress:         0,
		HostIPSet: &terwayTypes.IPNetSet{
			IPv4: eth0IPNet,
			IPv6: eth0IPNetIPv6,
		},
		DefaultRoute: true,
	}

	d := &PolicyRoute{}

	err = d.Setup(context.Background(), cfg, containerNS)
	assert.NoError(t, err)

	_ = containerNS.Do(func(netNS ns.NetNS) error {
		// addr
		containerLink, err := netlink.LinkByName(cfg.ContainerIfName)
		assert.NoError(t, err)
		assert.Equal(t, cfg.MTU, containerLink.Attrs().MTU)
		assert.True(t, containerLink.Attrs().Flags&net.FlagUp != 0)

		ok, err := FindIP(containerLink, utils.NewIPNet(cfg.ContainerIPNet))
		assert.NoError(t, err)
		assert.True(t, ok)

		// default via 169.254.1.1 dev eth0
		routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{
			Dst: nil,
		}, netlink.RT_FILTER_DST)
		assert.NoError(t, err)
		assert.Equal(t, len(routes), 1)

		assert.Equal(t, net.ParseIP("169.254.1.1").String(), routes[0].Gw.String())

		return nil
	})

	// eth0's ip 169.20.20.10/32 dev eni
	ok, err := FindIP(eni, &terwayTypes.IPNetSet{
		IPv4: &net.IPNet{
			IP:   cfg.HostIPSet.IPv4.IP,
			Mask: net.CIDRMask(32, 32),
		},
		IPv6: nil,
	})
	assert.NoError(t, err)
	assert.True(t, ok)

	hostVETHLink, err := netlink.LinkByName(cfg.HostVETHName)
	assert.NoError(t, err)
	assert.Equal(t, cfg.MTU, hostVETHLink.Attrs().MTU)

	addrs, err := netlink.AddrList(hostVETHLink, netlink.FAMILY_V4)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(addrs))

	// 169.10.0.10 dev hostVETH
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{
		Dst: &net.IPNet{
			IP:   cfg.ContainerIPNet.IPv4.IP,
			Mask: net.CIDRMask(32, 32),
		},
		LinkIndex: hostVETHLink.Attrs().Index,
	}, netlink.RT_FILTER_DST|netlink.RT_FILTER_OIF)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(routes))

	// 512 from all to 169.10.0.10 look up main
	rule := netlink.NewRule()
	rule.Priority = toContainerPriority
	rule.Table = unix.RT_TABLE_MAIN
	rule.Dst = &net.IPNet{
		IP:   cfg.ContainerIPNet.IPv4.IP,
		Mask: net.CIDRMask(32, 32),
	}

	rules, err := netlink.RuleListFiltered(netlink.FAMILY_V4, rule, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_DST|netlink.RT_FILTER_PRIORITY)
	assert.NoError(t, err)
	assert.Equal(t, len(rules), 1)
	assert.Nil(t, rules[0].Src)

	// 2048 from 169.10.0.10 lookup table
	rule = netlink.NewRule()
	rule.Priority = fromContainerPriority
	rule.Table = utils.GetRouteTableID(eni.Attrs().Index)
	rule.Src = &net.IPNet{
		IP:   cfg.ContainerIPNet.IPv4.IP,
		Mask: net.CIDRMask(32, 32),
	}
	rules, err = netlink.RuleListFiltered(netlink.FAMILY_V4, rule, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_SRC|netlink.RT_FILTER_PRIORITY|netlink.RT_FILTER_IIF)
	assert.NoError(t, err)
	assert.Equal(t, len(rules), 1)

	// add some ip rule make sure we don't delete it
	dummyRule := netlink.NewRule()
	dummyRule.Priority = toContainerPriority
	dummyRule.Table = unix.RT_TABLE_MAIN
	dummyRule.Dst = &net.IPNet{
		IP:   cfg.ContainerIPNet.IPv4.IP,
		Mask: net.CIDRMask(24, 32),
	}
	err = netlink.RuleAdd(dummyRule)
	assert.NoError(t, err)

	// tear down

	err = utils.GenericTearDown(context.Background(), containerNS)
	assert.NoError(t, err)

	err = d.Teardown(context.Background(), &types.TeardownCfg{
		HostVETHName:    cfg.HostVETHName,
		ContainerIfName: cfg.ContainerIfName,
		ContainerIPNet: &terwayTypes.IPNetSet{
			IPv4: containerIPNet,
			IPv6: containerIPNetIPv6,
		},
		ENIIndex: eni.Attrs().Index,
	}, containerNS)
	assert.NoError(t, err)

	_, err = netlink.LinkByName(cfg.HostVETHName)
	assert.Error(t, err)
	_, ok = err.(netlink.LinkNotFoundError)
	assert.True(t, ok)

	rules, err = netlink.RuleList(netlink.FAMILY_V4)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(rules))

	for _, r := range rules {
		t.Logf("%s %#v ", r, r)
	}

	err = d.Teardown(context.Background(), &types.TeardownCfg{
		HostVETHName:    cfg.HostVETHName,
		ContainerIfName: cfg.ContainerIfName,
		ContainerIPNet: &terwayTypes.IPNetSet{
			IPv4: containerIPNet,
			IPv6: containerIPNetIPv6,
		},
		ENIIndex: 0,
	}, containerNS)
	assert.NoError(t, err)
}

func TestDataPathPolicyRouteMultiNetwork(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var err error
	hostNS, err := testutils.NewNS()
	assert.NoError(t, err)

	containerNS, err := testutils.NewNS()
	assert.NoError(t, err)

	err = hostNS.Set()
	assert.NoError(t, err)

	defer func() {
		err := containerNS.Close()
		assert.NoError(t, err)

		err = testutils.UnmountNS(containerNS)
		assert.NoError(t, err)

		err = hostNS.Close()
		assert.NoError(t, err)

		err = testutils.UnmountNS(hostNS)
		assert.NoError(t, err)
	}()

	err = netlink.LinkAdd(&netlink.Dummy{
		LinkAttrs: netlink.LinkAttrs{Name: "eni"},
	})
	assert.NoError(t, err)
	eni, err := netlink.LinkByName("eni")
	assert.NoError(t, err)

	cfg := &types.SetupConfig{
		HostVETHName:    "hostveth",
		ContainerIfName: "eth0",
		ContainerIPNet: &terwayTypes.IPNetSet{
			IPv4: containerIPNet,
			IPv6: containerIPNetIPv6,
		},
		GatewayIP: &terwayTypes.IPSet{
			IPv4: ipv4GW,
			IPv6: ipv6GW,
		},
		MTU:       1499,
		ENIIndex:  eni.Attrs().Index,
		StripVlan: false,
		ServiceCIDR: &terwayTypes.IPNetSet{
			IPv4: serviceCIDR,
			IPv6: serviceCIDRIPv6,
		},
		HostIPSet: &terwayTypes.IPNetSet{
			IPv4: eth0IPNet,
			IPv6: eth0IPNetIPv6,
		},
		DefaultRoute: true,
		MultiNetwork: true, // Enable MultiNetwork feature
	}

	d := &PolicyRoute{}

	err = d.Setup(context.Background(), cfg, containerNS)
	assert.NoError(t, err)

	_ = containerNS.Do(func(netNS ns.NetNS) error {
		// addr
		containerLink, err := netlink.LinkByName(cfg.ContainerIfName)
		assert.NoError(t, err)
		assert.Equal(t, cfg.MTU, containerLink.Attrs().MTU)
		assert.True(t, containerLink.Attrs().Flags&net.FlagUp != 0)

		ok, err := FindIP(containerLink, utils.NewIPNet(cfg.ContainerIPNet))
		assert.NoError(t, err)
		assert.True(t, ok)

		// Check routing rules - MultiNetwork mode should have specific routing rules
		table := utils.GetRouteTableID(eni.Attrs().Index)

		// Check IPv4 routing rules
		if cfg.ContainerIPNet.IPv4 != nil {
			// Check source IP routing rules
			v4 := utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv4)
			rules, err := netlink.RuleList(netlink.FAMILY_V4)
			assert.NoError(t, err)

			foundSrcRule := false
			foundOifRule := false
			for _, rule := range rules {
				t.Logf("rule: %+v\n", rule)
				if rule.Src != nil && rule.Src.String() == v4.String() && rule.Table == table && rule.Priority == toContainerPriority {
					foundSrcRule = true
				}
				if rule.OifName == cfg.ContainerIfName && rule.Table == table && rule.Priority == toContainerPriority {
					foundOifRule = true
				}
			}
			assert.True(t, foundSrcRule, "should find source IP rule for IPv4")
			assert.True(t, foundOifRule, "should find output interface rule for IPv4")
		}

		// Check IPv6 routing rules
		if cfg.ContainerIPNet.IPv6 != nil {
			v6 := utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv6)
			rules, err := netlink.RuleList(netlink.FAMILY_V6)
			assert.NoError(t, err)

			foundSrcRule := false
			foundOifRule := false
			for _, rule := range rules {
				t.Logf("rule: %+v\n", rule)
				if rule.Src != nil && rule.Src.String() == v6.String() && rule.Table == table && rule.Priority == toContainerPriority {
					foundSrcRule = true
				}
				if rule.Table == table && rule.Priority == toContainerPriority {
					foundOifRule = true
				}
			}
			assert.True(t, foundSrcRule, "should find source IP rule for IPv6")
			assert.True(t, foundOifRule, "should find output interface rule for IPv6")
		}

		// Check default route in routing table
		routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{
			Table: table,
		}, netlink.RT_FILTER_TABLE)
		assert.NoError(t, err)

		foundDefaultRoute := false
		for _, route := range routes {
			if route.Dst == nil && route.Gw != nil && route.Gw.String() == ipv4GW.String() {
				foundDefaultRoute = true
				break
			}
		}
		assert.True(t, foundDefaultRoute, "should find default route in custom table")

		return nil
	})

	// check eni
	eniLink, err := netlink.LinkByName("eni")
	assert.NoError(t, err)
	assert.Equal(t, cfg.MTU, eniLink.Attrs().MTU)
	assert.True(t, eniLink.Attrs().Flags&net.FlagUp != 0)

	// check host veth

	hostVETHLink, err := netlink.LinkByName(cfg.HostVETHName)
	assert.NoError(t, err)
	assert.Equal(t, cfg.MTU, hostVETHLink.Attrs().MTU)

	addrs, err := netlink.AddrList(hostVETHLink, netlink.FAMILY_V4)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(addrs))

	// 169.10.0.10 dev hostVETH
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{
		Dst: &net.IPNet{
			IP:   cfg.ContainerIPNet.IPv4.IP,
			Mask: net.CIDRMask(32, 32),
		},
		LinkIndex: hostVETHLink.Attrs().Index,
	}, netlink.RT_FILTER_DST|netlink.RT_FILTER_OIF)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(routes))

	// 512 from all to 169.10.0.10 look up main
	rule := netlink.NewRule()
	rule.Priority = toContainerPriority
	rule.Table = unix.RT_TABLE_MAIN
	rule.Dst = &net.IPNet{
		IP:   cfg.ContainerIPNet.IPv4.IP,
		Mask: net.CIDRMask(32, 32),
	}

	rules, err := netlink.RuleListFiltered(netlink.FAMILY_V4, rule, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_DST|netlink.RT_FILTER_PRIORITY)
	assert.NoError(t, err)
	assert.Equal(t, len(rules), 1)
	assert.Nil(t, rules[0].Src)

	// 2048 from 169.10.0.10 lookup table
	rule = netlink.NewRule()
	rule.Priority = fromContainerPriority
	rule.Table = utils.GetRouteTableID(eni.Attrs().Index)
	rule.Src = &net.IPNet{
		IP:   cfg.ContainerIPNet.IPv4.IP,
		Mask: net.CIDRMask(32, 32),
	}
	rules, err = netlink.RuleListFiltered(netlink.FAMILY_V4, rule, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_SRC|netlink.RT_FILTER_PRIORITY|netlink.RT_FILTER_IIF)
	assert.NoError(t, err)
	assert.Equal(t, len(rules), 1)

	// add some ip rule make sure we don't delete it
	dummyRule := netlink.NewRule()
	dummyRule.Priority = toContainerPriority
	dummyRule.Table = unix.RT_TABLE_MAIN
	dummyRule.Dst = &net.IPNet{
		IP:   cfg.ContainerIPNet.IPv4.IP,
		Mask: net.CIDRMask(24, 32),
	}
	err = netlink.RuleAdd(dummyRule)
	assert.NoError(t, err)

	err = d.Check(context.Background(), &types.CheckConfig{
		DP:              0,
		RecordPodEvent:  nil,
		NetNS:           containerNS,
		HostVETHName:    "",
		ContainerIfName: "eth0",
		ContainerIPNet:  nil,
		HostIPSet:       nil,
		GatewayIP:       nil,
		ENIIndex:        0,
		TrunkENI:        false,
		MTU:             1499,
		DefaultRoute:    false,
		MultiNetwork:    false,
	})
	assert.NoError(t, err)

	// tear down
	err = utils.GenericTearDown(context.Background(), containerNS)
	assert.NoError(t, err)

	err = d.Teardown(context.Background(), &types.TeardownCfg{
		HostVETHName:    cfg.HostVETHName,
		ContainerIfName: cfg.ContainerIfName,
		ContainerIPNet: &terwayTypes.IPNetSet{
			IPv4: containerIPNet,
			IPv6: containerIPNetIPv6,
		},
		ENIIndex: eni.Attrs().Index,
	}, containerNS)
	assert.NoError(t, err)

	_, err = netlink.LinkByName(cfg.HostVETHName)
	assert.Error(t, err)
	_, ok := err.(netlink.LinkNotFoundError)
	assert.True(t, ok)

	rules, err = netlink.RuleList(netlink.FAMILY_V4)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(rules))

	for _, r := range rules {
		t.Logf("%s %#v ", r, r)
	}

	err = d.Teardown(context.Background(), &types.TeardownCfg{
		HostVETHName:    cfg.HostVETHName,
		ContainerIfName: cfg.ContainerIfName,
		ContainerIPNet: &terwayTypes.IPNetSet{
			IPv4: containerIPNet,
			IPv6: containerIPNetIPv6,
		},
		ENIIndex: 0,
	}, containerNS)
	assert.NoError(t, err)
}
