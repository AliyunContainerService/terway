package eni

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	podENITypes "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/metric"
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
		info, ok := l.podENI.Status.ENIInfos[alloc.ENI.ID]
		if !ok {
			return nil
		}
		if l.trunkENI.ID != "" {
			eniInfo.Trunk = true
			eniInfo.MAC = l.trunkENI.MAC
			eniInfo.GatewayIP = l.trunkENI.GatewayIP.ToRPC()

			vid := uint32(info.Vid)
			eniInfo.Vid = vid
		}

		eniInfo.VfId = info.VfID

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
	notifier *Notifier
}

func NewRemote(client client.Client, trunkENI *daemon.ENI, notifier *Notifier) *Remote {
	return &Remote{
		trunkENI: trunkENI,
		client:   client,
		notifier: notifier,
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
		l := logf.FromContext(ctx, "ipam", "remote")
		defer close(resp)

		if r.notifier == nil {
			l.Info("notifier is nil, falling back to backoff polling")
			r.allocateWithBackoff(ctx, cni, resp, l)
			return
		}

		ch := r.notifier.Subscribe()
		defer r.notifier.Unsubscribe(ch)

		if allocResp, success := r.tryAllocatePodENI(ctx, cni, l); success {
			resp <- allocResp
			return
		}

		for {
			select {
			case <-ctx.Done():
				l.Info("context cancelled, allocation failed")
				bgCtx, bgCancel := context.WithTimeout(context.Background(), 5*time.Second)
				podENI, _ := getPodENI(bgCtx, r.client, cni.PodNamespace, cni.PodName)
				bgCancel()
				errMsg := buildTimeoutErrorMessage(podENI)
				select {
				case resp <- &AllocResp{Err: fmt.Errorf("allocate remote IP timeout: %s", errMsg)}:
				default:
				}
				return
			case <-ch:
				l.V(2).Info("received PodENI change notification, trying to allocate")
				if allocResp, success := r.tryAllocatePodENI(ctx, cni, l); success {
					resp <- allocResp
					return
				}
			}
		}
	}()

	return resp, nil
}

func (r *Remote) tryAllocatePodENI(ctx context.Context, cni *daemon.CNI, l logr.Logger) (*AllocResp, bool) {
	podENI, innerErr := getPodENI(ctx, r.client, cni.PodNamespace, cni.PodName)
	if innerErr != nil {
		l.V(2).Info("failed to get PodENI", "error", innerErr)
		return nil, false
	}

	// Extract ENI IDs for logging
	eniIDs := extractENIIDs(podENI)

	if !podENI.DeletionTimestamp.IsZero() {
		l.Info("PodENI is being deleted", "eniIDs", eniIDs)
		return nil, false
	}

	if podENI.Status.Phase != podENITypes.ENIPhaseBind {
		l.Info("PodENI not ready",
			"phase", podENI.Status.Phase,
			"phaseDescription", phaseDescription(podENI.Status.Phase),
			"eniIDs", eniIDs,
			"msg", podENI.Status.Msg,
			"instanceID", podENI.Status.InstanceID,
			"trunkENIID", podENI.Status.TrunkENIID,
		)
		return nil, false
	}

	if cni.PodUID != "" && podENI.Annotations[types.PodUID] != cni.PodUID {
		l.V(2).Info("PodENI UID mismatch", "expected", cni.PodUID, "actual", podENI.Annotations[types.PodUID])
		return nil, false
	}

	if r.trunkENI != nil && podENI.Status.TrunkENIID != r.trunkENI.ID {
		l.Error(fmt.Errorf("trunk mismatch"), "PodENI used a different trunk", "expected", r.trunkENI.ID, "actual", podENI.Status.TrunkENIID)
		return &AllocResp{
			Err: &types.Error{
				Code: types.ErrPodENINotReady,
				Msg:  fmt.Sprintf("PodENI used a different trunk %s", podENI.Status.TrunkENIID),
			},
		}, true
	}

	if len(podENI.Spec.Allocations) == 0 {
		l.Error(fmt.Errorf("empty allocations"), "PodENI has empty allocations")
		return &AllocResp{
			Err: &types.Error{
				Code: types.ErrPodENINotReady,
				Msg:  "PodENI has empty allocations",
			},
		}, true
	}

	remote := &RemoteIPResource{
		podENI: *podENI,
	}
	if r.trunkENI != nil {
		remote.trunkENI = *r.trunkENI
	}
	l.Info("get pod eni success", "uid", podENI.UID, "eniIDs", eniIDs)

	metric.ResourcePoolAllocated.WithLabelValues(metric.ResourcePoolTypeRemote).Inc()

	return &AllocResp{
		NetworkConfigs: NetworkResources{remote},
	}, true
}

func (r *Remote) allocateWithBackoff(ctx context.Context, cni *daemon.CNI, resp chan *AllocResp, l logr.Logger) {
	var podENI *podENITypes.PodENI
	var err, innerErr error
	err = backoff.ExponentialBackoffWithInitialDelay(ctx, backoff.Backoff(backoff.WaitPodENIStatus), func(ctx context.Context) (bool, error) {
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
		if r.trunkENI != nil && podENI.Status.TrunkENIID != r.trunkENI.ID {
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

	if err != nil {
		if innerErr != nil {
			err = fmt.Errorf("%w: %s", err, innerErr)
		}
		select {
		case <-ctx.Done():
		case resp <- &AllocResp{Err: err}:
		}
		return
	}

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

		metric.ResourcePoolAllocated.WithLabelValues(metric.ResourcePoolTypeRemote).Inc()
	}

	select {
	case <-ctx.Done():
	case resp <- &AllocResp{
		NetworkConfigs: networkResources,
	}:
	}
}

func (r *Remote) Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) (bool, error) {
	return false, nil
}

func (r *Remote) Dispose(n int) int {
	return 0
}

// phaseDescription returns a human-readable description of the PodENI phase
func phaseDescription(phase podENITypes.Phase) string {
	switch phase {
	case podENITypes.ENIPhaseInitial, podENITypes.ENIPhaseBinding:
		// Both Initial and Binding are treated as "Binding" - ENI is being created/attached
		return "ENI is being created and attached to ECS instance"
	case podENITypes.ENIPhaseBind:
		return "ENI successfully bound to ECS instance"
	case podENITypes.ENIPhaseUnbind:
		return "ENI detached from ECS instance"
	case podENITypes.ENIPhaseDetaching:
		return "ENI is being detached from ECS instance"
	case podENITypes.ENIPhaseDeleting:
		return "PodENI is being deleted"
	default:
		return fmt.Sprintf("unknown phase: %s", phase)
	}
}

// extractENIIDs extracts ENI IDs from PodENI allocations for logging
func extractENIIDs(podENI *podENITypes.PodENI) []string {
	var eniIDs []string
	for _, alloc := range podENI.Spec.Allocations {
		if alloc.ENI.ID != "" {
			eniIDs = append(eniIDs, alloc.ENI.ID)
		}
	}
	return eniIDs
}

// buildTimeoutErrorMessage builds a detailed error message for PodENI allocation timeout
func buildTimeoutErrorMessage(podENI *podENITypes.PodENI) string {
	if podENI == nil {
		return "PodENI not found"
	}
	eniIDs := extractENIIDs(podENI)
	return fmt.Sprintf("ENI %v in phase %q (%s): %s",
		eniIDs,
		podENI.Status.Phase,
		phaseDescription(podENI.Status.Phase),
		podENI.Status.Msg,
	)
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
