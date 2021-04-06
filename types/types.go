package types

import (
	"fmt"
	"net"
)

type InternetChargeType string

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

// ENI aliyun ENI resource
type ENI struct {
	ID               string
	Name             string
	Address          net.IPNet
	MAC              string
	Gateway          net.IP
	DeviceNumber     int32
	MaxIPs           int
	VSwitch          string
	SecurityGroupIDs []string
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
	Eni        *ENI
	SecAddress net.IP
	PrimaryIP  net.IP
}

// GetResourceID return mac address of eni and secondary ip address
func (eniIP *ENIIP) GetResourceID() string {
	return fmt.Sprintf("%s.%s", eniIP.Eni.GetResourceID(), eniIP.SecAddress)
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
