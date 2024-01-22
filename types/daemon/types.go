package daemon

import (
	"fmt"

	"github.com/AliyunContainerService/terway/types"
)

const (
	PodNetworkTypeVPCIP      = "VPCIP"
	PodNetworkTypeVPCENI     = "VPCENI"
	PodNetworkTypeENIMultiIP = "ENIMultiIP"
)

// DEPRECATED
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
const (
	ModeVPC        = "VPC"
	ModeENIMultiIP = "ENIMultiIP"
	ModeENIOnly    = "ENIOnly"
)

// Vswitch Selection Policy
const (
	VSwitchSelectionPolicyRandom  = "random"
	VSwitchSelectionPolicyOrdered = "ordered"
)

// ENICapPolicy how eni cap is calculated
type ENICapPolicy string

// how eni cap is calculated
const (
	ENICapPolicyPreferTrunk = "preferTrunk"
	ENICapPolicyDefault     = ""
)

// ENI aliyun ENI resource
type ENI struct {
	ID               string
	MAC              string
	SecurityGroupIDs []string

	Trunk bool
	ERdma bool

	PrimaryIP types.IPSet
	GatewayIP types.IPSet

	VSwitchCIDR types.IPNetSet

	VSwitchID string

	Type string
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
	IPSet types.IPSet
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
