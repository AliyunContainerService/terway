package types

import (
	"net"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIPSet_SetIP(t *testing.T) {
	ipSet := &IPSet{}
	assert.Equal(t, "127.0.0.1", ipSet.SetIP("127.0.0.1").IPv4.String())
	assert.NotNil(t, ipSet.IPv4)
	assert.Equal(t, "127.0.0.1", ipSet.SetIP("127.0.0.x").IPv4.String())
	assert.Nil(t, ipSet.IPv6)
	assert.Equal(t, "fd00::100", ipSet.SetIP("fd00::100").IPv6.String())
	assert.NotNil(t, ipSet.IPv6)
}

func TestMergeIPs(t *testing.T) {
	type args struct {
		a []net.IP
		b []net.IP
	}
	tests := []struct {
		name string
		args args
		want []IPSet
	}{
		{
			name: "ipv4",
			args: args{
				a: []net.IP{net.ParseIP("127.0.0.1")},
				b: nil,
			},
			want: []IPSet{
				{
					IPv4: net.ParseIP("127.0.0.1"),
				},
			},
		},
		{
			name: "ipv6",
			args: args{
				a: nil,
				b: []net.IP{net.ParseIP("::1")},
			},
			want: []IPSet{
				{
					IPv6: net.ParseIP("::1"),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MergeIPs(tt.args.a, tt.args.b); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MergeIPs() = %v, want %v", got, tt.want)
			}
		})
	}

	result := MergeIPs([]net.IP{net.ParseIP("127.0.0.1")},
		[]net.IP{net.ParseIP("::1"), net.ParseIP("fd::1")})
	assert.Equal(t, 2, len(result))
}

func TestIPNetSet_SetIPNet(t *testing.T) {
	ipNetSet := &IPNetSet{}
	assert.Equal(t, "127.0.0.1/32", ipNetSet.SetIPNet("127.0.0.1/32").IPv4.String())
	assert.NotNil(t, ipNetSet.IPv4)
	assert.Equal(t, "127.0.0.0/24", ipNetSet.SetIPNet("127.0.0.1/24").IPv4.String())
	assert.NotNil(t, ipNetSet.IPv4)
	assert.Equal(t, "127.0.0.0/24", ipNetSet.SetIPNet("127.0.0.x").IPv4.String(), "no change")
	assert.Nil(t, ipNetSet.IPv6)
	assert.Equal(t, "fd00::/120", ipNetSet.SetIPNet("fd00::/120").IPv6.String())
	assert.NotNil(t, ipNetSet.IPv6)
}
