package client

import (
	"context"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
)

type VSwitch interface {
	DescribeVSwitchByID(ctx context.Context, vSwitchID string) (*vpc.VSwitch, error)
}
