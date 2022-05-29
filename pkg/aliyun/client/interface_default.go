//go:build default_build

package client

import (
	"context"
	"net"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"k8s.io/apimachinery/pkg/util/wait"
)

type ENI interface {
	CreateNetworkInterface(ctx context.Context, instanceType ENIType, vSwitch string, securityGroups []string, ipCount, ipv6Count int, eniTags map[string]string) (*ecs.CreateNetworkInterfaceResponse, error)
	DescribeNetworkInterface(ctx context.Context, vpcID string, eniID []string, instanceID string, instanceType ENIType, status ENIStatus) ([]ecs.NetworkInterfaceSet, error)
	AttachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error
	DetachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error
	DeleteNetworkInterface(ctx context.Context, eniID string) error
	WaitForNetworkInterface(ctx context.Context, eniID string, status ENIStatus, backoff wait.Backoff, ignoreNotExist bool) (*ecs.NetworkInterfaceSet, error)
	AssignPrivateIPAddress(ctx context.Context, eniID string, count int) ([]net.IP, error)
	UnAssignPrivateIPAddresses(ctx context.Context, eniID string, ips []net.IP) error
	AssignIpv6Addresses(ctx context.Context, eniID string, count int) ([]net.IP, error)
	UnAssignIpv6Addresses(ctx context.Context, eniID string, ips []net.IP) error
}

type ECS interface {
	DescribeInstanceTypes(ctx context.Context, types []string) ([]ecs.InstanceType, error)
}
