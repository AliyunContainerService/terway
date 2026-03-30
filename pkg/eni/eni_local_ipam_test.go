package eni

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/bits-and-blooms/bitset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

func TestCalculateIP(t *testing.T) {
	tests := []struct {
		name      string
		cidr      string
		offset    uint
		wantIP    string
		wantError bool
	}{
		{
			name:   "IPv4 CIDR 10.0.0.0/28 offset 0",
			cidr:   "10.0.0.0/28",
			offset: 0,
			wantIP: "10.0.0.0",
		},
		{
			name:   "IPv4 CIDR 10.0.0.0/28 offset 5",
			cidr:   "10.0.0.0/28",
			offset: 5,
			wantIP: "10.0.0.5",
		},
		{
			name:   "IPv4 CIDR 10.0.0.0/28 offset 15",
			cidr:   "10.0.0.0/28",
			offset: 15,
			wantIP: "10.0.0.15",
		},
		{
			name:   "IPv6 CIDR fd00::/112 offset 0",
			cidr:   "fd00::/112",
			offset: 0,
			wantIP: "fd00::",
		},
		{
			name:   "IPv6 CIDR fd00::/112 offset 256",
			cidr:   "fd00::/112",
			offset: 256,
			wantIP: "fd00::100",
		},
		{
			name:      "Invalid CIDR",
			cidr:      "invalid-cidr",
			offset:    0,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip, err := calculateIP(tt.cidr, tt.offset)
			if tt.wantError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantIP, ip.String())
		})
	}
}

func TestFindPrefixContainingIP(t *testing.T) {
	// Create a prefix map with some prefixes
	prefixMap := map[string]*PrefixInfo{
		"10.0.0.0/28": {
			Prefix:    "10.0.0.0/28",
			bitmap:    bitset.New(16),
			allocated: make(map[string]uint),
			status:    networkv1beta1.IPPrefixStatusValid,
		},
		"192.168.1.0/24": {
			Prefix:    "192.168.1.0/24",
			bitmap:    bitset.New(256),
			allocated: make(map[string]uint),
			status:    networkv1beta1.IPPrefixStatusValid,
		},
	}

	tests := []struct {
		name       string
		ip         string
		wantPrefix string
		wantOffset uint
		wantFound  bool
	}{
		{
			name:       "IP in first prefix",
			ip:         "10.0.0.5",
			wantPrefix: "10.0.0.0/28",
			wantOffset: 5,
			wantFound:  true,
		},
		{
			name:       "IP in second prefix",
			ip:         "192.168.1.100",
			wantPrefix: "192.168.1.0/24",
			wantOffset: 100,
			wantFound:  true,
		},
		{
			name:      "IP not in any prefix",
			ip:        "172.16.0.1",
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip, err := netip.ParseAddr(tt.ip)
			require.NoError(t, err)

			prefix, offset, found := findPrefixContainingIP(ip, prefixMap)
			if tt.wantFound {
				assert.True(t, found)
				assert.Equal(t, tt.wantPrefix, prefix)
				assert.Equal(t, tt.wantOffset, offset)
			} else {
				assert.False(t, found)
				assert.Empty(t, prefix)
				assert.Equal(t, uint(0), offset)
			}
		})
	}
}

func TestPrefixSize(t *testing.T) {
	tests := []struct {
		name     string
		cidr     string
		wantSize uint
	}{
		{
			name:     "/28 prefix",
			cidr:     "10.0.0.0/28",
			wantSize: 16,
		},
		{
			name:     "/24 prefix",
			cidr:     "10.0.0.0/24",
			wantSize: 256,
		},
		{
			name:     "/32 prefix",
			cidr:     "10.0.0.0/32",
			wantSize: 1,
		},
		{
			name:     "/112 IPv6 prefix",
			cidr:     "fd00::/112",
			wantSize: 65536,
		},
		{
			name:     "Invalid CIDR",
			cidr:     "invalid-cidr",
			wantSize: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			size := prefixSize(tt.cidr)
			assert.Equal(t, tt.wantSize, size)
		})
	}
}

func TestNewENILocalIPAMFromPrefix(t *testing.T) {
	// Create a Nic with IPv4 and IPv6 prefixes
	eni := &networkv1beta1.Nic{
		ID:         "eni-123456",
		MacAddress: "00:11:22:33:44:55",
		VSwitchID:  "vsw-123456",
		IPv4CIDR:   "10.0.0.0/24",
		IPv6CIDR:   "fd00::/112",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{
				Prefix: "10.0.0.0/28",
				Status: networkv1beta1.IPPrefixStatusValid,
			},
			{
				Prefix: "10.0.0.16/28",
				Status: networkv1beta1.IPPrefixStatusFrozen,
			},
			{
				Prefix: "10.0.0.32/28",
				Status: networkv1beta1.IPPrefixStatusInvalid, // Should be skipped
			},
		},
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{
				Prefix: "fd00::/120",
				Status: networkv1beta1.IPPrefixStatusValid,
			},
		},
	}

	ipam := NewENILocalIPAMFromPrefix("eni-123456", "00:11:22:33:44:55", eni, false)

	// Verify ENI info
	assert.Equal(t, "eni-123456", ipam.eniID)
	assert.Equal(t, "00:11:22:33:44:55", ipam.eniMAC)
	assert.Equal(t, "vsw-123456", ipam.vSwitchID)

	// Verify IPv4 prefix map - should only contain Valid and Frozen
	assert.Len(t, ipam.ipv4PrefixMap, 2)
	assert.Contains(t, ipam.ipv4PrefixMap, "10.0.0.0/28")
	assert.Contains(t, ipam.ipv4PrefixMap, "10.0.0.16/28")
	assert.NotContains(t, ipam.ipv4PrefixMap, "10.0.0.32/28")

	// Verify IPv4 prefix info
	v4PrefixInfo := ipam.ipv4PrefixMap["10.0.0.0/28"]
	assert.Equal(t, networkv1beta1.IPPrefixStatusValid, v4PrefixInfo.status)
	assert.Equal(t, uint(16), v4PrefixInfo.bitmap.Len())
	assert.Len(t, v4PrefixInfo.allocated, 0)

	// Verify IPv6 prefix map
	assert.Len(t, ipam.ipv6PrefixMap, 1)
	assert.Contains(t, ipam.ipv6PrefixMap, "fd00::/120")

	// Verify gateway IP is set
	assert.NotNil(t, ipam.gatewayIP.IPv4)
	assert.Equal(t, "10.0.0.253", ipam.gatewayIP.IPv4.String())
}

func TestENILocalIPAM_AllocateRelease_PrefixMode(t *testing.T) {
	// Create a Prefix mode IPAM with a small prefix
	eni := &networkv1beta1.Nic{
		ID:         "eni-123456",
		MacAddress: "00:11:22:33:44:55",
		VSwitchID:  "vsw-123456",
		IPv4CIDR:   "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{
				Prefix: "10.0.0.0/29", // 8 IPs
				Status: networkv1beta1.IPPrefixStatusValid,
			},
		},
	}

	ipam := NewENILocalIPAMFromPrefix("eni-123456", "00:11:22:33:44:55", eni, false)

	// Allocate first IPv4
	ip1, err := ipam.AllocateIPv4("pod-1")
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.0", ip1.String())

	// Allocate second IPv4
	ip2, err := ipam.AllocateIPv4("pod-2")
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1", ip2.String())
	assert.NotEqual(t, ip1, ip2)

	// Release first IPv4
	ipam.ReleaseIPv4("pod-1")

	// Allocate again - should reuse the released IP
	ip3, err := ipam.AllocateIPv4("pod-3")
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.0", ip3.String())

	// Allocate all remaining IPs (pod-2 at offset 1, pod-3 at offset 0, need 6 more for 8 total)
	for i := 4; i <= 9; i++ {
		_, err := ipam.AllocateIPv4(fmt.Sprintf("pod-%d", i))
		require.NoError(t, err)
	}

	// Try to allocate when full - should return error
	_, err = ipam.AllocateIPv4("pod-10")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no available IPv4 address")
}

