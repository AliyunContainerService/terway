package client

import (
	"context"
	"fmt"
	"time"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/types"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
)

// DescribeVSwitchByID get vsw by id
func (a *OpenAPI) DescribeVSwitchByID(ctx context.Context, vSwitchID string) (*vpc.VSwitch, error) {
	req := vpc.CreateDescribeVSwitchesRequest()
	req.VSwitchId = vSwitchID

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:       "DescribeVSwitches",
		LogFieldVSwitchID: vSwitchID,
	})

	start := time.Now()
	resp, err := a.ClientSet.VPC().DescribeVSwitches(req)
	metric.OpenAPILatency.WithLabelValues("DescribeVSwitches", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err)
		return nil, err
	}
	l.WithField(LogFieldRequestID, resp.RequestId).Debugf("DescribeVSwitches: vsw slice = %+v, err = %v", resp.VSwitches.VSwitch, err)
	if len(resp.VSwitches.VSwitch) == 0 {
		return nil, apiErr.ErrNotFound
	}
	if len(resp.VSwitches.VSwitch) > 0 {
		return &resp.VSwitches.VSwitch[0], nil
	}
	return nil, err
}

// AllocateEIPAddress create EIP
func (a *OpenAPI) AllocateEIPAddress(bandwidth, chargeType string) (*vpc.AllocateEipAddressResponse, error) {
	req := vpc.CreateAllocateEipAddressRequest()
	req.Bandwidth = bandwidth
	req.InternetChargeType = chargeType

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI: "AllocateEipAddress",
	})
	start := time.Now()
	resp, err := a.ClientSet.VPC().AllocateEipAddress(req)
	metric.OpenAPILatency.WithLabelValues("AllocateEipAddress", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithFields(map[string]interface{}{
			LogFieldRequestID: apiErr.ErrRequestID(err)}).Errorf("alloc EIP faild, %s", err.Error())
		return nil, fmt.Errorf("error create EIP, bandwidth %s, %w", bandwidth, err)
	}
	l.WithFields(map[string]interface{}{
		LogFieldEIPID:     resp.AllocationId,
		LogFieldRequestID: resp.RequestId}).Infof("alloc EIP %s", resp.EipAddress)
	return resp, nil
}

// AssociateEIPAddress bind eip to ip
func (a *OpenAPI) AssociateEIPAddress(eipID, eniID, privateIP string) error {
	req := vpc.CreateAssociateEipAddressRequest()
	req.AllocationId = eipID
	req.InstanceId = eniID
	req.PrivateIpAddress = privateIP
	req.InstanceType = string(types.EIPInstanceTypeNetworkInterface)

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "AssociateEipAddress",
		LogFieldEIPID: eipID,
		LogFieldENIID: eniID,
	})
	start := time.Now()
	resp, err := a.ClientSet.VPC().AssociateEipAddress(req)
	metric.OpenAPILatency.WithLabelValues("AssociateEipAddress", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithFields(map[string]interface{}{
			LogFieldRequestID: apiErr.ErrRequestID(err)}).Warnf("associate EIP to %s failed, %s", privateIP, err.Error())

		return fmt.Errorf("error associate EIP %s, %w", eipID, err)
	}
	l.WithFields(map[string]interface{}{
		LogFieldRequestID: resp.RequestId}).Infof("associate EIP to %s", privateIP)
	return nil
}

// UnAssociateEIPAddress un-bind eip
func (a *OpenAPI) UnAssociateEIPAddress(eipID, eniID, eniIP string) error {
	req := vpc.CreateUnassociateEipAddressRequest()
	req.AllocationId = eipID
	req.InstanceId = eniID
	req.PrivateIpAddress = eniIP
	req.InstanceType = string(types.EIPInstanceTypeNetworkInterface)

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "UnassociateEipAddress",
		LogFieldEIPID: eipID,
	})
	start := time.Now()
	resp, err := a.ClientSet.VPC().UnassociateEipAddress(req)
	metric.OpenAPILatency.WithLabelValues("UnassociateEipAddress", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithFields(map[string]interface{}{
			LogFieldRequestID: apiErr.ErrRequestID(err)}).Warnf("unassociate EIP failed, %s", err.Error())
		if apiErr.ErrAssert(apiErr.ErrInvalidAllocationIDNotFound, err) ||
			apiErr.ErrAssert(apiErr.ErrIncorrectEIPStatus, err) {
			return nil
		}
		return fmt.Errorf("error unassociate EIP %s, %w", eipID, err)
	}
	l.WithFields(map[string]interface{}{
		LogFieldRequestID: resp.RequestId}).Info("unassociate EIP")
	return nil
}

// ReleaseEIPAddress delete EIP
func (a *OpenAPI) ReleaseEIPAddress(eipID string) error {
	req := vpc.CreateReleaseEipAddressRequest()
	req.AllocationId = eipID

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "ReleaseEipAddress",
		LogFieldEIPID: eipID,
	})

	start := time.Now()
	resp, err := a.ClientSet.VPC().ReleaseEipAddress(req)
	metric.OpenAPILatency.WithLabelValues("ReleaseEipAddress", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithFields(map[string]interface{}{
			LogFieldRequestID: apiErr.ErrRequestID(err)}).Warnf("release EIP failed, %s", err.Error())
		return fmt.Errorf("error release EIP %s, %w", eipID, err)
	}
	l.WithFields(map[string]interface{}{
		LogFieldRequestID: resp.RequestId}).Info("release EIP")
	return nil
}
