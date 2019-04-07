package types

import (
	"encoding/json"
	"fmt"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"net"
)

const (
	ResourceTypeVeth  = "veth"
	ResourceTypeENI   = "eni"
	ResourceTypeENIIP = "eniIp"
)

type NetConf struct {
	types.NetConf
	IP string `ip:"ip"` //占位，暂不支持。考虑使用pod annotation
}

func NewConf(args *skel.CmdArgs) (*NetConf, error) {
	var cfg NetConf
	if err := json.Unmarshal(args.StdinData, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

type ENI struct {
	ID           string
	Name         string
	Address      net.IPNet
	MAC          string
	Gateway      net.IP
	DeviceNumber int32
	MaxIPs       int
}

func (eni *ENI) GetResourceId() string {
	return eni.MAC
}

func (eni *ENI) GetType() string {
	return ResourceTypeENI
}

type ENIIP struct {
	Eni        *ENI
	SecAddress net.IP
}

func (eniIP *ENIIP) GetResourceId() string {
	return fmt.Sprintf("%s.%s", eniIP.Eni.GetResourceId(), eniIP.SecAddress)
}

func (eniIP *ENIIP) GetType() string {
	return ResourceTypeENIIP
}

type Veth struct {
	HostVeth string
}

func (veth *Veth) GetResourceId() string {
	return veth.HostVeth
}

func (veth *Veth) GetType() string {
	return ResourceTypeVeth
}

type TrafficShappingRule struct {
	ID        string
	Source    string
	Bandwidth string
	Classify  int
}

type NetworkResource interface {
	GetResourceId() string
	GetType() string
}

type VethCreateArgs struct {
	PodName      string
	PodNamespace string
}

type ResourceCreateArgs struct {
	VethCreateArgs *VethCreateArgs
}

type ResourceDestroyArgs struct {
}
