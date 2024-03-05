package client

import (
	"context"
	"fmt"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
)

func (a *OpenAPI) CreateElasticNetworkInterface(zoneID, nodeID, vSwitchID, securityGroupID string) (string, string, error) {
	req := eflo.CreateCreateElasticNetworkInterfaceRequest()
	req.ZoneId = zoneID
	req.NodeId = nodeID
	req.VSwitchId = vSwitchID
	req.SecurityGroupId = securityGroupID

	resp, err := a.ClientSet.EFLO().CreateElasticNetworkInterface(req)
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

	resp, err := a.ClientSet.EFLO().DeleteElasticNetworkInterface(req)
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

	resp, err := a.ClientSet.EFLO().AssignLeniPrivateIpAddress(req)
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

	resp, err := a.ClientSet.EFLO().UnassignLeniPrivateIpAddress(req)
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

	resp, err := a.ClientSet.EFLO().GetElasticNetworkInterface(req)
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

	resp, err := a.ClientSet.EFLO().ListLeniPrivateIpAddresses(req)
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

	resp, err := a.ClientSet.EFLO().ListElasticNetworkInterfaces(req)
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

	resp, err := a.ClientSet.EFLO().GetNodeInfoForPod(req)
	if err != nil {
		return nil, err
	}
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("success")

	return &resp.Content, nil
}

func EfloGetLimit(client interface{}, instanceType string) (*Limits, error) {
	a, ok := client.(*OpenAPI)
	if !ok {
		return nil, fmt.Errorf("unsupported client")
	}

	resp, err := a.GetNodeInfoForPod(context.Background(), instanceType)
	if err != nil {
		return nil, err
	}

	return &Limits{
		Adapters:       resp.LeniQuota,
		TotalAdapters:  resp.LeniQuota,
		IPv4PerAdapter: resp.LniSipQuota,
	}, nil
}
