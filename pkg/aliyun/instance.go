package aliyun

import (
	"fmt"
	"sync"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/pkg/utils"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"k8s.io/apimachinery/pkg/util/wait"
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

	// IPv4PerAdapter is the maximum number of ipv4 addresses per adapter/interface
	IPv4PerAdapter int

	// IPv6PerAdapter is the maximum number of ipv6 addresses per adapter/interface
	IPv6PerAdapter int

	// MemberAdapterLimit is the number interfaces that type is member
	MemberAdapterLimit int

	// MaxMemberAdapterLimit is the limit to use member
	MaxMemberAdapterLimit int
}

func (l *Limits) SupportIPv6() bool {
	return l.IPv4PerAdapter <= l.IPv6PerAdapter
}

func (l *Limits) TrunkPod() int {
	return l.MemberAdapterLimit
}

func (l *Limits) MIPPod() int {
	return (l.Adapters - 1) * l.IPv4PerAdapter
}

func (l *Limits) ENIOnlyPod() int {
	return l.Adapters - 1
}

var limits = struct {
	sync.RWMutex
	m map[string]Limits
}{
	m: map[string]Limits{},
}

// UpdateFromAPI updates limits for instance
// https://www.alibabacloud.com/help/doc-detail/25620.htm
func UpdateFromAPI(client *ecs.Client, instanceType string) error {
	req := ecs.CreateDescribeInstanceTypesRequest()
	if instanceType != "" {
		req.InstanceTypes = &[]string{instanceType}
	}
	var innerErr error
	var resp *ecs.DescribeInstanceTypesResponse
	err := wait.ExponentialBackoff(backoff.Backoff(backoff.DefaultKey),
		func() (done bool, err error) {
			start := time.Now()
			resp, innerErr = client.DescribeInstanceTypes(req)
			metric.OpenAPILatency.WithLabelValues("DescribeInstanceTypes", fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))
			if innerErr != nil {
				return false, nil
			}
			return true, nil
		},
	)
	if err != nil {
		return fmt.Errorf("error get instance type %v,%w", innerErr, err)
	}

	limits.Lock()
	defer limits.Unlock()

	for _, instanceTypeInfo := range resp.InstanceTypes.InstanceType {
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

		limits.m[instanceType] = Limits{
			Adapters:              adapterLimit,
			IPv4PerAdapter:        utils.Minimal(ipv4PerAdapter),
			IPv6PerAdapter:        utils.Minimal(ipv6PerAdapter),
			MemberAdapterLimit:    utils.Minimal(memberAdapterLimit),
			MaxMemberAdapterLimit: utils.Minimal(maxMemberAdapterLimit),
		}
		logger.DefaultLogger.WithFields(map[string]interface{}{
			"instance-type":       instanceType,
			"adapters":            adapterLimit,
			"ipv4":                ipv4PerAdapter,
			"ipv6":                ipv6PerAdapter,
			"member-adapters":     memberAdapterLimit,
			"max-member-adapters": maxMemberAdapterLimit,
		}).Infof("instance limit")
	}

	return nil
}

// GetLimit returns the instance limits of a particular instance type.
func GetLimit(instanceType string) (limit Limits, ok bool) {
	limits.RLock()
	limit, ok = limits.m[instanceType]
	limits.RUnlock()
	return
}
