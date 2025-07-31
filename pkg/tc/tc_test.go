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
	"testing"

	"github.com/vishvananda/netlink"
)

func TestTrafficShapingRule_Validation(t *testing.T) {
	tests := []struct {
		name    string
		rate    uint64
		wantErr bool
	}{
		{
			name:    "valid rate",
			rate:    1000000, // 1MB/s
			wantErr: false,
		},
		{
			name:    "zero rate",
			rate:    0,
			wantErr: true,
		},
		{
			name:    "large rate",
			rate:    1000000000, // 1GB/s
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &TrafficShapingRule{
				Rate: tt.rate,
			}

			// Create a dummy link for testing
			dummyLink := &netlink.Dummy{
				LinkAttrs: netlink.LinkAttrs{
					Name:  "test-dummy",
					MTU:   1500,
					Index: 1,
				},
			}

			err := SetRule(dummyLink, rule)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetRule() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTrafficShapingRule_HelperFunctions(t *testing.T) {
	// Test burst calculation
	rate := uint64(1000000) // 1MB/s
	mtu := 1500
	expectedBurst := burst(rate, mtu)
	if expectedBurst <= 0 {
		t.Errorf("burst() returned invalid value: %d", expectedBurst)
	}

	// Test buffer calculation
	expectedBuffer := buffer(rate, expectedBurst)
	if expectedBuffer <= 0 {
		t.Errorf("buffer() returned invalid value: %d", expectedBuffer)
	}

	// Test limit calculation
	latency := latencyInUsec(latencyInMillis)
	expectedLimit := limit(rate, latency, expectedBuffer)
	if expectedLimit <= 0 {
		t.Errorf("limit() returned invalid value: %d", expectedLimit)
	}

	// Test time2Tick conversion
	time := uint32(1000)
	ticks := time2Tick(time)
	if ticks <= 0 {
		t.Errorf("time2Tick() returned invalid value: %d", ticks)
	}

	// Test latency conversion
	latencyMs := 25.0
	latencyUs := latencyInUsec(latencyMs)
	if latencyUs <= 0 {
		t.Errorf("latencyInUsec() returned invalid value: %f", latencyUs)
	}
}

func TestTrafficShapingRule_Integration(t *testing.T) {
	// Test integration with a valid rule
	rule := &TrafficShapingRule{
		Rate: 500000, // 500KB/s
	}

	// Create a dummy link for testing
	dummyLink := &netlink.Dummy{
		LinkAttrs: netlink.LinkAttrs{
			Name:  "test-dummy-integration",
			MTU:   1500,
			Index: 2,
		},
	}

	// Test that SetRule can be called without panicking
	// Note: This test may fail on systems without proper netlink support
	// but it should at least not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("SetRule() panicked: %v", r)
		}
	}()

	// This call may fail due to lack of netlink permissions or dummy interface
	// but it should not panic and should handle errors gracefully
	err := SetRule(dummyLink, rule)
	if err != nil {
		// Error is expected in test environment, just log it
		t.Logf("SetRule() returned expected error in test environment: %v", err)
	}
}

func TestMatchSrc(t *testing.T) {
	tests := []struct {
		name   string
		ipNet  *net.IPNet
		expect int // expected number of keys
	}{
		{
			name: "IPv4 single IP",
			ipNet: &net.IPNet{
				IP:   net.ParseIP("192.168.1.1"),
				Mask: net.CIDRMask(32, 32),
			},
			expect: 1,
		},
		{
			name: "IPv4 subnet",
			ipNet: &net.IPNet{
				IP:   net.ParseIP("192.168.1.0"),
				Mask: net.CIDRMask(24, 32),
			},
			expect: 1,
		},
		{
			name: "IPv6 single IP",
			ipNet: &net.IPNet{
				IP:   net.ParseIP("fd00::1"),
				Mask: net.CIDRMask(128, 128),
			},
			expect: 4,
		},
		{
			name: "IPv6 subnet",
			ipNet: &net.IPNet{
				IP:   net.ParseIP("fd00::"),
				Mask: net.CIDRMask(64, 128),
			},
			expect: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u32 := &netlink.U32{}

			// Test MatchSrc function
			MatchSrc(u32, tt.ipNet)

			// Verify that Sel is created
			if u32.Sel == nil {
				t.Errorf("MatchSrc() did not create Sel")
				return
			}

			// Verify the number of keys matches expectation
			if len(u32.Sel.Keys) != tt.expect {
				t.Errorf("MatchSrc() created %d keys, expected %d", len(u32.Sel.Keys), tt.expect)
			}

			// Verify Nkeys is set correctly
			if u32.Sel.Nkeys != uint8(len(u32.Sel.Keys)) {
				t.Errorf("MatchSrc() Nkeys = %d, expected %d", u32.Sel.Nkeys, len(u32.Sel.Keys))
			}

			// Verify Flags is set
			if u32.Sel.Flags == 0 {
				t.Errorf("MatchSrc() Flags not set")
			}
		})
	}
}

