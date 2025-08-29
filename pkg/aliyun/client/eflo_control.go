package client

import (
	"context"
	"fmt"
	"time"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
	"github.com/AliyunContainerService/terway/pkg/metric"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"go.opentelemetry.io/otel/trace"
)

var _ EFLOControl = &EFLOControlService{}

const (
	APIDescribeNode     = "DescribeNode"
	APIDescribeNodeType = "DescribeNodeType"
)

type DescribeNodeResponse struct {
	NodeID   string
	ZoneID   string
	NodeType string
}

type DescribeNodeTypeResponse struct {
	EniHighDenseQuantity        int
	EniIpv6AddressQuantity      int
	EniPrivateIpAddressQuantity int
	EniQuantity                 int
}

type EFLOControlService struct {
	ClientSet        credential.Client
	IdempotentKeyGen IdempotentKeyGen
	RateLimiter      *RateLimiter
	Tracer           trace.Tracer
}

func NewEFLOControlService(clientSet credential.Client, rateLimiter *RateLimiter, tracer trace.Tracer) *EFLOControlService {
	return &EFLOControlService{
		ClientSet:        clientSet,
		IdempotentKeyGen: NewIdempotentKeyGenerator(),
		RateLimiter:      rateLimiter,
		Tracer:           tracer,
	}
}

func (a *EFLOControlService) DescribeNode(ctx context.Context, opts ...DescribeNodeRequestOption) (*DescribeNodeResponse, error) {
	options := &DescribeNodeRequestOptions{}
	for _, opt := range opts {
		opt.ApplyTo(options)
	}
	err := a.RateLimiter.Wait(ctx, APIDescribeNode)
	if err != nil {
		return nil, err
	}

	req, err := options.EFLOControl()
	if err != nil {
		return nil, err
	}

	l := LogFields(logf.FromContext(ctx), req)

	start := time.Now()
	resp, err := a.ClientSet.EFLOController().DescribeNode(req)
	metric.OpenAPILatency.WithLabelValues(APIDescribeNode, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

	if err != nil {
		return nil, apiErr.WarpError2(err, APIDescribeNode)
	}

	if resp.GetBody() == nil {
		return nil, fmt.Errorf("describe node response body is nil")
	}

	describeNodeResponse := &DescribeNodeResponse{}
	describeNodeResponse.NodeID = FromPtr(resp.GetBody().NodeId)
	describeNodeResponse.ZoneID = FromPtr(resp.GetBody().ZoneId)
	describeNodeResponse.NodeType = FromPtr(resp.GetBody().NodeType)

	l.Info("describe node", "resp", describeNodeResponse, LogFieldRequestID, resp.GetBody().RequestId)
	return describeNodeResponse, nil
}

func (a *EFLOControlService) DescribeNodeType(ctx context.Context, opts ...DescribeNodeTypeRequestOption) (*DescribeNodeTypeResponse, error) {
	options := &DescribeNodeTypeRequestOptions{}
	for _, opt := range opts {
		opt.ApplyTo(options)
	}
	err := a.RateLimiter.Wait(ctx, APIDescribeNodeType)
	if err != nil {
		return nil, err
	}

	req, err := options.EFLOControl()
	if err != nil {
		return nil, err
	}

	l := LogFields(logf.FromContext(ctx), req)
	start := time.Now()
	resp, err := a.ClientSet.EFLOController().DescribeNodeType(req)
	metric.OpenAPILatency.WithLabelValues(APIDescribeNodeType, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

	if err != nil {
		return nil, apiErr.WarpError2(err, APIDescribeNodeType)
	}

	if resp.GetBody() == nil {
		return nil, fmt.Errorf("describe node type response body is nil")
	}

	describeNodeTypeResponse := &DescribeNodeTypeResponse{}
	describeNodeTypeResponse.EniHighDenseQuantity = int(FromPtr(resp.GetBody().EniHighDenseQuantity))
	describeNodeTypeResponse.EniIpv6AddressQuantity = int(FromPtr(resp.GetBody().EniIpv6AddressQuantity))
	describeNodeTypeResponse.EniPrivateIpAddressQuantity = int(FromPtr(resp.GetBody().EniPrivateIpAddressQuantity))
	describeNodeTypeResponse.EniQuantity = int(FromPtr(resp.GetBody().EniQuantity))

	l.Info("describe node type", "resp", describeNodeTypeResponse, LogFieldRequestID, resp.GetBody().RequestId)
	return describeNodeTypeResponse, nil
}
