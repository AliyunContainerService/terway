//go:build privileged

package daemon

import (
	"context"
	"encoding/binary"
	"net"
	"runtime"
	"testing"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
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

func TestGcTCFiltersWithExistingFilter(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Create test device link
		deviceLink := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "test-device"},
		}
		err := netlink.LinkAdd(deviceLink)
		require.NoError(t, err)

		// Get the link to ensure it's up
		link, err := netlink.LinkByName("test-device")
		require.NoError(t, err)

		err = netlink.LinkSetUp(link)
		require.NoError(t, err)

		// Add clsact qdisc
		qdisc := &netlink.GenericQdisc{
			QdiscAttrs: netlink.QdiscAttrs{
				LinkIndex: link.Attrs().Index,
				Handle:    netlink.MakeHandle(0xffff, 0),
				Parent:    netlink.HANDLE_CLSACT,
			},
			QdiscType: "clsact",
		}
		err = netlink.QdiscAdd(qdisc)
		require.NoError(t, err)

		// Create a U32 filter with VlanAction for IP 192.168.1.100 that should be GC'd
		vlanAct := netlink.NewVlanKeyAction()
		vlanAct.Attrs().Action = netlink.TC_ACT_PIPE
		vlanAct.Action = netlink.TCA_VLAN_KEY_PUSH
		vlanAct.Vid = 100

		// IP 192.168.1.100 in big endian u32 format: 0xc0a80164
		filter := &netlink.U32{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: link.Attrs().Index,
				Parent:    netlink.HANDLE_MIN_EGRESS,
				Priority:  50001,
				Protocol:  0x0800, // ETH_P_IP
			},
			Sel: &netlink.TcU32Sel{
				Nkeys: 1,
				Flags: netlink.TC_U32_TERMINAL,
				Keys: []netlink.TcU32Key{
					{
						Mask: 0xffffffff,
						Val:  0xc0a80164, // 192.168.1.100
						Off:  12,
					},
				},
			},
			Actions: []netlink.Action{vlanAct},
		}
		err = netlink.FilterAdd(filter)
		require.NoError(t, err)

		// Verify filter exists before GC
		filtersBefore, err := netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
		require.NoError(t, err)
		assert.NotEmpty(t, filtersBefore, "Filter should exist before GC")

		// existIP contains 192.168.1.2 but NOT 192.168.1.100, so the filter should be deleted
		existIP := sets.New[string]("192.168.1.2")

		// Run GC - should delete the filter for 192.168.1.100
		gcTCFilters([]netlink.Link{link}, existIP)

		// Verify filter is deleted after GC
		filtersAfter, err := netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
		require.NoError(t, err)
		assert.Empty(t, filtersAfter, "Filter for non-existent IP should be deleted")

		return nil
	})
	require.NoError(t, err)
}

