//go:build default_build

//go:generate mockery --name ECS --tags default_build

package client

import (
	"context"
	"net/netip"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"k8s.io/apimachinery/pkg/util/wait"
)

type ECS interface {
	CreateNetworkInterface(ctx context.Context, opts ...CreateNetworkInterfaceOption) (*NetworkInterface, error)
	DescribeNetworkInterface(ctx context.Context, vpcID string, eniID []string, instanceID string, instanceType string, status string, tags map[string]string) ([]*NetworkInterface, error)
	AttachNetworkInterface(ctx context.Context, opts ...AttachNetworkInterfaceOption) error
	DetachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error
	DeleteNetworkInterface(ctx context.Context, eniID string) error
	WaitForNetworkInterface(ctx context.Context, eniID string, status string, backoff wait.Backoff, ignoreNotExist bool) (*NetworkInterface, error)
	AssignPrivateIPAddress(ctx context.Context, opts ...AssignPrivateIPAddressOption) ([]netip.Addr, error)
	UnAssignPrivateIPAddresses(ctx context.Context, eniID string, ips []netip.Addr) error
	AssignIpv6Addresses(ctx context.Context, opts ...AssignIPv6AddressesOption) ([]netip.Addr, error)
	UnAssignIpv6Addresses(ctx context.Context, eniID string, ips []netip.Addr) error
	DescribeInstanceTypes(ctx context.Context, types []string) ([]ecs.InstanceType, error)
}

type BackendAPI int

const (
	BackendAPIECS BackendAPI = iota
	BackendAPIEFLO
	BackendAPIEFLOHDENI
)

type backendAPIKey struct{}

func GetBackendAPI(ctx context.Context) BackendAPI {
	value, ok := ctx.Value(backendAPIKey{}).(BackendAPI)
	if !ok {
		return BackendAPIECS
	}
	return value
}

func SetBackendAPI(ctx context.Context, b BackendAPI) context.Context {
	return context.WithValue(ctx, backendAPIKey{}, b)
}

type ENI interface {
	CreateNetworkInterfaceV2(ctx context.Context, opts ...CreateNetworkInterfaceOption) (*NetworkInterface, error)
	DescribeNetworkInterfaceV2(ctx context.Context, opts ...DescribeNetworkInterfaceOption) ([]*NetworkInterface, error)
	AttachNetworkInterface(ctx context.Context, opts ...AttachNetworkInterfaceOption) error
	AttachNetworkInterfaceV2(ctx context.Context, opts ...AttachNetworkInterfaceOption) error
	DetachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error
	DetachNetworkInterfaceV2(ctx context.Context, opts ...DetachNetworkInterfaceOption) error
	DeleteNetworkInterfaceV2(ctx context.Context, eniID string) error
	AssignPrivateIPAddressV2(ctx context.Context, opts ...AssignPrivateIPAddressOption) ([]IPSet, error)
	UnAssignPrivateIPAddressesV2(ctx context.Context, eniID string, ips []IPSet) error
	AssignIpv6AddressesV2(ctx context.Context, opts ...AssignIPv6AddressesOption) ([]IPSet, error)
	UnAssignIpv6AddressesV2(ctx context.Context, eniID string, ips []IPSet) error
	WaitForNetworkInterfaceV2(ctx context.Context, eniID string, status string, backoff wait.Backoff, ignoreNotExist bool) (*NetworkInterface, error)
}
