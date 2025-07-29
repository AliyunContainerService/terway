//go:build privileged

package nic

import (
	"context"
	"net"
	"runtime"
	"testing"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
)

func TestNicSetupBasic(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var err error
	hostNS, err := testutils.NewNS()
	assert.NoError(t, err)

	containerNS, err := testutils.NewNS()
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

	_ = hostNS.Do(func(netNS ns.NetNS) error {
		// Create a dummy link
		dummy := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "test-dummy"},
		}
		err = netlink.LinkAdd(dummy)
		require.NoError(t, err)

		// Get the created link
		link, err := netlink.LinkByName("test-dummy")
		require.NoError(t, err)

		// Basic configuration
		cfg := &Conf{
			IfName: "eth0",
			MTU:    1500,
		}

		err = Setup(context.Background(), link, cfg)
		require.NoError(t, err)

		// Verify the link was renamed and brought up
		renamedLink, err := netlink.LinkByName("eth0")
		assert.NoError(t, err)
		assert.Equal(t, "eth0", renamedLink.Attrs().Name)
		assert.Equal(t, 1500, renamedLink.Attrs().MTU)
		assert.True(t, renamedLink.Attrs().Flags&net.FlagUp != 0, "Link should be up")

		return nil
	})
}

func TestNicSetupWithAddresses(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var err error
	hostNS, err := testutils.NewNS()
	assert.NoError(t, err)

	containerNS, err := testutils.NewNS()
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

	_ = hostNS.Do(func(netNS ns.NetNS) error {
		// Create a dummy link
		dummy := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "test-dummy"},
		}
		err = netlink.LinkAdd(dummy)
		require.NoError(t, err)

		// Get the created link
		link, err := netlink.LinkByName("test-dummy")
		require.NoError(t, err)

		// Configuration with addresses
		_, ipNet, err := net.ParseCIDR("192.168.1.2/24")
		require.NoError(t, err)

		addr := &netlink.Addr{
			IPNet: ipNet,
		}

		cfg := &Conf{
			IfName: "eth0",
			MTU:    1500,
			Addrs:  []*netlink.Addr{addr},
		}

		err = Setup(context.Background(), link, cfg)
		require.NoError(t, err)

		// Verify the link has the address
		renamedLink, err := netlink.LinkByName("eth0")
		assert.NoError(t, err)

		addrs, err := netlink.AddrList(renamedLink, netlink.FAMILY_V4)
		assert.NoError(t, err)
		assert.Len(t, addrs, 1)
		// The IP might be adjusted to the network address, so check the network part
		assert.Equal(t, "192.168.1.0/24", addrs[0].IPNet.String())

		return nil
	})
}

func TestNicSetupWithRoutes(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var err error
	hostNS, err := testutils.NewNS()
	assert.NoError(t, err)

	containerNS, err := testutils.NewNS()
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

	_ = hostNS.Do(func(netNS ns.NetNS) error {
		// Create a dummy link
		dummy := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "test-dummy"},
		}
		err = netlink.LinkAdd(dummy)
		require.NoError(t, err)

		// Get the created link
		link, err := netlink.LinkByName("test-dummy")
		require.NoError(t, err)

		// Add an address to the link first
		_, ipNet, err := net.ParseCIDR("192.168.1.2/24")
		require.NoError(t, err)

		addr := &netlink.Addr{
			IPNet: ipNet,
		}
		err = netlink.AddrAdd(link, addr)
		require.NoError(t, err)

		// Configuration with routes
		_, dstNet, err := net.ParseCIDR("10.0.0.0/8")
		require.NoError(t, err)

		route := &netlink.Route{
			Dst:       dstNet,
			LinkIndex: link.Attrs().Index,
			Gw:        net.ParseIP("192.168.1.1"),
		}

		cfg := &Conf{
			IfName: "eth0",
			MTU:    1500,
			Routes: []*netlink.Route{route},
		}

		err = Setup(context.Background(), link, cfg)
		require.NoError(t, err)

		// Verify the route was added
		routes, err := netlink.RouteList(link, netlink.FAMILY_V4)
		assert.NoError(t, err)

		// Find our specific route
		found := false
		for _, route := range routes {
			if route.Dst != nil && route.Dst.String() == "10.0.0.0/8" && route.Gw.String() == "192.168.1.1" {
				found = true
				break
			}
		}
		assert.True(t, found, "Expected route not found")

		return nil
	})
}

