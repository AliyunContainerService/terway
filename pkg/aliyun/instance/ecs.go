package instance

import (
	"fmt"

	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
)

func DefaultPopulate() *Instance {
	regionID, err := metadata.GetLocalRegion()
	if err != nil || regionID == "" {
		panic(fmt.Errorf("error get regionID %w", err))
	}
	zoneID, err := metadata.GetLocalZone()
	if err != nil || zoneID == "" {
		panic(fmt.Errorf("error get zoneID %w", err))
	}
	vpcID, err := metadata.GetLocalVPC()
	if err != nil || vpcID == "" {
		panic(fmt.Errorf("error get vpcID %w", err))
	}
	instanceID, err := metadata.GetLocalInstanceID()
	if err != nil || instanceID == "" {
		panic(fmt.Errorf("error get instanceID %w", err))
	}
	instanceType, err := metadata.GetInstanceType()
	if err != nil || instanceType == "" {
		panic(fmt.Errorf("error get instanceType %w", err))
	}
	vSwitchID, err := metadata.GetLocalVswitch()
	if err != nil || vSwitchID == "" {
		panic(fmt.Errorf("error get vSwitchID %w", err))
	}
	mac, err := metadata.GetPrimaryENIMAC()
	if err != nil {
		panic(fmt.Errorf("error get eth0's mac %w", err))
	}

	return &Instance{
		RegionID:     regionID,
		ZoneID:       zoneID,
		VPCID:        vpcID,
		VSwitchID:    vSwitchID,
		InstanceID:   instanceID,
		InstanceType: instanceType,
		PrimaryMAC:   mac,
	}
}