func TestGcTCFiltersKeepsExistingIP(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Create test device link
		deviceLink := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "test-device"},
		}
		err := netlink.LinkAdd(deviceLink)
		require.NoError(t, err)

		// Get the link to ensure it's up
		link, err := netlink.LinkByName("test-device")
		require.NoError(t, err)

		err = netlink.LinkSetUp(link)
		require.NoError(t, err)

		// Add clsact qdisc
		qdisc := &netlink.GenericQdisc{
			QdiscAttrs: netlink.QdiscAttrs{
				LinkIndex: link.Attrs().Index,
				Handle:    netlink.MakeHandle(0xffff, 0),
				Parent:    netlink.HANDLE_CLSACT,
			},
			QdiscType: "clsact",
		}
		err = netlink.QdiscAdd(qdisc)
		require.NoError(t, err)

		// Create a U32 filter with VlanAction for IP 192.168.1.2 that should be KEPT
		vlanAct := netlink.NewVlanKeyAction()
		vlanAct.Attrs().Action = netlink.TC_ACT_PIPE
		vlanAct.Action = netlink.TCA_VLAN_KEY_PUSH
		vlanAct.Vid = 100

		// IP 192.168.1.2 in big endian u32 format: 0xc0a80102
		filter := &netlink.U32{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: link.Attrs().Index,
				Parent:    netlink.HANDLE_MIN_EGRESS,
				Priority:  50001,
				Protocol:  0x0800, // ETH_P_IP
			},
			Sel: &netlink.TcU32Sel{
				Nkeys: 1,
				Flags: netlink.TC_U32_TERMINAL,
				Keys: []netlink.TcU32Key{
					{
						Mask: 0xffffffff,
						Val:  0xc0a80102, // 192.168.1.2
						Off:  12,
					},
				},
			},
			Actions: []netlink.Action{vlanAct},
		}
		err = netlink.FilterAdd(filter)
		require.NoError(t, err)

		// Verify filter exists before GC
		filtersBefore, err := netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
		require.NoError(t, err)
		assert.NotEmpty(t, filtersBefore, "Filter should exist before GC")

		// existIP contains 192.168.1.2, so the filter should be KEPT
		existIP := sets.New[string]("192.168.1.2")

		// Run GC - should NOT delete the filter for 192.168.1.2
		gcTCFilters([]netlink.Link{link}, existIP)

		// Verify filter still exists after GC
		filtersAfter, err := netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
		require.NoError(t, err)
		assert.NotEmpty(t, filtersAfter, "Filter for existing IP should be kept")

		return nil
	})
	require.NoError(t, err)
}

// ipv6VlanFilter builds the 4-key U32 egress filter that EnsureVlanTag creates for an IPv6 src address.
// Keys are at offsets 8,12,16,20 in the IPv6 header (source address field).
func ipv6VlanFilter(linkIndex int, ip net.IP, vid uint16) *netlink.U32 {
	ip = ip.To16()
	vlanAct := netlink.NewVlanKeyAction()
	vlanAct.Attrs().Action = netlink.TC_ACT_PIPE
	vlanAct.Action = netlink.TCA_VLAN_KEY_PUSH
	vlanAct.Vid = vid

	keys := make([]netlink.TcU32Key, 4)
	for i := 0; i < 4; i++ {
		keys[i] = netlink.TcU32Key{
			Mask: 0xffffffff,
			Val:  binary.BigEndian.Uint32(ip[i*4 : i*4+4]),
			Off:  int32(8 + i*4),
		}
	}
	return &netlink.U32{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: linkIndex,
			Parent:    netlink.HANDLE_MIN_EGRESS,
			Priority:  50002,
			Protocol:  uint16(unix.ETH_P_IPV6),
		},
		Sel: &netlink.TcU32Sel{
			Nkeys: 4,
			Flags: netlink.TC_U32_TERMINAL,
			Keys:  keys,
		},
		Actions: []netlink.Action{vlanAct},
	}
}

func addClsact(t *testing.T, link netlink.Link) {
	t.Helper()
	qdisc := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: link.Attrs().Index,
			Handle:    netlink.MakeHandle(0xffff, 0),
			Parent:    netlink.HANDLE_CLSACT,
		},
		QdiscType: "clsact",
	}
	require.NoError(t, netlink.QdiscAdd(qdisc))
}

// TestGcTCFilters_IPv6_GCsStaleFilter verifies that an IPv6 VLAN push filter
// at priority 50002 is deleted when its src IP is absent from existIP.
func TestGcTCFilters_IPv6_GCsStaleFilter(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		dummy := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "gc-v6-stale"}}
		require.NoError(t, netlink.LinkAdd(dummy))
		link, err := netlink.LinkByName("gc-v6-stale")
		require.NoError(t, err)
		require.NoError(t, netlink.LinkSetUp(link))
		addClsact(t, link)

		podIP := net.ParseIP("fd00::1")
		require.NoError(t, netlink.FilterAdd(ipv6VlanFilter(link.Attrs().Index, podIP, 100)))

		filtersBefore, err := netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
		require.NoError(t, err)
		assert.NotEmpty(t, filtersBefore)

		// fd00::1 NOT in existIP → filter should be GC'd
		gcTCFilters([]netlink.Link{link}, sets.New[string]("fd00::2"))

		filtersAfter, err := netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
		require.NoError(t, err)
		assert.Empty(t, filtersAfter, "stale IPv6 VLAN filter should be deleted")
		return nil
	})
	require.NoError(t, err)
}

