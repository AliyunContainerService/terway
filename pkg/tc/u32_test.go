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
	"reflect"
	"testing"

	"github.com/vishvananda/netlink"
)

func TestU32IPv4Src(t *testing.T) {
	type args struct {
		ipNet *net.IPNet
	}
	tests := []struct {
		name string
		args args
		want netlink.TcU32Key
	}{
		{
			name: "127.0.0.1/32",
			args: args{ipNet: &net.IPNet{
				IP:   net.ParseIP("127.0.0.1"),
				Mask: net.CIDRMask(32, 32),
			}},
			want: netlink.TcU32Key{
				Mask:    0xffffffff,
				Val:     0x7f000001,
				Off:     12,
				OffMask: 0,
			},
		},
		{
			name: "127.0.0.0/8",
			args: args{ipNet: &net.IPNet{
				IP:   net.ParseIP("127.0.0.1"),
				Mask: net.CIDRMask(8, 32),
			}},
			want: netlink.TcU32Key{
				Mask:    0xff000000,
				Val:     0x7f000000,
				Off:     12,
				OffMask: 0,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := U32IPv4Src(tt.args.ipNet); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("U32IPv4Src() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestU32IPv6Src(t *testing.T) {
	type args struct {
		ipNet *net.IPNet
	}
	tests := []struct {
		name string
		args args
		want []netlink.TcU32Key
	}{
		{
			name: "fd00::1/128",
			args: args{ipNet: &net.IPNet{
				IP:   net.ParseIP("fd00::1"),
				Mask: net.CIDRMask(128, 128),
			}},
			want: []netlink.TcU32Key{
				{
					Mask:    0xffffffff,
					Val:     0xfd000000,
					Off:     8,
					OffMask: 0,
				}, {
					Mask:    0xffffffff,
					Val:     0x00000000,
					Off:     12,
					OffMask: 0,
				}, {
					Mask:    0xffffffff,
					Val:     0x00000000,
					Off:     16,
					OffMask: 0,
				}, {
					Mask:    0xffffffff,
					Val:     0x00000001,
					Off:     20,
					OffMask: 0,
				},
			},
		}, {
			name: "fd:aaaa::/64",
			args: args{ipNet: &net.IPNet{
				IP:   net.ParseIP("fd:aaaa::"),
				Mask: net.CIDRMask(64, 128),
			}},
			want: []netlink.TcU32Key{
				{
					Mask:    0xffffffff,
					Val:     0x00fdaaaa,
					Off:     8,
					OffMask: 0,
				}, {
					Mask:    0xffffffff,
					Val:     0x00000000,
					Off:     12,
					OffMask: 0,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := U32IPv6Src(tt.args.ipNet); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("U32IPv6Src() = %v, want %v", got, tt.want)
			}
		})
	}
}
