package fake

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
)

var _ client.VSwitch = &OpenAPI{}
var _ client.ENI = &OpenAPI{}

type OpenAPI struct {
	sync.Mutex
	VSwitches map[string]vpc.VSwitch
	ENIs      map[string]ecs.NetworkInterfaceSet
}

func (v *OpenAPI) CreateNetworkInterface(ctx context.Context, instanceType client.ENIType, vSwitch string, securityGroups []string, ipCount, ipv6Count int, eniTags map[string]string) (*ecs.CreateNetworkInterfaceResponse, error) {
	v.Lock()
	defer v.Unlock()

	eni := &ecs.CreateNetworkInterfaceResponse{
		Status:             "Available",
		Type:               string(instanceType),
		NetworkInterfaceId: fmt.Sprintf("eni-%s", uuid.NewUUID()),
		VSwitchId:          vSwitch,
		SecurityGroupIds: ecs.SecurityGroupIdsInCreateNetworkInterface{
			SecurityGroupId: securityGroups,
		},
		PrivateIpSets: ecs.PrivateIpSetsInCreateNetworkInterface{},
		Ipv6Sets:      ecs.Ipv6SetsInCreateNetworkInterface{},
	}
	if v.ENIs == nil {
		v.ENIs = make(map[string]ecs.NetworkInterfaceSet)
	}

	v.ENIs[eni.NetworkInterfaceId] = ecs.NetworkInterfaceSet{
		Type:               eni.Type,
		Status:             eni.Status,
		NetworkInterfaceId: eni.NetworkInterfaceId,
		VSwitchId:          eni.VSwitchId,
		SecurityGroupIds: ecs.SecurityGroupIdsInDescribeNetworkInterfaces{
			SecurityGroupId: securityGroups,
		},
	}

	return eni, nil
}

func (v *OpenAPI) DescribeNetworkInterface(ctx context.Context, vpcID string, eniID []string, instanceID string, instanceType client.ENIType, status client.ENIStatus, tags map[string]string) ([]ecs.NetworkInterfaceSet, error) {
	v.Lock()
	defer v.Unlock()

	var r []ecs.NetworkInterfaceSet
	for _, id := range eniID {
		eni, ok := v.ENIs[id]
		if !ok {
			return nil, apiErr.ErrNotFound
		}
		r = append(r, eni)
	}

	return r, nil
}

func (v *OpenAPI) AttachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	v.Lock()
	defer v.Unlock()

	eni, ok := v.ENIs[eniID]
	if !ok {
		return apiErr.ErrNotFound
	}
	eni.Status = "InUse"
	v.ENIs[eniID] = eni
	return nil
}

func (v *OpenAPI) DetachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	v.Lock()
	defer v.Unlock()

	eni, ok := v.ENIs[eniID]
	if !ok {
		return apiErr.ErrNotFound
	}
	eni.Status = "Available"
	v.ENIs[eniID] = eni
	return nil
}

func (v *OpenAPI) DeleteNetworkInterface(ctx context.Context, eniID string) error {
	v.Lock()
	defer v.Unlock()
	delete(v.ENIs, eniID)
	return nil
}

func (v *OpenAPI) WaitForNetworkInterface(ctx context.Context, eniID string, status client.ENIStatus, backoff wait.Backoff, ignoreNotExist bool) (*ecs.NetworkInterfaceSet, error) {
	eni, err := v.DescribeNetworkInterface(ctx, "", []string{eniID}, "", "", "", nil)
	if errors.Is(err, apiErr.ErrNotFound) && ignoreNotExist {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(eni) != 1 {
		return nil, apiErr.ErrNotFound
	}
	if eni[0].Status == string(status) {
		return &eni[0], nil
	}
	return nil, apiErr.ErrNotFound
}

func (v *OpenAPI) AssignPrivateIPAddress(ctx context.Context, eniID string, count int) ([]net.IP, error) {
	return nil, nil
}

func (v *OpenAPI) UnAssignPrivateIPAddresses(ctx context.Context, eniID string, ips []net.IP) error {
	return nil
}

func (v *OpenAPI) AssignIpv6Addresses(ctx context.Context, eniID string, count int) ([]net.IP, error) {
	return nil, nil
}

func (v *OpenAPI) UnAssignIpv6Addresses(ctx context.Context, eniID string, ips []net.IP) error {
	return nil
}

func (v *OpenAPI) DescribeVSwitchByID(ctx context.Context, vSwitchID string) (*vpc.VSwitch, error) {
	v.Lock()
	defer v.Unlock()
	vsw, ok := v.VSwitches[vSwitchID]
	if !ok {
		return nil, apiErr.ErrNotFound
	}
	return &vsw, nil
}