func TestENILocalIPAM_AllocateSkipsFrozenPrefix(t *testing.T) {
	// Create IPAM with Valid and Frozen prefixes
	eni := &networkv1beta1.Nic{
		ID:         "eni-123456",
		MacAddress: "00:11:22:33:44:55",
		VSwitchID:  "vsw-123456",
		IPv4CIDR:   "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{
				Prefix: "10.0.0.0/29", // Frozen - should be skipped
				Status: networkv1beta1.IPPrefixStatusFrozen,
			},
			{
				Prefix: "10.0.0.8/29", // Valid - should be used
				Status: networkv1beta1.IPPrefixStatusValid,
			},
		},
	}

	ipam := NewENILocalIPAMFromPrefix("eni-123456", "00:11:22:33:44:55", eni, false)

	// Allocate IPv4 - should get IP from Valid prefix
	ip1, err := ipam.AllocateIPv4("pod-1")
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.8", ip1.String())

	// Verify it's in the Valid prefix
	prefix, ok := ipam.podToPrefixV4["pod-1"]
	assert.True(t, ok)
	assert.Equal(t, "10.0.0.8/29", prefix)

	// Allocate another IP
	ip2, err := ipam.AllocateIPv4("pod-2")
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.9", ip2.String())
}

func TestENILocalIPAM_RestorePod_PrefixMode(t *testing.T) {
	// Create Prefix mode IPAM
	eni := &networkv1beta1.Nic{
		ID:         "eni-123456",
		MacAddress: "00:11:22:33:44:55",
		VSwitchID:  "vsw-123456",
		IPv4CIDR:   "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{
				Prefix: "10.0.0.0/28",
				Status: networkv1beta1.IPPrefixStatusValid,
			},
		},
	}

	ipam := NewENILocalIPAMFromPrefix("eni-123456", "00:11:22:33:44:55", eni, false)

	// Restore a Pod with IPv4
	ipam.RestorePod("pod-1", "10.0.0.5", "", "eni-123456")

	// Verify bitmap is set correctly
	prefixInfo, ok := ipam.ipv4PrefixMap["10.0.0.0/28"]
	require.True(t, ok)
	assert.True(t, prefixInfo.bitmap.Test(5))

	// Verify podToPrefixV4 mapping
	prefix, ok := ipam.podToPrefixV4["pod-1"]
	assert.True(t, ok)
	assert.Equal(t, "10.0.0.0/28", prefix)

	// Verify allocated mapping
	offset, ok := prefixInfo.allocated["pod-1"]
	assert.True(t, ok)
	assert.Equal(t, uint(5), offset)
}

func TestENILocalIPAM_UpdatePrefixes(t *testing.T) {
	// Create initial IPAM
	eni := &networkv1beta1.Nic{
		ID:         "eni-123456",
		MacAddress: "00:11:22:33:44:55",
		VSwitchID:  "vsw-123456",
		IPv4CIDR:   "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{
				Prefix: "10.0.0.0/28",
				Status: networkv1beta1.IPPrefixStatusValid,
			},
			{
				Prefix: "10.0.0.16/28",
				Status: networkv1beta1.IPPrefixStatusValid,
			},
		},
	}

	ipam := NewENILocalIPAMFromPrefix("eni-123456", "00:11:22:33:44:55", eni, false)

	// Allocate an IP from first prefix
	ipam.AllocateIPv4("pod-1")

	// Update prefixes: add new, delete empty, update status
	newPrefixes := []networkv1beta1.IPPrefix{
		{
			Prefix: "10.0.0.0/28",
			Status: networkv1beta1.IPPrefixStatusFrozen, // Update status
		},
		// 10.0.0.16/28 is deleted (empty)
		{
			Prefix: "10.0.0.32/28", // New prefix
			Status: networkv1beta1.IPPrefixStatusValid,
		},
	}

	ipam.UpdatePrefixes(newPrefixes, false)

	// Verify first prefix status updated
	assert.Equal(t, networkv1beta1.IPPrefixStatusFrozen, ipam.ipv4PrefixMap["10.0.0.0/28"].status)

	// Verify second prefix deleted (was empty)
	assert.NotContains(t, ipam.ipv4PrefixMap, "10.0.0.16/28")

	// Verify new prefix added
	assert.Contains(t, ipam.ipv4PrefixMap, "10.0.0.32/28")
	assert.Equal(t, networkv1beta1.IPPrefixStatusValid, ipam.ipv4PrefixMap["10.0.0.32/28"].status)
}

func TestENILocalIPAM_DualStack(t *testing.T) {
	// Create dual stack IPAM
	eni := &networkv1beta1.Nic{
		ID:         "eni-123456",
		MacAddress: "00:11:22:33:44:55",
		VSwitchID:  "vsw-123456",
		IPv4CIDR:   "10.0.0.0/24",
		IPv6CIDR:   "fd00::/112",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{
				Prefix: "10.0.0.0/28",
				Status: networkv1beta1.IPPrefixStatusValid,
			},
		},
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{
				Prefix: "fd00::/120",
				Status: networkv1beta1.IPPrefixStatusValid,
			},
		},
	}

	ipam := NewENILocalIPAMFromPrefix("eni-123456", "00:11:22:33:44:55", eni, false)

	// Allocate IPv4
	ipv4, err := ipam.AllocateIPv4("pod-1")
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.0", ipv4.String())

	// Allocate IPv6
	ipv6, err := ipam.AllocateIPv6("pod-1")
	require.NoError(t, err)
	assert.Equal(t, "fd00::", ipv6.String())

	// Verify both are allocated
	assert.Contains(t, ipam.podToPrefixV4, "pod-1")
	assert.Contains(t, ipam.podToPrefixV6, "pod-1")

	// Release IPv4
	ipam.ReleaseIPv4("pod-1")
	assert.NotContains(t, ipam.podToPrefixV4, "pod-1")
	assert.Contains(t, ipam.podToPrefixV6, "pod-1") // IPv6 still allocated

	// Release IPv6
	ipam.ReleaseIPv6("pod-1")
	assert.NotContains(t, ipam.podToPrefixV6, "pod-1")
}

// ---------------------------------------------------------------------------
// calculateIP boundary tests
// ---------------------------------------------------------------------------

func TestCalculateIP_BasicIPv4(t *testing.T) {
	// /28 has 16 IPs: offset 0..15
	for offset := uint(0); offset <= 15; offset++ {
		ip, err := calculateIP("10.0.0.0/28", offset)
		require.NoError(t, err, "offset %d should not error", offset)
		expected, _ := netip.ParseAddr(fmt.Sprintf("10.0.0.%d", offset))
		assert.Equal(t, expected, ip, "offset %d mismatch", offset)
	}
}

func TestCalculateIP_BasicIPv6(t *testing.T) {
	// /120 has 256 IPs: offset 0..255
	ip0, err := calculateIP("fd00::/120", 0)
	require.NoError(t, err)
	assert.Equal(t, "fd00::", ip0.String())

	ip255, err := calculateIP("fd00::/120", 255)
	require.NoError(t, err)
	assert.Equal(t, "fd00::ff", ip255.String())
}

func TestCalculateIP_OffsetZero(t *testing.T) {
	ip, err := calculateIP("192.168.1.0/24", 0)
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.0", ip.String())
}

func TestCalculateIP_OffsetOverflow(t *testing.T) {
	// /28 has 16 IPs; offset 16 overflows into the next subnet but calculateIP
	// does not validate range — it just does arithmetic. Verify no panic/error.
	ip, err := calculateIP("10.0.0.0/28", 16)
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.16", ip.String())
}

