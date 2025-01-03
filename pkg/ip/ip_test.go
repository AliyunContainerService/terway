package ip

import (
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_ipIntersect(t *testing.T) {
	type args struct {
		a []net.IP
		b []net.IP
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "intersect",
			args: args{
				a: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("127.0.0.3")},
				b: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("127.0.0.2")},
			},
			want: true,
		}, {
			name: "not intersect",
			args: args{
				a: []net.IP{net.ParseIP("127.0.0.4"), net.ParseIP("127.0.0.3")},
				b: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("127.0.0.2")},
			},
			want: false,
		}, {
			name: "nil val 1",
			args: args{
				a: []net.IP{net.ParseIP("127.0.0.3")},
				b: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("127.0.0.2")},
			},
			want: false,
		}, {
			name: "nil val 2",
			args: args{
				a: []net.IP{net.ParseIP("127.0.0.1")},
				b: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("127.0.0.2")},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IPsIntersect(tt.args.a, tt.args.b); got != tt.want {
				t.Errorf("IPsIntersect() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIPAddrs2str_MultipleValidIPs_ReturnsCorrectStrings(t *testing.T) {
	ip1, _ := netip.ParseAddr("192.0.2.1")
	ip2, _ := netip.ParseAddr("192.0.2.2")
	input := []netip.Addr{ip1, ip2}
	expected := []string{"192.0.2.1", "192.0.2.2"}
	result := IPAddrs2str(input)
	if len(result) != len(expected) {
		t.Errorf("Expected %v, got %v", expected, result)
	}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("Expected %v, got %v", expected, result)
		}
	}
}

func TestDeriveGatewayIP(t *testing.T) {
	assert.Equal(t, "192.168.0.253", DeriveGatewayIP("192.168.0.0/24"))
}
