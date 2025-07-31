//go:build privileged

package datapath

import (
	"context"
	"net"
	"runtime"
	"testing"

	types2 "github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	terwayTypes "github.com/AliyunContainerService/terway/types"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func TestDataPathExclusiveENI(t *testing.T) {
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

	cfg := &types2.SetupConfig{
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
	}

	d := NewExclusiveENIDriver()
	err = d.Setup(context.Background(), cfg, containerNS)
	assert.NoError(t, err)

	_ = containerNS.Do(func(netNS ns.NetNS) error {
		// check eth0
		containerLink, err := netlink.LinkByName(cfg.ContainerIfName)
		if assert.NoError(t, err) {
			assert.Equal(t, cfg.MTU, containerLink.Attrs().MTU)
			assert.True(t, containerLink.Attrs().Flags&net.FlagUp != 0)

			ok, err := FindIP(containerLink, utils.NewIPNet(cfg.ContainerIPNet))
			if assert.NoError(t, err) {
				assert.True(t, ok, "expect ip %s", cfg.ContainerIPNet.String())
			}
		}

		// check veth1
		vethLink, err := netlink.LinkByName("veth1")
		if assert.NoError(t, err) {
			assert.Equal(t, cfg.MTU, vethLink.Attrs().MTU)
			assert.True(t, vethLink.Attrs().Flags&net.FlagUp != 0)
			assert.Equal(t, "veth", vethLink.Type())

			ok, err := FindIP(vethLink, utils.NewIPNet(cfg.ContainerIPNet))
			assert.NoError(t, err)
			assert.True(t, ok)
		}

		// default via gw dev eth0
		routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{
			Dst: nil,
		}, netlink.RT_FILTER_DST)
		if assert.NoError(t, err) {
			assert.Equal(t, len(routes), 1)
			assert.Equal(t, ipv4GW.String(), routes[0].Gw.String())
		}

		// extra routes
		return nil
	})

	// eni should be moved to container
	_, err = netlink.LinkByName(eni.Attrs().Name)
	assert.Error(t, err)
	_, ok := err.(netlink.LinkNotFoundError)
	assert.True(t, ok)

	// host veth link
	hostVETHLink, err := netlink.LinkByName(cfg.HostVETHName)
	assert.NoError(t, err)
	assert.Equal(t, cfg.MTU, hostVETHLink.Attrs().MTU)
	assert.True(t, hostVETHLink.Attrs().Flags&net.FlagUp != 0)

	// ip 169.254.1.1/32
	ok, err = FindIP(hostVETHLink, &terwayTypes.IPNetSet{
		IPv4: LinkIPNet,
		IPv6: LinkIPNetv6,
	})
	assert.NoError(t, err)
	assert.True(t, ok)

	// route 169.10.0.10 dev hostVETH
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{
		Dst: &net.IPNet{
			IP:   cfg.ContainerIPNet.IPv4.IP,
			Mask: net.CIDRMask(32, 32),
		},
		LinkIndex: hostVETHLink.Attrs().Index,
	}, netlink.RT_FILTER_DST|netlink.RT_FILTER_OIF)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(routes))

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

	_, err = netlink.LinkByName(cfg.HostVETHName)
	assert.Error(t, err)
	_, ok = err.(netlink.LinkNotFoundError)
	assert.True(t, ok)
}

func TestDataPathExclusiveENIMultiNetwork(t *testing.T) {
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

	cfg := &types2.SetupConfig{
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

	d := NewExclusiveENIDriver()
	err = d.Setup(context.Background(), cfg, containerNS)
	assert.NoError(t, err)

	_ = containerNS.Do(func(netNS ns.NetNS) error {
		// check eth0
		containerLink, err := netlink.LinkByName(cfg.ContainerIfName)
		if assert.NoError(t, err) {
			assert.Equal(t, cfg.MTU, containerLink.Attrs().MTU)
			assert.True(t, containerLink.Attrs().Flags&net.FlagUp != 0)

			//ok, err := FindIP(containerLink, utils.NewIPNet(cfg.ContainerIPNet))
			//if assert.NoError(t, err) {
			//	assert.True(t, ok, "expect ip %s", cfg.ContainerIPNet.String())
			//}
		}

		// check veth1
		vethLink, err := netlink.LinkByName("veth1")
		if assert.NoError(t, err) {
			assert.Equal(t, cfg.MTU, vethLink.Attrs().MTU)
			assert.True(t, vethLink.Attrs().Flags&net.FlagUp != 0)
			assert.Equal(t, "veth", vethLink.Type())

			//ok, err := FindIP(vethLink, utils.NewIPNet(cfg.ContainerIPNet))
			//assert.NoError(t, err)
			//assert.True(t, ok)
		}

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

		// default via gw dev eth0
		routes, err = netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{
			Dst: nil,
		}, netlink.RT_FILTER_DST)
		if assert.NoError(t, err) {
			assert.Equal(t, len(routes), 1)
			assert.Equal(t, ipv4GW.String(), routes[0].Gw.String())
		}

		return nil
	})

	// eni should be moved to container
	_, err = netlink.LinkByName(eni.Attrs().Name)
	assert.Error(t, err)
	_, ok := err.(netlink.LinkNotFoundError)
	assert.True(t, ok)

	// host veth link
	hostVETHLink, err := netlink.LinkByName(cfg.HostVETHName)
	assert.NoError(t, err)
	assert.Equal(t, cfg.MTU, hostVETHLink.Attrs().MTU)
	assert.True(t, hostVETHLink.Attrs().Flags&net.FlagUp != 0)

	// ip 169.254.1.1/32
	ok, err = FindIP(hostVETHLink, &terwayTypes.IPNetSet{
		IPv4: LinkIPNet,
		IPv6: LinkIPNetv6,
	})
	assert.NoError(t, err)
	assert.True(t, ok)

	// route 169.10.0.10 dev hostVETH
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{
		Dst: &net.IPNet{
			IP:   cfg.ContainerIPNet.IPv4.IP,
			Mask: net.CIDRMask(32, 32),
		},
		LinkIndex: hostVETHLink.Attrs().Index,
	}, netlink.RT_FILTER_DST|netlink.RT_FILTER_OIF)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(routes))

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

	err = d.Check(context.Background(), &types2.CheckConfig{
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

	_, err = netlink.LinkByName(cfg.HostVETHName)
	assert.Error(t, err)
	_, ok = err.(netlink.LinkNotFoundError)
	assert.True(t, ok)
}
