//go:build default_build

package client

import (
	"context"
	"net"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"k8s.io/apimachinery/pkg/util/wait"
)

type ENI interface {
	CreateNetworkInterface(ctx context.Context, trunk bool, vSwitchID string, securityGroups []string, resourceGroupID string, ipCount, ipv6Count int, eniTags map[string]string) (*NetworkInterface, error)
	DescribeNetworkInterface(ctx context.Context, vpcID string, eniID []string, instanceID string, instanceType string, status string, tags map[string]string) ([]*NetworkInterface, error)
	AttachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error
	DetachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error
	DeleteNetworkInterface(ctx context.Context, eniID string) error
	WaitForNetworkInterface(ctx context.Context, eniID string, status string, backoff wait.Backoff, ignoreNotExist bool) (*NetworkInterface, error)
	AssignPrivateIPAddress(ctx context.Context, eniID string, count int, idempotent string) ([]net.IP, error)
	UnAssignPrivateIPAddresses(ctx context.Context, eniID string, ips []net.IP) error
	AssignIpv6Addresses(ctx context.Context, eniID string, count int, idempotentKey string) ([]net.IP, error)
	UnAssignIpv6Addresses(ctx context.Context, eniID string, ips []net.IP) error
	ModifyNetworkInterfaceAttribute(ctx context.Context, eniID string, securityGroupIDs []string) error
}

type ECS interface {
	DescribeInstanceTypes(ctx context.Context, types []string) ([]ecs.InstanceType, error)
}
