package eni

import (
	"net/netip"
	"reflect"
	"sort"
	"testing"
)

func Test_syncIPLocked(t *testing.T) {
	type args struct {
		lo     Set
		remote []netip.Addr
	}
	tests := []struct {
		name   string
		args   args
		expect Set
	}{
		{
			name: "test invalid ip",
			args: args{
				lo: Set{
					netip.MustParseAddr("127.0.0.1"): &IP{
						ip:      netip.MustParseAddr("127.0.0.1"),
						primary: true,
						status:  ipStatusValid,
					},
				},
				remote: nil,
			},
			expect: Set{
				netip.MustParseAddr("127.0.0.1"): &IP{
					ip:      netip.MustParseAddr("127.0.0.1"),
					primary: true,
					status:  ipStatusInvalid,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncIPLocked(tt.args.lo, tt.args.remote)
			if !reflect.DeepEqual(tt.args.lo, tt.expect) {
				t.Errorf("syncIPLocked() = %v, want %v", tt.args.lo, tt.expect)
			}
		})
	}
}

func TestSet_Allocatable(t *testing.T) {
	tests := []struct {
		name     string
		set      Set
		expected []*IP
	}{
		{
			name: "Allocatable IPs",
			set: Set{
				netip.MustParseAddr("192.0.2.1"): NewValidIP(netip.MustParseAddr("192.0.2.1"), false),
				netip.MustParseAddr("192.0.2.2"): NewValidIP(netip.MustParseAddr("192.0.2.2"), false),
			},
			expected: []*IP{
				NewValidIP(netip.MustParseAddr("192.0.2.1"), false),
				NewValidIP(netip.MustParseAddr("192.0.2.2"), false),
			},
		},
		{
			name: "No Allocatable IPs",
			set: Set{
				netip.MustParseAddr("192.0.2.1"): &IP{
					ip:      netip.MustParseAddr("192.0.2.1"),
					primary: false,
					status:  ipStatusDeleting,
				},
				netip.MustParseAddr("192.0.2.2"): &IP{
					ip:      netip.MustParseAddr("192.0.2.2"),
					primary: false,
					status:  ipStatusInvalid,
				},
			},
			expected: nil,
		},
		{
			name:     "Empty Set",
			set:      Set{},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.set.Allocatable()
			sort.SliceStable(result, func(i, j int) bool {
				return result[i].ip.Compare(result[j].ip) < 0
			})
			sort.SliceStable(tt.expected, func(i, j int) bool {
				return tt.expected[i].ip.Compare(tt.expected[j].ip) < 0
			})
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Allocatable() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIP_String(t *testing.T) {
	tests := []struct {
		name     string
		ip       *IP
		expected string
	}{
		{
			name:     "Nil IP",
			ip:       nil,
			expected: "",
		},
		{
			name:     "Valid IPv4",
			ip:       NewIP(netip.MustParseAddr("192.168.1.1"), false),
			expected: "192.168.1.1",
		},
		{
			name:     "Valid IPv6",
			ip:       NewIP(netip.MustParseAddr("2001:db8::1"), false),
			expected: "2001:db8::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.ip.String()
			if result != tt.expected {
				t.Errorf("IP.String() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestNewIP(t *testing.T) {
	addr := netip.MustParseAddr("192.168.1.1")
	ip := NewIP(addr, true)

	if ip.ip != addr {
		t.Errorf("NewIP() ip = %v, want %v", ip.ip, addr)
	}
	if ip.primary != true {
		t.Errorf("NewIP() primary = %v, want %v", ip.primary, true)
	}
	if ip.status != ipStatusInit {
		t.Errorf("NewIP() status = %v, want %v", ip.status, ipStatusInit)
	}
}

func TestNewValidIP(t *testing.T) {
	addr := netip.MustParseAddr("192.168.1.1")
	ip := NewValidIP(addr, false)

	if ip.ip != addr {
		t.Errorf("NewValidIP() ip = %v, want %v", ip.ip, addr)
	}
	if ip.primary != false {
		t.Errorf("NewValidIP() primary = %v, want %v", ip.primary, false)
	}
	if ip.status != ipStatusValid {
		t.Errorf("NewValidIP() status = %v, want %v", ip.status, ipStatusValid)
	}
}

func TestIP_IPField(t *testing.T) {
	addr := netip.MustParseAddr("10.0.0.1")
	ip := NewIP(addr, true)

	// Test the String() method which accesses the ip field
	result := ip.String()
	if result != addr.String() {
		t.Errorf("IP.String() = %v, want %v", result, addr.String())
	}
}

func TestIP_Primary(t *testing.T) {
	tests := []struct {
		name     string
		primary  bool
		expected bool
	}{
		{
			name:     "Primary IP",
			primary:  true,
			expected: true,
		},
		{
			name:     "Secondary IP",
			primary:  false,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := NewIP(netip.MustParseAddr("192.168.1.1"), tt.primary)
			result := ip.Primary()
			if result != tt.expected {
				t.Errorf("IP.Primary() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIP_Allocate(t *testing.T) {
	ip := NewValidIP(netip.MustParseAddr("192.168.1.1"), false)
	podID := "test-pod-123"

	ip.Allocate(podID)

	if ip.podID != podID {
		t.Errorf("IP.Allocate() podID = %v, want %v", ip.podID, podID)
	}
}

func TestIP_Release(t *testing.T) {
	ip := NewValidIP(netip.MustParseAddr("192.168.1.1"), false)
	podID := "test-pod-123"
	ip.Allocate(podID)

	ip.Release(podID)

	if ip.podID != "" {
		t.Errorf("IP.Release() podID = %v, want empty string", ip.podID)
	}
}

func TestIP_PodIDAccess(t *testing.T) {
	ip := NewValidIP(netip.MustParseAddr("192.168.1.1"), false)
	podID := "test-pod-456"
	ip.Allocate(podID)

	// Test that podID is correctly set by checking InUse()
	result := ip.InUse()
	if !result {
		t.Errorf("IP should be InUse after allocation")
	}
}

func TestIP_InUse(t *testing.T) {
	tests := []struct {
		name     string
		podID    string
		expected bool
	}{
		{
			name:     "Assigned IP",
			podID:    "test-pod-123",
			expected: true,
		},
		{
			name:     "Unassigned IP",
			podID:    "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := NewValidIP(netip.MustParseAddr("192.168.1.1"), false)
			if tt.podID != "" {
				ip.Allocate(tt.podID)
			}

			result := ip.InUse()
			if result != tt.expected {
				t.Errorf("IP.InUse() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIP_Valid(t *testing.T) {
	tests := []struct {
		name     string
		status   ipStatus
		expected bool
	}{
		{
			name:     "Valid IP",
			status:   ipStatusValid,
			expected: true,
		},
		{
			name:     "Invalid IP",
			status:   ipStatusInvalid,
			expected: false,
		},
		{
			name:     "Init IP",
			status:   ipStatusInit,
			expected: false,
		},
		{
			name:     "Deleting IP",
			status:   ipStatusDeleting,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := NewIP(netip.MustParseAddr("192.168.1.1"), false)
			ip.status = tt.status

			result := ip.Valid()
			if result != tt.expected {
				t.Errorf("IP.Valid() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIP_SetInvalid(t *testing.T) {
	ip := NewValidIP(netip.MustParseAddr("192.168.1.1"), false)
	ip.SetInvalid()

	if ip.status != ipStatusInvalid {
		t.Errorf("IP.SetInvalid() status = %v, want %v", ip.status, ipStatusInvalid)
	}
}

func TestIP_Dispose(t *testing.T) {
	ip := NewValidIP(netip.MustParseAddr("192.168.1.1"), false)
	ip.Dispose()

	if ip.status != ipStatusDeleting {
		t.Errorf("IP.Dispose() status = %v, want %v", ip.status, ipStatusDeleting)
	}
}

func TestIP_Dispose_Primary(t *testing.T) {
	// Primary IP should not be disposed
	ip := NewValidIP(netip.MustParseAddr("192.168.1.1"), true)
	originalStatus := ip.status
	ip.Dispose()

	if ip.status != originalStatus {
		t.Errorf("Primary IP.Dispose() should not change status, got %v", ip.status)
	}
}
