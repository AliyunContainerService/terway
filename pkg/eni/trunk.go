package eni

import (
	"context"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/terway/types/daemon"
)

var _ NetworkInterface = &Trunk{}
var _ Usage = &Trunk{}
var _ ReportStatus = &Trunk{}

type Trunk struct {
	trunkENI *daemon.ENI

	remote *Remote
	local  *Local
}

func NewTrunk(client client.Client, local *Local) *Trunk {
	return &Trunk{
		trunkENI: local.eni,
		local:    local,
		remote:   NewRemote(client, local.eni),
	}
}

func (r *Trunk) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	return r.local.Run(ctx, podResources, wg)
}

func (r *Trunk) Priority() int {
	return 100
}

func (r *Trunk) Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	switch request.ResourceType() {
	case ResourceTypeLocalIP:
		return r.local.Allocate(ctx, cni, request)
	case ResourceTypeRemoteIP:
		return r.remote.Allocate(ctx, cni, request)
	default:
		return nil, []Trace{{Condition: ResourceTypeMismatch}}
	}
}

func (r *Trunk) Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) (bool, error) {
	switch request.ResourceType() {
	case ResourceTypeLocalIP:
		return r.local.Release(ctx, cni, request)
	case ResourceTypeRemoteIP:
		return r.remote.Release(ctx, cni, request)
	default:
		return false, nil
	}
}

func (r *Trunk) Dispose(n int) int {
	return r.local.Dispose(n)
}

func (r *Trunk) Status() Status {
	return r.local.Status()
}

func (r *Trunk) Usage() (int, int, error) {
	return r.local.Usage()
}
