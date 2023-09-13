package daemon

import (
	"github.com/AliyunContainerService/terway/pkg/tracing"

	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/types"
)

const (
	defaultPrefix = "cali"
)

type vethResourceManager struct {
}

func (*vethResourceManager) Allocate(context *networkContext, prefer string) (types.NetworkResource, error) {
	vethName, _ := link.VethNameForPod(context.pod.Name, context.pod.Namespace, "", defaultPrefix)
	return &types.Veth{
		HostVeth: vethName,
	}, nil
}

func (*vethResourceManager) Release(context *networkContext, resItem types.ResourceItem) error {
	return nil
}

func (f *vethResourceManager) GarbageCollection(inUseResSet map[string]types.ResourceItem, expireResSet map[string]types.ResourceItem) error {
	return nil
}

func (f *vethResourceManager) GetResourceMapping() (tracing.ResourcePoolStats, error) {
	return nil, nil
}

func (f *vethResourceManager) Stat(context *networkContext, resID string) (types.NetworkResource, error) {
	vethName, _ := link.VethNameForPod(context.pod.Name, context.pod.Namespace, "", defaultPrefix)
	return &types.Veth{
		HostVeth: vethName,
	}, nil
}

func newVPCResourceManager() (ResourceManager, error) {
	mgr := &vethResourceManager{}
	return mgr, nil
}
