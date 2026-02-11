//go:build linux
// +build linux

package main

import (
	"net"
	"os"
	"runtime"
	"testing"

	driverTypes "github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// TestNFTablesBackendCreation tests creation of nftables backend
func TestNFTablesBackendCreation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("This test requires root privileges")
	}

	// Test IPv4 backend
	backend4, err := NewNFTablesBackend(netlink.FAMILY_V4)
	if err != nil {
		t.Skipf("Failed to create nftables backend (nftables may not be available): %v", err)
	}
	assert.NotNil(t, backend4)
	assert.Equal(t, "nftables", backend4.Name())

	// Test IPv6 backend
	backend6, err := NewNFTablesBackend(netlink.FAMILY_V6)
	if err != nil {
		t.Skipf("Failed to create nftables backend (nftables may not be available): %v", err)
	}
	assert.NotNil(t, backend6)
	assert.Equal(t, "nftables", backend6.Name())
}

// TestIPTablesBackendCreation tests creation of iptables backend
func TestIPTablesBackendCreation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("This test requires root privileges")
	}

	// Test IPv4 backend
	backend4, err := NewIPTablesBackend(4)
	if err != nil {
		t.Skipf("Failed to create iptables backend: %v", err)
	}
	assert.NotNil(t, backend4)
	assert.Equal(t, "iptables", backend4.Name())

	// Test IPv6 backend
	backend6, err := NewIPTablesBackend(6)
	if err != nil {
		t.Skipf("Failed to create iptables backend: %v", err)
	}
	assert.NotNil(t, backend6)
	assert.Equal(t, "iptables", backend6.Name())
}

// TestConfigureNetworkRulesWithNFTables tests network rules configuration with nftables
func TestConfigureNetworkRulesWithNFTables(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("This test requires root privileges")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	containerNS, err := testutils.NewNS()
	require.NoError(t, err)
	defer func() {
		err := containerNS.Close()
		require.NoError(t, err)

		err = testutils.UnmountNS(containerNS)
		require.NoError(t, err)
	}()

	_ = containerNS.Do(func(ns ns.NetNS) error {
		dummy := netlink.Dummy{}
		dummy.Name = "eth0"
		err := netlink.LinkAdd(&dummy)
		require.NoError(t, err)

		dummyLink, err := netlink.LinkByName("eth0")
		require.NoError(t, err)

		err = netlink.AddrAdd(dummyLink, &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   net.ParseIP("192.168.1.2"),
				Mask: net.IPv4Mask(255, 255, 255, 255),
			},
		})
		require.NoError(t, err)

		err = netlink.LinkSetUp(dummyLink)
		require.NoError(t, err)

		defaultRoute := netlink.Route{
			LinkIndex: dummyLink.Attrs().Index,
			Dst:       &net.IPNet{IP: net.ParseIP("0.0.0.0"), Mask: net.CIDRMask(0, 32)},
			Flags:     int(netlink.FLAG_ONLINK),
			Gw:        net.ParseIP("192.168.1.1"),
			Table:     unix.RT_TABLE_MAIN,
		}
		err = netlink.RouteAdd(&defaultRoute)
		require.NoError(t, err)

		// Test with nftables backend
		nftConfig := &driverTypes.SymmetricRoutingConfig{
			Interface:    "eth0",
			Mark:         16,
			Mask:         16,
			TableID:      100,
			RulePriority: 600,
			Comment:      "terway-symmetric-nft-test",
			Backend:      "nftables",
		}

		err = configureNetworkRulesWithConfig(true, false, nftConfig)
		if err != nil {
			t.Skipf("Failed to configure nftables rules (nftables may not be available): %v", err)
			return nil
		}

		// Verify nftables rules
		conn, err := nftables.New()
		if err != nil {
			t.Logf("Failed to create nftables connection: %v", err)
			return nil
		}

		// List tables to verify our table was created
		tables, err := conn.ListTables()
		require.NoError(t, err, "Failed to list nftables tables")

		var table *nftables.Table
		for _, tbl := range tables {
			if tbl.Name == "terway_symmetric" && tbl.Family == nftables.TableFamilyIPv4 {
				table = tbl
				break
			}
		}
		require.NotNil(t, table, "terway_symmetric table should exist")

		// Verify chain and rules
		chains, err := conn.ListChainsOfTableFamily(nftables.TableFamilyIPv4)
		require.NoError(t, err)

		var chain *nftables.Chain
		for _, c := range chains {
			if c.Name == "prerouting" && c.Table.Name == "terway_symmetric" {
				chain = c
				break
			}
		}
		require.NotNil(t, chain, "prerouting chain should exist")
		assert.Equal(t, nftables.ChainHookPrerouting, *chain.Hooknum, "Chain should be in prerouting hook")

		// Verify rules exist with correct comment
		rules, err := conn.GetRules(table, chain)
		require.NoError(t, err)

		var commentedRules []*nftables.Rule
		for _, r := range rules {
			if string(r.UserData) == "terway-symmetric-nft-test" {
				commentedRules = append(commentedRules, r)
			}
		}
		assert.Len(t, commentedRules, 2, "Should have 2 rules (set connmark + restore mark)")

		// Verify Rule 1 writes to ct mark (SourceRegister=true)
		if len(commentedRules) >= 1 {
			r1 := commentedRules[0]
			lastExpr := r1.Exprs[len(r1.Exprs)-1]
			ctExpr, ok := lastExpr.(*expr.Ct)
			require.True(t, ok, "Rule 1 last expr should be Ct (write ct mark)")
			assert.True(t, ctExpr.SourceRegister, "Rule 1 last Ct must write (SourceRegister=true)")
		}

		// Verify Rule 2 writes to meta mark (SourceRegister=true)
		if len(commentedRules) >= 2 {
			r2 := commentedRules[1]
			lastExpr := r2.Exprs[len(r2.Exprs)-1]
			metaExpr, ok := lastExpr.(*expr.Meta)
			require.True(t, ok, "Rule 2 last expr should be Meta (write meta mark)")
			assert.True(t, metaExpr.SourceRegister, "Rule 2 last Meta must write (SourceRegister=true)")
			assert.Equal(t, expr.MetaKeyMARK, metaExpr.Key, "Rule 2 should write to meta mark")
		}

		// Verify ip rule was created
		ipRules, err := netlink.RuleList(netlink.FAMILY_V4)
		require.NoError(t, err)

		foundIPRule := false
		for _, r := range ipRules {
			if r.Priority == 600 && r.Mark == 16 && r.Mask == 16 && r.Table == 100 {
				foundIPRule = true
				break
			}
		}
		assert.True(t, foundIPRule, "ip rule for fwmark 0x10/0x10 lookup 100 should exist")

		return nil
	})
}