func TestCalculateIP_InvalidCIDR(t *testing.T) {
	_, err := calculateIP("not-a-cidr", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse CIDR")
}

// ---------------------------------------------------------------------------
// Allocate boundary tests
// ---------------------------------------------------------------------------

func TestAllocate_AllPrefixesFull(t *testing.T) {
	// /30 has 4 IPs; allocate all 4 then expect error
	eni := &networkv1beta1.Nic{
		ID:       "eni-full",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/30", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-full", "aa:bb:cc:dd:ee:ff", eni, false)

	for i := 0; i < 4; i++ {
		_, err := ipam.AllocateIPv4(fmt.Sprintf("pod-%d", i))
		require.NoError(t, err, "allocation %d should succeed", i)
	}

	_, err := ipam.AllocateIPv4("pod-overflow")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no available IPv4 address")
}

func TestAllocate_AllPrefixesFrozen(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-frozen",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/29", Status: networkv1beta1.IPPrefixStatusFrozen},
			{Prefix: "10.0.0.8/29", Status: networkv1beta1.IPPrefixStatusFrozen},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-frozen", "aa:bb:cc:dd:ee:ff", eni, false)

	_, err := ipam.AllocateIPv4("pod-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no available IPv4 address")
}

func TestAllocate_SkipFullPrefixUseNext(t *testing.T) {
	// Use a single /30 prefix (4 IPs) to fill it completely, then verify allocation fails.
	// Then add a second prefix and verify allocation succeeds from the new prefix.
	// Note: ipv4PrefixMap is a Go map, so iteration order is non-deterministic.
	// We use a single-prefix setup to reliably test "skip full prefix" behavior.
	eni := &networkv1beta1.Nic{
		ID:       "eni-skip",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/30", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-skip", "aa:bb:cc:dd:ee:ff", eni, false)

	// Fill the /30 prefix completely (4 IPs)
	for i := 0; i < 4; i++ {
		_, err := ipam.AllocateIPv4(fmt.Sprintf("pod-fill-%d", i))
		require.NoError(t, err, "allocation %d should succeed", i)
	}

	// Verify /30 is now full
	_, err := ipam.AllocateIPv4("pod-overflow")
	require.Error(t, err, "/30 should be full after 4 allocations")

	// Add a second prefix via UpdatePrefixes
	ipam.UpdatePrefixes([]networkv1beta1.IPPrefix{
		{Prefix: "10.0.0.0/30", Status: networkv1beta1.IPPrefixStatusValid},
		{Prefix: "10.0.0.8/29", Status: networkv1beta1.IPPrefixStatusValid},
	}, false)

	// Now allocation should succeed from the new /29 prefix (since /30 is full)
	ip, err := ipam.AllocateIPv4("pod-next")
	require.NoError(t, err)
	prefix := ipam.podToPrefixV4["pod-next"]
	assert.Equal(t, "10.0.0.8/29", prefix, "should use /29 prefix since /30 is full")
	assert.True(t, ip.IsValid())
}

func TestAllocate_AfterFrozenBecomesValid(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-thaw",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/29", Status: networkv1beta1.IPPrefixStatusFrozen},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-thaw", "aa:bb:cc:dd:ee:ff", eni, false)

	// Frozen: allocation should fail
	_, err := ipam.AllocateIPv4("pod-1")
	require.Error(t, err)

	// Thaw: update prefix to Valid
	ipam.UpdatePrefixes([]networkv1beta1.IPPrefix{
		{Prefix: "10.0.0.0/29", Status: networkv1beta1.IPPrefixStatusValid},
	}, false)

	// Now allocation should succeed
	ip, err := ipam.AllocateIPv4("pod-1")
	require.NoError(t, err)
	assert.True(t, ip.IsValid())
}

// ---------------------------------------------------------------------------
// Release boundary tests
// ---------------------------------------------------------------------------

func TestRelease_UnknownPodID(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-rel",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-rel", "aa:bb:cc:dd:ee:ff", eni, false)

	// Releasing an unknown pod should be a no-op (no panic)
	assert.NotPanics(t, func() {
		ipam.ReleaseIPv4("nonexistent-pod")
	})
}

func TestRelease_DoubleFree(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-df",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-df", "aa:bb:cc:dd:ee:ff", eni, false)

	_, err := ipam.AllocateIPv4("pod-1")
	require.NoError(t, err)

	// First release
	ipam.ReleaseIPv4("pod-1")
	// Second release should be a no-op (no panic)
	assert.NotPanics(t, func() {
		ipam.ReleaseIPv4("pod-1")
	})
}

func TestRelease_AfterPrefixRemoved(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-pr",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
			{Prefix: "10.0.0.16/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-pr", "aa:bb:cc:dd:ee:ff", eni, false)

	// Allocate from first prefix
	_, err := ipam.AllocateIPv4("pod-1")
	require.NoError(t, err)
	prefix1 := ipam.podToPrefixV4["pod-1"]

	// Remove the second (empty) prefix via UpdatePrefixes
	ipam.UpdatePrefixes([]networkv1beta1.IPPrefix{
		{Prefix: prefix1, Status: networkv1beta1.IPPrefixStatusValid},
	}, false)

	// The second prefix should be gone
	assert.NotContains(t, ipam.ipv4PrefixMap, "10.0.0.16/28")

	// Releasing pod-1 (on the remaining prefix) should still work
	assert.NotPanics(t, func() {
		ipam.ReleaseIPv4("pod-1")
	})
	assert.NotContains(t, ipam.podToPrefixV4, "pod-1")
}

// ---------------------------------------------------------------------------
// UpdatePrefixes boundary tests
// ---------------------------------------------------------------------------

func TestUpdatePrefixes_FrozenToValid(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-ftv",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/29", Status: networkv1beta1.IPPrefixStatusFrozen},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-ftv", "aa:bb:cc:dd:ee:ff", eni, false)

	// Confirm it's Frozen
	assert.Equal(t, networkv1beta1.IPPrefixStatusFrozen, ipam.ipv4PrefixMap["10.0.0.0/29"].status)

	// Update to Valid
	ipam.UpdatePrefixes([]networkv1beta1.IPPrefix{
		{Prefix: "10.0.0.0/29", Status: networkv1beta1.IPPrefixStatusValid},
	}, false)

	assert.Equal(t, networkv1beta1.IPPrefixStatusValid, ipam.ipv4PrefixMap["10.0.0.0/29"].status)

	// Allocation should now succeed
	ip, err := ipam.AllocateIPv4("pod-1")
	require.NoError(t, err)
	assert.True(t, ip.IsValid())
}

func TestUpdatePrefixes_RemoveOccupied(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-ro",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-ro", "aa:bb:cc:dd:ee:ff", eni, false)

	// Allocate a pod into the prefix
	_, err := ipam.AllocateIPv4("pod-1")
	require.NoError(t, err)

	// Try to remove the prefix (it's occupied, so it should be retained)
	ipam.UpdatePrefixes([]networkv1beta1.IPPrefix{}, false)

	// Prefix should still exist because pod-1 is allocated
	assert.Contains(t, ipam.ipv4PrefixMap, "10.0.0.0/28",
		"occupied prefix must not be removed by UpdatePrefixes")
}

func TestUpdatePrefixes_RemoveEmpty(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-re",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
			{Prefix: "10.0.0.16/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-re", "aa:bb:cc:dd:ee:ff", eni, false)

	// Remove the second prefix (empty)
	ipam.UpdatePrefixes([]networkv1beta1.IPPrefix{
		{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
	}, false)

	assert.Contains(t, ipam.ipv4PrefixMap, "10.0.0.0/28")
	assert.NotContains(t, ipam.ipv4PrefixMap, "10.0.0.16/28",
		"empty prefix should be removed by UpdatePrefixes")
}

func TestUpdatePrefixes_AllRemoved(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-ar",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-ar", "aa:bb:cc:dd:ee:ff", eni, false)

	// Remove all prefixes
	ipam.UpdatePrefixes([]networkv1beta1.IPPrefix{}, false)

	assert.Empty(t, ipam.ipv4PrefixMap)

	// Allocation should now fail
	_, err := ipam.AllocateIPv4("pod-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no available IPv4 address")
}

// ---------------------------------------------------------------------------
// RestorePod boundary tests
// ---------------------------------------------------------------------------

func TestRestorePod_IPInFrozenPrefix(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-frozen-restore",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusFrozen},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-frozen-restore", "aa:bb:cc:dd:ee:ff", eni, false)

	// RestorePod should work even for Frozen prefixes (bitmap set)
	ipam.RestorePod("pod-1", "10.0.0.3", "", "eni-frozen-restore")

	info, ok := ipam.ipv4PrefixMap["10.0.0.0/28"]
	require.True(t, ok)
	assert.True(t, info.bitmap.Test(3), "bit 3 should be set after restore")
	assert.Equal(t, uint(3), info.allocated["pod-1"])
	assert.Equal(t, "10.0.0.0/28", ipam.podToPrefixV4["pod-1"])
}

