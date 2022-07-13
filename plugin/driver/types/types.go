package types

import (
	"net"
	"strings"

	"github.com/AliyunContainerService/terway/plugin/terway/cni"
	terwayTypes "github.com/AliyunContainerService/terway/types"

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

	// RuntimeConfig represents the options to be passed in by the runtime.
	RuntimeConfig cni.RuntimeConfig `json:"runtimeConfig"`

	BandwidthMode string `json:"bandwidth_mode"`

	// EnableNetworkPriority by enable priority control, eni qdisc is replaced with tc_prio
	EnableNetworkPriority bool `json:"enable_network_priority"`

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
	ExtraRoutes []cniTypes.Route

	ServiceCIDR *terwayTypes.IPNetSet
	HostIPSet   *terwayTypes.IPNetSet
	// ipvlan
	HostStackCIDRs []*net.IPNet

	BandwidthMode string
	Ingress       uint64
	Egress        uint64

	EnableNetworkPriority bool
	NetworkPriority       uint32

	RuntimeConfig cni.RuntimeConfig

	// for windows
	AssistantContainerIPNet *terwayTypes.IPNetSet
	AssistantGatewayIP      *terwayTypes.IPSet
}

type TeardownCfg struct {
	DP DataPath

	HostVETHName string

	ENIIndex int

	ContainerIfName string
	ContainerIPNet  *terwayTypes.IPNetSet

	ServiceCIDR *terwayTypes.IPNetSet

	EnableNetworkPriority bool
}
