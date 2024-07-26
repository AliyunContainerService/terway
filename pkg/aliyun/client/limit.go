//go:generate mockery --name LimitProvider --tags default_build

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"golang.org/x/sync/singleflight"
	"k8s.io/apimachinery/pkg/util/cache"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

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
	return l.ERdmaAdapters
}

func (l *Limits) ExclusiveENIPod() int {
	return l.Adapters - 1
}

type LimitProvider interface {
	GetLimit(client interface{}, instanceType string) (*Limits, error)
	GetLimitFromAnno(anno map[string]string) (*Limits, error)
}

type EfloLimitProvider struct{}

func NewEfloLimitProvider() *EfloLimitProvider {
	return &EfloLimitProvider{}
}

func (e *EfloLimitProvider) GetLimit(client interface{}, instanceType string) (*Limits, error) {
	a, ok := client.(EFLO)
	if !ok {
		return nil, fmt.Errorf("unsupported client")
	}
	resp, err := a.GetNodeInfoForPod(context.Background(), instanceType)
	if err != nil {
		return nil, err
	}

	return &Limits{
		Adapters:       resp.LeniQuota,
		TotalAdapters:  resp.LeniQuota,
		IPv4PerAdapter: resp.LniSipQuota,
	}, nil
}

func (e *EfloLimitProvider) GetLimitFromAnno(anno map[string]string) (*Limits, error) {
	return nil, nil
}

type ECSLimitProvider struct {
	cache cache.LRUExpireCache
	ttl   time.Duration

	g singleflight.Group
}

func NewECSLimitProvider() *ECSLimitProvider {
	return &ECSLimitProvider{
		cache: *cache.NewLRUExpireCache(10 * 1000),
		ttl:   15 * 24 * time.Hour,
	}
}

func (d *ECSLimitProvider) GetLimit(client interface{}, instanceType string) (*Limits, error) {
	a, ok := client.(ECS)
	if !ok {
		return nil, fmt.Errorf("unsupported client")
	}
	v, ok := d.cache.Get(instanceType)
	if ok {
		return v.(*Limits), nil
	}

	var req []string
	if instanceType != "" {
		req = append(req, instanceType)
	}

	v, err, _ := d.g.Do(instanceType, func() (interface{}, error) {
		ins, err := a.DescribeInstanceTypes(context.Background(), req)
		if err != nil {
			return nil, err
		}
		return ins, nil
	})
	if err != nil {
		return nil, err
	}

	ins := v.([]ecs.InstanceType)

	for _, instanceTypeInfo := range ins {
		instanceTypeID := instanceTypeInfo.InstanceTypeId

		limit := getInstanceType(&instanceTypeInfo)

		d.cache.Add(instanceTypeID, limit, d.ttl)
		logf.Log.Info("instance limit", instanceTypeID, limit)
	}
	if instanceType == "" {
		return nil, nil
	}
	v, ok = d.cache.Get(instanceType)
	if !ok {
		return nil, fmt.Errorf("unexpected error")
	}

	return v.(*Limits), nil
}

func (d *ECSLimitProvider) GetLimitFromAnno(anno map[string]string) (*Limits, error) {
	v, ok := anno["alibabacloud.com/instance-type-info"]
	if !ok {
		return nil, nil
	}

	instanceType := &ecs.InstanceType{}
	err := json.Unmarshal([]byte(v), instanceType)
	if err != nil {
		return nil, err
	}

	return getInstanceType(instanceType), nil
}

func getInstanceType(instanceTypeInfo *ecs.InstanceType) *Limits {
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
	}
}

var ecsProvider LimitProvider
var efloProvider LimitProvider

var LimitProviders = map[string]LimitProvider{}

func init() {
	ecsProvider = NewECSLimitProvider()
	efloProvider = NewEfloLimitProvider()

	LimitProviders = map[string]LimitProvider{
		"ecs":  ecsProvider,
		"eflo": efloProvider,
	}
}
