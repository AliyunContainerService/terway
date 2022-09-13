package aliyun

import (
	"context"
	"fmt"
	"sync"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/AliyunContainerService/terway/pkg/utils"
)

var defaultIns *Instance
var once sync.Once
var logIns = logger.DefaultLogger

type Instance struct {
	RegionID   string
	ZoneID     string
	VPCID      string
	VSwitchID  string
	PrimaryMAC string

	InstanceID   string
	InstanceType string
}

func GetInstanceMeta() *Instance {
	once.Do(func() {
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

		defaultIns = &Instance{
			RegionID:     regionID,
			ZoneID:       zoneID,
			VPCID:        vpcID,
			VSwitchID:    vSwitchID,
			InstanceID:   instanceID,
			InstanceType: instanceType,
			PrimaryMAC:   mac,
		}
		logIns.WithFields(map[string]interface{}{
			"region-id":     regionID,
			"zone-id":       zoneID,
			"vpc-id":        vpcID,
			"instance-id":   instanceID,
			"instance-type": instanceType,
			"vswitch-id":    vSwitchID,
			"primary-mac":   mac,
		}).Infof("instance metadata")
	})

	return defaultIns
}

// Limits specifies the IPAM relevant instance limits
type Limits struct {
	// Adapters specifies the maximum number of interfaces that can be
	// attached to the instance
	Adapters int

	// TotalAdapters maximum number of interfaces that can be
	// attached to the instance
	TotalAdapters int

	// IPv4PerAdapter is the maximum number of ipv4 addresses per adapter/interface
	IPv4PerAdapter int

	// IPv6PerAdapter is the maximum number of ipv6 addresses per adapter/interface
	IPv6PerAdapter int

	// MemberAdapterLimit is the number interfaces that type is member
	MemberAdapterLimit int

	// MaxMemberAdapterLimit is the limit to use member
	MaxMemberAdapterLimit int

	InstanceBandwidthRx int

	InstanceBandwidthTx int
}

func (l *Limits) SupportMultiIPIPv6() bool {
	return l.IPv6PerAdapter == l.IPv4PerAdapter
}

func (l *Limits) SupportIPv6() bool {
	return l.IPv6PerAdapter > 0
}

func (l *Limits) TrunkPod() int {
	return l.MemberAdapterLimit
}

func (l *Limits) MaximumTrunkPod() int {
	return l.MaxMemberAdapterLimit
}

func (l *Limits) MultiIPPod() int {
	return (l.Adapters - 1) * l.IPv4PerAdapter
}

func (l *Limits) ExclusiveENIPod() int {
	return l.Adapters - 1
}

var limits sync.Map

// GetLimit returns the instance limits of a particular instance type. // https://www.alibabacloud.com/help/doc-detail/25620.htm
// if instanceType is empty will list all instanceType and warm the cache, no error and Limits will return
func GetLimit(client client.ECS, instanceType string) (*Limits, error) {
	v, ok := limits.Load(instanceType)
	if ok {
		return v.(*Limits), nil
	}
	var req []string
	if instanceType != "" {
		req = append(req, instanceType)
	}
	ins, err := client.DescribeInstanceTypes(context.Background(), req)
	if err != nil {
		return nil, err
	}

	for _, instanceTypeInfo := range ins {
		instanceType := instanceTypeInfo.InstanceTypeId
		adapterLimit := instanceTypeInfo.EniQuantity
		ipv4PerAdapter := instanceTypeInfo.EniPrivateIpAddressQuantity
		ipv6PerAdapter := instanceTypeInfo.EniIpv6AddressQuantity
		memberAdapterLimit := instanceTypeInfo.EniTotalQuantity - instanceTypeInfo.EniQuantity
		// exclude eth0 eth1
		maxMemberAdapterLimit := instanceTypeInfo.EniTotalQuantity - 2
		if !instanceTypeInfo.EniTrunkSupported {
			memberAdapterLimit = 0
			maxMemberAdapterLimit = 0
		}
		limits.Store(instanceType, &Limits{
			Adapters:              adapterLimit,
			TotalAdapters:         instanceTypeInfo.EniTotalQuantity,
			IPv4PerAdapter:        utils.Minimal(ipv4PerAdapter),
			IPv6PerAdapter:        utils.Minimal(ipv6PerAdapter),
			MemberAdapterLimit:    utils.Minimal(memberAdapterLimit),
			MaxMemberAdapterLimit: utils.Minimal(maxMemberAdapterLimit),
			InstanceBandwidthRx:   instanceTypeInfo.InstanceBandwidthRx,
			InstanceBandwidthTx:   instanceTypeInfo.InstanceBandwidthTx,
		})

		logger.DefaultLogger.WithFields(map[string]interface{}{
			"instance-type":       instanceType,
			"adapters":            adapterLimit,
			"total-adapters":      instanceTypeInfo.EniTotalQuantity,
			"ipv4":                ipv4PerAdapter,
			"ipv6":                ipv6PerAdapter,
			"member-adapters":     memberAdapterLimit,
			"max-member-adapters": maxMemberAdapterLimit,
			"bandwidth-rx":        instanceTypeInfo.InstanceBandwidthRx,
			"bandwidth-tx":        instanceTypeInfo.InstanceBandwidthTx,
		}).Infof("instance limit")
	}
	if instanceType == "" {
		return nil, nil
	}
	v, ok = limits.Load(instanceType)
	if !ok {
		return nil, fmt.Errorf("unexpected error")
	}

	return v.(*Limits), nil
}
