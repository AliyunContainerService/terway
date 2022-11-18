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
	"fmt"
	"net"
	"strings"

	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/rpc"
)

type InternetChargeType string

// EIP pay type
const (
	PayByBandwidth = InternetChargeType("PayByBandwidth")
	PayByTraffic   = InternetChargeType("PayByTraffic")
)

// network resource type
const (
	ResourceTypeVeth  = "veth"
	ResourceTypeENI   = "eni"
	ResourceTypeENIIP = "eniIp"
	ResourceTypeEIP   = "eip"
)

// Vswitch Selection Policy
const (
	VSwitchSelectionPolicyRandom  = "random"
	VSwitchSelectionPolicyOrdered = "ordered"
)

// IPStack is the ip family type
type IPStack string

// IPStack is the ip family type
const (
	IPStackIPv4 IPStack = "ipv4"
	IPStackDual IPStack = "dual"
	IPStackIPv6 IPStack = "ipv6"
)

type IPFamily struct {
	IPv4 bool
	IPv6 bool
}

// IPAMType how terway deal with ip resource
type IPAMType string

// how terway deal with ip resource
const (
	IPAMTypeCRD       = "crd"
	IPAMTypePreferCRD = "preferCRD"
	IPAMTypeDefault   = ""
)

// ENICapPolicy how eni cap is calculated
type ENICapPolicy string

// how eni cap is calculated
const (
	ENICapPolicyPreferTrunk = "preferTrunk"
	ENICapPolicyDefault     = ""
)

// NewIPFamilyFromIPStack parse IPStack to IPFamily
func NewIPFamilyFromIPStack(ipStack IPStack) *IPFamily {
	f := &IPFamily{}
	switch ipStack {
	case IPStackIPv4:
		f.IPv4 = true
	case IPStackDual:
		f.IPv4 = true
		f.IPv6 = true
	case IPStackIPv6:
		f.IPv6 = true
	}
	return f
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

func MergeIPs(a, b []net.IP) []IPSet {
	result := make([]IPSet, utils.Max(len(a), len(b)))
	for i, ip := range a {
		result[i].IPv4 = ip
	}
	for i, ip := range b {
		result[i].IPv6 = ip
	}
	return result
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

// ENI aliyun ENI resource
type ENI struct {
	ID               string
	MAC              string
	SecurityGroupIDs []string

	Trunk bool

	PrimaryIP IPSet
	GatewayIP IPSet

	VSwitchCIDR IPNetSet

	VSwitchID string
}

// GetResourceID return mac address of eni
func (e *ENI) GetResourceID() string {
	return e.MAC
}

// GetType return type name
func (e *ENI) GetType() string {
	return ResourceTypeENI
}

func (e *ENI) ToResItems() []ResourceItem {
	return []ResourceItem{
		{
			Type:   e.GetType(),
			ID:     e.GetResourceID(),
			ENIID:  e.ID,
			ENIMAC: e.MAC,
			IPv4:   e.PrimaryIP.GetIPv4(),
			IPv6:   e.PrimaryIP.GetIPv6(),
		},
	}
}

// ENIIP aliyun secondary IP resource
type ENIIP struct {
	ENI   *ENI
	IPSet IPSet
}

// GetResourceID return mac address of eni and secondary ip address
func (e *ENIIP) GetResourceID() string {
	return fmt.Sprintf("%s.%s", e.ENI.GetResourceID(), e.IPSet.String())
}

// GetType return type name
func (e *ENIIP) GetType() string {
	return ResourceTypeENIIP
}

func (e *ENIIP) ToResItems() []ResourceItem {
	return []ResourceItem{
		{
			Type:   e.GetType(),
			ID:     e.GetResourceID(),
			ENIID:  e.ENI.ID,
			ENIMAC: e.ENI.MAC,
			IPv4:   e.IPSet.GetIPv4(),
			IPv6:   e.IPSet.GetIPv6(),
		},
	}
}

// Veth veth pair resource on system
type Veth struct {
	HostVeth string
}

// GetResourceID return host veth name of veth resource
func (e *Veth) GetResourceID() string {
	return e.HostVeth
}

// GetType return type name
func (e *Veth) GetType() string {
	return ResourceTypeVeth
}

func (e *Veth) ToResItems() []ResourceItem {
	return []ResourceItem{
		{
			Type: e.GetType(),
			ID:   e.GetResourceID(),
		},
	}
}

// EIP Aliyun public ip
type EIP struct {
	ID             string
	Address        net.IP
	Delete         bool // delete related eip on pod deletion
	AssociateENI   string
	AssociateENIIP net.IP
}

// GetResourceID return eip id
func (e *EIP) GetResourceID() string {
	return e.ID
}

// GetType return type name
func (e *EIP) GetType() string {
	return ResourceTypeEIP
}

func (e *EIP) ToResItems() []ResourceItem {
	return []ResourceItem{
		{
			Type: e.GetType(),
			ID:   e.GetResourceID(),
			ExtraEipInfo: &ExtraEipInfo{
				Delete:         e.Delete,
				AssociateENI:   e.AssociateENI,
				AssociateENIIP: e.AssociateENIIP,
			},
		},
	}
}

// NetworkResource interface of network resources
type NetworkResource interface {
	GetResourceID() string
	GetType() string
	ToResItems() []ResourceItem
}

// Res is the func for res
type Res interface {
	GetID() string
	GetType() string
	GetStatus() ResStatus
}

// ResStatus ResStatus
type ResStatus int

// ResStatus
const (
	ResStatusInvalid ResStatus = iota
	ResStatusIdle
	ResStatusInUse
)

// FakeRes for test
type FakeRes struct {
	ID     string
	Type   string
	Status ResStatus
}

// GetID GetID
func (r *FakeRes) GetID() string {
	return r.ID
}

// GetType GetType
func (r *FakeRes) GetType() string {
	return r.Type
}

// GetStatus GetStatus
func (r *FakeRes) GetStatus() ResStatus {
	return r.Status
}