func TestRestorePod_IPNotInAnyPrefix(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-noprefix",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-noprefix", "aa:bb:cc:dd:ee:ff", eni, false)

	// IP 10.0.1.5 is not in 10.0.0.0/28 — restore should be a no-op
	ipam.RestorePod("pod-1", "10.0.1.5", "", "eni-noprefix")

	assert.NotContains(t, ipam.podToPrefixV4, "pod-1",
		"pod should not be tracked when IP is not in any prefix")
}

func TestRestorePod_DuplicateRestore(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-dup",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-dup", "aa:bb:cc:dd:ee:ff", eni, false)

	// First restore
	ipam.RestorePod("pod-1", "10.0.0.7", "", "eni-dup")
	info := ipam.ipv4PrefixMap["10.0.0.0/28"]
	assert.True(t, info.bitmap.Test(7))

	// Second restore (idempotent) — should not panic or corrupt state
	assert.NotPanics(t, func() {
		ipam.RestorePod("pod-1", "10.0.0.7", "", "eni-dup")
	})
	assert.True(t, info.bitmap.Test(7), "bit should still be set after duplicate restore")
	assert.Equal(t, "10.0.0.0/28", ipam.podToPrefixV4["pod-1"])
}

func TestAllocateIPv4_SkipsNonValidPrefixes(t *testing.T) {
	// Construct an ENILocalIPAM with prefixes in different states
	ipam := &ENILocalIPAM{
		ipv4PrefixMap: make(map[string]*PrefixInfo),
		podToPrefixV4: make(map[string]string),
	}

	// Invalid prefix (should be skipped)
	ipam.ipv4PrefixMap["10.0.0.0/28"] = &PrefixInfo{
		Prefix:    "10.0.0.0/28",
		bitmap:    bitset.New(16),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusInvalid,
	}

	// Deleting prefix (should be skipped)
	ipam.ipv4PrefixMap["10.0.0.16/28"] = &PrefixInfo{
		Prefix:    "10.0.0.16/28",
		bitmap:    bitset.New(16),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusDeleting,
	}

	// Frozen prefix (should be skipped)
	ipam.ipv4PrefixMap["10.0.0.32/28"] = &PrefixInfo{
		Prefix:    "10.0.0.32/28",
		bitmap:    bitset.New(16),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusFrozen,
	}

	// Valid prefix (should be used)
	ipam.ipv4PrefixMap["10.0.0.48/28"] = &PrefixInfo{
		Prefix:    "10.0.0.48/28",
		bitmap:    bitset.New(16),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusValid,
	}

	// Call AllocateIPv4, should allocate from Valid prefix
	ip, err := ipam.AllocateIPv4("pod-1")
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.48", ip.String())

	// Verify Invalid/Deleting/Frozen prefixes have no allocated records
	assert.Empty(t, ipam.ipv4PrefixMap["10.0.0.0/28"].allocated, "Invalid prefix should have no allocations")
	assert.Empty(t, ipam.ipv4PrefixMap["10.0.0.16/28"].allocated, "Deleting prefix should have no allocations")
	assert.Empty(t, ipam.ipv4PrefixMap["10.0.0.32/28"].allocated, "Frozen prefix should have no allocations")

	// Verify Valid prefix has one allocation record
	assert.Len(t, ipam.ipv4PrefixMap["10.0.0.48/28"].allocated, 1)
	assert.Contains(t, ipam.ipv4PrefixMap["10.0.0.48/28"].allocated, "pod-1")
}

func TestAllocateIPv6_SkipsNonValidPrefixes(t *testing.T) {
	// Construct ENILocalIPAM with ipv6PrefixMap containing Invalid, Deleting, Frozen, Valid prefixes
	ipam := &ENILocalIPAM{
		ipv6PrefixMap: make(map[string]*PrefixInfo),
		podToPrefixV6: make(map[string]string),
		ipv4PrefixMap: make(map[string]*PrefixInfo),
		podToPrefixV4: make(map[string]string),
	}

	// Invalid prefix
	ipam.ipv6PrefixMap["fd00::/120"] = &PrefixInfo{
		Prefix:    "fd00::/120",
		bitmap:    bitset.New(256),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusInvalid,
	}

	// Deleting prefix
	ipam.ipv6PrefixMap["fd00::100/120"] = &PrefixInfo{
		Prefix:    "fd00::100/120",
		bitmap:    bitset.New(256),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusDeleting,
	}

	// Frozen prefix
	ipam.ipv6PrefixMap["fd00::200/120"] = &PrefixInfo{
		Prefix:    "fd00::200/120",
		bitmap:    bitset.New(256),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusFrozen,
	}

	// Valid prefix
	ipam.ipv6PrefixMap["fd00::300/120"] = &PrefixInfo{
		Prefix:    "fd00::300/120",
		bitmap:    bitset.New(256),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusValid,
	}

	// Call AllocateIPv6, should succeed, IP must be within Valid prefix range
	ip, err := ipam.AllocateIPv6("pod-1")
	require.NoError(t, err)
	assert.Equal(t, "fd00::300", ip.String())

	// Verify Invalid/Deleting/Frozen prefixes have no allocated records
	assert.Empty(t, ipam.ipv6PrefixMap["fd00::/120"].allocated)
	assert.Empty(t, ipam.ipv6PrefixMap["fd00::100/120"].allocated)
	assert.Empty(t, ipam.ipv6PrefixMap["fd00::200/120"].allocated)

	// Verify Valid prefix has allocated records
	assert.Len(t, ipam.ipv6PrefixMap["fd00::300/120"].allocated, 1)
}

func TestAllocateIPv4_AllNonValid_Fails(t *testing.T) {
	// Construct ENILocalIPAM with ipv4PrefixMap containing only Invalid, Deleting, Frozen prefixes
	ipam := &ENILocalIPAM{
		ipv4PrefixMap: make(map[string]*PrefixInfo),
		podToPrefixV4: make(map[string]string),
		ipv6PrefixMap: make(map[string]*PrefixInfo),
		podToPrefixV6: make(map[string]string),
	}

	// Invalid prefix
	ipam.ipv4PrefixMap["10.0.0.0/28"] = &PrefixInfo{
		Prefix:    "10.0.0.0/28",
		bitmap:    bitset.New(16),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusInvalid,
	}

	// Deleting prefix
	ipam.ipv4PrefixMap["10.0.0.16/28"] = &PrefixInfo{
		Prefix:    "10.0.0.16/28",
		bitmap:    bitset.New(16),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusDeleting,
	}

	// Frozen prefix
	ipam.ipv4PrefixMap["10.0.0.32/28"] = &PrefixInfo{
		Prefix:    "10.0.0.32/28",
		bitmap:    bitset.New(16),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusFrozen,
	}

	// Call AllocateIPv4 should return error
	_, err := ipam.AllocateIPv4("pod-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no available IPv4 address")
}

func TestAllocateIPv6_AllNonValid_Fails(t *testing.T) {
	// Construct ENILocalIPAM with ipv6PrefixMap containing only Invalid, Deleting, Frozen prefixes
	ipam := &ENILocalIPAM{
		ipv6PrefixMap: make(map[string]*PrefixInfo),
		podToPrefixV6: make(map[string]string),
		ipv4PrefixMap: make(map[string]*PrefixInfo),
		podToPrefixV4: make(map[string]string),
	}

	// Invalid prefix
	ipam.ipv6PrefixMap["fd00::/120"] = &PrefixInfo{
		Prefix:    "fd00::/120",
		bitmap:    bitset.New(256),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusInvalid,
	}

	// Deleting prefix
	ipam.ipv6PrefixMap["fd00::100/120"] = &PrefixInfo{
		Prefix:    "fd00::100/120",
		bitmap:    bitset.New(256),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusDeleting,
	}

	// Frozen prefix
	ipam.ipv6PrefixMap["fd00::200/120"] = &PrefixInfo{
		Prefix:    "fd00::200/120",
		bitmap:    bitset.New(256),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusFrozen,
	}

	// Call AllocateIPv6 should return error
	_, err := ipam.AllocateIPv6("pod-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no available IPv6 address")
}

