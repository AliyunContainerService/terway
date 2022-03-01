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

type RuntimeConfig struct {
	DNS      RuntimeDNS            `json:"dns"`
	PortMaps []RuntimePortMapEntry `json:"portMappings,omitempty"`
}
