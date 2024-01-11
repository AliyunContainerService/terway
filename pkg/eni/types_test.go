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
