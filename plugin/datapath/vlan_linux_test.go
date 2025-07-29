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
