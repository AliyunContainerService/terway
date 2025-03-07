package client

import (
	"context"
	"fmt"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/metric"
)

const (
	APICreateElasticNetworkInterface = "CreateElasticNetworkInterface"
	APIAssignLeniPrivateIPAddress    = "AssignLeniPrivateIpAddress"
	APIDeleteElasticNetworkInterface = "DeleteElasticNetworkInterface"
	APIUnassignLeniPrivateIPAddress  = "UnassignLeniPrivateIpAddress"
	APIListLeniPrivateIPAddresses    = "ListLeniPrivateIpAddresses"
	APIListElasticNetworkInterfaces  = "ListElasticNetworkInterfaces"
	APIGetNodeInfoForPod             = "GetNodeInfoForPod"
)

func (a *OpenAPI) CreateElasticNetworkInterfaceV2(ctx context.Context, opts ...CreateNetworkInterfaceOption) (*NetworkInterface, error) {
	options := &CreateNetworkInterfaceOptions{}
	for _, opt := range opts {
		opt.ApplyCreateNetworkInterface(options)
	}

	req, rollBackFunc, err := options.EFLO(a.IdempotentKeyGen)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			rollBackFunc()
		}
	}()

	err = a.RateLimiter.Wait(ctx, APICreateElasticNetworkInterface)
	if err != nil {
		return nil, err
	}

	l := LogFields(logr.FromContextOrDiscard(ctx), req)

	start := time.Now()
	resp, err := a.ClientSet.EFLO().CreateElasticNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues("CreateElasticNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
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

	l.WithValues(LogFieldRequestID, resp.RequestId).Info("leni created", "leni", resp.Content.ElasticNetworkInterfaceId)

	return &NetworkInterface{
		NetworkInterfaceID:          resp.Content.ElasticNetworkInterfaceId,
		Type:                        ENITypeSecondary,
		NetworkInterfaceTrafficMode: ENITrafficModeStandard,
	}, err
}

func (a *OpenAPI) DescribeLeniNetworkInterface(ctx context.Context, opts ...DescribeNetworkInterfaceOption) ([]*NetworkInterface, error) {
	options := &DescribeNetworkInterfaceOptions{}
	for _, opt := range opts {
		opt.ApplyTo(options)
	}
	req := options.EFLO()

	l := LogFields(logr.FromContextOrDiscard(ctx), req)

	resp, err := a.ClientSet.EFLO().ListElasticNetworkInterfaces(req)
	if err != nil {
		l.Error(err, "failed to list leni")
		return nil, err
	}
	if resp.Code != 0 {
		err = &apiErr.EFLOCode{
			Code:      resp.Code,
			Message:   resp.Message,
			RequestID: resp.RequestId,
			Content:   resp.Content,
		}
		l.Error(err, "failed to list leni")
		return nil, err
	}
	enis := make([]*NetworkInterface, 0)
	for _, data := range resp.Content.Data {

		l.Info("ListElasticNetworkInterfaces", "data", data)

		if data.Type != "CUSTOM" { // CUSTOM for our own card
			continue
		}

		var privateIPs []IPSet
		eni := &NetworkInterface{
			Status:                      data.Status,
			MacAddress:                  data.Mac,
			NetworkInterfaceID:          data.ElasticNetworkInterfaceId,
			VSwitchID:                   data.VSwitchId,
			VPCID:                       data.VpcId,
			PrivateIPAddress:            data.Ip,
			ZoneID:                      data.ZoneId,
			SecurityGroupIDs:            []string{data.SecurityGroupId},
			ResourceGroupID:             data.ResourceGroupId,
			IPv6Set:                     nil,
			Tags:                        nil,
			Type:                        ENITypeSecondary,
			InstanceID:                  data.NodeId,
			TrunkNetworkInterfaceID:     "",
			NetworkInterfaceTrafficMode: ENITrafficModeStandard,
			DeviceIndex:                 0,
		}

		// For now use ecs status
		switch eni.Status {
		case LENIStatusUnattached:
			eni.Status = ENIStatusAvailable
		case LENIStatusAvailable:
			eni.Status = ENIStatusInUse

		case LENIStatusCreateFailed, LENIStatusDeleting, LENIStatusDeleteFailed:
			eni.Status = ENIStatusDeleting
		}

		privateIPs = append(privateIPs, IPSet{
			IPAddress: data.Ip,
			Primary:   true, // primary will not hav ipname
		})

		ipsData, err := a.ListLeniPrivateIPAddresses(ctx, eni.NetworkInterfaceID, "", "")
		if err != nil {
			return nil, err
		}

		for _, ips := range ipsData.Data {
			privateIPs = append(privateIPs, IPSet{
				IPAddress: ips.PrivateIpAddress,
				Primary:   false,
				IPName:    ips.IpName,
				IPStatus:  ips.Status,
			})
		}

		eni.PrivateIPSets = privateIPs
		enis = append(enis, eni)
	}
	return enis, nil
}

