package client

import (
	"context"
	"fmt"
	"time"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
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
func (a *VPCService) DescribeVSwitchByID(ctx context.Context, vSwitchID string) (*vpc.VSwitch, error) {
	ctx, span := a.Tracer.Start(ctx, APIDescribeVSwitches)
	defer span.End()

	err := a.RateLimiter.Wait(ctx, APIDescribeVSwitches)
	if err != nil {
		return nil, err
	}

	req := vpc.CreateDescribeVSwitchesRequest()
	req.VSwitchId = vSwitchID

	l := LogFields(logf.FromContext(ctx), req)

	start := time.Now()
	resp, err := a.ClientSet.VPC().DescribeVSwitches(req)
	metric.OpenAPILatency.WithLabelValues(APIDescribeVSwitches, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "DescribeVSwitches failed")
		return nil, err
	}
	if len(resp.VSwitches.VSwitch) == 0 {
		return nil, apiErr.ErrNotFound
	}
	if len(resp.VSwitches.VSwitch) > 0 {
		return &resp.VSwitches.VSwitch[0], nil
	}
	return nil, err
}
