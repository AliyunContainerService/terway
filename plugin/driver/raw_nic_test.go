//go:build privileged
// +build privileged

package driver

import (
	"net"
	"runtime"
	"testing"

	terwayTypes "github.com/AliyunContainerService/terway/types"
	"github.com/containernetworking/cni/pkg/types"
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

	_, svcCIDR, err := net.ParseCIDR(serviceCIDR.String())
	assert.NoError(t, err)
	_, svcCIDRV6, err := net.ParseCIDR(serviceCIDRIPv6.String())
	assert.NoError(t, err)

	cfg := &SetupConfig{
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
		MTU:      1499,
		ENIIndex: eni.Attrs().Index,
		TrunkENI: false,
		ExtraRoutes: []types.Route{
			{
				Dst: *svcCIDR,
				GW:  LinkIP,
			},
			{
				Dst: *svcCIDRV6,
				GW:  LinkIPv6,
			},
		},
		HostIPSet: &terwayTypes.IPNetSet{
			IPv4: eth0IPNet,
			IPv6: eth0IPNetIPv6,
		},
	}

	vethDriver := NewVETHDriver(true, true)
	vethCfg := *cfg
	vethCfg.ContainerIfName = "veth1"
	err = vethDriver.Setup(&vethCfg, containerNS)
	assert.NoError(t, err)

	d := NewRawNICDriver(true, true)
	err = d.Setup(cfg, containerNS)
	assert.NoError(t, err)

	_ = containerNS.Do(func(netNS ns.NetNS) error {
		// check eth0
		containerLink, err := netlink.LinkByName(cfg.ContainerIfName)
		assert.NoError(t, err)
		assert.Equal(t, cfg.MTU, containerLink.Attrs().MTU)
		assert.True(t, containerLink.Attrs().Flags&net.FlagUp != 0)

		ok, err := FindIP(containerLink, cfg.ContainerIPNet)
		assert.NoError(t, err)
		assert.True(t, ok)

		// check veth1
		vethLink, err := netlink.LinkByName("veth1")
		assert.NoError(t, err)
		assert.Equal(t, cfg.MTU, vethLink.Attrs().MTU)
		assert.True(t, vethLink.Attrs().Flags&net.FlagUp != 0)
		assert.Equal(t, "veth", vethLink.Type())

		ok, err = FindIP(vethLink, cfg.ContainerIPNet)
		assert.NoError(t, err)
		assert.True(t, ok)

		// default via gw dev eth0
		routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{
			Dst: nil,
		}, netlink.RT_FILTER_DST)
		assert.NoError(t, err)
		assert.Equal(t, len(routes), 1)
		assert.Equal(t, ipv4GW.String(), routes[0].Gw.String())

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
		IPv4: linkIPNet,
		IPv6: linkIPNetv6,
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
	err = vethDriver.Teardown(&TeardownCfg{
		HostVETHName:    vethCfg.HostVETHName,
		ContainerIfName: vethCfg.ContainerIfName,
		ContainerIPNet:  nil,
	}, containerNS)
	assert.NoError(t, err)

	err = d.Teardown(&TeardownCfg{
		HostVETHName:    cfg.HostVETHName,
		ContainerIfName: cfg.ContainerIfName,
		ContainerIPNet:  nil,
	}, containerNS)
	assert.NoError(t, err)

	_, err = netlink.LinkByName(cfg.HostVETHName)
	assert.Error(t, err)
	_, ok = err.(netlink.LinkNotFoundError)
	assert.True(t, ok)
}
