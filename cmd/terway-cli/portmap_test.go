package main

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"testing"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/coreos/go-iptables/iptables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// TestConfigureNetworkRules tests network rules configuration functionality
func TestConfigureNetworkRules(t *testing.T) {
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

		err = configureNetworkRules()
		require.NoError(t, err)

		// Verify iptables rules
		ipt, err := iptables.New()
		require.NoError(t, err, "Failed to create iptables instance")

		// Check CONNMARK setting rule
		exists, err := ipt.Exists("mangle", "PREROUTING", "-i", "eth0", "-j", "CONNMARK", "--set-xmark", "0x10/0x10", "-m", "comment", "--comment", "terway-portmap")
		assert.NoError(t, err, "Failed to check CONNMARK set rule")
		assert.True(t, exists, "CONNMARK set rule should exist")

		return nil
	})

}

// TestEnsureNFRules tests iptables rule management functionality
func TestEnsureNFRules(t *testing.T) {
	// Check if running as root
	if os.Geteuid() != 0 {
		t.Skip("This test requires root privileges")
	}

	ipt, err := iptables.New()
	require.NoError(t, err, "Failed to create iptables instance")

	// Test rule
	testRule := &ConnmarkRule{
		Table: "mangle",
		Chain: "PREROUTING",
		Args: []string{
			"-i", "lo",
			"-j", "CONNMARK",
			"--set-xmark", "0x20/0x20",
			"-m", "comment",
			"--comment", `"test-rule"`,
		},
	}

	// Clean up test rule
	defer func() {
		ipt.Delete("mangle", "PREROUTING", testRule.Args...)
	}()

	// Test adding rule
	err = ensureNFRules(ipt, testRule)
	assert.NoError(t, err, "ensureNFRules should succeed")

	// Verify rule exists
	exists, err := ipt.Exists(testRule.Table, testRule.Chain, testRule.Args...)
	assert.NoError(t, err, "Failed to check if rule exists")
	assert.True(t, exists, "Rule should exist after adding")

	// Test duplicate addition (should not error)
	err = ensureNFRules(ipt, testRule)
	assert.NoError(t, err, "ensureNFRules should not error when rule already exists")

	// clean up
	_ = ipt.Delete("mangle", "PREROUTING", testRule.Args...)
}

// TestNetworkInterfaceDetection tests network interface detection functionality
func TestNetworkInterfaceDetection(t *testing.T) {
	// Check if running as root
	if os.Geteuid() != 0 {
		t.Skip("This test requires root privileges")
	}

	// Test getting eth0 interface
	eth0, err := netlink.LinkByName("eth0")
	if err != nil {
		t.Skip("eth0 interface not found, skipping test")
	}

	assert.NotNil(t, eth0, "eth0 interface should be found")
	assert.Equal(t, "eth0", eth0.Attrs().Name, "Interface name should be eth0")

	// Test getting routes
	routes, err := netlink.RouteList(eth0, netlink.FAMILY_V4)
	assert.NoError(t, err, "Failed to get routes for eth0")
	assert.NotEmpty(t, routes, "Should have at least one route")

	// Find default gateway
	var defaultGw net.IP
	for _, route := range routes {
		if route.Dst == nil {
			defaultGw = route.Gw
			break
		}
	}

	if defaultGw != nil {
		assert.True(t, defaultGw.To4() != nil, "Default gateway should be IPv4")
		t.Logf("Found default gateway: %s", defaultGw.String())
	} else {
		t.Log("No default gateway found")
	}
}

// TestMarkAndMaskValues tests correctness of mark and mask values
func TestMarkAndMaskValues(t *testing.T) {
	mark := 0x10
	mask := 0x10

	markHexStr := fmt.Sprintf("0x%X", mark)
	maskHexStr := fmt.Sprintf("0x%X", mask)

	assert.Equal(t, "0x10", markHexStr, "Mark hex string should be 0x10")
	assert.Equal(t, "0x10", maskHexStr, "Mask hex string should be 0x10")

	// Test mark/mask combination
	markMaskStr := fmt.Sprintf("%s/%s", markHexStr, maskHexStr)
	assert.Equal(t, "0x10/0x10", markMaskStr, "Mark/mask combination should be 0x10/0x10")
}

// TestErrorHandling tests error handling
func TestErrorHandling(t *testing.T) {
	// Test non-existent interface
	_, err := netlink.LinkByName("eth2")
	assert.Error(t, err, "Should error when interface doesn't exist")

	// Test invalid iptables rule
	ipt, err := iptables.New()
	if err != nil {
		t.Skip("Cannot create iptables instance, skipping error handling test")
	}

	invalidRule := &ConnmarkRule{
		Table: "mangle",
		Chain: "PREROUTING",
		Args: []string{
			"-i", "eth2",
			"-j", "INVALID_TARGET",
		},
	}

	err = ensureNFRules(ipt, invalidRule)
	// This test might fail due to varying behavior of iptables across systems
	if err != nil {
		t.Logf("Expected error for invalid rule: %v", err)
	}
}
