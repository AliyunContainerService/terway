//go:generate mockery --name VPC --tags default_build
//go:generate mockery --name EFLO --tags default_build

package client

import (
	"context"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
)

type VPC interface {
	DescribeVSwitchByID(ctx context.Context, vSwitchID string) (*vpc.VSwitch, error)
}

type EFLO interface {
	GetNodeInfoForPod(ctx context.Context, nodeID string) (*eflo.Content, error)
}
