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
	cniTypes "github.com/containernetworking/cni/pkg/types"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/vishvananda/netlink"
)

func TestVlanDataPath(t *testing.T) {
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
		Vid:       100,
		StripVlan: false,
		ExtraRoutes: []cniTypes.Route{
			{
				Dst: net.IPNet{
					IP:   net.ParseIP("169.254.1.0"),
					Mask: net.CIDRMask(24, 32),
				},
				GW: net.ParseIP("169.254.1.1"),
			},
		},
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

	d := &Vlan{}

	err = d.Setup(context.Background(), cfg, containerNS)
	assert.NoError(t, err)

	_ = containerNS.Do(func(netNS ns.NetNS) error {
		// addr
		containerLink, err := netlink.LinkByName(cfg.ContainerIfName)
		assert.NoError(t, err)
		assert.Equal(t, cfg.MTU, containerLink.Attrs().MTU)
		assert.True(t, containerLink.Attrs().Flags&net.FlagUp != 0)

		t.Logf("container link %s, ip %s", containerLink.Attrs().Name, cfg.ContainerIPNet.String())

		// 169.10.0.10/24
		ok, err := FindIP(containerLink, cfg.ContainerIPNet)
		assert.NoError(t, err)
		assert.True(t, ok)

		// default via 169.10.0.253 dev eth0
		routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{
			Dst: nil,
		}, netlink.RT_FILTER_DST)
		assert.NoError(t, err)
		assert.Equal(t, len(routes), 1)

		assert.Equal(t, net.ParseIP("169.10.0.253").String(), routes[0].Gw.String())

		// check extra routes
		extraRoutes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{
			Dst: &net.IPNet{
				IP:   net.ParseIP("169.254.1.0"),
				Mask: net.CIDRMask(24, 32),
			},
		}, netlink.RT_FILTER_DST)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(extraRoutes))
		assert.Equal(t, net.ParseIP("169.254.1.1").String(), extraRoutes[0].Gw.String())

		return nil
	})

	// check eni
	eniLink, err := netlink.LinkByName("eni")
	assert.NoError(t, err)
	assert.Equal(t, cfg.MTU, eniLink.Attrs().MTU)
	assert.True(t, eniLink.Attrs().Flags&net.FlagUp != 0)

	// tear down
	err = utils.GenericTearDown(context.Background(), containerNS)
	assert.NoError(t, err)

	// verify cleanup
	_, err = netlink.LinkByName(cfg.HostVETHName)
	assert.Error(t, err)
}

func TestVlanDataPathMultiNetwork(t *testing.T) {
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
		Vid:       100,
		StripVlan: false,
		ExtraRoutes: []cniTypes.Route{
			{
				Dst: net.IPNet{
					IP:   net.ParseIP("169.254.1.0"),
					Mask: net.CIDRMask(24, 32),
				},
				GW: net.ParseIP("169.254.1.1"),
			},
		},
		ServiceCIDR:    nil,
		HostStackCIDRs: nil,
		Ingress:        0,
		Egress:         0,
		HostIPSet: &terwayTypes.IPNetSet{
			IPv4: eth0IPNet,
			IPv6: eth0IPNetIPv6,
		},
		DefaultRoute: true,
		MultiNetwork: true, // Enable MultiNetwork feature
	}

	d := &Vlan{}

	err = d.Setup(context.Background(), cfg, containerNS)
	assert.NoError(t, err)

	_ = containerNS.Do(func(netNS ns.NetNS) error {
		// addr
		containerLink, err := netlink.LinkByName(cfg.ContainerIfName)
		assert.NoError(t, err)
		assert.Equal(t, cfg.MTU, containerLink.Attrs().MTU)
		assert.True(t, containerLink.Attrs().Flags&net.FlagUp != 0)

		t.Logf("container link %s, ip %s", containerLink.Attrs().Name, cfg.ContainerIPNet.String())

		// 169.10.0.10/24
		ok, err := FindIP(containerLink, cfg.ContainerIPNet)
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

		// check extra routes
		extraRoutes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{
			Dst: &net.IPNet{
				IP:   net.ParseIP("169.254.1.0"),
				Mask: net.CIDRMask(24, 32),
			},
		}, netlink.RT_FILTER_DST)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(extraRoutes))
		assert.Equal(t, net.ParseIP("169.254.1.1").String(), extraRoutes[0].Gw.String())

		return nil
	})

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
		MultiNetwork:    true,
	})
	assert.NoError(t, err)
	// check eni
	eniLink, err := netlink.LinkByName("eni")
	assert.NoError(t, err)
	assert.Equal(t, cfg.MTU, eniLink.Attrs().MTU)
	assert.True(t, eniLink.Attrs().Flags&net.FlagUp != 0)

	// tear down
	err = utils.GenericTearDown(context.Background(), containerNS)
	assert.NoError(t, err)

	// verify cleanup
	_, err = netlink.LinkByName(cfg.HostVETHName)
	assert.Error(t, err)
}
