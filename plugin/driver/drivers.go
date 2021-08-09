package driver

import (
	"net"

	terwayTypes "github.com/AliyunContainerService/terway/types"

	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/plugins/pkg/ns"
)

type RecordPodEvent func(msg string)

type SetupConfig struct {
	HostVETHName string

	ContainerIfName string
	ContainerIPNet  *terwayTypes.IPNetSet
	GatewayIP       *terwayTypes.IPSet
	MTU             int
	ENIIndex        int
	TrunkENI        bool

	// add extra route in container
	ExtraRoutes []types.Route

	ServiceCIDR *terwayTypes.IPNetSet
	// ipvlan
	HostStackCIDRs []*net.IPNet

	Ingress uint64
	Egress  uint64
}

type TeardownCfg struct {
	HostVETHName string

	ContainerIfName string
	ContainerIPNet  *terwayTypes.IPNetSet
}

type CheckConfig struct {
	RecordPodEvent

	NetNS ns.NetNS

	HostVETHName    string
	ContainerIFName string

	ContainerIPNet *terwayTypes.IPNetSet
	GatewayIP      *terwayTypes.IPSet

	ENIIndex int32 // phy device
	TrunkENI bool
	MTU      int
}

// NetnsDriver to config container netns interface and routes
type NetnsDriver interface {
	Setup(cfg *SetupConfig, netNS ns.NetNS) error

	Teardown(cfg *TeardownCfg, netNS ns.NetNS) error

	Check(cfg *CheckConfig) error
}