// TestGcTCFilters_IPv6_KeepsLiveFilter verifies that an IPv6 VLAN push filter
// at priority 50002 is retained when its src IP is present in existIP.
func TestGcTCFilters_IPv6_KeepsLiveFilter(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		dummy := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "gc-v6-live"}}
		require.NoError(t, netlink.LinkAdd(dummy))
		link, err := netlink.LinkByName("gc-v6-live")
		require.NoError(t, err)
		require.NoError(t, netlink.LinkSetUp(link))
		addClsact(t, link)

		podIP := net.ParseIP("fd00::1")
		require.NoError(t, netlink.FilterAdd(ipv6VlanFilter(link.Attrs().Index, podIP, 100)))

		// fd00::1 IS in existIP → filter should be kept
		gcTCFilters([]netlink.Link{link}, sets.New[string]("fd00::1"))

		filtersAfter, err := netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
		require.NoError(t, err)
		assert.NotEmpty(t, filtersAfter, "live IPv6 VLAN filter should be kept")
		return nil
	})
	require.NoError(t, err)
}

// TestGcTCFilters_DualStack_SelectiveGC verifies that with both an IPv4 filter
// (priority 50001) and an IPv6 filter (priority 50002), only the stale one is removed.
func TestGcTCFilters_DualStack_SelectiveGC(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		dummy := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "gc-dual"}}
		require.NoError(t, netlink.LinkAdd(dummy))
		link, err := netlink.LinkByName("gc-dual")
		require.NoError(t, err)
		require.NoError(t, netlink.LinkSetUp(link))
		addClsact(t, link)

		// IPv4 filter for 192.168.1.2 (alive) at priority 50001
		vlanActV4 := netlink.NewVlanKeyAction()
		vlanActV4.Attrs().Action = netlink.TC_ACT_PIPE
		vlanActV4.Action = netlink.TCA_VLAN_KEY_PUSH
		vlanActV4.Vid = 100
		v4Filter := &netlink.U32{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: link.Attrs().Index,
				Parent:    netlink.HANDLE_MIN_EGRESS,
				Priority:  50001,
				Protocol:  uint16(unix.ETH_P_IP),
			},
			Sel: &netlink.TcU32Sel{
				Nkeys: 1,
				Flags: netlink.TC_U32_TERMINAL,
				Keys:  []netlink.TcU32Key{{Mask: 0xffffffff, Val: 0xc0a80102, Off: 12}},
			},
			Actions: []netlink.Action{vlanActV4},
		}
		require.NoError(t, netlink.FilterAdd(v4Filter))

		// IPv6 filter for fd00::1 (stale) at priority 50002
		require.NoError(t, netlink.FilterAdd(ipv6VlanFilter(link.Attrs().Index, net.ParseIP("fd00::1"), 100)))

		// existIP: IPv4 192.168.1.2 alive, IPv6 fd00::1 absent
		existIP := sets.New[string]("192.168.1.2")
		gcTCFilters([]netlink.Link{link}, existIP)

		filters, err := netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
		require.NoError(t, err)

		var v4Alive, v6Alive bool
		for _, f := range filters {
			u32, ok := f.(*netlink.U32)
			if !ok {
				continue
			}
			switch u32.Attrs().Priority {
			case 50001:
				v4Alive = true
			case 50002:
				v6Alive = true
			}
		}
		assert.True(t, v4Alive, "IPv4 filter for live IP should be kept")
		assert.False(t, v6Alive, "IPv6 filter for stale IP should be deleted")
		return nil
	})
	require.NoError(t, err)
}

