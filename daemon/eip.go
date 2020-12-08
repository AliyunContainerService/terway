package daemon

import (
	"fmt"
	"net"

	"github.com/AliyunContainerService/terway/pkg/tracing"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/AliyunContainerService/terway/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// eip resource manager for pod public ip address
type eipResourceManager struct {
	ecs         aliyun.ECS
	k8s         Kubernetes
	allowEipRob bool
}

func newEipResourceManager(e aliyun.ECS, k Kubernetes, allowEipRob bool) ResourceManager {
	return &eipResourceManager{
		ecs:         e,
		k8s:         k,
		allowEipRob: allowEipRob,
	}
}

func (e *eipResourceManager) Allocate(context *networkContext, prefer string) (types.NetworkResource, error) {
	logrus.Infof("Allocate EIP: %v, %v", context.pod, context.resources)
	if context.pod == nil {
		return nil, fmt.Errorf("invalid pod info: %v", context.pod)
	}
	var (
		eniID string
		eniIP net.IP
		err   error
	)
	podIP := net.ParseIP(context.pod.PodIP)
	if podIP == nil {
		return nil, errors.Errorf("invalid pod ip: %v", context.pod.PodIP)
	}
	for _, res := range context.resources {
		switch res.Type {
		case types.ResourceTypeENI, types.ResourceTypeENIIP:
			eniID, err = e.ecs.QueryEniIDByIP(podIP)
			if err != nil {
				return nil, errors.Wrapf(err, "error Query ENI by pod IP, %v", context.pod)
			}
			eniIP = podIP
		}
	}
	if eniID == "" && eniIP == nil {
		return nil, fmt.Errorf("pod network mode not support EIP associate")
	}

	eipInfo, err := e.ecs.AllocateEipAddress(context.pod.EipInfo.PodEipBandWidth, context.pod.EipInfo.PodEipID, eniID, eniIP, e.allowEipRob)
	if err != nil {
		return nil, errors.Errorf("error allocate eip info: %v", err)
	}
	context.pod.EipInfo.PodEipIP = eipInfo.Address.String()
	err = e.k8s.PatchEipInfo(context.pod)
	if err != nil {
		var err1 error
		if eipInfo.Delete {
			err1 = e.ecs.ReleaseEipAddress(eipInfo.ID, eniID, eniIP)
		} else {
			err1 = e.ecs.UnassociateEipAddress(eipInfo.ID, eniID, eniIP.String())
		}
		if err1 != nil {
			logrus.Errorf("error rollback eip: %v", err1)
		}
		return nil, errors.Errorf("error patch pod info: %v", err)
	}
	return eipInfo, nil
}

func (e *eipResourceManager) Release(context *networkContext, resItem ResourceItem) error {
	if resItem.ExtraEipInfo == nil {
		return nil
	}
	logrus.Infof("release eip: %v, %v", resItem.ID, resItem.ExtraEipInfo)
	if resItem.ExtraEipInfo.Delete {
		err := e.ecs.ReleaseEipAddress(resItem.ID, resItem.ExtraEipInfo.AssociateENI, resItem.ExtraEipInfo.AssociateENIIP)
		if err != nil {
			return err
		}
	} else {
		err := e.ecs.UnassociateEipAddress(resItem.ID, resItem.ExtraEipInfo.AssociateENI, resItem.ExtraEipInfo.AssociateENIIP.String())
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *eipResourceManager) GarbageCollection(inUseResSet map[string]ResourceItem, expireResSet map[string]ResourceItem) error {
	for expireRes, expireItem := range expireResSet {
		if expireItem.ExtraEipInfo == nil {
			continue
		}
		logrus.Infof("release eip: %v, %v", expireRes, expireItem)
		if expireItem.ExtraEipInfo.Delete {
			err := e.ecs.ReleaseEipAddress(expireRes, expireItem.ExtraEipInfo.AssociateENI, expireItem.ExtraEipInfo.AssociateENIIP)
			if err != nil {
				return err
			}
		} else {
			err := e.ecs.UnassociateEipAddress(expireRes, expireItem.ExtraEipInfo.AssociateENI, expireItem.ExtraEipInfo.AssociateENIIP.String())
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *eipResourceManager) GetResourceMapping() (tracing.ResourcePoolStats, error) {
	return nil, errors.New("eip resource manager store network resource")
}
