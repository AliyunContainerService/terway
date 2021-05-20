package types

import (
	"fmt"
	"net"
	"strings"

	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/rpc"
)

type InternetChargeType string

// EIP pay type
const (
	PayByBandwidth = InternetChargeType("PayByBandwidth")
	PayByTraffic   = InternetChargeType("PayByTraffic")
)

type EIPInstanceType string

const EIPInstanceTypeNetworkInterface EIPInstanceType = "NetworkInterface"

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
)

type IPFamily struct {
	IPv4 bool
	IPv6 bool
}

func NewIPFamilyFromIPStack(ipStack IPStack) *IPFamily {
	f := &IPFamily{}
	switch ipStack {
	case IPStackIPv4:
		f.IPv4 = true
	case IPStackDual:
		f.IPv4 = true
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

// ENI aliyun ENI resource
type ENI struct {
	ID               string
	MAC              string
	MaxIPs           int
	SecurityGroupIDs []string

	PrimaryIP IPSet
	GatewayIP IPSet

	VSwitchCIDR IPNetSet

	VSwitch string
}

// GetResourceID return mac address of eni
func (eni *ENI) GetResourceID() string {
	return eni.MAC
}

// GetType return type name
func (eni *ENI) GetType() string {
	return ResourceTypeENI
}

// ENIIP aliyun secondary IP resource
type ENIIP struct {
	ENI         *ENI
	SecondaryIP IPSet
}

// GetResourceID return mac address of eni and secondary ip address
func (eniIP *ENIIP) GetResourceID() string {
	return fmt.Sprintf("%s.%s", eniIP.ENI.GetResourceID(), eniIP.SecondaryIP.String())
}

// GetType return type name
func (eniIP *ENIIP) GetType() string {
	return ResourceTypeENIIP
}

// Veth veth pair resource on system
type Veth struct {
	HostVeth string
}

// GetResourceID return host veth name of veth resource
func (veth *Veth) GetResourceID() string {
	return veth.HostVeth
}

// GetType return type name
func (veth *Veth) GetType() string {
	return ResourceTypeVeth
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

// NetworkResource interface of network resources
type NetworkResource interface {
	GetResourceID() string
	GetType() string
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
