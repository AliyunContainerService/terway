package cni

import "github.com/containernetworking/cni/pkg/types"

type RuntimeDNS struct {
	Nameservers []string `json:"servers,omitempty"`
	Search      []string `json:"searches,omitempty"`
	Options     []string `json:"options,omitempty"`
}

func (i *RuntimeDNS) AsCNIDns() types.DNS {
	return types.DNS{
		Nameservers: i.Nameservers,
		Search:      i.Search,
		Options:     i.Options,
	}
}

type RuntimePortMapEntry struct {
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
	HostIP        string `json:"hostIP,omitempty"`
}

type RuntimeBandwidthEntry struct {
	// IngressRate is the bandwidth rate in bits per second for traffic through container. 0 for no limit. If IngressRate is set, IngressBurst must also be set
	IngressRate int `json:"ingressRate,omitempty"`
	// IngressBurst is the bandwidth burst in bits for traffic through container. 0 for no limit. If IngressBurst is set, IngressRate must also be set
	// NOTE: it's not used for now and defaults to 0. If IngressRate is set IngressBurst will be math.MaxInt32 ~ 2Gbit
	IngressBurst int `json:"ingressBurst,omitempty"`
	// EgressRate is the bandwidth is the bandwidth rate in bits per second for traffic through container. 0 for no limit. If EgressRate is set, EgressBurst must also be set
	EgressRate int `json:"egressRate,omitempty"`
	// EgressBurst is the bandwidth burst in bits for traffic through container. 0 for no limit. If EgressBurst is set, EgressRate must also be set
	// NOTE: it's not used for now and defaults to 0. If EgressRate is set EgressBurst will be math.MaxInt32 ~ 2Gbit
	EgressBurst int `json:"egressBurst,omitempty"`
}

type RuntimeConfig struct {
	DNS       RuntimeDNS            `json:"dns"`
	PortMaps  []RuntimePortMapEntry `json:"portMappings,omitempty"`
	Bandwidth RuntimeBandwidthEntry `json:"bandwidth,omitempty"`
}
