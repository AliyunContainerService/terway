//go:build privileged

package daemon

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
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/AliyunContainerService/terway/types"
)

// setupTestNS creates a new network namespace for testing
func setupTestNS(t *testing.T) ns.NetNS {
	runtime.LockOSThread()

	testNS, err := testutils.NewNS()
	require.NoError(t, err)

	return testNS
}

// cleanupTestNS cleans up the test network namespace
func cleanupTestNS(t *testing.T, testNS ns.NetNS) {
	if testNS != nil {
		_ = testNS.Close()
		_ = testutils.UnmountNS(testNS)
	}
	runtime.UnlockOSThread()
}

func TestGcPolicyRoutesNormalCase(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Create a dummy link to simulate ENI
		dummyLink := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{
				Name: "test-eni",
			},
		}
		err := netlink.LinkAdd(dummyLink)
		require.NoError(t, err)

		// Get the created link to get its MAC address
		link, err := netlink.LinkByName("test-eni")
		require.NoError(t, err)
		mac := link.Attrs().HardwareAddr.String()

		containerIPNet := &types.IPNetSet{
			IPv4: &net.IPNet{
				IP:   net.ParseIP("192.168.1.2"),
				Mask: net.CIDRMask(24, 32),
			},
		}

		ctx := context.Background()
		err = gcPolicyRoutes(ctx, mac, containerIPNet, "test-namespace", "test-pod")
		// Should not error even if policy route doesn't exist
		assert.NoError(t, err)
		return nil
	})
	require.NoError(t, err)
}

func TestGcPolicyRoutesNilContainerIPNet(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Create a dummy link to simulate ENI
		dummyLink := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{
				Name: "test-eni",
			},
		}
		err := netlink.LinkAdd(dummyLink)
		require.NoError(t, err)

		// Get the created link to get its MAC address
		link, err := netlink.LinkByName("test-eni")
		require.NoError(t, err)
		mac := link.Attrs().HardwareAddr.String()

		ctx := context.Background()
		err = gcPolicyRoutes(ctx, mac, nil, "test-namespace", "test-pod")
		// Should not error
		assert.NoError(t, err)
		return nil
	})
	require.NoError(t, err)
}

func TestGcLeakedRulesEmptyLinks(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Create test IP set
		existIP := sets.New[string]("192.168.1.2", "192.168.1.3")

		// Test with no links - should not panic
		assert.NotPanics(t, func() {
			gcLeakedRules(existIP)
		})
		return nil
	})
	require.NoError(t, err)
}

func TestGcLeakedRulesIPVlanLinks(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Create test IP set
		existIP := sets.New[string]("192.168.1.2", "192.168.1.3")

		// Create an IPVlan link
		parentLink := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "parent-dummy"},
		}
		err := netlink.LinkAdd(parentLink)
		require.NoError(t, err)

		ipvlanLink := &netlink.IPVlan{
			LinkAttrs: netlink.LinkAttrs{
				Name:        "test-ipvlan",
				ParentIndex: parentLink.Attrs().Index,
			},
			Mode: netlink.IPVLAN_MODE_L2,
		}
		err = netlink.LinkAdd(ipvlanLink)
		require.NoError(t, err)

		// Add IP address to parent link
		addr := &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   net.ParseIP("10.0.0.2"),
				Mask: net.CIDRMask(24, 32),
			},
		}
		err = netlink.AddrAdd(ipvlanLink, addr)
		require.NoError(t, err)

		err = netlink.LinkSetUp(ipvlanLink)
		require.NoError(t, err)

		// Add a route to the IPVlan link
		route := &netlink.Route{
			LinkIndex: ipvlanLink.Attrs().Index,
			Dst: &net.IPNet{
				IP:   net.ParseIP("10.0.0.0"),
				Mask: net.CIDRMask(24, 32),
			},
			Flags: int(netlink.FLAG_ONLINK),
			Gw:    net.ParseIP("10.0.0.1"),
		}
		err = netlink.RouteReplace(route)
		require.NoError(t, err)

		// Test GC with IPVlan links
		assert.NotPanics(t, func() {
			gcLeakedRules(existIP)
		})

		routes, err := netlink.RouteList(ipvlanLink, netlink.FAMILY_V4)
		require.NoError(t, err)
		assert.Len(t, routes, 0)
		return nil
	})
	require.NoError(t, err)
}

func TestGcLeakedRulesNormalDeviceLinks(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Create test IP set
		existIP := sets.New[string]("192.168.1.2", "192.168.1.3")

		// Create a normal device link
		normalLink := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "normal-dummy"},
		}
		err := netlink.LinkAdd(normalLink)
		require.NoError(t, err)

		// Test GC with normal device links
		assert.NotPanics(t, func() {
			gcLeakedRules(existIP)
		})
		return nil
	})
	require.NoError(t, err)
}