func TestAllocateIPv4_ValidPrefixFull_SkipsToNext(t *testing.T) {
	// Construct ENILocalIPAM with two Valid prefixes, first one is full
	ipam := &ENILocalIPAM{
		ipv4PrefixMap: make(map[string]*PrefixInfo),
		podToPrefixV4: make(map[string]string),
		ipv6PrefixMap: make(map[string]*PrefixInfo),
		podToPrefixV6: make(map[string]string),
	}

	// First Valid prefix (10.0.0.0/30, only 4 IPs), bitmap all set to 1 (full)
	firstPrefix := &PrefixInfo{
		Prefix:    "10.0.0.0/30",
		bitmap:    bitset.New(4),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusValid,
	}
	// Set all bits to 1 (full)
	firstPrefix.bitmap.Set(0)
	firstPrefix.bitmap.Set(1)
	firstPrefix.bitmap.Set(2)
	firstPrefix.bitmap.Set(3)
	ipam.ipv4PrefixMap["10.0.0.0/30"] = firstPrefix

	// Second Valid prefix (10.0.0.4/30, only 4 IPs), bitmap empty
	ipam.ipv4PrefixMap["10.0.0.4/30"] = &PrefixInfo{
		Prefix:    "10.0.0.4/30",
		bitmap:    bitset.New(4),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusValid,
	}

	// Call AllocateIPv4, should succeed, IP in the second prefix range
	ip, err := ipam.AllocateIPv4("pod-1")
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.4", ip.String())

	// Verify the first prefix's allocated is still empty
	assert.Empty(t, ipam.ipv4PrefixMap["10.0.0.0/30"].allocated)

	// Verify the second prefix has allocated record
	assert.Len(t, ipam.ipv4PrefixMap["10.0.0.4/30"].allocated, 1)
}

// TestAllocateIPv4_MostFullFirst verifies the most-full-first prefix selection strategy.
// Allocations should be packed into the prefix with fewest remaining IPs.
func TestAllocateIPv4_MostFullFirst(t *testing.T) {
	ipam := &ENILocalIPAM{
		ipv4PrefixMap: make(map[string]*PrefixInfo),
		podToPrefixV4: make(map[string]string),
		ipv6PrefixMap: make(map[string]*PrefixInfo),
		podToPrefixV6: make(map[string]string),
	}

	// Prefix A: /30 = 4 IPs, pre-allocate 2 -> 2 remaining
	prefixA := &PrefixInfo{
		Prefix:    "10.0.0.0/30",
		bitmap:    bitset.New(4),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusValid,
	}
	prefixA.bitmap.Set(0)
	prefixA.bitmap.Set(1)
	prefixA.allocated["existing-pod-1"] = 0
	prefixA.allocated["existing-pod-2"] = 1
	ipam.ipv4PrefixMap["10.0.0.0/30"] = prefixA

	// Prefix B: /29 = 8 IPs, empty -> 8 remaining
	prefixB := &PrefixInfo{
		Prefix:    "10.0.0.8/29",
		bitmap:    bitset.New(8),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusValid,
	}
	ipam.ipv4PrefixMap["10.0.0.8/29"] = prefixB

	// Allocate -> should go to Prefix A (2 remaining < 8 remaining)
	ip, err := ipam.AllocateIPv4("pod-new")
	require.NoError(t, err)

	prefix := ipam.podToPrefixV4["pod-new"]
	assert.Equal(t, "10.0.0.0/30", prefix, "should allocate from most-full prefix (fewest remaining)")
	assert.Equal(t, "10.0.0.2", ip.String())
}

// TestAllocateIPv6_MostFullFirst verifies the most-full-first prefix selection for IPv6.
func TestAllocateIPv6_MostFullFirst(t *testing.T) {
	ipam := &ENILocalIPAM{
		ipv4PrefixMap: make(map[string]*PrefixInfo),
		podToPrefixV4: make(map[string]string),
		ipv6PrefixMap: make(map[string]*PrefixInfo),
		podToPrefixV6: make(map[string]string),
	}

	// Prefix A: /126 = 4 IPs, pre-allocate 3 -> 1 remaining
	prefixA := &PrefixInfo{
		Prefix:    "fd00::/126",
		bitmap:    bitset.New(4),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusValid,
	}
	prefixA.bitmap.Set(0)
	prefixA.bitmap.Set(1)
	prefixA.bitmap.Set(2)
	prefixA.allocated["pod-a"] = 0
	prefixA.allocated["pod-b"] = 1
	prefixA.allocated["pod-c"] = 2
	ipam.ipv6PrefixMap["fd00::/126"] = prefixA

	// Prefix B: /126 = 4 IPs, empty -> 4 remaining
	prefixB := &PrefixInfo{
		Prefix:    "fd00::4/126",
		bitmap:    bitset.New(4),
		allocated: make(map[string]uint),
		status:    networkv1beta1.IPPrefixStatusValid,
	}
	ipam.ipv6PrefixMap["fd00::4/126"] = prefixB

	ip, err := ipam.AllocateIPv6("pod-new")
	require.NoError(t, err)

	prefix := ipam.podToPrefixV6["pod-new"]
	assert.Equal(t, "fd00::/126", prefix, "should allocate from most-full prefix")
	assert.Equal(t, "fd00::3", ip.String())
}

// TestSortedValidPrefixes verifies the sorting order: most-full first, then by CIDR.
func TestSortedValidPrefixes(t *testing.T) {
	prefixMap := map[string]*PrefixInfo{
		"10.0.0.0/28": {
			Prefix:    "10.0.0.0/28",
			bitmap:    bitset.New(16),
			allocated: map[string]uint{"p1": 0, "p2": 1, "p3": 2}, // 13 remaining
			status:    networkv1beta1.IPPrefixStatusValid,
		},
		"10.0.0.16/28": {
			Prefix:    "10.0.0.16/28",
			bitmap:    bitset.New(16),
			allocated: map[string]uint{"p1": 0, "p2": 1, "p3": 2, "p4": 3, "p5": 4}, // 11 remaining
			status:    networkv1beta1.IPPrefixStatusValid,
		},
		"10.0.0.32/28": {
			Prefix:    "10.0.0.32/28",
			bitmap:    bitset.New(16),
			allocated: make(map[string]uint), // 16 remaining
			status:    networkv1beta1.IPPrefixStatusValid,
		},
		"10.0.0.48/28": {
			Prefix:    "10.0.0.48/28",
			bitmap:    bitset.New(16),
			allocated: make(map[string]uint),
			status:    networkv1beta1.IPPrefixStatusFrozen, // should be excluded
		},
	}

	result := sortedValidPrefixes(prefixMap)

	require.Len(t, result, 3, "Frozen prefix should be excluded")
	assert.Equal(t, "10.0.0.16/28", result[0].cidr, "most full first (11 remaining)")
	assert.Equal(t, "10.0.0.0/28", result[1].cidr, "second most full (13 remaining)")
	assert.Equal(t, "10.0.0.32/28", result[2].cidr, "least full (16 remaining)")
}

// ---------------------------------------------------------------------------
// RestorePod dual-stack tests
// ---------------------------------------------------------------------------

