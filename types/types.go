/*
Copyright 2018-2021 Terway Authors.

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

package types

import (
	"net"
	"net/netip"
	"strings"

	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/rpc"
)

// IPStack is the ip family type
type IPStack string

// IPStack is the ip family type
const (
	IPStackIPv4 IPStack = "ipv4"
	IPStackDual IPStack = "dual"
	IPStackIPv6 IPStack = "ipv6"
)

// IPAMType how terway deal with ip resource
type IPAMType string

// how terway deal with ip resource
const (
	IPAMTypeCRD       = "crd"
	IPAMTypePreferCRD = "preferCRD"
	IPAMTypeDefault   = ""
)

type IPSet2 struct {
	IPv4 netip.Addr
	IPv6 netip.Addr
}

func (i *IPSet2) String() string {
	var result []string
	if i.IPv4.IsValid() {
		result = append(result, i.IPv4.String())
	}
	if i.IPv6.IsValid() {
		result = append(result, i.IPv6.String())
	}
	return strings.Join(result, "-")
}

func (i *IPSet2) ToRPC() *rpc.IPSet {
	var ipv4, ipv6 string
	if i.IPv4.IsValid() {
		ipv4 = i.IPv4.String()
	}
	if i.IPv6.IsValid() {
		ipv6 = i.IPv6.String()
	}
	return &rpc.IPSet{
		IPv4: ipv4,
		IPv6: ipv6,
	}
}

func (i *IPSet2) GetIPv4() string {
	if !i.IPv4.IsValid() {
		return ""
	}
	return i.IPv4.String()
}

func (i *IPSet2) GetIPv6() string {
	if !i.IPv6.IsValid() {
		return ""
	}
	return i.IPv6.String()
}

// IPSet is the type hole both ipv4 and ipv6 net.IP
type IPSet struct {
	IPv4 net.IP
	IPv6 net.IP
}

func (i *IPSet) String() string {
	var result []string
	if i.IPv4 != nil {
		result = append(result, i.IPv4.String())
	}
	if i.IPv6 != nil {
		result = append(result, i.IPv6.String())
	}
	return strings.Join(result, "-")
}

func (i *IPSet) ToRPC() *rpc.IPSet {
	var ipv4, ipv6 string
	if i.IPv4 != nil {
		ipv4 = i.IPv4.String()
	}
	if i.IPv6 != nil {
		ipv6 = i.IPv6.String()
	}
	return &rpc.IPSet{
		IPv4: ipv4,
		IPv6: ipv6,
	}
}

func (i *IPSet) SetIP(str string) *IPSet {
	ip := net.ParseIP(str)
	if ip == nil {
		return i
	}
	if terwayIP.IPv6(ip) {
		i.IPv6 = ip
		return i
	}
	i.IPv4 = ip
	return i
}

func (i *IPSet) GetIPv4() string {
	if i.IPv4 == nil {
		return ""
	}
	return i.IPv4.String()
}

func (i *IPSet) GetIPv6() string {
	if i.IPv6 == nil {
		return ""
	}
	return i.IPv6.String()
}

type IPNetSet struct {
	IPv4 *net.IPNet
	IPv6 *net.IPNet
}

func (i *IPNetSet) ToRPC() *rpc.IPSet {
	var ipv4, ipv6 string
	if i.IPv4 != nil {
		ipv4 = i.IPv4.String()
	}
	if i.IPv6 != nil {
		ipv6 = i.IPv6.String()
	}
	return &rpc.IPSet{
		IPv4: ipv4,
		IPv6: ipv6,
	}
}

func (i *IPNetSet) String() string {
	if i == nil {
		return ""
	}
	var result []string
	if i.IPv4 != nil {
		result = append(result, i.IPv4.String())
	}
	if i.IPv6 != nil {
		result = append(result, i.IPv6.String())
	}
	return strings.Join(result, "-")
}

func (i *IPNetSet) SetIPNet(str string) *IPNetSet {
	ip, ipNet, err := net.ParseCIDR(str)
	if err != nil {
		return i
	}
	if ip == nil {
		return i
	}
	if terwayIP.IPv6(ip) {
		i.IPv6 = ipNet
		return i
	}
	i.IPv4 = ipNet
	return i
}
