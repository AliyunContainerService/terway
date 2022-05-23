package client

import (
	"context"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
)

type VSwitch interface {
	DescribeVSwitchByID(ctx context.Context, vSwitchID string) (*vpc.VSwitch, error)
}

type EIP interface {
	AllocateEIPAddress(bandwidth, chargeType, isp string) (*vpc.AllocateEipAddressResponse, error)
	AssociateEIPAddress(eipID, eniID, privateIP string) error
	UnAssociateEIPAddress(eipID, eniID, eniIP string) error
	ReleaseEIPAddress(eipID string) error
	AddCommonBandwidthPackageIP(eipID, packageID string) error
}
