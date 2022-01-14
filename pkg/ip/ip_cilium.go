// Copyright 2017-2020 Authors of Cilium
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ip

import (
	"math/big"
	"net"
)

var (
	// v4Mappedv6Prefix is the RFC2765 IPv4-mapped address prefix.
	v4Mappedv6Prefix = []byte{0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0xff, 0xff}
)

func ipNetToRange(ipNet net.IPNet) netWithRange {
	firstIP := make(net.IP, len(ipNet.IP))
	lastIP := make(net.IP, len(ipNet.IP))

	copy(firstIP, ipNet.IP)
	copy(lastIP, ipNet.IP)

	firstIP = firstIP.Mask(ipNet.Mask)
	lastIP = lastIP.Mask(ipNet.Mask)

	if firstIP.To4() != nil {
		firstIP = append(v4Mappedv6Prefix, firstIP...)
		lastIP = append(v4Mappedv6Prefix, lastIP...)
	}

	lastIPMask := make(net.IPMask, len(ipNet.Mask))
	copy(lastIPMask, ipNet.Mask)
	for i := range lastIPMask {
		lastIPMask[len(lastIPMask)-i-1] = ^lastIPMask[len(lastIPMask)-i-1]
		lastIP[net.IPv6len-i-1] = lastIP[net.IPv6len-i-1] | lastIPMask[len(lastIPMask)-i-1]
	}

	return netWithRange{First: &firstIP, Last: &lastIP, Network: &ipNet}
}

// GetIPAtIndex get the IP by index in the range of ipNet. The index is start with 0.
func GetIPAtIndex(ipNet net.IPNet, index int64) net.IP {
	netRange := ipNetToRange(ipNet)
	val := big.NewInt(0)
	var ip net.IP
	if index >= 0 {
		ip = *netRange.First
	} else {
		ip = *netRange.Last
		index += 1
	}
	if ip.To4() != nil {
		val.SetBytes(ip.To4())
	} else {
		val.SetBytes(ip)
	}
	val.Add(val, big.NewInt(index))
	if ipNet.Contains(val.Bytes()) {
		return val.Bytes()
	}
	return nil
}

type netWithRange struct {
	First   *net.IP
	Last    *net.IP
	Network *net.IPNet
}
