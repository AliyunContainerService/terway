package eni

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

var _ NetworkInterface = &RemoteV2{}

var remoteV2Log = logf.Log.WithName("remote-v2")

type RemoteV2 struct {
	sharedMgr      *SharedCRDManager
	client         client.Client
	nodeName       string
	podENINotifier *Notifier
	trunkENI       *daemon.ENI
}

func NewRemoteV2(sharedMgr *SharedCRDManager, nodeName string) *RemoteV2 {
	return &RemoteV2{
		sharedMgr:      sharedMgr,
		client:         sharedMgr.Client(),
		nodeName:       nodeName,
		podENINotifier: NewNotifier(),
	}
}

func (r *RemoteV2) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	remoteV2Log.Info("start remote-v2 controller")

	<-r.sharedMgr.CacheSynced()
	remoteV2Log.Info("remote-v2 cache synced")

	podENIInformer, err := r.sharedMgr.GetCache().GetInformer(ctx, &networkv1beta1.PodENI{})
	if err != nil {
		return err
	}

	_, err = podENIInformer.AddEventHandler(&toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			r.podENINotifier.Notify()
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			newPodENI := newObj.(*networkv1beta1.PodENI)
			if newPodENI.Status.Phase == networkv1beta1.ENIPhaseBind {
				r.podENINotifier.Notify()
			}
		},
	})
	if err != nil {
		return err
	}

	trunk, err := r.getTrunkENI(ctx)
	if err != nil {
		return fmt.Errorf("failed to resolve trunk ENI: %w", err)
	}
	r.trunkENI = trunk

	remoteV2Log.Info("remote-v2 initialized", "hasTrunk", trunk != nil)
	return nil
}

func (r *RemoteV2) Priority() int {
	return 100
}

func (r *RemoteV2) Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	if request.ResourceType() != ResourceTypeRemoteIP {
		return nil, []Trace{{Condition: ResourceTypeMismatch}}
	}

	remote := &Remote{
		client:   r.client,
		trunkENI: r.trunkENI,
		notifier: r.podENINotifier,
	}
	return remote.Allocate(ctx, cni, request)
}

func (r *RemoteV2) Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) (bool, error) {
	return false, nil
}

func (r *RemoteV2) Dispose(n int) int {
	return 0
}

func (r *RemoteV2) getTrunkENI(ctx context.Context) (*daemon.ENI, error) {
	var node *networkv1beta1.Node
	var trunkENI *daemon.ENI

	var innerErr error
	err := wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.WaitPodENIStatus).Backoff, func(ctx context.Context) (bool, error) {
		node = &networkv1beta1.Node{}
		innerErr = r.client.Get(ctx, client.ObjectKey{Name: r.nodeName}, node)
		if innerErr != nil {
			return false, nil
		}
		if node.Spec.ENISpec == nil {
			innerErr = fmt.Errorf("nodes.network.alibabacloud.com %s has not been initialized", r.nodeName)
			return false, nil
		}
		if !node.Spec.ENISpec.EnableTrunk {
			return true, nil
		}

		k8sNode := &corev1.Node{}
		innerErr = r.client.Get(ctx, client.ObjectKey{Name: r.nodeName}, k8sNode)
		if innerErr != nil {
			return false, nil
		}
		trunkID := k8sNode.Annotations[types.TrunkOn]

		trunk, ok := node.Status.NetworkInterfaces[trunkID]
		if !ok {
			innerErr = fmt.Errorf("trunk %s has not been initialized", trunkID)
			return false, nil
		}

		trunkENI = &daemon.ENI{
			ID:               trunk.ID,
			MAC:              trunk.MacAddress,
			SecurityGroupIDs: trunk.SecurityGroupIDs,
			Trunk:            true,
			ERdma:            false,
			PrimaryIP:        types.IPSet{},
			GatewayIP:        types.IPSet{},
			VSwitchCIDR:      types.IPNetSet{},
			VSwitchID:        trunk.VSwitchID,
		}
		trunkENI.PrimaryIP.SetIP(trunk.PrimaryIPAddress)
		if node.Spec.ENISpec.EnableIPv4 {
			trunkENI.GatewayIP.SetIP(terwayIP.DeriveGatewayIP(trunk.IPv4CIDR))
			trunkENI.VSwitchCIDR.SetIPNet(trunk.IPv4CIDR)
		}
		if node.Spec.ENISpec.EnableIPv6 {
			trunkENI.GatewayIP.SetIP(terwayIP.DeriveGatewayIP(trunk.IPv6CIDR))
			trunkENI.VSwitchCIDR.SetIPNet(trunk.IPv6CIDR)
		}

		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("error get trunk eni %w, innerErr %s", err, innerErr)
	}

	return trunkENI, err
}
