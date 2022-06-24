package daemon

import (
	"context"
	"fmt"
	"net"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/AliyunContainerService/terway/pkg/ipam"
	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/AliyunContainerService/terway/pkg/tracing"

	"github.com/AliyunContainerService/terway/types"
)

var eipLog = logger.DefaultLogger

// eip resource manager for pod public ip address
type eipResourceManager struct {
	ecs         ipam.API
	k8s         Kubernetes
	allowEipRob bool
}

func newEipResourceManager(e ipam.API, k Kubernetes, allowEipRob bool) ResourceManager {
	return &eipResourceManager{
		ecs:         e,
		k8s:         k,
		allowEipRob: allowEipRob,
	}
}

func (e *eipResourceManager) Allocate(context *networkContext, prefer string) (types.NetworkResource, error) {
	eipLog.Infof("Allocate EIP: %v, %v, %s", context.pod, context.resources, prefer)
	if context.pod == nil {
		return nil, fmt.Errorf("invalid pod info: %v", context.pod)
	}
	var (
		eniID string
		eniIP net.IP
		err   error
	)
	ctx := context.Context
	podIP := context.pod.PodIPs.IPv4
	if podIP == nil {
		return nil, fmt.Errorf("pod ipv4 addr is empty")
	}
	for _, res := range context.resources {
		switch res.Type {
		case types.ResourceTypeENI, types.ResourceTypeENIIP:
			eniID, err = e.ecs.QueryEniIDByIP(ctx, aliyun.GetInstanceMeta().VPCID, podIP)
			if err != nil {
				return nil, fmt.Errorf("error Query ENI by pod IP, %v, %w", context.pod, err)
			}
			eniIP = podIP
		}
	}
	if eniID == "" && eniIP == nil {
		return nil, fmt.Errorf("pod network mode not support EIP associate")
	}
	eipID := context.pod.EipInfo.PodEipID
	if eipID == "" {
		eipLog.Infof("eip id empty pod")
		eipID = prefer
	}
	eipInfo, err := e.ecs.AllocateEipAddress(ctx, context.pod.EipInfo.PodEipBandWidth, context.pod.EipInfo.PodEipChargeType,
		eipID, eniID, eniIP, e.allowEipRob, context.pod.EipInfo.PodEipISP, context.pod.EipInfo.PodEipBandwidthPackageID, context.pod.EipInfo.PodEipPoolID)
	if err != nil {
		return nil, fmt.Errorf("error allocate eip info: %w", err)
	}

	// set eip to delete if pod not specific eip id
	if context.pod.EipInfo.PodEipID == "" {
		eipInfo.Delete = true
	}
	context.pod.EipInfo.PodEipIP = eipInfo.Address.String()
	err = e.k8s.PatchEipInfo(context.pod)
	if err != nil {
		var err1 error
		if eipInfo.Delete {
			err1 = e.ecs.ReleaseEipAddress(ctx, eipInfo.ID, eniID, eniIP)
		} else {
			err1 = e.ecs.UnassociateEipAddress(ctx, eipInfo.ID, eniID, eniIP.String())
		}
		if err1 != nil {
			eipLog.Errorf("error rollback eip: %v", err1)
		}
		return nil, fmt.Errorf("error patch pod info: %w", err)
	}
	return eipInfo, nil
}

func (e *eipResourceManager) Release(context *networkContext, resItem types.ResourceItem) error {
	if resItem.ExtraEipInfo == nil {
		return nil
	}
	eipLog.Infof("release eip: %v, %v", resItem.ID, resItem.ExtraEipInfo)
	ctx := context.Context

	if resItem.ExtraEipInfo.Delete {
		err := e.ecs.ReleaseEipAddress(ctx, resItem.ID, resItem.ExtraEipInfo.AssociateENI, resItem.ExtraEipInfo.AssociateENIIP)
		if err != nil {
			return err
		}
	} else {
		err := e.ecs.UnassociateEipAddress(ctx, resItem.ID, resItem.ExtraEipInfo.AssociateENI, resItem.ExtraEipInfo.AssociateENIIP.String())
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *eipResourceManager) GarbageCollection(inUseResSet map[string]types.ResourceItem, expireResSet map[string]types.ResourceItem) error {
	for expireRes, expireItem := range expireResSet {
		if expireItem.ExtraEipInfo == nil {
			continue
		}
		eipLog.Infof("release eip: %v, %v", expireRes, expireItem)
		if expireItem.ExtraEipInfo.Delete {
			err := e.ecs.ReleaseEipAddress(context.Background(), expireRes, expireItem.ExtraEipInfo.AssociateENI, expireItem.ExtraEipInfo.AssociateENIIP)
			if err != nil {
				return err
			}
		} else {
			err := e.ecs.UnassociateEipAddress(context.Background(), expireRes, expireItem.ExtraEipInfo.AssociateENI, expireItem.ExtraEipInfo.AssociateENIIP.String())
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *eipResourceManager) Stat(context *networkContext, resID string) (types.NetworkResource, error) {
	return nil, nil
}

func (e *eipResourceManager) GetResourceMapping() (tracing.ResourcePoolStats, error) {
	return nil, fmt.Errorf("eip resource manager store network resource")
}