// legacyIPv6VlanFilter builds an old-style IPv6 VLAN filter that uses
// ETH_P_IP and priority 50001, as older builds created.
func legacyIPv6VlanFilter(linkIndex int, ip net.IP, vid uint16) *netlink.U32 {
	ip = ip.To16()
	vlanAct := netlink.NewVlanKeyAction()
	vlanAct.Attrs().Action = netlink.TC_ACT_PIPE
	vlanAct.Action = netlink.TCA_VLAN_KEY_PUSH
	vlanAct.Vid = vid

	keys := make([]netlink.TcU32Key, 4)
	for i := 0; i < 4; i++ {
		keys[i] = netlink.TcU32Key{
			Mask: 0xffffffff,
			Val:  binary.BigEndian.Uint32(ip[i*4 : i*4+4]),
			Off:  int32(8 + i*4),
		}
	}
	return &netlink.U32{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: linkIndex,
			Parent:    netlink.HANDLE_MIN_EGRESS,
			Priority:  50001,
			Protocol:  uint16(unix.ETH_P_IP),
		},
		Sel: &netlink.TcU32Sel{
			Nkeys: 4,
			Flags: netlink.TC_U32_TERMINAL,
			Keys:  keys,
		},
		Actions: []netlink.Action{vlanAct},
	}
}

// TestGcTCFilters_LegacyIPv6_GCsStaleFilter verifies that legacy IPv6 VLAN
// filters (priority 50001, ETH_P_IP, 4 keys) are GC'd when the IP is stale.
func TestGcTCFilters_LegacyIPv6_GCsStaleFilter(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		dummy := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "gc-legacy-v6"}}
		require.NoError(t, netlink.LinkAdd(dummy))
		link, err := netlink.LinkByName("gc-legacy-v6")
		require.NoError(t, err)
		require.NoError(t, netlink.LinkSetUp(link))
		addClsact(t, link)

		podIP := net.ParseIP("fd00::1")
		require.NoError(t, netlink.FilterAdd(legacyIPv6VlanFilter(link.Attrs().Index, podIP, 100)))

		filtersBefore, err := netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
		require.NoError(t, err)
		assert.NotEmpty(t, filtersBefore)

		// fd00::1 NOT in existIP → legacy filter should be GC'd
		gcTCFilters([]netlink.Link{link}, sets.New[string]("fd00::2"))

		filtersAfter, err := netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
		require.NoError(t, err)
		assert.Empty(t, filtersAfter, "stale legacy IPv6 VLAN filter should be deleted")
		return nil
	})
	require.NoError(t, err)
}

// TestGcTCFilters_LegacyIPv6_KeepsLiveFilter verifies that legacy IPv6 VLAN
// filters are retained when the IP is still alive.
func TestGcTCFilters_LegacyIPv6_KeepsLiveFilter(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		dummy := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "gc-legacy-v6-k"}}
		require.NoError(t, netlink.LinkAdd(dummy))
		link, err := netlink.LinkByName("gc-legacy-v6-k")
		require.NoError(t, err)
		require.NoError(t, netlink.LinkSetUp(link))
		addClsact(t, link)

		podIP := net.ParseIP("fd00::1")
		require.NoError(t, netlink.FilterAdd(legacyIPv6VlanFilter(link.Attrs().Index, podIP, 100)))

		// fd00::1 IS in existIP → legacy filter should be kept
		gcTCFilters([]netlink.Link{link}, sets.New[string]("fd00::1"))

		filtersAfter, err := netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
		require.NoError(t, err)
		assert.NotEmpty(t, filtersAfter, "live legacy IPv6 VLAN filter should be kept")
		return nil
	})
	require.NoError(t, err)
}