func TestRestorePod_DualStack(t *testing.T) {
	// Create dual-stack prefix mode IPAM
	eni := &networkv1beta1.Nic{
		ID:         "eni-dualstack-restore",
		MacAddress: "00:11:22:33:44:55",
		VSwitchID:  "vsw-123456",
		IPv4CIDR:   "10.0.0.0/24",
		IPv6CIDR:   "fd00::/112",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{
				Prefix: "10.0.0.0/28",
				Status: networkv1beta1.IPPrefixStatusValid,
			},
		},
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{
				Prefix: "fd00::/120",
				Status: networkv1beta1.IPPrefixStatusValid,
			},
		},
	}

	ipam := NewENILocalIPAMFromPrefix("eni-dualstack-restore", "00:11:22:33:44:55", eni, false)

	// Restore a Pod with both IPv4 and IPv6
	ipam.RestorePod("pod-1", "10.0.0.5", "fd00::10", "eni-dualstack-restore")

	// Verify IPv4 restoration
	v4PrefixInfo, ok := ipam.ipv4PrefixMap["10.0.0.0/28"]
	require.True(t, ok)
	assert.True(t, v4PrefixInfo.bitmap.Test(5), "IPv4 bit 5 should be set")
	assert.Equal(t, uint(5), v4PrefixInfo.allocated["pod-1"])
	assert.Equal(t, "10.0.0.0/28", ipam.podToPrefixV4["pod-1"])

	// Verify IPv6 restoration
	v6PrefixInfo, ok := ipam.ipv6PrefixMap["fd00::/120"]
	require.True(t, ok)
	assert.True(t, v6PrefixInfo.bitmap.Test(16), "IPv6 bit 16 should be set (0x10 = 16)")
	assert.Equal(t, uint(16), v6PrefixInfo.allocated["pod-1"])
	assert.Equal(t, "fd00::/120", ipam.podToPrefixV6["pod-1"])

	// Verify HasAllocations and AllocationCount
	assert.True(t, ipam.HasAllocations())
	assert.Equal(t, 2, ipam.AllocationCount()) // 1 IPv4 + 1 IPv6
}

func TestRestorePod_DualStack_IPv4Only(t *testing.T) {
	// Restore with only IPv4 in dual-stack IPAM
	eni := &networkv1beta1.Nic{
		ID:         "eni-ds-v4only",
		MacAddress: "00:11:22:33:44:55",
		VSwitchID:  "vsw-123456",
		IPv4CIDR:   "10.0.0.0/24",
		IPv6CIDR:   "fd00::/112",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}

	ipam := NewENILocalIPAMFromPrefix("eni-ds-v4only", "00:11:22:33:44:55", eni, false)

	// Restore with only IPv4 (empty IPv6)
	ipam.RestorePod("pod-1", "10.0.0.3", "", "eni-ds-v4only")

	// Verify IPv4 is restored
	assert.Contains(t, ipam.podToPrefixV4, "pod-1")
	assert.Equal(t, "10.0.0.0/28", ipam.podToPrefixV4["pod-1"])

	// Verify IPv6 is NOT restored
	assert.NotContains(t, ipam.podToPrefixV6, "pod-1")
}

func TestRestorePod_DualStack_IPv6Only(t *testing.T) {
	// Restore with only IPv6 in dual-stack IPAM
	eni := &networkv1beta1.Nic{
		ID:         "eni-ds-v6only",
		MacAddress: "00:11:22:33:44:55",
		VSwitchID:  "vsw-123456",
		IPv4CIDR:   "10.0.0.0/24",
		IPv6CIDR:   "fd00::/112",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}

	ipam := NewENILocalIPAMFromPrefix("eni-ds-v6only", "00:11:22:33:44:55", eni, false)

	// Restore with only IPv6 (empty IPv4)
	ipam.RestorePod("pod-1", "", "fd00::5", "eni-ds-v6only")

	// Verify IPv4 is NOT restored
	assert.NotContains(t, ipam.podToPrefixV4, "pod-1")

	// Verify IPv6 is restored
	assert.Contains(t, ipam.podToPrefixV6, "pod-1")
	assert.Equal(t, "fd00::/120", ipam.podToPrefixV6["pod-1"])
}

// ---------------------------------------------------------------------------
// UpdatePrefixes IPv6 tests
// ---------------------------------------------------------------------------

