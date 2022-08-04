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
	"encoding/binary"
	"net"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

// FilterBySrcIP found u32 filter by pod ip
// used for prio only
func FilterBySrcIP(link netlink.Link, parent uint32, ipNet *net.IPNet) (*netlink.U32, error) {
	filters, err := netlink.FilterList(link, parent)
	if err != nil {
		return nil, err
	}

	matches := U32MatchSrc(ipNet)
	if len(matches) == 0 {
		return nil, nil
	}
OUT:
	for _, f := range filters {
		u32, ok := f.(*netlink.U32)
		if !ok {
			continue
		}
		if u32.Attrs().LinkIndex != link.Attrs().Index ||
			u32.Protocol != unix.ETH_P_IP ||
			u32.Sel == nil {
			continue
		}

		// check all matches is satisfied with current
		for _, m := range matches {
			found := false
			for _, key := range u32.Sel.Keys {
				if key.Off != m.Off || key.Val != m.Val || key.Mask != m.Mask {
					continue
				}
				found = true
				break
			}
			if !found {
				continue OUT
			}
		}
		return u32, nil
	}
	return nil, nil
}

// MatchSrc add match for source ip
func MatchSrc(u32 *netlink.U32, ipNet *net.IPNet) {
	if u32.Sel == nil {
		u32.Sel = &netlink.TcU32Sel{
			Flags: nl.TC_U32_TERMINAL,
		}
	}

	u32.Sel.Keys = append(u32.Sel.Keys, U32MatchSrc(ipNet)...)
	u32.Sel.Nkeys = uint8(len(u32.Sel.Keys))
}

// U32MatchSrc return u32 match key by src ip
func U32MatchSrc(ipNet *net.IPNet) []netlink.TcU32Key {
	if ipNet.IP.To4() == nil {
		return U32IPv6Src(ipNet)
	}
	return []netlink.TcU32Key{U32IPv4Src(ipNet)}
}

func U32IPv4Src(ipNet *net.IPNet) netlink.TcU32Key {
	mask := net.IP(ipNet.Mask).To4()
	val := ipNet.IP.Mask(ipNet.Mask).To4()
	return netlink.TcU32Key{
		Mask: binary.BigEndian.Uint32(mask),
		Val:  binary.BigEndian.Uint32(val),
		Off:  12,
	}
}

func U32IPv6Src(ipNet *net.IPNet) []netlink.TcU32Key {
	mask := ipNet.Mask
	val := ipNet.IP.Mask(ipNet.Mask)

	r := make([]netlink.TcU32Key, 0, 4)
	for i := 0; i < 4; i++ {
		m := binary.BigEndian.Uint32(mask)
		if m != 0 {
			r = append(r, netlink.TcU32Key{
				Mask: m,
				Val:  binary.BigEndian.Uint32(val),
				Off:  int32(8 + 4*i),
			})
		}
		mask = mask[4:]
		val = val[4:]
	}
	return r
}

func Contain(keys []netlink.TcU32Key, subKeys []netlink.TcU32Key) bool {
	for _, subKey := range subKeys {
		found := false

		for _, key := range keys {
			if key.Off != subKey.Off || key.Val != subKey.Val || key.Mask != subKey.Mask {
				continue
			}
			found = true
			break
		}
		if !found {
			return false
		}
	}
	return true
}
