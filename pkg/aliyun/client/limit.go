//go:generate mockery --name LimitProvider --tags default_build

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/samber/lo"
	"golang.org/x/sync/singleflight"
	"k8s.io/apimachinery/pkg/util/cache"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type NetworkCard struct {
	Index int
}

// Limits specifies the IPAM relevant instance limits
type Limits struct {
	InstanceTypeID string

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

	// ERdmaAdapters specifies the maximum number of erdma interfaces
	ERdmaAdapters int

	InstanceBandwidthRx int

	InstanceBandwidthTx int

	HighDenseQuantity int

	NetworkCards []NetworkCard
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

func (l *Limits) ERDMARes() int {
	if l.ERdmaAdapters <= 0 || l.Adapters <= 2 {
		return 0
	}
	// limit adapters
	if l.Adapters >= 8 {
		// for multi physical network card instance
		return min(2, l.ERdmaAdapters)
	}
	// limit normal ecs eri to 1, to avoid too many normal multiip pod quota consume
	return min(1, l.ERdmaAdapters)
}

func (l *Limits) ExclusiveENIPod() int {
	return l.Adapters - 1
}

type LimitProvider interface {
	GetLimit(client interface{}, instanceType string) (*Limits, error)
	GetLimitFromAnno(anno map[string]string) (*Limits, error)
}

type Provider struct {
	cache cache.LRUExpireCache
	ttl   time.Duration

	g singleflight.Group
}

func NewProvider() *Provider {
	return &Provider{
		cache: *cache.NewLRUExpireCache(10 * 1000),
		ttl:   15 * 24 * time.Hour,
	}
}

func (d *Provider) GetLimit(client interface{}, instanceType string) (*Limits, error) {
	switch cc := client.(type) {
	case ECS:
		v, ok := d.cache.Get(instanceType)
		if ok {
			return v.(*Limits), nil
		}

		var req []string
		if instanceType != "" {
			req = append(req, instanceType)
		}

		v, err, _ := d.g.Do(instanceType, func() (interface{}, error) {
			ins, err := cc.DescribeInstanceTypes(context.Background(), req)
			if err != nil {
				return nil, err
			}

			for _, instanceTypeInfo := range ins {
				instanceTypeID := instanceTypeInfo.InstanceTypeId
				limit := GetInstanceType(&instanceTypeInfo)
				d.cache.Add(instanceTypeID, limit, d.ttl)
				logf.Log.Info("instance limit", instanceTypeID, limit)
			}

			if instanceType == "" {
				return nil, nil
			}

			if cachedLimit, ok := d.cache.Get(instanceType); ok {
				return cachedLimit, nil
			}

			return nil, fmt.Errorf("unexpected error: instance type %s not found in cache after processing", instanceType)
		})
		if err != nil {
			return nil, err
		}

		if v == nil {
			return nil, nil
		}

		return v.(*Limits), nil
	case EFLO:
		resp, err := cc.GetNodeInfoForPod(context.Background(), instanceType)
		if err != nil {
			return nil, err
		}

		return &Limits{
			Adapters:       resp.LeniQuota,
			TotalAdapters:  resp.LeniQuota,
			IPv4PerAdapter: resp.LniSipQuota,
		}, nil

	case EFLOControl:
		v, ok := d.cache.Get(instanceType)
		if ok {
			return v.(*Limits), nil
		}

		if instanceType == "" {
			return nil, fmt.Errorf("instance type is empty")
		}

		v, err, _ := d.g.Do(instanceType, func() (interface{}, error) {
			describeReq := &DescribeNodeTypeRequestOptions{}
			describeReq.NodeType = &instanceType

			ins, err := cc.DescribeNodeType(context.Background(), describeReq)
			if err != nil {
				return nil, err
			}

			limit := &Limits{
				Adapters:          ins.EniQuantity,
				TotalAdapters:     ins.EniQuantity,
				IPv4PerAdapter:    ins.EniPrivateIpAddressQuantity,
				HighDenseQuantity: ins.EniHighDenseQuantity,
			}
			d.cache.Add(instanceType, limit, d.ttl)

			return limit, nil
		})
		if err != nil {
			return nil, err
		}

		return v.(*Limits), nil
	default:
		return nil, fmt.Errorf("unsupported client")
	}

}

func (d *Provider) GetLimitFromAnno(anno map[string]string) (*Limits, error) {
	v, ok := anno["alibabacloud.com/instance-type-info"]
	if !ok {
		// nb(l1b0k): eflo instance type info is not supported
		return nil, nil
	}

	instanceType := &ecs.InstanceType{}
	err := json.Unmarshal([]byte(v), instanceType)
	if err != nil {
		return nil, err
	}

	return GetInstanceType(instanceType), nil
}

func GetInstanceType(instanceTypeInfo *ecs.InstanceType) *Limits {
	adapterLimit := instanceTypeInfo.EniQuantity
	ipv4PerAdapter := instanceTypeInfo.EniPrivateIpAddressQuantity
	ipv6PerAdapter := instanceTypeInfo.EniIpv6AddressQuantity
	memberAdapterLimit := instanceTypeInfo.EniTotalQuantity - instanceTypeInfo.EniQuantity
	eRdmaLimit := instanceTypeInfo.EriQuantity
	// exclude eth0 eth1
	maxMemberAdapterLimit := instanceTypeInfo.EniTotalQuantity - 2
	if !instanceTypeInfo.EniTrunkSupported {
		memberAdapterLimit = 0
		maxMemberAdapterLimit = 0
	}

	cards := lo.Map(instanceTypeInfo.NetworkCards.NetworkCardInfo, func(item ecs.NetworkCardInfo, index int) NetworkCard {
		return NetworkCard{
			Index: item.NetworkCardIndex,
		}
	})
	if len(cards) == 0 {
		cards = nil
	}
	return &Limits{
		InstanceTypeID:        instanceTypeInfo.InstanceTypeId,
		Adapters:              adapterLimit,
		TotalAdapters:         instanceTypeInfo.EniTotalQuantity,
		IPv4PerAdapter:        max(ipv4PerAdapter, 0),
		IPv6PerAdapter:        max(ipv6PerAdapter, 0),
		MemberAdapterLimit:    max(memberAdapterLimit, 0),
		MaxMemberAdapterLimit: max(maxMemberAdapterLimit, 0),
		ERdmaAdapters:         max(eRdmaLimit, 0),
		InstanceBandwidthRx:   instanceTypeInfo.InstanceBandwidthRx,
		InstanceBandwidthTx:   instanceTypeInfo.InstanceBandwidthTx,
		NetworkCards:          cards,
	}
}

var defaultLimitProvider LimitProvider
var once sync.Once

func GetLimitProvider() LimitProvider {
	once.Do(func() {
		defaultLimitProvider = NewProvider()
	})
	return defaultLimitProvider
}
