package fake

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
)

var _ client.VPC = &OpenAPI{}
var _ client.ECS = &OpenAPI{}

type OpenAPI struct {
	sync.Mutex
	VSwitches map[string]vpc.VSwitch
	ENIs      map[string]*client.NetworkInterface

	IPAM   map[string]netip.Addr // index by vSwitch id
	IPAMV6 map[string]netip.Addr // index by vSwitch id
}

func New() *OpenAPI {
	return &OpenAPI{
		Mutex:     sync.Mutex{},
		VSwitches: map[string]vpc.VSwitch{},
		ENIs:      map[string]*client.NetworkInterface{},
		IPAM:      map[string]netip.Addr{},
		IPAMV6:    map[string]netip.Addr{},
	}
}

func (o *OpenAPI) DescribeInstanceTypes(ctx context.Context, types []string) ([]ecs.InstanceType, error) {
	return []ecs.InstanceType{
		{
			InstancePpsTx:               1600000,
			PrimaryEniQueueNumber:       8,
			TotalEniQueueQuantity:       32,
			EniTrunkSupported:           true,
			InstanceTypeFamily:          "ecs.g7",
			InstancePpsRx:               1600000,
			InstanceBandwidthRx:         5120000,
			InstanceBandwidthTx:         5120000,
			SecondaryEniQueueNumber:     8,
			InstanceTypeId:              "ecs.g7.2xlarge",
			MemorySize:                  32,
			EniIpv6AddressQuantity:      15,
			EniTotalQuantity:            10,
			CpuCoreCount:                8,
			EniQuantity:                 4,
			EniPrivateIpAddressQuantity: 15,
		},
	}, nil
}

func (o *OpenAPI) CreateNetworkInterface(ctx context.Context, trunk, erdma bool, vSwitch string, securityGroups []string, resourceGroupID string, ipCount, ipv6Count int, eniTags map[string]string) (*client.NetworkInterface, error) {
	o.Lock()
	defer o.Unlock()

	var v4Set []ecs.PrivateIpSet
	var v6Set []ecs.Ipv6Set

	v4Set = append(v4Set, ecs.PrivateIpSet{
		PrivateIpAddress: o.nextIP(vSwitch).String(),
		Primary:          true,
	})
	for i := 1; i < ipCount; i++ {
		v4Set = append(v4Set, ecs.PrivateIpSet{
			PrivateIpAddress: o.nextIP(vSwitch).String(),
		})
	}
	for i := 0; i < ipv6Count; i++ {
		v6Set = append(v6Set, ecs.Ipv6Set{
			Ipv6Address: o.nextIPV6(vSwitch).String(),
		})
	}

	eni := &client.NetworkInterface{
		Status:             "Available",
		Type:               "Secondary",
		NetworkInterfaceID: fmt.Sprintf("eni-%s", uuid.NewUUID()),
		VSwitchID:          vSwitch,
		SecurityGroupIDs:   securityGroups,
		ResourceGroupID:    resourceGroupID,
		PrivateIPAddress:   v4Set[0].PrivateIpAddress,
		PrivateIPSets:      v4Set,
		IPv6Set:            v6Set,
	}
	for k, val := range eniTags {
		eni.Tags = append(eni.Tags, ecs.Tag{
			Key:      k,
			Value:    val,
			TagValue: k,
			TagKey:   val,
		})
	}

	if trunk {
		eni.Type = "Trunk"
	}
	if erdma {
		eni.NetworkInterfaceTrafficMode = client.ENITrafficModeRDMA
	}
	if o.ENIs == nil {
		o.ENIs = make(map[string]*client.NetworkInterface)
	}

	o.ENIs[eni.NetworkInterfaceID] = eni

	return eni, nil
}

func (o *OpenAPI) DescribeNetworkInterface(ctx context.Context, vpcID string, eniID []string, instanceID, instanceType, status string, tags map[string]string) ([]*client.NetworkInterface, error) {
	o.Lock()
	defer o.Unlock()

	var r []*client.NetworkInterface
	for _, id := range eniID {
		eni, ok := o.ENIs[id]
		if !ok {
			return nil, apiErr.ErrNotFound
		}
		r = append(r, eni)
	}

	return r, nil
}

func (o *OpenAPI) AttachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	o.Lock()
	defer o.Unlock()

	eni, ok := o.ENIs[eniID]
	if !ok {
		return apiErr.ErrNotFound
	}
	if trunkENIID != "" {
		eni.Type = "Member"
	}
	eni.Status = "InUse"
	o.ENIs[eniID] = eni
	return nil
}