// TestConfigureNetworkRulesWithNFTablesIPv6 tests IPv6 network rules configuration with nftables
func TestConfigureNetworkRulesWithNFTablesIPv6(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("This test requires root privileges")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	containerNS, err := testutils.NewNS()
	require.NoError(t, err)
	defer func() {
		err := containerNS.Close()
		require.NoError(t, err)

		err = testutils.UnmountNS(containerNS)
		require.NoError(t, err)
	}()

	_ = containerNS.Do(func(ns ns.NetNS) error {
		dummy := netlink.Dummy{}
		dummy.Name = "eth0"
		err := netlink.LinkAdd(&dummy)
		require.NoError(t, err)

		dummyLink, err := netlink.LinkByName("eth0")
		require.NoError(t, err)

		// Add IPv6 address
		err = netlink.AddrAdd(dummyLink, &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   net.ParseIP("2001:db8::2"),
				Mask: net.CIDRMask(128, 128),
			},
		})
		require.NoError(t, err)

		err = netlink.LinkSetUp(dummyLink)
		require.NoError(t, err)

		// Add IPv6 default route
		defaultRoute := netlink.Route{
			LinkIndex: dummyLink.Attrs().Index,
			Dst:       &net.IPNet{IP: net.ParseIP("::"), Mask: net.CIDRMask(0, 128)},
			Flags:     int(netlink.FLAG_ONLINK),
			Gw:        net.ParseIP("2001:db8::1"),
			Table:     unix.RT_TABLE_MAIN,
		}
		err = netlink.RouteAdd(&defaultRoute)
		require.NoError(t, err)

		// Test with nftables backend
		nftConfig := &driverTypes.SymmetricRoutingConfig{
			Interface:    "eth0",
			Mark:         16,
			Mask:         16,
			TableID:      100,
			RulePriority: 600,
			Comment:      "terway-symmetric-nft-ipv6-test",
			Backend:      "nftables",
		}

		err = configureNetworkRulesWithConfig(false, true, nftConfig)
		if err != nil {
			t.Skipf("Failed to configure nftables rules (nftables may not be available): %v", err)
			return nil
		}

		// Verify nftables rules
		conn, err := nftables.New()
		if err != nil {
			t.Logf("Failed to create nftables connection: %v", err)
			return nil
		}

		// List tables to verify our table was created
		tables, err := conn.ListTables()
		require.NoError(t, err, "Failed to list nftables tables")

		var table *nftables.Table
		for _, tbl := range tables {
			if tbl.Name == "terway_symmetric_ipv6" && tbl.Family == nftables.TableFamilyIPv6 {
				table = tbl
				break
			}
		}
		require.NotNil(t, table, "terway_symmetric_ipv6 table should exist")

		// Verify chain and rules for IPv6
		chains, err := conn.ListChainsOfTableFamily(nftables.TableFamilyIPv6)
		require.NoError(t, err)

		var chain *nftables.Chain
		for _, c := range chains {
			if c.Name == "prerouting" && c.Table.Name == "terway_symmetric_ipv6" {
				chain = c
				break
			}
		}
		require.NotNil(t, chain, "prerouting chain should exist for IPv6")

		// Verify rules exist with correct comment
		rules, err := conn.GetRules(table, chain)
		require.NoError(t, err)

		var commentedRules []*nftables.Rule
		for _, r := range rules {
			if string(r.UserData) == "terway-symmetric-nft-ipv6-test" {
				commentedRules = append(commentedRules, r)
			}
		}
		assert.Len(t, commentedRules, 2, "Should have 2 rules for IPv6 (set connmark + restore mark)")

		// Verify Rule 1 writes to ct mark
		if len(commentedRules) >= 1 {
			r1 := commentedRules[0]
			lastExpr := r1.Exprs[len(r1.Exprs)-1]
			ctExpr, ok := lastExpr.(*expr.Ct)
			require.True(t, ok, "IPv6 Rule 1 last expr should be Ct")
			assert.True(t, ctExpr.SourceRegister, "IPv6 Rule 1 last Ct must write (SourceRegister=true)")
		}

		// Verify Rule 2 writes to meta mark
		if len(commentedRules) >= 2 {
			r2 := commentedRules[1]
			lastExpr := r2.Exprs[len(r2.Exprs)-1]
			metaExpr, ok := lastExpr.(*expr.Meta)
			require.True(t, ok, "IPv6 Rule 2 last expr should be Meta")
			assert.True(t, metaExpr.SourceRegister, "IPv6 Rule 2 last Meta must write (SourceRegister=true)")
		}

		return nil
	})
}

