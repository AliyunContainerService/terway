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

func TestIP_GetAddr(t *testing.T) {
	addr := netip.MustParseAddr("10.0.0.1")
	ip := NewIP(addr, true)

	result := ip.GetAddr()
	if result != addr {
		t.Errorf("IP.GetAddr() = %v, want %v", result, addr)
	}
}

func TestIP_IsPrimary(t *testing.T) {
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
			result := ip.IsPrimary()
			if result != tt.expected {
				t.Errorf("IP.IsPrimary() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIP_AssignPod(t *testing.T) {
	ip := NewValidIP(netip.MustParseAddr("192.168.1.1"), false)
	podID := "test-pod-123"

	ip.AssignPod(podID)

	if ip.podID != podID {
		t.Errorf("IP.AssignPod() podID = %v, want %v", ip.podID, podID)
	}
}

func TestIP_UnAssignPod(t *testing.T) {
	ip := NewValidIP(netip.MustParseAddr("192.168.1.1"), false)
	ip.AssignPod("test-pod-123")

	ip.UnAssignPod()

	if ip.podID != "" {
		t.Errorf("IP.UnAssignPod() podID = %v, want empty string", ip.podID)
	}
}

func TestIP_GetPodID(t *testing.T) {
	ip := NewValidIP(netip.MustParseAddr("192.168.1.1"), false)
	podID := "test-pod-456"
	ip.AssignPod(podID)

	result := ip.GetPodID()
	if result != podID {
		t.Errorf("IP.GetPodID() = %v, want %v", result, podID)
	}
}

func TestIP_IsAssigned(t *testing.T) {
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
				ip.AssignPod(tt.podID)
			}

			result := ip.IsAssigned()
			if result != tt.expected {
				t.Errorf("IP.IsAssigned() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIP_IsValid(t *testing.T) {
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

			result := ip.IsValid()
			if result != tt.expected {
				t.Errorf("IP.IsValid() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIP_MarkValid(t *testing.T) {
	ip := NewIP(netip.MustParseAddr("192.168.1.1"), false)
	ip.MarkValid()

	if ip.status != ipStatusValid {
		t.Errorf("IP.MarkValid() status = %v, want %v", ip.status, ipStatusValid)
	}
}

func TestIP_MarkInvalid(t *testing.T) {
	ip := NewValidIP(netip.MustParseAddr("192.168.1.1"), false)
	ip.MarkInvalid()

	if ip.status != ipStatusInvalid {
		t.Errorf("IP.MarkInvalid() status = %v, want %v", ip.status, ipStatusInvalid)
	}
}

func TestIP_MarkDeleting(t *testing.T) {
	ip := NewValidIP(netip.MustParseAddr("192.168.1.1"), false)
	ip.MarkDeleting()

	if ip.status != ipStatusDeleting {
		t.Errorf("IP.MarkDeleting() status = %v, want %v", ip.status, ipStatusDeleting)
	}
}