func TestU32MatchSrc(t *testing.T) {
	tests := []struct {
		name   string
		ipNet  *net.IPNet
		expect int // expected number of keys
	}{
		{
			name: "IPv4 single IP",
			ipNet: &net.IPNet{
				IP:   net.ParseIP("10.0.0.1"),
				Mask: net.CIDRMask(32, 32),
			},
			expect: 1,
		},
		{
			name: "IPv4 subnet /24",
			ipNet: &net.IPNet{
				IP:   net.ParseIP("10.0.0.0"),
				Mask: net.CIDRMask(24, 32),
			},
			expect: 1,
		},
		{
			name: "IPv4 subnet /16",
			ipNet: &net.IPNet{
				IP:   net.ParseIP("10.0.0.0"),
				Mask: net.CIDRMask(16, 32),
			},
			expect: 1,
		},
		{
			name: "IPv6 single IP",
			ipNet: &net.IPNet{
				IP:   net.ParseIP("2001:db8::1"),
				Mask: net.CIDRMask(128, 128),
			},
			expect: 4,
		},
		{
			name: "IPv6 subnet /64",
			ipNet: &net.IPNet{
				IP:   net.ParseIP("2001:db8::"),
				Mask: net.CIDRMask(64, 128),
			},
			expect: 2,
		},
		{
			name: "IPv6 subnet /32",
			ipNet: &net.IPNet{
				IP:   net.ParseIP("2001:db8::"),
				Mask: net.CIDRMask(32, 128),
			},
			expect: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys := U32MatchSrc(tt.ipNet)

			// Verify the number of keys matches expectation
			if len(keys) != tt.expect {
				t.Errorf("U32MatchSrc() returned %d keys, expected %d", len(keys), tt.expect)
			}

			// Verify each key has valid values
			for i, key := range keys {
				if key.Mask == 0 {
					t.Errorf("U32MatchSrc() key[%d] has zero mask", i)
				}
				if key.Off < 0 {
					t.Errorf("U32MatchSrc() key[%d] has negative offset: %d", i, key.Off)
				}
			}

			// For IPv4, verify offset is 12
			if tt.ipNet.IP.To4() != nil {
				if len(keys) > 0 && keys[0].Off != 12 {
					t.Errorf("U32MatchSrc() IPv4 key has wrong offset: %d, expected 12", keys[0].Off)
				}
			}

			// For IPv6, verify offsets are in sequence
			if tt.ipNet.IP.To4() == nil {
				for i, key := range keys {
					expectedOff := int32(8 + 4*i)
					if key.Off != expectedOff {
						t.Errorf("U32MatchSrc() IPv6 key[%d] has wrong offset: %d, expected %d", i, key.Off, expectedOff)
					}
				}
			}
		})
	}
}

func TestMatchSrc_WithExistingSel(t *testing.T) {
	// Test MatchSrc with existing Sel
	u32 := &netlink.U32{
		Sel: &netlink.TcU32Sel{
			Keys: []netlink.TcU32Key{
				{Mask: 0xffffffff, Val: 0x0a000001, Off: 12}, // 10.0.0.1
			},
			Nkeys: 1,
			Flags: 0x10, // Some existing flags
		},
	}

	initialKeyCount := len(u32.Sel.Keys)

	// Add another match
	ipNet := &net.IPNet{
		IP:   net.ParseIP("192.168.1.1"),
		Mask: net.CIDRMask(32, 32),
	}

	MatchSrc(u32, ipNet)

	// Verify that keys were appended
	if len(u32.Sel.Keys) != initialKeyCount+1 {
		t.Errorf("MatchSrc() did not append keys correctly: got %d, expected %d", len(u32.Sel.Keys), initialKeyCount+1)
	}

	// Verify Nkeys was updated
	if u32.Sel.Nkeys != uint8(len(u32.Sel.Keys)) {
		t.Errorf("MatchSrc() did not update Nkeys correctly: got %d, expected %d", u32.Sel.Nkeys, len(u32.Sel.Keys))
	}
}
