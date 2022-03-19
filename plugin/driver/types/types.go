package types

import (
	"net"
	"strings"

	terwayTypes "github.com/AliyunContainerService/terway/types"
	"github.com/containernetworking/plugins/pkg/ns"

	"github.com/containernetworking/cni/pkg/types"
	cniTypes "github.com/containernetworking/cni/pkg/types"
)

// CNIConf is the cni network config
type CNIConf struct {
	cniTypes.NetConf

	// HostVethPrefix is the veth for container prefix on host
	HostVethPrefix string `json:"veth_prefix"`

	// eniIPVirtualType is the ipvlan for container
	ENIIPVirtualType string `json:"eniip_virtual_type"`

	// HostStackCIDRs is a list of CIDRs, all traffic targeting these CIDRs will be redirected to host network stack
	HostStackCIDRs []string `json:"host_stack_cidrs"`

	DisableHostPeer bool `yaml:"disable_host_peer" json:"disable_host_peer"` // disable create peer for host and container. This will also disable ability for service

	VlanStripType VlanStripType `yaml:"vlan_strip_type" json:"vlan_strip_type"` // used in multi ip mode, how datapath config vlan

	// MTU is container and ENI network interface MTU
	MTU int `json:"mtu"`

	// Debug
	Debug bool `json:"debug"`
}

func (n *CNIConf) IPVlan() bool {
	return strings.ToLower(n.ENIIPVirtualType) == "ipvlan"
}

// VlanStripType how datapath handle vlan
type VlanStripType string

// how datapath handle vlan
const (
	VlanStripTypeFilter = "filter"
	VlanStripTypeVlan   = "vlan"
)

type DataPath int

// datapath terway supported
const (
	VPCRoute DataPath = iota
	PolicyRoute
	IPVlan
	ExclusiveENI
	Vlan
)

// K8SArgs is cni args of kubernetes
type K8SArgs struct {
	cniTypes.CommonArgs
	IP                         net.IP
	K8S_POD_NAME               cniTypes.UnmarshallableString // nolint
	K8S_POD_NAMESPACE          cniTypes.UnmarshallableString // nolint
	K8S_POD_INFRA_CONTAINER_ID cniTypes.UnmarshallableString // nolint
}

type RecordPodEvent func(msg string)

type SetupConfig struct {
	DP DataPath

	HostVETHName string

	ContainerIfName string
	ContainerIPNet  *terwayTypes.IPNetSet
	GatewayIP       *terwayTypes.IPSet
	MTU             int
	ENIIndex        int
	ENIGatewayIP    *terwayTypes.IPSet

	// disable create peer for exclusiveENI
	DisableCreatePeer bool

	// StripVlan or use vlan
	StripVlan bool
	Vid       int

	DefaultRoute bool
	MultiNetwork bool

	// add extra route in container
	ExtraRoutes []types.Route

	ServiceCIDR *terwayTypes.IPNetSet
	HostIPSet   *terwayTypes.IPNetSet
	// ipvlan
	HostStackCIDRs []*net.IPNet

	Ingress uint64
	Egress  uint64
}

type TeardownCfg struct {
	DP DataPath

	HostVETHName string

	ContainerIfName string
	ContainerIPNet  *terwayTypes.IPNetSet
}

type CheckConfig struct {
	DP DataPath

	RecordPodEvent

	NetNS ns.NetNS

	HostVETHName    string
	ContainerIfName string

	ContainerIPNet *terwayTypes.IPNetSet
	HostIPSet      *terwayTypes.IPNetSet
	GatewayIP      *terwayTypes.IPSet

	ENIIndex int32 // phy device
	TrunkENI bool
	MTU      int

	DefaultRoute bool
	MultiNetwork bool
}