func TestGcRoutesDeleteNotInExistIP(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Create test IPVlan link
		parentLink := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "parent-dummy"},
		}
		err := netlink.LinkAdd(parentLink)
		require.NoError(t, err)

		ipvlanLink := &netlink.IPVlan{
			LinkAttrs: netlink.LinkAttrs{
				Name:        "test-ipvlan",
				ParentIndex: parentLink.Attrs().Index,
			},
			Mode: netlink.IPVLAN_MODE_L2,
		}
		err = netlink.LinkAdd(ipvlanLink)
		require.NoError(t, err)

		// Add an IP address to the parent link so we can add routes
		addr := &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   net.ParseIP("10.0.0.2"),
				Mask: net.CIDRMask(24, 32),
			},
		}
		err = netlink.AddrAdd(parentLink, addr)
		require.NoError(t, err)

		err = netlink.LinkSetUp(ipvlanLink)
		require.NoError(t, err)

		// Add routes that should be garbage collected
		route1 := &netlink.Route{
			LinkIndex: ipvlanLink.Attrs().Index,
			Dst: &net.IPNet{
				IP:   net.ParseIP("10.0.0.0"),
				Mask: net.CIDRMask(24, 32),
			},
			Gw:    net.ParseIP("10.0.0.1"),
			Flags: int(netlink.FLAG_ONLINK),
		}
		err = netlink.RouteAdd(route1)
		require.NoError(t, err)

		route2 := &netlink.Route{
			LinkIndex: ipvlanLink.Attrs().Index,
			Dst: &net.IPNet{
				IP:   net.ParseIP("10.0.1.0"),
				Mask: net.CIDRMask(24, 32),
			},
			Gw:    net.ParseIP("10.0.1.1"),
			Flags: int(netlink.FLAG_ONLINK),
		}
		err = netlink.RouteAdd(route2)
		require.NoError(t, err)

		// Create existIP set with only one of the routes
		existIP := sets.New[string]("10.0.0.0")

		// Run GC
		gcRoutes([]netlink.Link{ipvlanLink}, existIP)

		// Verify that route2 was deleted but route1 remains
		routes, err := netlink.RouteList(ipvlanLink, netlink.FAMILY_V4)
		require.NoError(t, err)
		assert.Len(t, routes, 1)
		assert.Equal(t, "10.0.0.0/24", routes[0].Dst.String())
		return nil
	})
	require.NoError(t, err)
}

func TestGcRoutesNilDst(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Create test IPVlan link
		parentLink := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "parent-dummy"},
		}
		err := netlink.LinkAdd(parentLink)
		require.NoError(t, err)

		ipvlanLink := &netlink.IPVlan{
			LinkAttrs: netlink.LinkAttrs{
				Name:        "test-ipvlan",
				ParentIndex: parentLink.Attrs().Index,
			},
			Mode: netlink.IPVLAN_MODE_L2,
		}
		err = netlink.LinkAdd(ipvlanLink)
		require.NoError(t, err)

		// Add an IP address to the parent link so we can add routes
		addr := &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   net.ParseIP("10.0.0.2"),
				Mask: net.CIDRMask(24, 32),
			},
		}
		err = netlink.AddrAdd(ipvlanLink, addr)
		require.NoError(t, err)

		err = netlink.LinkSetUp(ipvlanLink)
		require.NoError(t, err)

		// Add a route with nil Dst (default route)
		defaultRoute := &netlink.Route{
			LinkIndex: ipvlanLink.Attrs().Index,
			Gw:        net.ParseIP("10.0.0.1"),
			Flags:     int(netlink.FLAG_ONLINK),
		}
		err = netlink.RouteAdd(defaultRoute)
		require.NoError(t, err)

		existIP := sets.New[string]("10.0.0.0")

		// Run GC - should not delete default route
		gcRoutes([]netlink.Link{ipvlanLink}, existIP)

		// Verify default route still exists
		routes, err := netlink.RouteList(ipvlanLink, netlink.FAMILY_V4)
		require.NoError(t, err)
		// Should have at least one route (the default route)
		assert.GreaterOrEqual(t, len(routes), 1)
		return nil
	})
	require.NoError(t, err)
}

func TestGcTCFiltersNotInExistIP(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Create test device link
		deviceLink := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "test-device"},
		}
		err := netlink.LinkAdd(deviceLink)
		require.NoError(t, err)

		existIP := sets.New[string]("192.168.1.2")

		// Run GC - should not panic even with no filters
		assert.NotPanics(t, func() {
			gcTCFilters([]netlink.Link{deviceLink}, existIP)
		})
		return nil
	})
	require.NoError(t, err)
}

func TestGcTCFiltersEmptyExistIP(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Create test device link
		deviceLink := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "test-device"},
		}
		err := netlink.LinkAdd(deviceLink)
		require.NoError(t, err)

		// Test with empty existIP set
		existIP := sets.New[string]()

		// Run GC - should not panic
		assert.NotPanics(t, func() {
			gcTCFilters([]netlink.Link{deviceLink}, existIP)
		})
		return nil
	})
	require.NoError(t, err)
}

func TestGcTCFiltersInvalidIP(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Create test device link
		deviceLink := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "test-device"},
		}
		err := netlink.LinkAdd(deviceLink)
		require.NoError(t, err)

		// Test with invalid IP in existIP set
		existIP := sets.New[string]("invalid-ip")

		// Run GC - should handle invalid IP gracefully
		assert.NotPanics(t, func() {
			gcTCFilters([]netlink.Link{deviceLink}, existIP)
		})
		return nil
	})
	require.NoError(t, err)
}
