package daemon

import (
	"net"

	"github.com/AliyunContainerService/terway/pkg/tracing"

	"github.com/AliyunContainerService/terway/types"
)

const (
	resDBPath = "/var/lib/cni/terway/ResRelation.db"
	resDBName = "relation"
)

// ExtraEipInfo store extra eip info
// To judge whether delete user eip instance
type ExtraEipInfo struct {
	Delete         bool   `json:"delete"` // delete related eip on pod deletion
	AssociateENI   string `json:"associate_eni"`
	AssociateENIIP net.IP `json:"associate_eniip"`
}

// ResourceItem to be store
type ResourceItem struct {
	Type         string        `json:"type"`
	ID           string        `json:"id"`
	ExtraEipInfo *ExtraEipInfo `json:"extra_eip_info"`
}

// PodResources pod resources related
type PodResources struct {
	Resources []ResourceItem
	PodInfo   *podInfo
}

type resourceManagerInitItem struct {
	resourceID string
	podInfo    *podInfo
}

// GetResourceItemByType get pod resource by resource type
func (p PodResources) GetResourceItemByType(resType string) []ResourceItem {
	var ret []ResourceItem
	for _, r := range p.Resources {
		if resType == r.Type {
			ret = append(ret, ResourceItem{Type: resType, ID: r.ID})
		}
	}
	return ret
}

// ResourceManager Allocate/Release/Pool/Stick/GC pod resource
// managed pod and resource relationship
type ResourceManager interface {
	Allocate(context *networkContext, prefer string) (types.NetworkResource, error)
	Release(context *networkContext, resItem ResourceItem) error
	GarbageCollection(inUseResSet map[string]ResourceItem, expireResSet map[string]ResourceItem) error
	GetResourceMapping() ([]tracing.ResourceMapping, error)
}
