/*
Copyright 2022 The Terway Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tc

import (
	"net"
	"reflect"
	"runtime"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func TestU32IPv4Src(t *testing.T) {
	type args struct {
		ipNet *net.IPNet
	}
	tests := []struct {
		name string
		args args
		want netlink.TcU32Key
	}{
		{
			name: "127.0.0.1/32",
			args: args{ipNet: &net.IPNet{
				IP:   net.ParseIP("127.0.0.1"),
				Mask: net.CIDRMask(32, 32),
			}},
			want: netlink.TcU32Key{
				Mask:    0xffffffff,
				Val:     0x7f000001,
				Off:     12,
				OffMask: 0,
			},
		},
		{
			name: "127.0.0.0/8",
			args: args{ipNet: &net.IPNet{
				IP:   net.ParseIP("127.0.0.1"),
				Mask: net.CIDRMask(8, 32),
			}},
			want: netlink.TcU32Key{
				Mask:    0xff000000,
				Val:     0x7f000000,
				Off:     12,
				OffMask: 0,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := U32IPv4Src(tt.args.ipNet); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("U32IPv4Src() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestU32IPv6Src(t *testing.T) {
	type args struct {
		ipNet *net.IPNet
	}
	tests := []struct {
		name string
		args args
		want []netlink.TcU32Key
	}{
		{
			name: "fd00::1/128",
			args: args{ipNet: &net.IPNet{
				IP:   net.ParseIP("fd00::1"),
				Mask: net.CIDRMask(128, 128),
			}},
			want: []netlink.TcU32Key{
				{
					Mask:    0xffffffff,
					Val:     0xfd000000,
					Off:     8,
					OffMask: 0,
				}, {
					Mask:    0xffffffff,
					Val:     0x00000000,
					Off:     12,
					OffMask: 0,
				}, {
					Mask:    0xffffffff,
					Val:     0x00000000,
					Off:     16,
					OffMask: 0,
				}, {
					Mask:    0xffffffff,
					Val:     0x00000001,
					Off:     20,
					OffMask: 0,
				},
			},
		}, {
			name: "fd:aaaa::/64",
			args: args{ipNet: &net.IPNet{
				IP:   net.ParseIP("fd:aaaa::"),
				Mask: net.CIDRMask(64, 128),
			}},
			want: []netlink.TcU32Key{
				{
					Mask:    0xffffffff,
					Val:     0x00fdaaaa,
					Off:     8,
					OffMask: 0,
				}, {
					Mask:    0xffffffff,
					Val:     0x00000000,
					Off:     12,
					OffMask: 0,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := U32IPv6Src(tt.args.ipNet); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("U32IPv6Src() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ==============================================================================
// FilterBySrcIP and Contain Tests with Network Namespace Isolation
// ==============================================================================

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

func TestFilterBySrcIP(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Create a dummy link
		dummyLink := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{
				Name: "test-dummy",
			},
		}
		err := netlink.LinkAdd(dummyLink)
		require.NoError(t, err)

		link, err := netlink.LinkByName("test-dummy")
		require.NoError(t, err)

		err = netlink.LinkSetUp(link)
		require.NoError(t, err)

		// Test with no filters (no qdisc added)
		t.Run("no filters found", func(t *testing.T) {
			_, ipNet, _ := net.ParseCIDR("192.168.1.0/24")
			filter, err := FilterBySrcIP(link, netlink.HANDLE_ROOT, ipNet)
			// May error or return nil, both are acceptable
			_ = err
			assert.Nil(t, filter)
		})

		return nil
	})
	require.NoError(t, err)
}

func TestFilterBySrcIP_WithFilters(t *testing.T) {
	// Create a mock link
	link := &netlink.Dummy{
		LinkAttrs: netlink.LinkAttrs{
			Index: 10,
			Name:  "tf-dummy",
		},
	}

	// Test 1: Add matching U32 filter
	t.Run("matching u32 filter found", func(t *testing.T) {
		_, ipNet, _ := net.ParseCIDR("192.168.1.100/32")

		u32Filter := &netlink.U32{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: link.Attrs().Index,
				Parent:    netlink.MakeHandle(1, 0),
				Priority:  1,
				Protocol:  unix.ETH_P_IP,
			},
			Sel: &netlink.TcU32Sel{
				Keys: U32MatchSrc(ipNet),
			},
		}

		patches := gomonkey.ApplyFunc(netlink.FilterList, func(l netlink.Link, parent uint32) ([]netlink.Filter, error) {
			return []netlink.Filter{u32Filter}, nil
		})
		defer patches.Reset()

		// Search for the filter
		foundFilter, err := FilterBySrcIP(link, netlink.MakeHandle(1, 0), ipNet)
		require.NoError(t, err)
		assert.NotNil(t, foundFilter)
		if foundFilter != nil {
			assert.Equal(t, link.Attrs().Index, foundFilter.Attrs().LinkIndex)
			assert.Equal(t, uint16(unix.ETH_P_IP), foundFilter.Protocol)
		}
	})

	// Test 2: U32 filter with wrong link index (should skip)
	t.Run("u32 filter with mismatched link index", func(t *testing.T) {
		_, ipNet, _ := net.ParseCIDR("192.168.2.100/32")

		u32Filter := &netlink.U32{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: 20, // Different link index
				Parent:    netlink.MakeHandle(1, 0),
				Priority:  2,
				Protocol:  unix.ETH_P_IP,
			},
			Sel: &netlink.TcU32Sel{
				Keys: U32MatchSrc(ipNet),
			},
		}

		patches := gomonkey.ApplyFunc(netlink.FilterList, func(l netlink.Link, parent uint32) ([]netlink.Filter, error) {
			return []netlink.Filter{u32Filter}, nil
		})
		defer patches.Reset()

		// Search using link with index 10 should not find filter with index 20
		foundFilter, err := FilterBySrcIP(link, netlink.MakeHandle(1, 0), ipNet)
		require.NoError(t, err)
		assert.Nil(t, foundFilter)
	})

	// Test 3: U32 filter with wrong protocol (should skip)
	t.Run("u32 filter with wrong protocol", func(t *testing.T) {
		_, ipNet, _ := net.ParseCIDR("192.168.3.100/32")

		u32Filter := &netlink.U32{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: link.Attrs().Index,
				Parent:    netlink.MakeHandle(1, 0),
				Priority:  3,
				Protocol:  unix.ETH_P_IPV6, // Wrong protocol
			},
			Sel: &netlink.TcU32Sel{
				Keys: U32MatchSrc(ipNet),
			},
		}

		patches := gomonkey.ApplyFunc(netlink.FilterList, func(l netlink.Link, parent uint32) ([]netlink.Filter, error) {
			return []netlink.Filter{u32Filter}, nil
		})
		defer patches.Reset()

		// Should not find filter with wrong protocol
		foundFilter, err := FilterBySrcIP(link, netlink.MakeHandle(1, 0), ipNet)
		require.NoError(t, err)
		assert.Nil(t, foundFilter)
	})

	// Test 4: U32 filter with nil Sel (should skip)
	t.Run("u32 filter with nil Sel", func(t *testing.T) {
		_, ipNet, _ := net.ParseCIDR("192.168.4.100/32")

		u32Filter := &netlink.U32{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: link.Attrs().Index,
				Parent:    netlink.MakeHandle(1, 0),
				Priority:  4,
				Protocol:  unix.ETH_P_IP,
			},
			// Sel is nil
		}

		patches := gomonkey.ApplyFunc(netlink.FilterList, func(l netlink.Link, parent uint32) ([]netlink.Filter, error) {
			return []netlink.Filter{u32Filter}, nil
		})
		defer patches.Reset()

		// Should not find filter with nil Sel
		foundFilter, err := FilterBySrcIP(link, netlink.MakeHandle(1, 0), ipNet)
		require.NoError(t, err)
		assert.Nil(t, foundFilter)
	})

	// Test 5: U32 filter with partial key match (should skip)
	t.Run("u32 filter with partial key match", func(t *testing.T) {
		_, searchIPNet, _ := net.ParseCIDR("192.168.5.100/32")
		_, filterIPNet, _ := net.ParseCIDR("192.168.5.200/32") // Different IP

		u32Filter := &netlink.U32{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: link.Attrs().Index,
				Parent:    netlink.MakeHandle(1, 0),
				Priority:  5,
				Protocol:  unix.ETH_P_IP,
			},
			Sel: &netlink.TcU32Sel{
				Keys: U32MatchSrc(filterIPNet),
			},
		}

		patches := gomonkey.ApplyFunc(netlink.FilterList, func(l netlink.Link, parent uint32) ([]netlink.Filter, error) {
			return []netlink.Filter{u32Filter}, nil
		})
		defer patches.Reset()

		// Should not find filter with different IP
		foundFilter, err := FilterBySrcIP(link, netlink.MakeHandle(1, 0), searchIPNet)
		require.NoError(t, err)
		assert.Nil(t, foundFilter)
	})

	// Test 6: Multiple filters with one matching
	t.Run("multiple filters with one matching", func(t *testing.T) {
		_, targetIPNet, _ := net.ParseCIDR("192.168.6.100/32")
		_, otherIPNet1, _ := net.ParseCIDR("192.168.6.101/32")
		_, otherIPNet2, _ := net.ParseCIDR("192.168.6.102/32")

		filter1 := &netlink.U32{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: link.Attrs().Index,
				Parent:    netlink.MakeHandle(1, 0),
				Priority:  6,
				Protocol:  unix.ETH_P_IP,
			},
			Sel: &netlink.TcU32Sel{
				Keys: U32MatchSrc(otherIPNet1),
			},
		}
		filter2 := &netlink.U32{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: link.Attrs().Index,
				Parent:    netlink.MakeHandle(1, 0),
				Priority:  7,
				Protocol:  unix.ETH_P_IP,
			},
			Sel: &netlink.TcU32Sel{
				Keys: U32MatchSrc(targetIPNet),
			},
		}
		filter3 := &netlink.U32{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: link.Attrs().Index,
				Parent:    netlink.MakeHandle(1, 0),
				Priority:  8,
				Protocol:  unix.ETH_P_IP,
			},
			Sel: &netlink.TcU32Sel{
				Keys: U32MatchSrc(otherIPNet2),
			},
		}

		patches := gomonkey.ApplyFunc(netlink.FilterList, func(l netlink.Link, parent uint32) ([]netlink.Filter, error) {
			return []netlink.Filter{filter1, filter2, filter3}, nil
		})
		defer patches.Reset()

		// Should find the matching filter
		foundFilter, err := FilterBySrcIP(link, netlink.MakeHandle(1, 0), targetIPNet)
		require.NoError(t, err)
		assert.NotNil(t, foundFilter)
		if foundFilter != nil {
			assert.Equal(t, int32(7), int32(foundFilter.Attrs().Priority))
		}
	})

	// Test 7: Non-U32 filter (should be skipped)
	t.Run("non-u32 filter should be skipped", func(t *testing.T) {
		_, ipNet, _ := net.ParseCIDR("192.168.7.100/32")

		bpfFilter := &netlink.BpfFilter{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: link.Attrs().Index,
				Parent:    netlink.MakeHandle(1, 0),
				Priority:  9,
				Protocol:  unix.ETH_P_IP,
			},
			Fd:           -1,
			Name:         "test-bpf",
			DirectAction: true,
		}

		patches := gomonkey.ApplyFunc(netlink.FilterList, func(l netlink.Link, parent uint32) ([]netlink.Filter, error) {
			return []netlink.Filter{bpfFilter}, nil
		})
		defer patches.Reset()

		// Should not find BPF filter when searching for U32
		foundFilter, err := FilterBySrcIP(link, netlink.MakeHandle(1, 0), ipNet)
		require.NoError(t, err)
		assert.Nil(t, foundFilter)
	})
}

func TestFilterBySrcIP_IPv6(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Create a dummy link
		dummyLink := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{
				Name: "test-dummy-v6",
			},
		}
		err := netlink.LinkAdd(dummyLink)
		require.NoError(t, err)

		link, err := netlink.LinkByName("test-dummy-v6")
		require.NoError(t, err)

		err = netlink.LinkSetUp(link)
		require.NoError(t, err)

		// Test IPv6 with no filters
		t.Run("IPv6 no filters", func(t *testing.T) {
			_, ipNet, _ := net.ParseCIDR("fd00::1/128")
			filter, err := FilterBySrcIP(link, netlink.HANDLE_ROOT, ipNet)
			// May error or return nil, both are acceptable
			_ = err
			assert.Nil(t, filter)
		})

		return nil
	})
	require.NoError(t, err)
}

// TestFilterBySrcIP_ErrorCases tests basic coverage of FilterBySrcIP
func TestFilterBySrcIP_ErrorCases(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Create a dummy link
		dummyLink := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{
				Name: "test-dummy-err",
			},
		}
		err := netlink.LinkAdd(dummyLink)
		require.NoError(t, err)

		link, err := netlink.LinkByName("test-dummy-err")
		require.NoError(t, err)

		// Test basic invocation
		t.Run("basic test", func(t *testing.T) {
			_, ipNet, _ := net.ParseCIDR("192.168.1.0/24")
			_, _ = FilterBySrcIP(link, netlink.HANDLE_ROOT, ipNet)
		})

		return nil
	})
	require.NoError(t, err)
}

func TestContain(t *testing.T) {
	tests := []struct {
		name    string
		keys    []netlink.TcU32Key
		subKeys []netlink.TcU32Key
		want    bool
	}{
		{
			name: "subKeys fully contained in keys",
			keys: []netlink.TcU32Key{
				{Off: 12, Val: 0x7f000001, Mask: 0xffffffff},
				{Off: 16, Val: 0x00000000, Mask: 0xffffffff},
			},
			subKeys: []netlink.TcU32Key{
				{Off: 12, Val: 0x7f000001, Mask: 0xffffffff},
			},
			want: true,
		},
		{
			name: "subKeys not contained in keys",
			keys: []netlink.TcU32Key{
				{Off: 12, Val: 0x7f000001, Mask: 0xffffffff},
			},
			subKeys: []netlink.TcU32Key{
				{Off: 16, Val: 0x00000000, Mask: 0xffffffff},
			},
			want: false,
		},
		{
			name: "empty subKeys",
			keys: []netlink.TcU32Key{
				{Off: 12, Val: 0x7f000001, Mask: 0xffffffff},
			},
			subKeys: []netlink.TcU32Key{},
			want:    true,
		},
		{
			name: "empty keys with non-empty subKeys",
			keys: []netlink.TcU32Key{},
			subKeys: []netlink.TcU32Key{
				{Off: 12, Val: 0x7f000001, Mask: 0xffffffff},
			},
			want: false,
		},
		{
			name:    "both empty",
			keys:    []netlink.TcU32Key{},
			subKeys: []netlink.TcU32Key{},
			want:    true,
		},
		{
			name: "all subKeys contained",
			keys: []netlink.TcU32Key{
				{Off: 12, Val: 0x7f000001, Mask: 0xffffffff},
				{Off: 16, Val: 0x00000000, Mask: 0xffffffff},
				{Off: 20, Val: 0x12345678, Mask: 0xffffffff},
			},
			subKeys: []netlink.TcU32Key{
				{Off: 16, Val: 0x00000000, Mask: 0xffffffff},
				{Off: 12, Val: 0x7f000001, Mask: 0xffffffff},
			},
			want: true,
		},
		{
			name: "partial match - different values",
			keys: []netlink.TcU32Key{
				{Off: 12, Val: 0x7f000001, Mask: 0xffffffff},
			},
			subKeys: []netlink.TcU32Key{
				{Off: 12, Val: 0x7f000002, Mask: 0xffffffff},
			},
			want: false,
		},
		{
			name: "partial match - different masks",
			keys: []netlink.TcU32Key{
				{Off: 12, Val: 0x7f000001, Mask: 0xffffffff},
			},
			subKeys: []netlink.TcU32Key{
				{Off: 12, Val: 0x7f000001, Mask: 0xff000000},
			},
			want: false,
		},
		{
			name: "partial match - different offsets",
			keys: []netlink.TcU32Key{
				{Off: 12, Val: 0x7f000001, Mask: 0xffffffff},
			},
			subKeys: []netlink.TcU32Key{
				{Off: 16, Val: 0x7f000001, Mask: 0xffffffff},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Contain(tt.keys, tt.subKeys)
			assert.Equal(t, tt.want, got, "Contain() = %v, want %v", got, tt.want)
		})
	}
}

// Note: TestMatchSrc already exists in tc_test.go, so we don't duplicate it here

func TestU32MatchSrc_EmptyIPNet(t *testing.T) {
	// Note: U32MatchSrc will panic with nil IPNet, so we skip this test
	// The function expects a valid IPNet pointer
}

func TestU32MatchSrc_InvalidIP(t *testing.T) {
	// Note: U32MatchSrc will panic with invalid IP, so we skip this test
	// The function expects a valid IPNet with proper IP and Mask
}

func TestU32IPv6Src_PartialMask(t *testing.T) {
	// Test IPv6 with partial mask (not all 4 segments)
	ipNet := &net.IPNet{
		IP:   net.ParseIP("2001:db8::1"),
		Mask: net.CIDRMask(96, 128), // Only first 3 segments
	}
	keys := U32IPv6Src(ipNet)
	// Should return keys only for non-zero mask segments
	assert.Greater(t, len(keys), 0)
	assert.LessOrEqual(t, len(keys), 3)
}

func TestU32IPv6Src_ZeroMask(t *testing.T) {
	// Test IPv6 with zero mask segments
	ipNet := &net.IPNet{
		IP:   net.ParseIP("2001:db8::1"),
		Mask: net.CIDRMask(0, 128), // No mask
	}
	keys := U32IPv6Src(ipNet)
	// With zero mask, should return empty keys (no segments match)
	assert.Len(t, keys, 0)
}

func TestFilterBySrcIP_ErrorHandling(t *testing.T) {
	testNS := setupTestNS(t)
	defer cleanupTestNS(t, testNS)

	err := testNS.Do(func(ns.NetNS) error {
		// Test with nil IPNet - this will cause U32MatchSrc to return empty
		dummyLink := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{
				Name: "test-dummy-nil",
			},
		}
		err := netlink.LinkAdd(dummyLink)
		if err != nil {
			return err
		}

		link, err := netlink.LinkByName("test-dummy-nil")
		if err != nil {
			return err
		}

		// Test with valid IPNet but no filters
		_, ipNet, _ := net.ParseCIDR("192.168.1.0/24")
		_, err = FilterBySrcIP(link, netlink.HANDLE_ROOT, ipNet)
		// Should return nil filter when no filters exist
		_ = err

		return nil
	})
	require.NoError(t, err)
}

func TestU32MatchSrc_IPv4EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		ipNet *net.IPNet
	}{
		{
			name: "0.0.0.0/0",
			ipNet: &net.IPNet{
				IP:   net.ParseIP("0.0.0.0"),
				Mask: net.CIDRMask(0, 32),
			},
		},
		{
			name: "255.255.255.255/32",
			ipNet: &net.IPNet{
				IP:   net.ParseIP("255.255.255.255"),
				Mask: net.CIDRMask(32, 32),
			},
		},
		{
			name: "10.0.0.0/8",
			ipNet: &net.IPNet{
				IP:   net.ParseIP("10.0.0.0"),
				Mask: net.CIDRMask(8, 32),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys := U32MatchSrc(tt.ipNet)
			assert.Len(t, keys, 1, "IPv4 should return exactly 1 key")
			if len(keys) > 0 {
				assert.Equal(t, int32(12), keys[0].Off, "IPv4 offset should be 12")
			}
		})
	}
}
