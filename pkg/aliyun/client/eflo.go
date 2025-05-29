package client

import (
	"context"
	"fmt"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/metric"
)

func (a *OpenAPI) DeleteElasticNetworkInterface(ctx context.Context, eniID string) error {
	req := eflo.CreateDeleteElasticNetworkInterfaceRequest()
	req.ElasticNetworkInterfaceId = eniID
	l := LogFields(logf.FromContext(ctx), req)

	err := a.RateLimiter.Wait(ctx, APIDeleteElasticNetworkInterface)
	if err != nil {
		return err
	}

	start := time.Now()
	resp, err := a.ClientSet.EFLO().DeleteElasticNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues(APIDeleteElasticNetworkInterface, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "failed")
		return err
	}

	if resp.Code != 0 {
		err = &apiErr.EFLOCode{
			Code:      resp.Code,
			Message:   resp.Message,
			RequestID: resp.RequestId,
			Content:   resp.Content,
		}
		// 1011 IpName/leni not exist
		if !apiErr.IsEfloCode(err, apiErr.ErrEfloResourceNotFound) {
			l.Error(err, "failed")
			return err
		}
	}

	l.WithValues(LogFieldRequestID, resp.RequestId).Info("succeed")
	return nil
}

func (a *OpenAPI) UnassignLeniPrivateIPAddress(ctx context.Context, eniID, ipName string) error {
	err := a.RateLimiter.Wait(ctx, APIUnassignLeniPrivateIPAddress)
	if err != nil {
		return err
	}

	req := eflo.CreateUnassignLeniPrivateIpAddressRequest()
	req.ElasticNetworkInterfaceId = eniID
	req.IpName = ipName

	l := LogFields(logf.FromContext(ctx), req)

	start := time.Now()
	resp, err := a.ClientSet.EFLO().UnassignLeniPrivateIpAddress(req)
	metric.OpenAPILatency.WithLabelValues(APIUnassignLeniPrivateIPAddress, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "failed")
		return err
	}

	if resp.Code != 0 {
		err = &apiErr.EFLOCode{
			Code:      resp.Code,
			Message:   resp.Message,
			RequestID: resp.RequestId,
			Content:   resp.Content,
		}
		// 1011 IpName/leni not exist
		if !apiErr.IsEfloCode(err, apiErr.ErrEfloResourceNotFound) {
			l.Error(err, "failed")
			return err
		}
	}
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("success")

	return nil
}

func (a *OpenAPI) ListLeniPrivateIPAddresses(ctx context.Context, eniID, ipName, ipAddress string) (*eflo.Content, error) {
	req := eflo.CreateListLeniPrivateIpAddressesRequest()
	req.ElasticNetworkInterfaceId = eniID
	req.IpName = ipName
	req.PrivateIpAddress = ipAddress

	l := LogFields(logf.FromContext(ctx), req)

	start := time.Now()
	resp, err := a.ClientSet.EFLO().ListLeniPrivateIpAddresses(req)
	metric.OpenAPILatency.WithLabelValues("ListLeniPrivateIpAddresses", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "failed")

		return nil, err
	}

	if resp.Code != 0 {
		err = &apiErr.EFLOCode{
			Code:      resp.Code,
			Message:   resp.Message,
			RequestID: resp.RequestId,
			Content:   resp.Content,
		}
		l.Error(err, "failed")
		return nil, err
	}

	l.WithValues(LogFieldRequestID, resp.RequestId).Info("success")

	return &resp.Content, nil
}

func (a *OpenAPI) GetNodeInfoForPod(ctx context.Context, nodeID string) (*eflo.Content, error) {
	req := eflo.CreateGetNodeInfoForPodRequest()
	req.NodeId = nodeID

	l := LogFields(logf.FromContext(ctx), req)

	start := time.Now()
	resp, err := a.ClientSet.EFLO().GetNodeInfoForPod(req)
	metric.OpenAPILatency.WithLabelValues("GetNodeInfoForPod", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, err
	}
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("success",
		"HdeniQuota",
		resp.Content.HdeniQuota,
		"LeniQuota",
		resp.Content.LeniQuota,
	)

	if resp.Code != 0 {
		err = &apiErr.EFLOCode{
			Code:      resp.Code,
			Message:   resp.Message,
			RequestID: resp.RequestId,
			Content:   resp.Content,
		}
		l.Error(err, "failed")
		return nil, err
	}

	return &resp.Content, nil
}
