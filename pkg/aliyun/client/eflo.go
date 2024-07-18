package client

import (
	"context"
	"fmt"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/AliyunContainerService/terway/pkg/metric"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
)

func (a *OpenAPI) CreateElasticNetworkInterface(zoneID, nodeID, vSwitchID, securityGroupID string) (string, string, error) {
	req := eflo.CreateCreateElasticNetworkInterfaceRequest()
	req.ZoneId = zoneID
	req.NodeId = nodeID
	req.VSwitchId = vSwitchID
	req.SecurityGroupId = securityGroupID

	start := time.Now()
	resp, err := a.ClientSet.EFLO().CreateElasticNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues("CreateElasticNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return "", "", err
	}

	return resp.Content.NodeId, resp.Content.ElasticNetworkInterfaceId, nil
}

func (a *OpenAPI) DeleteElasticNetworkInterface(ctx context.Context, eniID string) error {
	l := logf.FromContext(ctx).WithValues(
		LogFieldAPI, "DeleteElasticNetworkInterface",
		LogFieldENIID, eniID,
	)
	req := eflo.CreateDeleteElasticNetworkInterfaceRequest()
	req.ElasticNetworkInterfaceId = eniID

	start := time.Now()
	resp, err := a.ClientSet.EFLO().DeleteElasticNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues("DeleteElasticNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "failed")
		return err
	}

	l.WithValues(LogFieldRequestID, resp.RequestId).Info("succeed")
	return nil
}

func (a *OpenAPI) AssignLeniPrivateIPAddress(ctx context.Context, eniID, prefer string) (string, error) {
	l := logf.FromContext(ctx).WithValues(
		LogFieldAPI, "AssignLeniPrivateIpAddress",
		LogFieldENIID, eniID,
	)

	req := eflo.CreateAssignLeniPrivateIpAddressRequest()
	req.ElasticNetworkInterfaceId = eniID
	req.PrivateIpAddress = prefer

	start := time.Now()
	resp, err := a.ClientSet.EFLO().AssignLeniPrivateIpAddress(req)
	metric.OpenAPILatency.WithLabelValues("AssignLeniPrivateIPAddress", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "failed")

		return "", err
	}

	l.WithValues(LogFieldRequestID, resp.RequestId).Info("assign", "ipName", resp.Content.IpName, "ip", resp.Content.Ip, "private", resp.Content.PrivateIpAddress)

	return resp.Content.IpName, nil
}

func (a *OpenAPI) UnassignLeniPrivateIPAddress(ctx context.Context, eniID, ipName string) error {
	l := logf.FromContext(ctx).WithValues(
		LogFieldAPI, "UnassignLeniPrivateIpAddress",
		LogFieldENIID, eniID,
		"ipName", ipName,
	)

	req := eflo.CreateUnassignLeniPrivateIpAddressRequest()
	req.ElasticNetworkInterfaceId = eniID
	req.IpName = ipName

	start := time.Now()
	resp, err := a.ClientSet.EFLO().UnassignLeniPrivateIpAddress(req)
	metric.OpenAPILatency.WithLabelValues("UnassignLeniPrivateIpAddress", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "failed")
		return err
	}

	l.WithValues(LogFieldRequestID, resp.RequestId).Info("success")

	return nil
}

func (a *OpenAPI) GetElasticNetworkInterface(eniID string) (*eflo.Content, error) {
	req := eflo.CreateGetElasticNetworkInterfaceRequest()
	req.ElasticNetworkInterfaceId = eniID

	start := time.Now()
	resp, err := a.ClientSet.EFLO().GetElasticNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues("GetElasticNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, err
	}

	return &resp.Content, nil
}

func (a *OpenAPI) ListLeniPrivateIPAddresses(ctx context.Context, eniID, ipName, ipAddress string) (*eflo.Content, error) {
	l := logf.FromContext(ctx).WithValues(
		LogFieldAPI, "ListLeniPrivateIpAddresses",
		LogFieldENIID, eniID,
		"ipName", ipName,
		"ipAddress", ipAddress,
	)
	req := eflo.CreateListLeniPrivateIpAddressesRequest()
	req.ElasticNetworkInterfaceId = eniID
	req.IpName = ipName
	req.PrivateIpAddress = ipAddress

	start := time.Now()
	resp, err := a.ClientSet.EFLO().ListLeniPrivateIpAddresses(req)
	metric.OpenAPILatency.WithLabelValues("ListLeniPrivateIpAddresses", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "failed")

		return nil, err
	}

	l.WithValues(LogFieldRequestID, resp.RequestId).Info("success")

	return &resp.Content, nil
}

func (a *OpenAPI) ListElasticNetworkInterfaces(ctx context.Context, zoneID, nodeID, eniID string) (*eflo.Content, error) {
	l := logf.FromContext(ctx).WithValues(
		LogFieldAPI, "ListElasticNetworkInterfaces",
		LogFieldInstanceID, nodeID,
	)
	req := eflo.CreateListElasticNetworkInterfacesRequest()
	req.ElasticNetworkInterfaceId = eniID
	req.ZoneId = zoneID
	req.NodeId = nodeID
	req.PageSize = requests.NewInteger(100)

	start := time.Now()
	resp, err := a.ClientSet.EFLO().ListElasticNetworkInterfaces(req)
	metric.OpenAPILatency.WithLabelValues("ListElasticNetworkInterfaces", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "failed")

		return nil, err
	}

	l.WithValues(LogFieldRequestID, resp.RequestId).Info("success")

	return &resp.Content, nil
}

func (a *OpenAPI) GetNodeInfoForPod(ctx context.Context, nodeID string) (*eflo.Content, error) {
	l := logf.FromContext(ctx).WithValues(
		LogFieldAPI, "GetNodeInfoForPod",
		LogFieldInstanceID, nodeID,
	)

	req := eflo.CreateGetNodeInfoForPodRequest()
	req.NodeId = nodeID

	start := time.Now()
	resp, err := a.ClientSet.EFLO().GetNodeInfoForPod(req)
	metric.OpenAPILatency.WithLabelValues("GetNodeInfoForPod", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, err
	}
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("success")

	return &resp.Content, nil
}