func TestNicSetupWithSysCtl(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var err error
	hostNS, err := testutils.NewNS()
	assert.NoError(t, err)

	containerNS, err := testutils.NewNS()
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

	_ = hostNS.Do(func(netNS ns.NetNS) error {
		// Create a dummy link
		dummy := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "test-dummy"},
		}
		err = netlink.LinkAdd(dummy)
		require.NoError(t, err)

		// Get the created link
		link, err := netlink.LinkByName("test-dummy")
		require.NoError(t, err)

		// Configuration with sysctl
		cfg := &Conf{
			IfName: "eth0",
			MTU:    1500,
			SysCtl: map[string][]string{
				"net.ipv4.conf.all.forwarding": {"net.ipv4.conf.all.forwarding", "1"},
			},
		}

		err = Setup(context.Background(), link, cfg)
		require.NoError(t, err)

		// Verify the link was configured and brought up
		renamedLink, err := netlink.LinkByName("eth0")
		assert.NoError(t, err)
		assert.Equal(t, "eth0", renamedLink.Attrs().Name)
		assert.Equal(t, 1500, renamedLink.Attrs().MTU)
		assert.True(t, renamedLink.Attrs().Flags&net.FlagUp != 0, "Link should be up")

		return nil
	})
}

func TestNicSetupWithNeighbors(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var err error
	hostNS, err := testutils.NewNS()
	assert.NoError(t, err)

	containerNS, err := testutils.NewNS()
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

	_ = hostNS.Do(func(netNS ns.NetNS) error {
		// Create a dummy link
		dummy := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "test-dummy"},
		}
		err = netlink.LinkAdd(dummy)
		require.NoError(t, err)

		// Get the created link
		link, err := netlink.LinkByName("test-dummy")
		require.NoError(t, err)

		// Configuration with neighbors
		neigh := &netlink.Neigh{
			LinkIndex: link.Attrs().Index,
			IP:        net.ParseIP("192.168.1.1"),
			HardwareAddr: func() net.HardwareAddr {
				hw, _ := net.ParseMAC("00:11:22:33:44:55")
				return hw
			}(),
			State: netlink.NUD_PERMANENT,
		}

		cfg := &Conf{
			IfName: "eth0",
			MTU:    1500,
			Neighs: []*netlink.Neigh{neigh},
		}

		err = Setup(context.Background(), link, cfg)
		require.NoError(t, err)

		// Verify the neighbor was added
		neighs, err := netlink.NeighList(link.Attrs().Index, netlink.FAMILY_V4)
		assert.NoError(t, err)
		assert.Len(t, neighs, 1)
		assert.Equal(t, "192.168.1.1", neighs[0].IP.String())
		assert.Equal(t, "00:11:22:33:44:55", neighs[0].HardwareAddr.String())

		// Verify the link was renamed and brought up
		renamedLink, err := netlink.LinkByName("eth0")
		assert.NoError(t, err)
		assert.Equal(t, "eth0", renamedLink.Attrs().Name)
		assert.Equal(t, 1500, renamedLink.Attrs().MTU)
		assert.True(t, renamedLink.Attrs().Flags&net.FlagUp != 0, "Link should be up")

		return nil
	})
}

func TestNicSetupWithStripVlan(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var err error
	hostNS, err := testutils.NewNS()
	assert.NoError(t, err)

	containerNS, err := testutils.NewNS()
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

	_ = hostNS.Do(func(netNS ns.NetNS) error {
		// Create a dummy link
		dummy := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "test-dummy"},
		}
		err = netlink.LinkAdd(dummy)
		require.NoError(t, err)

		// Get the created link
		link, err := netlink.LinkByName("test-dummy")
		require.NoError(t, err)

		// Configuration with strip vlan
		cfg := &Conf{
			IfName:    "eth0",
			MTU:       1500,
			StripVlan: true,
		}

		err = Setup(context.Background(), link, cfg)
		require.NoError(t, err)

		// Verify the link was configured and brought up
		renamedLink, err := netlink.LinkByName("eth0")
		assert.NoError(t, err)
		assert.Equal(t, "eth0", renamedLink.Attrs().Name)
		assert.Equal(t, 1500, renamedLink.Attrs().MTU)
		assert.True(t, renamedLink.Attrs().Flags&net.FlagUp != 0, "Link should be up")

		return nil
	})
}

func TestNicSetupInvalidSysCtl(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var err error
	hostNS, err := testutils.NewNS()
	assert.NoError(t, err)

	containerNS, err := testutils.NewNS()
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

	_ = hostNS.Do(func(netNS ns.NetNS) error {
		// Create a dummy link
		dummy := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "test-dummy"},
		}
		err = netlink.LinkAdd(dummy)
		require.NoError(t, err)

		// Get the created link
		link, err := netlink.LinkByName("test-dummy")
		require.NoError(t, err)

		// Configuration with invalid sysctl
		cfg := &Conf{
			IfName: "eth0",
			MTU:    1500,
			SysCtl: map[string][]string{
				"invalid.sysctl": {"1", "2", "3"}, // Invalid: should have exactly 2 elements
			},
		}

		err = Setup(context.Background(), link, cfg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "sysctl config err")

		return nil
	})
}