func (o *OpenAPI) DetachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	o.Lock()
	defer o.Unlock()

	eni, ok := o.ENIs[eniID]
	if !ok {
		return apiErr.ErrNotFound
	}
	if trunkENIID != "" {
		eni.Type = "Secondary"
	}
	eni.Status = "Available"
	o.ENIs[eniID] = eni
	return nil
}

func (o *OpenAPI) DeleteNetworkInterface(ctx context.Context, eniID string) error {
	o.Lock()
	defer o.Unlock()
	delete(o.ENIs, eniID)
	return nil
}

func (o *OpenAPI) WaitForNetworkInterface(ctx context.Context, eniID string, status string, backoff wait.Backoff, ignoreNotExist bool) (*client.NetworkInterface, error) {
	eni, err := o.DescribeNetworkInterface(ctx, "", []string{eniID}, "", "", "", nil)
	if errors.Is(err, apiErr.ErrNotFound) && ignoreNotExist {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(eni) != 1 {
		return nil, apiErr.ErrNotFound
	}
	if eni[0].Status == status {
		return eni[0], nil
	}
	return nil, apiErr.ErrNotFound
}

func (o *OpenAPI) AssignPrivateIPAddress(ctx context.Context, eniID string, count int, idempotent string) ([]netip.Addr, error) {
	o.Lock()
	defer o.Unlock()

	eni, ok := o.ENIs[eniID]
	if !ok {
		return nil, apiErr.ErrNotFound
	}
	var ips []netip.Addr
	for i := 0; i < count; i++ {
		ip := o.nextIP(eni.VSwitchID)
		ips = append(ips, ip)
		eni.PrivateIPSets = append(eni.PrivateIPSets, ecs.PrivateIpSet{
			PrivateIpAddress: ip.String(),
		})
	}
	o.ENIs[eniID] = eni

	return ips, nil
}

func (o *OpenAPI) UnAssignPrivateIPAddresses(ctx context.Context, eniID string, ips []netip.Addr) error {
	return nil
}

func (o *OpenAPI) AssignIpv6Addresses(ctx context.Context, eniID string, count int, idempotentKey string) ([]netip.Addr, error) {
	eni, ok := o.ENIs[eniID]
	if !ok {
		return nil, apiErr.ErrNotFound
	}
	var ips []netip.Addr
	for i := 0; i < count; i++ {
		ip := o.nextIPV6(eni.VSwitchID)
		ips = append(ips, ip)
		eni.IPv6Set = append(eni.IPv6Set, ecs.Ipv6Set{
			Ipv6Address: ip.String(),
		})
	}
	o.ENIs[eniID] = eni

	return ips, nil
}

func (o *OpenAPI) UnAssignIpv6Addresses(ctx context.Context, eniID string, ips []netip.Addr) error {
	return nil
}

func (o *OpenAPI) DescribeVSwitchByID(ctx context.Context, vSwitchID string) (*vpc.VSwitch, error) {
	o.Lock()
	defer o.Unlock()
	vsw, ok := o.VSwitches[vSwitchID]
	if !ok {
		return nil, apiErr.ErrNotFound
	}
	return &vsw, nil
}

func (o *OpenAPI) ModifyNetworkInterfaceAttribute(ctx context.Context, eniID string, securityGroupIDs []string) error {
	o.Lock()
	defer o.Unlock()

	eni, ok := o.ENIs[eniID]
	if !ok {
		return apiErr.ErrNotFound
	}
	eni.SecurityGroupIDs = securityGroupIDs
	return nil
}

func (o *OpenAPI) nextIP(vSwitchID string) netip.Addr {
	pre, ok := o.IPAM[vSwitchID]
	if ok {
		o.IPAM[vSwitchID] = pre.Next()
		return o.IPAM[vSwitchID]
	}
	vsw, ok := o.VSwitches[vSwitchID]
	if !ok {
		return netip.Addr{}
	}
	prefix, err := netip.ParsePrefix(vsw.CidrBlock)
	if err != nil {
		return netip.Addr{}
	}
	o.IPAM[vSwitchID] = prefix.Addr().Next()
	return o.IPAM[vSwitchID]
}

func (o *OpenAPI) nextIPV6(vSwitchID string) netip.Addr {
	pre, ok := o.IPAMV6[vSwitchID]
	if ok {
		o.IPAMV6[vSwitchID] = pre.Next()
		return o.IPAMV6[vSwitchID]
	}
	vsw, ok := o.VSwitches[vSwitchID]
	if !ok {
		return netip.Addr{}
	}

	prefix, err := netip.ParsePrefix(vsw.Ipv6CidrBlock)
	if err != nil {
		return netip.Addr{}
	}
	o.IPAMV6[vSwitchID] = prefix.Addr().Next()
	return o.IPAMV6[vSwitchID]
}