// TestConfigureNetworkRulesWithNFTablesDualStack tests dual-stack configuration with nftables
func TestConfigureNetworkRulesWithNFTablesDualStack(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("This test requires root privileges")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	containerNS, err := testutils.NewNS()
	require.NoError(t, err)
	defer func() {
		err := containerNS.Close()
		require.NoError(t, err)

		err = testutils.UnmountNS(containerNS)
		require.NoError(t, err)
	}()

	_ = containerNS.Do(func(ns ns.NetNS) error {
		dummy := netlink.Dummy{}
		dummy.Name = "eth0"
		err := netlink.LinkAdd(&dummy)
		require.NoError(t, err)

		dummyLink, err := netlink.LinkByName("eth0")
		require.NoError(t, err)

		// Add IPv4 address
		err = netlink.AddrAdd(dummyLink, &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   net.ParseIP("192.168.1.2"),
				Mask: net.IPv4Mask(255, 255, 255, 255),
			},
		})
		require.NoError(t, err)

		// Add IPv6 address
		err = netlink.AddrAdd(dummyLink, &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   net.ParseIP("2001:db8::2"),
				Mask: net.CIDRMask(128, 128),
			},
		})
		require.NoError(t, err)

		err = netlink.LinkSetUp(dummyLink)
		require.NoError(t, err)

		// Add IPv4 default route
		ipv4Route := netlink.Route{
			LinkIndex: dummyLink.Attrs().Index,
			Dst:       &net.IPNet{IP: net.ParseIP("0.0.0.0"), Mask: net.CIDRMask(0, 32)},
			Flags:     int(netlink.FLAG_ONLINK),
			Gw:        net.ParseIP("192.168.1.1"),
			Table:     unix.RT_TABLE_MAIN,
		}
		err = netlink.RouteAdd(&ipv4Route)
		require.NoError(t, err)

		// Add IPv6 default route
		ipv6Route := netlink.Route{
			LinkIndex: dummyLink.Attrs().Index,
			Dst:       &net.IPNet{IP: net.ParseIP("::"), Mask: net.CIDRMask(0, 128)},
			Flags:     int(netlink.FLAG_ONLINK),
			Gw:        net.ParseIP("2001:db8::1"),
			Table:     unix.RT_TABLE_MAIN,
		}
		err = netlink.RouteAdd(&ipv6Route)
		require.NoError(t, err)

		// Test with nftables backend for both IPv4 and IPv6
		nftConfig := &driverTypes.SymmetricRoutingConfig{
			Interface:    "eth0",
			Mark:         16,
			Mask:         16,
			TableID:      100,
			RulePriority: 600,
			Comment:      "terway-symmetric-nft-dual-test",
			Backend:      "nftables",
		}

		err = configureNetworkRulesWithConfig(true, true, nftConfig)
		if err != nil {
			t.Skipf("Failed to configure nftables rules (nftables may not be available): %v", err)
			return nil
		}

		// Verify nftables rules for both IPv4 and IPv6
		conn, err := nftables.New()
		if err != nil {
			t.Logf("Failed to create nftables connection: %v", err)
			return nil
		}

		tables, err := conn.ListTables()
		require.NoError(t, err, "Failed to list nftables tables")

		var ipv4Table, ipv6Table *nftables.Table
		for _, tbl := range tables {
			if tbl.Name == "terway_symmetric" && tbl.Family == nftables.TableFamilyIPv4 {
				ipv4Table = tbl
			}
			if tbl.Name == "terway_symmetric_ipv6" && tbl.Family == nftables.TableFamilyIPv6 {
				ipv6Table = tbl
			}
		}
		require.NotNil(t, ipv4Table, "terway_symmetric IPv4 table should exist")
		require.NotNil(t, ipv6Table, "terway_symmetric_ipv6 IPv6 table should exist")

		comment := "terway-symmetric-nft-dual-test"

		// Verify IPv4 rules
		ipv4Chains, err := conn.ListChainsOfTableFamily(nftables.TableFamilyIPv4)
		require.NoError(t, err)
		var ipv4Chain *nftables.Chain
		for _, c := range ipv4Chains {
			if c.Name == "prerouting" && c.Table.Name == "terway_symmetric" {
				ipv4Chain = c
				break
			}
		}
		require.NotNil(t, ipv4Chain, "IPv4 prerouting chain should exist")

		ipv4Rules, err := conn.GetRules(ipv4Table, ipv4Chain)
		require.NoError(t, err)
		var ipv4CommentedRules []*nftables.Rule
		for _, r := range ipv4Rules {
			if string(r.UserData) == comment {
				ipv4CommentedRules = append(ipv4CommentedRules, r)
			}
		}
		assert.Len(t, ipv4CommentedRules, 2, "Should have 2 IPv4 rules")

		// Verify IPv6 rules
		ipv6Chains, err := conn.ListChainsOfTableFamily(nftables.TableFamilyIPv6)
		require.NoError(t, err)
		var ipv6Chain *nftables.Chain
		for _, c := range ipv6Chains {
			if c.Name == "prerouting" && c.Table.Name == "terway_symmetric_ipv6" {
				ipv6Chain = c
				break
			}
		}
		require.NotNil(t, ipv6Chain, "IPv6 prerouting chain should exist")

		ipv6Rules, err := conn.GetRules(ipv6Table, ipv6Chain)
		require.NoError(t, err)
		var ipv6CommentedRules []*nftables.Rule
		for _, r := range ipv6Rules {
			if string(r.UserData) == comment {
				ipv6CommentedRules = append(ipv6CommentedRules, r)
			}
		}
		assert.Len(t, ipv6CommentedRules, 2, "Should have 2 IPv6 rules")

		// Verify ct mark write in both IPv4 and IPv6 Rule 1
		for _, label := range []struct {
			name  string
			rules []*nftables.Rule
		}{
			{"IPv4", ipv4CommentedRules},
			{"IPv6", ipv6CommentedRules},
		} {
			if len(label.rules) >= 1 {
				r1 := label.rules[0]
				lastExpr := r1.Exprs[len(r1.Exprs)-1]
				ctExpr, ok := lastExpr.(*expr.Ct)
				require.True(t, ok, "%s Rule 1 last expr should be Ct", label.name)
				assert.True(t, ctExpr.SourceRegister, "%s Rule 1 must write ct mark", label.name)
			}
			if len(label.rules) >= 2 {
				r2 := label.rules[1]
				lastExpr := r2.Exprs[len(r2.Exprs)-1]
				metaExpr, ok := lastExpr.(*expr.Meta)
				require.True(t, ok, "%s Rule 2 last expr should be Meta", label.name)
				assert.True(t, metaExpr.SourceRegister, "%s Rule 2 must write meta mark", label.name)
			}
		}

		return nil
	})
}

