package ipam

import (
	"context"
	"net"

	"github.com/AliyunContainerService/terway/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
)

// API the interface of ecs operation set
type API interface {
	AllocateENI(ctx context.Context, vSwitch string, securityGroup []string, instanceID string, trunk bool, ipCount int, eniTags map[string]string) (*types.ENI, error)
	GetAttachedENIs(ctx context.Context, containsMainENI bool, trunkENIID string) ([]*types.ENI, error)
	GetSecondaryENIMACs(ctx context.Context) ([]string, error)
	GetENIByMac(ctx context.Context, mac string) (*types.ENI, error)
	FreeENI(ctx context.Context, eniID string, instanceID string) error
	GetENIIPs(ctx context.Context, mac string) ([]net.IP, []net.IP, error)
	AssignNIPsForENI(ctx context.Context, eniID, mac string, count int) ([]net.IP, []net.IP, error)
	UnAssignIPsForENI(ctx context.Context, eniID, mac string, ipv4s []net.IP, ipv6s []net.IP) error
	GetAttachedSecurityGroups(ctx context.Context, instanceID string) ([]string, error)
	CheckEniSecurityGroup(ctx context.Context, sgIDs []string) error
	DescribeInstanceTypes(ctx context.Context, types []string) ([]ecs.InstanceType, error)

	// FIXME remove vendor for vpc
	DescribeVSwitchByID(ctx context.Context, vSwitch string) (*vpc.VSwitch, error)
	// EIP
	AllocateEipAddress(ctx context.Context, bandwidth int, chargeType types.InternetChargeType, eipID, eniID string, eniIP net.IP, allowRob bool, isp, bandwidthPackageID, poolID string) (*types.EIP, error)
	UnassociateEipAddress(ctx context.Context, eipID, eniID, eniIP string) error
	ReleaseEipAddress(ctx context.Context, eipID, eniID string, eniIP net.IP) error
	QueryEniIDByIP(ctx context.Context, vpcID string, address net.IP) (string, error)
}
