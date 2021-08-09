package ip

import (
	"net"
	"testing"
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
