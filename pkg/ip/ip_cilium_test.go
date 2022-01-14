package ip

import (
	"net"
	"testing"
)

func TestGetIPAtIndex(t *testing.T) {
	type args struct {
		cidr  string
		index int64
		want  net.IP
	}

	tests := []args{
		{
			cidr:  "10.0.0.0/29",
			index: -1,
			want:  net.ParseIP("10.0.0.7"),
		}, {
			cidr:  "10.0.0.0/29",
			index: 0,
			want:  net.ParseIP("10.0.0.0"),
		}, {
			cidr:  "10.0.0.0/29",
			index: 1,
			want:  net.ParseIP("10.0.0.1"),
		}, {
			cidr:  "10.0.0.16/28",
			index: -3,
			want:  net.ParseIP("10.0.0.29"),
		}, {
			cidr:  "10.0.0.0/29",
			index: -3,
			want:  net.ParseIP("10.0.0.5"),
		}, {
			cidr:  "10.0.0.0/25",
			index: -3,
			want:  net.ParseIP("10.0.0.125"),
		}, {
			cidr:  "10.0.0.128/25",
			index: -3,
			want:  net.ParseIP("10.0.0.253"),
		}, {
			cidr:  "10.0.8.0/21",
			index: -3,
			want:  net.ParseIP("10.0.15.253"),
		}, {
			cidr:  "fd00::/64",
			index: -3,
			want:  net.ParseIP("fd00::ffff:ffff:ffff:fffd"),
		},
	}
	for _, tt := range tests {
		_, ipNet, _ := net.ParseCIDR(tt.cidr)
		if got := GetIPAtIndex(*ipNet, tt.index); !got.Equal(tt.want) {
			t.Errorf("GetIPAtIndex() = %v, want %v", got, tt.want)
		}

	}
}