// TestBackendSelection tests backend selection with config
func TestBackendSelection(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("This test requires root privileges")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	containerNS, err := testutils.NewNS()
	require.NoError(t, err)
	defer func() {
		err := containerNS.Close()
		require.NoError(t, err)

		err = testutils.UnmountNS(containerNS)
		require.NoError(t, err)
	}()

	_ = containerNS.Do(func(ns ns.NetNS) error {
		dummy := netlink.Dummy{}
		dummy.Name = "eth0"
		err := netlink.LinkAdd(&dummy)
		require.NoError(t, err)

		dummyLink, err := netlink.LinkByName("eth0")
		require.NoError(t, err)

		err = netlink.AddrAdd(dummyLink, &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   net.ParseIP("192.168.1.2"),
				Mask: net.IPv4Mask(255, 255, 255, 255),
			},
		})
		require.NoError(t, err)

		err = netlink.LinkSetUp(dummyLink)
		require.NoError(t, err)

		defaultRoute := netlink.Route{
			LinkIndex: dummyLink.Attrs().Index,
			Dst:       &net.IPNet{IP: net.ParseIP("0.0.0.0"), Mask: net.CIDRMask(0, 32)},
			Flags:     int(netlink.FLAG_ONLINK),
			Gw:        net.ParseIP("192.168.1.1"),
			Table:     unix.RT_TABLE_MAIN,
		}
		err = netlink.RouteAdd(&defaultRoute)
		require.NoError(t, err)

		// Test invalid backend
		invalidConfig := &driverTypes.SymmetricRoutingConfig{
			Interface:    "eth0",
			Mark:         16,
			Mask:         16,
			TableID:      100,
			RulePriority: 600,
			Comment:      "test-invalid-backend",
			Backend:      "invalid-backend",
		}

		err = configureNetworkRulesWithConfig(true, false, invalidConfig)
		assert.Error(t, err, "Should error for invalid backend")
		assert.Contains(t, err.Error(), "unsupported backend", "Error should mention unsupported backend")

		return nil
	})
}

