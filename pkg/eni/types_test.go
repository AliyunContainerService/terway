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

func TestIP_Release_wrongPodID(t *testing.T) {
	ip := NewValidIP(netip.MustParseAddr("192.0.2.1"), false)
	ip.Allocate("pod-a")
	ip.Release("pod-b")
	if ip.podID != "pod-a" {
		t.Errorf("Release with wrong podID should not clear; got podID %q", ip.podID)
	}
	ip.Release("pod-a")
	if ip.podID != "" {
		t.Errorf("Release with correct podID should clear; got podID %q", ip.podID)
	}
}

func TestSet_PutDeleting(t *testing.T) {
	s := make(Set)
	a1 := netip.MustParseAddr("192.0.2.1")
	a2 := netip.MustParseAddr("192.0.2.2")
	s.PutDeleting(a1, a2)
	if len(s) != 2 {
		t.Fatalf("PutDeleting len = %d, want 2", len(s))
	}
	for _, a := range []netip.Addr{a1, a2} {
		if p, ok := s[a]; !ok || !p.Deleting() {
			t.Errorf("PutDeleting(%s): ok=%v, Deleting=%v", a, ok, p != nil && p.Deleting())
		}
	}
}

func TestSet_ByPodID(t *testing.T) {
	s := make(Set)
	a1 := netip.MustParseAddr("192.0.2.1")
	ip := NewValidIP(a1, false)
	ip.Allocate("my-pod")
	s[a1] = ip
	if got := s.ByPodID("my-pod"); got != ip {
		t.Errorf("ByPodID(my-pod) = %v, want %v", got, ip)
	}
	if got := s.ByPodID("other"); got != nil {
		t.Errorf("ByPodID(other) = %v, want nil", got)
	}
}

func TestSet_PeekAvailable_withPodID(t *testing.T) {
	s := make(Set)
	a1 := netip.MustParseAddr("192.0.2.1")
	a2 := netip.MustParseAddr("192.0.2.2")
	s.PutValid(a1, a2)
	s[a1].Allocate("existing-pod")
	got := s.PeekAvailable("existing-pod")
	if got == nil || got.ip != a1 {
		t.Errorf("PeekAvailable(existing-pod) = %v, want IP %s", got, a1)
	}
}