func (a *OpenAPI) AssignLeniPrivateIPAddress2(ctx context.Context, opts ...AssignPrivateIPAddressOption) ([]IPSet, error) {
	ctx, span := a.Tracer.Start(ctx, APIAssignLeniPrivateIPAddress)
	defer span.End()

	option := &AssignPrivateIPAddressOptions{}
	for _, opt := range opts {
		opt.ApplyAssignPrivateIPAddress(option)
	}

	req, rollBackFunc, err := option.EFLO(a.IdempotentKeyGen)
	if err != nil {
		return nil, err
	}
	l := LogFields(logr.FromContextOrDiscard(ctx), req)

	var (
		resp     *eflo.AssignLeniPrivateIpAddressResponse
		innerErr error
	)

	err = wait.ExponentialBackoffWithContext(ctx, *option.Backoff, func(ctx context.Context) (bool, error) {
		innerErr = a.RateLimiter.Wait(ctx, APIAssignLeniPrivateIPAddress)
		if innerErr != nil {
			return true, innerErr
		}
		start := time.Now()
		resp, innerErr = a.ClientSet.EFLO().AssignLeniPrivateIpAddress(req)
		metric.OpenAPILatency.WithLabelValues(APIAssignLeniPrivateIPAddress, fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))

		if innerErr != nil {
			innerErr = apiErr.WarpError(innerErr)
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(innerErr)).Error(innerErr, "failed")
			if apiErr.ErrorIs(innerErr, apiErr.IsURLError, apiErr.WarpFn(apiErr.ErrThrottling, apiErr.ErrInternalError, apiErr.ErrOperationConflict)) {
				return false, nil
			}
			return true, innerErr
		}

		if resp.Code != 0 {
			err = &apiErr.EFLOCode{
				Code:      resp.Code,
				Message:   resp.Message,
				RequestID: resp.RequestId,
				Content:   resp.Content,
			}
			l.Error(err, "failed")
			return true, err
		}
		return true, nil
	})
	if err != nil {
		rollBackFunc()
		return nil, err
	}

	ipName := resp.Content.IpName
	eniID := resp.Content.ElasticNetworkInterfaceId

	re := make([]IPSet, 0)
	err = retry.OnError(wait.Backoff{
		Duration: 2 * time.Second,
		Factor:   1,
		Jitter:   0,
		Steps:    3,
	}, func(err error) bool {
		return true
	}, func() error {
		content, err := a.ListLeniPrivateIPAddresses(ctx, eniID, ipName, "")
		if err != nil {
			return err
		}
		for _, data := range content.Data {
			if data.IpName != ipName {
				continue
			}
			if data.Status != LENIIPStatusAvailable {
				return fmt.Errorf("ip %s status %s", data.PrivateIpAddress, data.Status)
			}

			re = append(re, IPSet{
				Primary:   false,
				IPAddress: data.PrivateIpAddress,
				IPName:    data.IpName,
				IPStatus:  data.Status,
			})
		}
		return nil
	})
	if err != nil {
		re = append(re, IPSet{
			Primary:  false,
			IPName:   ipName,
			IPStatus: "",
		})
	}

	l.Info("assign leni private ip address", "re", re)

	return re, err
}

func (a *OpenAPI) UnAssignLeniPrivateIPAddresses2(ctx context.Context, eniID string, ips []IPSet) error {
	for _, ip := range ips {
		if ip.IPName == "" {
			continue
		}
		err := a.UnassignLeniPrivateIPAddress(ctx, eniID, ip.IPName)
		if err != nil {
			return err
		}
	}
	return nil
}

// WaitForLeniNetworkInterface wait status of eni
func (a *OpenAPI) WaitForLeniNetworkInterface(ctx context.Context, eniID string, status string, backoff wait.Backoff, ignoreNotExist bool) (*NetworkInterface, error) {
	ctx, span := a.Tracer.Start(ctx, "WaitForNetworkInterface")
	defer span.End()

	var eniInfo *NetworkInterface
	if eniID == "" {
		return nil, fmt.Errorf("eniID not set")
	}
	err := wait.ExponentialBackoff(backoff,
		func() (done bool, err error) {
			eni, err := a.DescribeLeniNetworkInterface(ctx, &DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{eniID},
				Status:              &status,
			})
			if err != nil {
				return false, nil
			}
			if len(eni) == 0 && ignoreNotExist {
				return true, apiErr.ErrNotFound
			}
			if len(eni) == 1 {
				if string(status) != "" && status != eni[0].Status {
					return false, nil
				}

				eniInfo = eni[0]
				return true, nil
			}
			return false, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error wait for eni %v to status %s, %w", eniID, status, err)
	}
	return eniInfo, nil
}
