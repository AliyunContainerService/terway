package daemon

import (
	"github.com/AliyunContainerService/terway/pkg/tracing"

	"github.com/AliyunContainerService/terway/types"
)

const (
	resDBPath = "/var/lib/cni/terway/ResRelation.db"
	resDBName = "relation"
)

type resourceManagerInitItem struct {
	item    types.ResourceItem
	podInfo *types.PodInfo
}

// ResourceManager Allocate/Release/Pool/Stick/GC pod resource
// managed pod and resource relationship
type ResourceManager interface {
	Allocate(context *networkContext, prefer string) (types.NetworkResource, error)
	Release(context *networkContext, resItem types.ResourceItem) error
	GarbageCollection(inUseResSet map[string]types.ResourceItem, expireResSet map[string]types.ResourceItem) error
	Stat(context *networkContext, resID string) (types.NetworkResource, error)
	tracing.ResourceMappingHandler
}
