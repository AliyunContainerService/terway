package eni

import (
	"context"
	"sync"

	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/AliyunContainerService/terway/types/daemon"
)

var _ ResourceRequest = &VethRequest{}

type VethRequest struct{}

func (l *VethRequest) ResourceType() ResourceType {
	return ResourceTypeVeth
}

type VethResource struct {
	Name string
}

func (v VethResource) ResourceType() ResourceType {
	return ResourceTypeVeth
}

func (v VethResource) ToRPC() []*rpc.NetConf {
	return []*rpc.NetConf{{
		BasicInfo:    nil,
		ENIInfo:      nil,
		Pod:          nil,
		IfName:       "",
		ExtraRoutes:  nil,
		DefaultRoute: true,
	}}
}

func (v VethResource) ToStore() []daemon.ResourceItem {
	r := daemon.ResourceItem{
		Type: daemon.ResourceTypeVeth,
		ID:   v.Name,
	}

	return []daemon.ResourceItem{r}
}

var _ NetworkResource = &VethResource{}

var _ NetworkInterface = &Veth{}

const (
	defaultPrefix = "cali"
)

type Veth struct{}

func (r *Veth) Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	if request.ResourceType() != ResourceTypeVeth {
		return nil, nil
	}
	ch := make(chan *AllocResp)

	go func() {
		name, _ := link.VethNameForPod(cni.PodName, cni.PodNamespace, "", defaultPrefix)
		var nfs []NetworkResource
		nfs = append(nfs, &VethResource{Name: name})

		select {
		case <-ctx.Done():

		case ch <- &AllocResp{
			NetworkConfigs: nfs,
		}:

		}
	}()

	return ch, nil
}

func (r *Veth) Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) bool {
	return request.ResourceType() == ResourceTypeVeth
}

func (r *Veth) Priority() int {
	return 0
}

func (r *Veth) Dispose(n int) int {
	return 0
}

func (r *Veth) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	return nil
}