func TestUpdatePrefixes_IPv6_AddNewPrefix(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-v6-add",
		IPv6CIDR: "fd00::/112",
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-v6-add", "aa:bb:cc:dd:ee:ff", eni, false)

	// Verify initial state
	assert.Len(t, ipam.ipv6PrefixMap, 1)
	assert.Contains(t, ipam.ipv6PrefixMap, "fd00::/120")

	// Add a new IPv6 prefix
	ipam.UpdatePrefixes([]networkv1beta1.IPPrefix{
		{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
		{Prefix: "fd00::100/120", Status: networkv1beta1.IPPrefixStatusValid}, // new
	}, true)

	// Verify new prefix is added
	assert.Len(t, ipam.ipv6PrefixMap, 2)
	assert.Contains(t, ipam.ipv6PrefixMap, "fd00::/120")
	assert.Contains(t, ipam.ipv6PrefixMap, "fd00::100/120")

	// Verify allocation from new prefix works
	ip, err := ipam.AllocateIPv6("pod-1")
	require.NoError(t, err)
	assert.True(t, ip.IsValid())
}

func TestUpdatePrefixes_IPv6_RemoveEmptyPrefix(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-v6-remove",
		IPv6CIDR: "fd00::/112",
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
			{Prefix: "fd00::100/120", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-v6-remove", "aa:bb:cc:dd:ee:ff", eni, false)

	// Allocate from first prefix
	_, err := ipam.AllocateIPv6("pod-1")
	require.NoError(t, err)

	// Remove second (empty) prefix
	ipam.UpdatePrefixes([]networkv1beta1.IPPrefix{
		{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
	}, true)

	// Verify second prefix is removed
	assert.Len(t, ipam.ipv6PrefixMap, 1)
	assert.Contains(t, ipam.ipv6PrefixMap, "fd00::/120")
	assert.NotContains(t, ipam.ipv6PrefixMap, "fd00::100/120")
}

func TestUpdatePrefixes_IPv6_RetainOccupiedPrefix(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-v6-retain",
		IPv6CIDR: "fd00::/112",
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-v6-retain", "aa:bb:cc:dd:ee:ff", eni, false)

	// Allocate from the prefix
	_, err := ipam.AllocateIPv6("pod-1")
	require.NoError(t, err)

	// Try to remove the occupied prefix
	ipam.UpdatePrefixes([]networkv1beta1.IPPrefix{}, true)

	// Verify prefix is retained (has allocation)
	assert.Len(t, ipam.ipv6PrefixMap, 1)
	assert.Contains(t, ipam.ipv6PrefixMap, "fd00::/120")
}

func TestUpdatePrefixes_IPv6_StatusChange(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-v6-status",
		IPv6CIDR: "fd00::/112",
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-v6-status", "aa:bb:cc:dd:ee:ff", eni, false)

	// Verify initial status
	assert.Equal(t, networkv1beta1.IPPrefixStatusValid, ipam.ipv6PrefixMap["fd00::/120"].status)

	// Change status to Frozen
	ipam.UpdatePrefixes([]networkv1beta1.IPPrefix{
		{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusFrozen},
	}, true)

	// Verify status updated
	assert.Equal(t, networkv1beta1.IPPrefixStatusFrozen, ipam.ipv6PrefixMap["fd00::/120"].status)

	// Allocation should now fail (Frozen)
	_, err := ipam.AllocateIPv6("pod-1")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// IPv6 /80 prefix tests
// ---------------------------------------------------------------------------

func TestPrefixSize_IPv6_80(t *testing.T) {
	tests := []struct {
		name     string
		cidr     string
		wantSize uint
	}{
		{
			name:     "/80 IPv6 prefix capped to 65536",
			cidr:     "2408:4005:3e2:ee00::/80",
			wantSize: 65536,
		},
		{
			name:     "/64 IPv6 prefix capped to 65536",
			cidr:     "fd00::/64",
			wantSize: 65536,
		},
		{
			name:     "/112 IPv6 prefix remains 65536",
			cidr:     "fd00::/112",
			wantSize: 65536,
		},
		{
			name:     "/120 IPv6 prefix remains 256",
			cidr:     "fd00::/120",
			wantSize: 256,
		},
		{
			name:     "/126 IPv6 prefix remains 4",
			cidr:     "fd00::/126",
			wantSize: 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			size := prefixSize(tt.cidr)
			assert.Equal(t, tt.wantSize, size)
		})
	}
}

func TestCalculateIP_IPv6_80Prefix(t *testing.T) {
	cidr := "2408:4005:3e2:ee00::/80"

	ip0, err := calculateIP(cidr, 0)
	require.NoError(t, err)
	assert.Equal(t, "2408:4005:3e2:ee00::", ip0.String())

	ip1, err := calculateIP(cidr, 1)
	require.NoError(t, err)
	assert.Equal(t, "2408:4005:3e2:ee00::1", ip1.String())

	ip256, err := calculateIP(cidr, 256)
	require.NoError(t, err)
	assert.Equal(t, "2408:4005:3e2:ee00::100", ip256.String())

	ipMax, err := calculateIP(cidr, 65535)
	require.NoError(t, err)
	assert.Equal(t, "2408:4005:3e2:ee00::ffff", ipMax.String())
}

func TestFindPrefixContainingIP_IPv6_80(t *testing.T) {
	prefixMap := map[string]*PrefixInfo{
		"2408:4005:3e2:ee00::/80": {
			Prefix:    "2408:4005:3e2:ee00::/80",
			bitmap:    bitset.New(65536),
			allocated: make(map[string]uint),
			status:    networkv1beta1.IPPrefixStatusValid,
		},
	}

	t.Run("offset within range", func(t *testing.T) {
		ip, err := netip.ParseAddr("2408:4005:3e2:ee00::1234")
		require.NoError(t, err)

		prefix, offset, found := findPrefixContainingIP(ip, prefixMap)
		assert.True(t, found)
		assert.Equal(t, "2408:4005:3e2:ee00::/80", prefix)
		assert.Equal(t, uint(0x1234), offset)
	})

	t.Run("offset 0", func(t *testing.T) {
		ip, err := netip.ParseAddr("2408:4005:3e2:ee00::")
		require.NoError(t, err)

		prefix, offset, found := findPrefixContainingIP(ip, prefixMap)
		assert.True(t, found)
		assert.Equal(t, "2408:4005:3e2:ee00::/80", prefix)
		assert.Equal(t, uint(0), offset)
	})

	t.Run("offset max 65535", func(t *testing.T) {
		ip, err := netip.ParseAddr("2408:4005:3e2:ee00::ffff")
		require.NoError(t, err)

		prefix, offset, found := findPrefixContainingIP(ip, prefixMap)
		assert.True(t, found)
		assert.Equal(t, "2408:4005:3e2:ee00::/80", prefix)
		assert.Equal(t, uint(65535), offset)
	})

	t.Run("middle bits non-zero exceeds bitmap", func(t *testing.T) {
		// 2408:4005:3e2:ee00:0:1:0:1 is within the /80 CIDR but offset is
		// 0x000100000001 which exceeds the 65536 bitmap cap.
		ip, err := netip.ParseAddr("2408:4005:3e2:ee00:0:1:0:1")
		require.NoError(t, err)

		_, _, found := findPrefixContainingIP(ip, prefixMap)
		assert.False(t, found, "should reject IP with offset exceeding bitmap size")
	})

	t.Run("IP outside /80 CIDR", func(t *testing.T) {
		ip, err := netip.ParseAddr("2408:4005:3e2:ff00::1")
		require.NoError(t, err)

		_, _, found := findPrefixContainingIP(ip, prefixMap)
		assert.False(t, found)
	})
}

func TestAllocateIPv6_80Prefix(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-v6-80",
		IPv6CIDR: "2408:4005:3e2:ee00::/64",
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "2408:4005:3e2:ee00::/80", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-v6-80", "aa:bb:cc:dd:ee:ff", eni, false)

	// Verify bitmap size is capped
	info := ipam.ipv6PrefixMap["2408:4005:3e2:ee00::/80"]
	require.NotNil(t, info)
	assert.Equal(t, uint(65536), info.bitmap.Len())

	// Allocate first IP
	ip1, err := ipam.AllocateIPv6("pod-1")
	require.NoError(t, err)
	assert.Equal(t, "2408:4005:3e2:ee00::", ip1.String())

	// Allocate second IP
	ip2, err := ipam.AllocateIPv6("pod-2")
	require.NoError(t, err)
	assert.Equal(t, "2408:4005:3e2:ee00::1", ip2.String())

	// Idempotent re-allocation for same pod returns same IP
	ip1Again, err := ipam.AllocateIPv6("pod-1")
	require.NoError(t, err)
	assert.Equal(t, ip1, ip1Again)

	// Release and re-allocate
	ipam.ReleaseIPv6("pod-1")
	ip3, err := ipam.AllocateIPv6("pod-3")
	require.NoError(t, err)
	assert.Equal(t, "2408:4005:3e2:ee00::", ip3.String())

	// Pool stats should reflect capped total
	_, _, totalV6, idleV6 := ipam.PoolStats()
	assert.Equal(t, 65536, totalV6)
	assert.Equal(t, 65536-2, idleV6) // pod-2, pod-3
}

func TestRestorePod_IPv6_80Prefix(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-v6-80-restore",
		IPv6CIDR: "2408:4005:3e2:ee00::/64",
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "2408:4005:3e2:ee00::/80", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-v6-80-restore", "aa:bb:cc:dd:ee:ff", eni, false)

	// Restore a pod with an IP at offset 0x1234
	ipam.RestorePod("pod-1", "", "2408:4005:3e2:ee00::1234", "eni-v6-80-restore")

	info := ipam.ipv6PrefixMap["2408:4005:3e2:ee00::/80"]
	require.NotNil(t, info)
	assert.True(t, info.bitmap.Test(0x1234))
	assert.Equal(t, uint(0x1234), info.allocated["pod-1"])
	assert.Equal(t, "2408:4005:3e2:ee00::/80", ipam.podToPrefixV6["pod-1"])

	// Allocate should not reuse the restored offset
	ip, err := ipam.AllocateIPv6("pod-2")
	require.NoError(t, err)
	assert.Equal(t, "2408:4005:3e2:ee00::", ip.String()) // offset 0
}

func TestRestorePod_IPv6_80Prefix_OutOfRange(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-v6-80-oor",
		IPv6CIDR: "2408:4005:3e2:ee00::/64",
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "2408:4005:3e2:ee00::/80", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-v6-80-oor", "aa:bb:cc:dd:ee:ff", eni, false)

	// Try to restore an IP with middle bits set (offset > 65535)
	ipam.RestorePod("pod-1", "", "2408:4005:3e2:ee00:0:1:0:1", "eni-v6-80-oor")

	// Should not be tracked since offset exceeds bitmap range
	assert.NotContains(t, ipam.podToPrefixV6, "pod-1")
}

func TestUpdatePrefixes_IPv6_80Prefix(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-v6-80-update",
		IPv6CIDR: "2408:4005:3e2:ee00::/64",
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "2408:4005:3e2:ee00::/80", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-v6-80-update", "aa:bb:cc:dd:ee:ff", eni, false)

	// Allocate one IP
	_, err := ipam.AllocateIPv6("pod-1")
	require.NoError(t, err)

	// Add a second /80 prefix
	ipam.UpdatePrefixes([]networkv1beta1.IPPrefix{
		{Prefix: "2408:4005:3e2:ee00::/80", Status: networkv1beta1.IPPrefixStatusValid},
		{Prefix: "2408:4005:3e2:ee00:1::/80", Status: networkv1beta1.IPPrefixStatusValid},
	}, true)

	assert.Len(t, ipam.ipv6PrefixMap, 2)

	// Verify new prefix also has capped bitmap
	newInfo := ipam.ipv6PrefixMap["2408:4005:3e2:ee00:1::/80"]
	require.NotNil(t, newInfo)
	assert.Equal(t, uint(65536), newInfo.bitmap.Len())
}

func TestStatusSnapshot_Empty(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:         "eni-snap-empty",
		MacAddress: "aa:bb:cc:dd:ee:01",
		VSwitchID:  "vsw-snap",
		IPv4CIDR:   "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-snap-empty", "aa:bb:cc:dd:ee:01", eni, false)

	eniID, mac, ipv4, ipv6 := ipam.StatusSnapshot()
	assert.Equal(t, "eni-snap-empty", eniID)
	assert.Equal(t, "aa:bb:cc:dd:ee:01", mac)
	assert.Len(t, ipv4, 1)
	assert.Equal(t, "10.0.0.0/28", ipv4[0].Prefix)
	assert.Equal(t, "Valid", ipv4[0].Status)
	assert.Equal(t, 16, ipv4[0].Total)
	assert.Equal(t, 0, ipv4[0].Used)
	assert.Equal(t, 16, ipv4[0].Available)
	assert.Empty(t, ipv4[0].Allocations)
	assert.Empty(t, ipv6)
}

func TestStatusSnapshot_WithAllocations(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:         "eni-snap-alloc",
		MacAddress: "aa:bb:cc:dd:ee:02",
		VSwitchID:  "vsw-snap",
		IPv4CIDR:   "10.0.0.0/24",
		IPv6CIDR:   "fd00::/112",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-snap-alloc", "aa:bb:cc:dd:ee:02", eni, false)

	_, err := ipam.AllocateIPv4("pod-1")
	require.NoError(t, err)
	_, err = ipam.AllocateIPv4("pod-2")
	require.NoError(t, err)
	_, err = ipam.AllocateIPv6("pod-1")
	require.NoError(t, err)

	eniID, mac, ipv4, ipv6 := ipam.StatusSnapshot()
	assert.Equal(t, "eni-snap-alloc", eniID)
	assert.Equal(t, "aa:bb:cc:dd:ee:02", mac)

	require.Len(t, ipv4, 1)
	assert.Equal(t, 16, ipv4[0].Total)
	assert.Equal(t, 2, ipv4[0].Used)
	assert.Equal(t, 14, ipv4[0].Available)
	assert.Len(t, ipv4[0].Allocations, 2)

	allocIPs := make(map[string]string)
	for _, a := range ipv4[0].Allocations {
		allocIPs[a.PodID] = a.IP
	}
	assert.Equal(t, "10.0.0.0", allocIPs["pod-1"])
	assert.Equal(t, "10.0.0.1", allocIPs["pod-2"])

	require.Len(t, ipv6, 1)
	assert.Equal(t, 256, ipv6[0].Total)
	assert.Equal(t, 1, ipv6[0].Used)
	assert.Equal(t, 255, ipv6[0].Available)
}

func TestStatusSnapshot_FrozenPrefix(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:         "eni-snap-frozen",
		MacAddress: "aa:bb:cc:dd:ee:03",
		IPv4CIDR:   "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusFrozen},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-snap-frozen", "aa:bb:cc:dd:ee:03", eni, false)

	_, _, ipv4, _ := ipam.StatusSnapshot()
	require.Len(t, ipv4, 1)
	assert.Equal(t, "Frozen", ipv4[0].Status)
}

func TestAllocateIPv4_Idempotent(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:         "eni-idemp",
		MacAddress: "aa:bb:cc:dd:ee:10",
		IPv4CIDR:   "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-idemp", "aa:bb:cc:dd:ee:10", eni, false)

	ip1, err := ipam.AllocateIPv4("pod-1")
	require.NoError(t, err)

	ip2, err := ipam.AllocateIPv4("pod-1")
	require.NoError(t, err)
	assert.Equal(t, ip1, ip2, "idempotent allocation must return the same IP")
}