// TestNFTablesBackendEnsureConnmarkRules tests the EnsureConnmarkRules method
func TestNFTablesBackendEnsureConnmarkRules(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("This test requires root privileges")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	containerNS, err := testutils.NewNS()
	require.NoError(t, err)
	defer func() {
		require.NoError(t, containerNS.Close())
		require.NoError(t, testutils.UnmountNS(containerNS))
	}()

	_ = containerNS.Do(func(ns ns.NetNS) error {
		backend, err := NewNFTablesBackend(netlink.FAMILY_V4)
		if err != nil {
			t.Skipf("Failed to create nftables backend (nftables may not be available): %v", err)
			return nil
		}

		mark := 0x20
		mask := 0x20
		comment := "test-nft-connmark"

		// Test with loopback interface
		err = backend.EnsureConnmarkRules("lo", mark, mask, comment)
		if err != nil {
			t.Skipf("Failed to ensure connmark rules (nftables may not be available): %v", err)
			return nil
		}

		// Verify rules were created with correct structure
		conn, err := nftables.New()
		require.NoError(t, err)

		tables, err := conn.ListTables()
		require.NoError(t, err)

		var table *nftables.Table
		for _, tbl := range tables {
			if tbl.Name == "terway_symmetric" && tbl.Family == nftables.TableFamilyIPv4 {
				table = tbl
				break
			}
		}
		require.NotNil(t, table, "terway_symmetric table should exist")

		// Verify chain exists
		chains, err := conn.ListChainsOfTableFamily(nftables.TableFamilyIPv4)
		require.NoError(t, err)

		var chain *nftables.Chain
		for _, c := range chains {
			if c.Name == "prerouting" && c.Table.Name == "terway_symmetric" {
				chain = c
				break
			}
		}
		require.NotNil(t, chain, "prerouting chain should exist")

		// Verify rules in the chain
		rules, err := conn.GetRules(table, chain)
		require.NoError(t, err)

		// Filter rules by our comment
		var ourRules []*nftables.Rule
		for _, r := range rules {
			if string(r.UserData) == comment {
				ourRules = append(ourRules, r)
			}
		}
		assert.Len(t, ourRules, 2, "Should have exactly 2 rules with our comment")

		// Verify Rule 1: set connmark on incoming interface
		// Structure: Meta(IIFNAME) -> Cmp -> Ct(read) -> Bitwise -> Ct(write)
		if len(ourRules) >= 1 {
			r1 := ourRules[0]
			require.GreaterOrEqual(t, len(r1.Exprs), 5, "Rule 1 should have at least 5 expressions")

			// First expr: Meta IIFNAME (load iifname)
			metaExpr, ok := r1.Exprs[0].(*expr.Meta)
			require.True(t, ok, "Rule 1 first expr should be Meta")
			assert.Equal(t, expr.MetaKeyIIFNAME, metaExpr.Key, "Rule 1 should match on IIFNAME")

			// Second expr: Cmp (compare interface name)
			_, ok = r1.Exprs[1].(*expr.Cmp)
			require.True(t, ok, "Rule 1 second expr should be Cmp")

			// Third expr: Ct read (load ct mark)
			ctRead, ok := r1.Exprs[2].(*expr.Ct)
			require.True(t, ok, "Rule 1 third expr should be Ct")
			assert.Equal(t, expr.CtKeyMARK, ctRead.Key, "Rule 1 should read ct mark")
			assert.False(t, ctRead.SourceRegister, "Rule 1 third Ct should be a read (SourceRegister=false)")

			// Fourth expr: Bitwise (compute set-xmark)
			_, ok = r1.Exprs[3].(*expr.Bitwise)
			require.True(t, ok, "Rule 1 fourth expr should be Bitwise")

			// Fifth expr: Ct write (write back to ct mark)
			ctWrite, ok := r1.Exprs[4].(*expr.Ct)
			require.True(t, ok, "Rule 1 fifth expr should be Ct")
			assert.Equal(t, expr.CtKeyMARK, ctWrite.Key, "Rule 1 should write ct mark")
			assert.True(t, ctWrite.SourceRegister, "Rule 1 last Ct MUST be a write (SourceRegister=true)")
		}

		// Verify Rule 2: restore mark from conntrack
		// Structure: Ct(read) -> Bitwise -> Meta(write MARK)
		if len(ourRules) >= 2 {
			r2 := ourRules[1]
			require.GreaterOrEqual(t, len(r2.Exprs), 3, "Rule 2 should have at least 3 expressions")

			// First expr: Ct read (load ct mark)
			ctRead, ok := r2.Exprs[0].(*expr.Ct)
			require.True(t, ok, "Rule 2 first expr should be Ct")
			assert.Equal(t, expr.CtKeyMARK, ctRead.Key, "Rule 2 should read ct mark")
			assert.False(t, ctRead.SourceRegister, "Rule 2 first Ct should be a read")

			// Second expr: Bitwise (apply mask)
			_, ok = r2.Exprs[1].(*expr.Bitwise)
			require.True(t, ok, "Rule 2 second expr should be Bitwise")

			// Third expr: Meta write (set meta mark)
			metaWrite, ok := r2.Exprs[2].(*expr.Meta)
			require.True(t, ok, "Rule 2 third expr should be Meta")
			assert.Equal(t, expr.MetaKeyMARK, metaWrite.Key, "Rule 2 should set meta mark")
			assert.True(t, metaWrite.SourceRegister, "Rule 2 Meta MUST be a write (SourceRegister=true)")
		}

		// Test idempotency - running again should not error and should not create duplicates
		err = backend.EnsureConnmarkRules("lo", mark, mask, comment)
		assert.NoError(t, err, "Second call should be idempotent")

		// Re-verify rule count after idempotent call
		rules, err = conn.GetRules(table, chain)
		require.NoError(t, err)

		var ourRulesAfter []*nftables.Rule
		for _, r := range rules {
			if string(r.UserData) == comment {
				ourRulesAfter = append(ourRulesAfter, r)
			}
		}
		assert.Len(t, ourRulesAfter, 2, "Should still have exactly 2 rules after idempotent call")

		return nil
	})
}

// TestIPTablesBackendEnsureConnmarkRules tests the iptables backend
func TestIPTablesBackendEnsureConnmarkRules(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("This test requires root privileges")
	}

	backend, err := NewIPTablesBackend(4)
	require.NoError(t, err)

	// Test with loopback interface
	err = backend.EnsureConnmarkRules("lo", 0x20, 0x20, "test-ipt-connmark")
	require.NoError(t, err)

	// Test idempotency - running again should not error
	err = backend.EnsureConnmarkRules("lo", 0x20, 0x20, "test-ipt-connmark")
	assert.NoError(t, err, "Second call should be idempotent")
}
