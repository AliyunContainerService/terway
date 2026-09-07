//go:build privileged

package datapath

import (
	"context"
	"runtime"
	"testing"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"

	driverTypes "github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/types"
)

func TestIPv6OnlyDatapath(t *testing.T) {
	for _, name := range []string{"ipvlan", "policy-route"} {
		t.Run(name, func(t *testing.T) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			original, err := ns.GetCurrentNS()
			require.NoError(t, err)
			defer original.Close()
			hostNS, err := testutils.NewNS()
			require.NoError(t, err)
			defer testutils.UnmountNS(hostNS)
			defer hostNS.Close()
			containerNS, err := testutils.NewNS()
			require.NoError(t, err)
			defer testutils.UnmountNS(containerNS)
			defer containerNS.Close()
			require.NoError(t, hostNS.Set())
			defer original.Set()
			setupTestHostIPs(t)

			require.NoError(t, netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "eni"}}))
			eni, err := netlink.LinkByName("eni")
			require.NoError(t, err)
			cfg := &driverTypes.SetupConfig{
				HostVETHName: "hostv6", ContainerIfName: "eth0", MTU: 1499,
				ENIIndex: eni.Attrs().Index, DefaultRoute: true,
				ContainerIPNet: &types.IPNetSet{IPv6: containerIPNetIPv6},
				GatewayIP:      &types.IPSet{IPv6: ipv6GW},
				HostIPSet:      &types.IPNetSet{IPv6: eth0IPNetIPv6},
				// ACK retains both Service CIDRs while Pods have only IPv6.
				ServiceCIDR: &types.IPNetSet{IPv4: serviceCIDR, IPv6: serviceCIDRIPv6},
			}
			var driver interface {
				Setup(context.Context, *driverTypes.SetupConfig, ns.NetNS) error
				Teardown(context.Context, *driverTypes.TeardownCfg, ns.NetNS) error
			} = NewIPVlanDriver()
			if name == "policy-route" {
				driver = &PolicyRoute{}
			}
			ctx := context.Background()
			require.NoError(t, driver.Setup(ctx, cfg, containerNS))
			require.NoError(t, containerNS.Do(func(ns.NetNS) error {
				link, err := netlink.LinkByName("eth0")
				require.NoError(t, err)
				require.Equal(t, cfg.MTU, link.Attrs().MTU)
				addresses, err := netlink.AddrList(link, netlink.FAMILY_V4)
				require.NoError(t, err)
				require.Empty(t, addresses)
				expected := cfg.ContainerIPNet
				if name == "policy-route" {
					expected = utils.NewIPNet(cfg.ContainerIPNet)
				}
				found, err := FindIP(link, expected)
				require.NoError(t, err)
				require.True(t, found)
				routes, err := netlink.RouteList(link, netlink.FAMILY_V4)
				require.NoError(t, err)
				require.Empty(t, routes)
				routes, err = netlink.RouteList(link, netlink.FAMILY_V6)
				require.NoError(t, err)
				defaultFound := false
				for _, route := range routes {
					if route.Dst == nil || route.Dst.String() == "::/0" {
						defaultFound = true
					}
				}
				require.True(t, defaultFound, "IPv6 default route must exist")
				return nil
			}))
			teardown := &driverTypes.TeardownCfg{
				HostVETHName: cfg.HostVETHName, ContainerIfName: cfg.ContainerIfName,
				ContainerIPNet: cfg.ContainerIPNet, ENIIndex: cfg.ENIIndex,
			}
			require.NoError(t, driver.Teardown(ctx, teardown, containerNS))
			require.NoError(t, driver.Teardown(ctx, teardown, containerNS), "DEL is idempotent")
			routes, err := netlink.RouteListFiltered(netlink.FAMILY_V6,
				&netlink.Route{Dst: utils.NewIPNetWithMaxMask(containerIPNetIPv6)}, netlink.RT_FILTER_DST)
			require.NoError(t, err)
			require.Empty(t, routes, "Pod route must be removed")
		})
	}
}
