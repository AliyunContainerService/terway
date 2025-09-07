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

func TestToIP(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		want    net.IP
		wantErr bool
	}{
		{
			name:    "Valid IPv4",
			addr:    "192.168.1.1",
			want:    net.ParseIP("192.168.1.1"),
			wantErr: false,
		},
		{
			name:    "Valid IPv6",
			addr:    "2001:db8::1",
			want:    net.ParseIP("2001:db8::1"),
			wantErr: false,
		},
		{
			name:    "Invalid IP",
			addr:    "invalid-ip",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "Empty string",
			addr:    "",
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToIP(tt.addr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToIP() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !got.Equal(tt.want) {
				t.Errorf("ToIP() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToIPAddrs(t *testing.T) {
	tests := []struct {
		name    string
		addrs   []string
		want    []netip.Addr
		wantErr bool
	}{
		{
			name:  "Valid IPv4 addresses",
			addrs: []string{"192.168.1.1", "10.0.0.1"},
			want: []netip.Addr{
				netip.MustParseAddr("192.168.1.1"),
				netip.MustParseAddr("10.0.0.1"),
			},
			wantErr: false,
		},
		{
			name:  "Valid IPv6 addresses",
			addrs: []string{"2001:db8::1", "::1"},
			want: []netip.Addr{
				netip.MustParseAddr("2001:db8::1"),
				netip.MustParseAddr("::1"),
			},
			wantErr: false,
		},
		{
			name:    "Invalid address",
			addrs:   []string{"192.168.1.1", "invalid-ip"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "Empty list",
			addrs:   []string{},
			want:    []netip.Addr{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToIPAddrs(tt.addrs)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToIPAddrs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIPv6(t *testing.T) {
	tests := []struct {
		name string
		ip   net.IP
		want bool
	}{
		{
			name: "IPv4 address",
			ip:   net.ParseIP("192.168.1.1"),
			want: false,
		},
		{
			name: "IPv6 address",
			ip:   net.ParseIP("2001:db8::1"),
			want: true,
		},
		{
			name: "IPv4-mapped IPv6 address",
			ip:   net.ParseIP("::ffff:192.168.1.1"),
			want: true,
		},
		{
			name: "Loopback IPv6",
			ip:   net.ParseIP("::1"),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IPv6(tt.ip)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIPs2str(t *testing.T) {
	tests := []struct {
		name string
		ips  []net.IP
		want []string
	}{
		{
			name: "Multiple IPs",
			ips: []net.IP{
				net.ParseIP("192.168.1.1"),
				net.ParseIP("2001:db8::1"),
			},
			want: []string{"192.168.1.1", "2001:db8::1"},
		},
		{
			name: "Empty list",
			ips:  []net.IP{},
			want: []string{},
		},
		{
			name: "Single IP",
			ips:  []net.IP{net.ParseIP("10.0.0.1")},
			want: []string{"10.0.0.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IPs2str(tt.ips)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDeriveGatewayIP_Extended(t *testing.T) {
	tests := []struct {
		name string
		cidr string
		want string
	}{
		{
			name: "Valid IPv4 CIDR /24",
			cidr: "192.168.1.0/24",
			want: "192.168.1.253",
		},
		{
			name: "Invalid CIDR",
			cidr: "invalid-cidr",
			want: "",
		},
		{
			name: "Empty CIDR",
			cidr: "",
			want: "",
		},
		{
			name: "Small subnet /30",
			cidr: "192.168.1.0/30",
			want: "192.168.1.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveGatewayIP(tt.cidr)
			assert.Equal(t, tt.want, got)
		})
	}
}
