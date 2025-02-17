package instance

import (
	"sync/atomic"

	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
)

type ECS struct {
	regionID     atomic.Value
	zoneID       atomic.Value
	vSwitchID    atomic.Value
	primaryMAC   atomic.Value
	instanceID   atomic.Value
	instanceType atomic.Value
}

func (e *ECS) GetRegionID() (string, error) {
	if v := e.regionID.Load(); v != nil {
		return v.(string), nil
	}
	regionID, err := metadata.GetLocalRegion()
	if err != nil {
		return "", err
	}
	e.regionID.Store(regionID)
	return regionID, nil
}

func (e *ECS) GetZoneID() (string, error) {
	if v := e.zoneID.Load(); v != nil {
		return v.(string), nil
	}
	zoneID, err := metadata.GetLocalZone()
	if err != nil {
		return "", err
	}
	e.zoneID.Store(zoneID)
	return zoneID, nil
}

func (e *ECS) GetVSwitchID() (string, error) {
	if v := e.vSwitchID.Load(); v != nil {
		return v.(string), nil
	}
	vSwitchID, err := metadata.GetLocalVswitch()
	if err != nil {
		return "", err
	}
	e.vSwitchID.Store(vSwitchID)
	return vSwitchID, nil
}

func (e *ECS) GetPrimaryMAC() (string, error) {
	if v := e.primaryMAC.Load(); v != nil {
		return v.(string), nil
	}
	primaryMAC, err := metadata.GetPrimaryENIMAC()
	if err != nil {
		return "", err
	}
	e.primaryMAC.Store(primaryMAC)
	return primaryMAC, nil
}

func (e *ECS) GetInstanceID() (string, error) {
	if v := e.instanceID.Load(); v != nil {
		return v.(string), nil
	}
	instanceID, err := metadata.GetLocalInstanceID()
	if err != nil {
		return "", err
	}
	e.instanceID.Store(instanceID)
	return instanceID, nil
}

func (e *ECS) GetInstanceType() (string, error) {
	if v := e.instanceType.Load(); v != nil {
		return v.(string), nil
	}
	instanceType, err := metadata.GetInstanceType()
	if err != nil {
		return "", err
	}
	e.instanceType.Store(instanceType)
	return instanceType, nil
}
