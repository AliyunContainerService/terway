package client

import (
	"context"
	"fmt"
	"time"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
	"github.com/AliyunContainerService/terway/pkg/metric"
	vpc20160428 "github.com/alibabacloud-go/vpc-20160428/v6/client"
	"go.opentelemetry.io/otel/trace"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	APIDescribeVSwitches = "DescribeVSwitches"
)

var _ VPC = &VPCService{}

type VPCService struct {
	ClientSet        credential.Client
	IdempotentKeyGen IdempotentKeyGen
	RateLimiter      *RateLimiter
	Tracer           trace.Tracer
}

func NewVPCService(clientSet credential.Client, rateLimiter *RateLimiter, tracer trace.Tracer) *VPCService {
	return &VPCService{
		ClientSet:        clientSet,
		IdempotentKeyGen: NewIdempotentKeyGenerator(),
		RateLimiter:      rateLimiter,
		Tracer:           tracer,
	}
}

// DescribeVSwitchByID get vsw by id
func (a *VPCService) DescribeVSwitchByID(ctx context.Context, vSwitchID string) (*VSwitch, error) {
	ctx, span := a.Tracer.Start(ctx, APIDescribeVSwitches)
	defer span.End()

	err := a.RateLimiter.Wait(ctx, APIDescribeVSwitches)
	if err != nil {
		return nil, err
	}

	req := &vpc20160428.DescribeVSwitchesRequest{}
	req.VSwitchId = &vSwitchID

	l := LogFields(logf.FromContext(ctx), req)

	start := time.Now()
	resp, err := a.ClientSet.VPC().DescribeVSwitches(req)
	metric.OpenAPILatency.WithLabelValues(APIDescribeVSwitches, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "DescribeVSwitches failed")
		return nil, err
	}

	if resp.Body == nil || resp.Body.VSwitches == nil {
		return nil, fmt.Errorf("empty response body")
	}

	if len(resp.Body.VSwitches.VSwitch) == 0 {
		return nil, apiErr.ErrNotFound
	}
	if len(resp.Body.VSwitches.VSwitch) > 0 {
		return &VSwitch{
			VpcId:                   FromPtr(resp.Body.VSwitches.VSwitch[0].VSwitchId),
			Status:                  FromPtr(resp.Body.VSwitches.VSwitch[0].Status),
			AvailableIpAddressCount: FromPtr(resp.Body.VSwitches.VSwitch[0].AvailableIpAddressCount),
			NetworkAclId:            FromPtr(resp.Body.VSwitches.VSwitch[0].NetworkAclId),
			OwnerId:                 FromPtr(resp.Body.VSwitches.VSwitch[0].OwnerId),
			VSwitchId:               FromPtr(resp.Body.VSwitches.VSwitch[0].VSwitchId),
			CidrBlock:               FromPtr(resp.Body.VSwitches.VSwitch[0].CidrBlock),
			Description:             FromPtr(resp.Body.VSwitches.VSwitch[0].Description),
			ResourceGroupId:         FromPtr(resp.Body.VSwitches.VSwitch[0].ResourceGroupId),
			ZoneId:                  FromPtr(resp.Body.VSwitches.VSwitch[0].ZoneId),
			Ipv6CidrBlock:           FromPtr(resp.Body.VSwitches.VSwitch[0].Ipv6CidrBlock),
			VSwitchName:             FromPtr(resp.Body.VSwitches.VSwitch[0].VSwitchName),
			ShareType:               FromPtr(resp.Body.VSwitches.VSwitch[0].ShareType),
			EnabledIpv6:             FromPtr(resp.Body.VSwitches.VSwitch[0].EnabledIpv6),
		}, nil
	}
	return nil, err
}

type VSwitch struct {
	VpcId                   string
	Status                  string
	AvailableIpAddressCount int64
	NetworkAclId            string
	OwnerId                 int64
	VSwitchId               string
	CidrBlock               string
	Description             string
	ResourceGroupId         string
	ZoneId                  string
	Ipv6CidrBlock           string
	VSwitchName             string
	ShareType               string
	EnabledIpv6             bool
}