func TestAllocateIPv6_Idempotent(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-idemp-v6",
		IPv6CIDR: "fd00::/112",
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-idemp-v6", "aa:bb:cc:dd:ee:11", eni, false)

	ip1, err := ipam.AllocateIPv6("pod-1")
	require.NoError(t, err)

	ip2, err := ipam.AllocateIPv6("pod-1")
	require.NoError(t, err)
	assert.Equal(t, ip1, ip2)
}

func TestAllocateIPv4_AllPrefixesFull(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-full",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/30", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-full", "aa:bb:cc:dd:ee:20", eni, false)

	for i := 0; i < 4; i++ {
		_, err := ipam.AllocateIPv4(fmt.Sprintf("pod-%d", i))
		require.NoError(t, err)
	}

	_, err := ipam.AllocateIPv4("pod-overflow")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no available IPv4 address")
}

func TestAllocateIPv6_NoPrefixes(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-noprefix",
		IPv6CIDR: "fd00::/112",
	}
	ipam := NewENILocalIPAMFromPrefix("eni-noprefix", "aa:bb:cc:dd:ee:21", eni, false)

	_, err := ipam.AllocateIPv6("pod-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no available IPv6 address")
}

func TestReleaseIPv4_NonExistentPod(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-rel-ne",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-rel-ne", "aa:bb:cc:dd:ee:30", eni, false)

	assert.NotPanics(t, func() {
		ipam.ReleaseIPv4("nonexistent-pod")
	})
}

func TestReleaseIPv6_NonExistentPod(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-rel-ne-v6",
		IPv6CIDR: "fd00::/112",
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-rel-ne-v6", "aa:bb:cc:dd:ee:31", eni, false)

	assert.NotPanics(t, func() {
		ipam.ReleaseIPv6("nonexistent-pod")
	})
}

func TestRestorePod_WrongENI(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-restore-wrong",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-restore-wrong", "aa:bb:cc:dd:ee:40", eni, false)

	ipam.RestorePod("pod-1", "10.0.0.1", "", "eni-different")
	assert.Empty(t, ipam.podToPrefixV4)
}

func TestRestorePod_InvalidIPv4(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-restore-inv",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-restore-inv", "aa:bb:cc:dd:ee:41", eni, false)

	assert.NotPanics(t, func() {
		ipam.RestorePod("pod-1", "not-an-ip", "", "eni-restore-inv")
	})
	assert.Empty(t, ipam.podToPrefixV4)
}

func TestRestorePod_InvalidIPv6(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-restore-inv-v6",
		IPv6CIDR: "fd00::/112",
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-restore-inv-v6", "aa:bb:cc:dd:ee:42", eni, false)

	assert.NotPanics(t, func() {
		ipam.RestorePod("pod-1", "", "not-an-ip", "eni-restore-inv-v6")
	})
	assert.Empty(t, ipam.podToPrefixV6)
}

func TestIsERDMA(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:                          "eni-erdma",
		NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeHighPerformance,
	}
	ipam := NewENILocalIPAMFromPrefix("eni-erdma", "aa:bb:cc:dd:ee:50", eni, true)
	assert.True(t, ipam.IsERDMA())

	ipam2 := NewENILocalIPAMFromPrefix("eni-erdma2", "aa:bb:cc:dd:ee:51", eni, false)
	assert.False(t, ipam2.IsERDMA())

	eniStd := &networkv1beta1.Nic{
		ID:                          "eni-std",
		NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
	}
	ipam3 := NewENILocalIPAMFromPrefix("eni-std", "aa:bb:cc:dd:ee:52", eniStd, true)
	assert.False(t, ipam3.IsERDMA())
}

func TestNewENILocalIPAMFromPrefix_IPv6Only(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-v6only",
		IPv6CIDR: "fd00::/112",
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-v6only", "aa:bb:cc:dd:ee:60", eni, false)

	assert.Empty(t, ipam.ipv4PrefixMap)
	assert.Len(t, ipam.ipv6PrefixMap, 1)
	assert.NotNil(t, ipam.gatewayIP.IPv6)
}

func TestPoolStats_DualStack(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-stats-ds",
		IPv4CIDR: "10.0.0.0/24",
		IPv6CIDR: "fd00::/112",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-stats-ds", "aa:bb:cc:dd:ee:70", eni, false)

	totalV4, idleV4, totalV6, idleV6 := ipam.PoolStats()
	assert.Equal(t, 16, totalV4)
	assert.Equal(t, 16, idleV4)
	assert.Equal(t, 256, totalV6)
	assert.Equal(t, 256, idleV6)

	_, _ = ipam.AllocateIPv4("pod-1")
	_, _ = ipam.AllocateIPv6("pod-1")

	totalV4, idleV4, totalV6, idleV6 = ipam.PoolStats()
	assert.Equal(t, 16, totalV4)
	assert.Equal(t, 15, idleV4)
	assert.Equal(t, 256, totalV6)
	assert.Equal(t, 255, idleV6)
}
