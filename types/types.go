package types

import (
	"fmt"
	"net"
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
func (e *ENI) GetResourceID() string {
	return e.MAC
}

// GetType return type name
func (e *ENI) GetType() string {
	return ResourceTypeENI
}

// ENIIP aliyun secondary IP resource
type ENIIP struct {
	Eni        *ENI
	SecAddress net.IP
	PrimaryIP  net.IP
}

// GetResourceID return mac address of eni and secondary ip address
func (e *ENIIP) GetResourceID() string {
	return fmt.Sprintf("%s.%s", e.Eni.GetResourceID(), e.SecAddress)
}

// GetType return type name
func (e *ENIIP) GetType() string {
	return ResourceTypeENIIP
}

// Veth veth pair resource on system
type Veth struct {
	HostVeth string
}

// GetResourceID return host veth name of veth resource
func (v *Veth) GetResourceID() string {
	return v.HostVeth
}

// GetType return type name
func (v *Veth) GetType() string {
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

var _ NetworkResource = (*ENI)(nil)
var _ NetworkResource = (*ENIIP)(nil)
var _ NetworkResource = (*Veth)(nil)
var _ NetworkResource = (*EIP)(nil)

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
