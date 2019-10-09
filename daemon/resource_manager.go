package daemon

import (
	"github.com/AliyunContainerService/terway/types"
)

const (
	resDBPath = "/var/lib/cni/terway/ResRelation.db"
	resDBName = "relation"
)

// ResourceItem to be store
type ResourceItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`
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
	Release(context *networkContext, resID string) error
	GarbageCollection(inUseResList map[string]interface{}, expireResList map[string]interface{}) error
}
