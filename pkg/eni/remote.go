package eni

import (
	"context"
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	podENITypes "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

var _ ResourceRequest = &RemoteIPRequest{}

type RemoteIPRequest struct{}

func (l *RemoteIPRequest) ResourceType() ResourceType {
	return ResourceTypeRemoteIP
}

var _ NetworkResource = &RemoteIPResource{}

type RemoteIPResource struct {
	trunkENI daemon.ENI
	podENI   podENITypes.PodENI
}

func (l *RemoteIPResource) ToStore() []daemon.ResourceItem {
	return nil
}

func (l *RemoteIPResource) ToRPC() []*rpc.NetConf {
	var netConf []*rpc.NetConf
	for _, alloc := range l.podENI.Spec.Allocations {
		podIP := &rpc.IPSet{}
		cidr := &rpc.IPSet{}
		gw := &rpc.IPSet{}
		if alloc.IPv4 != "" {
			podIP.IPv4 = alloc.IPv4
			cidr.IPv4 = alloc.IPv4CIDR
			gw.IPv4 = terwayIP.DeriveGatewayIP(alloc.IPv4CIDR)

			if cidr.IPv4 == "" || gw.IPv4 == "" {
				return nil
			}
		}
		if alloc.IPv6 != "" {
			podIP.IPv6 = alloc.IPv6
			cidr.IPv6 = alloc.IPv6CIDR
			gw.IPv6 = terwayIP.DeriveGatewayIP(alloc.IPv6CIDR)

			if cidr.IPv6 == "" || gw.IPv6 == "" {
				return nil
			}
		}
		eniInfo := &rpc.ENIInfo{
			MAC: alloc.ENI.MAC,
		}
		if l.trunkENI.ID != "" {
			eniInfo.Trunk = true
			eniInfo.MAC = l.trunkENI.MAC
			eniInfo.GatewayIP = l.trunkENI.GatewayIP.ToRPC()

			info, ok := l.podENI.Status.ENIInfos[alloc.ENI.ID]
			if !ok {
				return nil
			}
			vid := uint32(info.Vid)
			eniInfo.Vid = vid
		}

		netConf = append(netConf, &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				PodIP:     podIP,
				PodCIDR:   cidr,
				GatewayIP: gw,
			},
			ENIInfo:      eniInfo,
			IfName:       alloc.Interface,
			ExtraRoutes:  parseExtraRoute(alloc.ExtraRoutes),
			DefaultRoute: alloc.DefaultRoute,
		})
	}
	return netConf
}

func (l *RemoteIPResource) ResourceType() ResourceType {
	return ResourceTypeRemoteIP
}

var _ NetworkInterface = &Remote{}

type Remote struct {
	trunkENI *daemon.ENI // for nil , this is not a trunk
	client   client.Client
}

func NewRemote(client client.Client, trunkENI *daemon.ENI) *Remote {
	return &Remote{
		trunkENI: trunkENI,
		client:   client,
	}
}

func (r *Remote) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	return nil
}

func (r *Remote) Priority() int {
	return 100
}

func (r *Remote) Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	if request.ResourceType() != ResourceTypeRemoteIP {
		return nil, []Trace{{Condition: ResourceTypeMismatch}}
	}

	resp := make(chan *AllocResp)

	go func() {
		l := logf.FromContext(ctx)

		var podENI *podENITypes.PodENI
		var err, innerErr error
		err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.WaitPodENIStatus), func(ctx context.Context) (bool, error) {
			podENI, innerErr = getPodENI(ctx, r.client, cni.PodNamespace, cni.PodName)
			if innerErr != nil {
				innerErr = &types.Error{
					Code: types.ErrPodENINotReady,
					Msg:  "Get PodENI error",
					R:    innerErr,
				}
				return false, nil
			}
			if !podENI.DeletionTimestamp.IsZero() {
				innerErr = &types.Error{
					Code: types.ErrPodENINotReady,
					Msg:  "DeletionTimestamp is not zero",
				}
				return false, nil
			}
			if podENI.Status.Phase != podENITypes.ENIPhaseBind {
				innerErr = &types.Error{
					Code: types.ErrPodENINotReady,
					Msg:  fmt.Sprintf("PodENI Phase is %s", podENI.Status.Phase),
				}
				return false, nil
			}
			if cni.PodUID != "" {
				if podENI.Annotations[types.PodUID] != cni.PodUID {
					return false, nil
				}
			}
			if podENI.Status.TrunkENIID != r.trunkENI.ID {
				innerErr = &types.Error{
					Code: types.ErrPodENINotReady,
					Msg:  fmt.Sprintf("PodENI used a different trunk %s", podENI.Status.TrunkENIID),
				}
				return false, innerErr
			}

			if len(podENI.Spec.Allocations) == 0 {
				innerErr = &types.Error{
					Code: types.ErrPodENINotReady,
					Msg:  "PodENI has empty allocations",
				}
				return false, innerErr
			}
			return true, nil
		})

		var networkResources NetworkResources
		if podENI != nil {
			remote := &RemoteIPResource{
				podENI: *podENI,
			}
			if r.trunkENI != nil {
				remote.trunkENI = *r.trunkENI
			}
			l.Info("get pod eni success", "uid", podENI.UID)

			networkResources = append(networkResources, remote)
		}

		select {
		case <-ctx.Done():
		case resp <- &AllocResp{
			Err:            err,
			NetworkConfigs: networkResources,
		}:
		}
	}()

	return resp, nil
}

func (r *Remote) Release(ctx context.Context, cni *daemon.CNI, request ResourceRequest) bool {
	return false
}

func (r *Remote) Dispose(n int) int {
	return 0
}

func getPodENI(ctx context.Context, c client.Client, namespace, name string) (*podENITypes.PodENI, error) {
	obj := &podENITypes.PodENI{}
	err := c.Get(ctx, k8stypes.NamespacedName{Namespace: namespace, Name: name}, obj, &client.GetOptions{Raw: &metav1.GetOptions{
		ResourceVersion: "0",
	}})
	return obj, err
}

func parseExtraRoute(routes []podENITypes.Route) []*rpc.Route {
	if routes == nil {
		return nil
	}
	var res []*rpc.Route
	for _, r := range routes {
		res = append(res, &rpc.Route{
			Dst: r.Dst,
		})
	}
	return res
}
