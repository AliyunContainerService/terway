package client

import (
	"fmt"
	"time"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"k8s.io/client-go/util/retry"
)

// AllocateEIPAddress create EIP
func (a *OpenAPI) AllocateEIPAddress(bandwidth, chargeType, isp string) (*vpc.AllocateEipAddressResponse, error) {
	req := vpc.CreateAllocateEipAddressRequest()
	req.Bandwidth = bandwidth
	req.InternetChargeType = chargeType
	req.ISP = isp

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
	req.InstanceType = EIPInstanceTypeNetworkInterface

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "AssociateEipAddress",
		LogFieldEIPID: eipID,
		LogFieldENIID: eniID,
	})
	start := time.Now()

	return retry.OnError(backoff.Backoff(backoff.DefaultKey), func(err error) bool {
		return apiErr.ErrAssert(apiErr.ErrTaskConflict, err)
	}, func() error {
		resp, err := a.ClientSet.VPC().AssociateEipAddress(req)
		metric.OpenAPILatency.WithLabelValues("AssociateEipAddress", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
		if err != nil {
			l.WithFields(map[string]interface{}{
				LogFieldRequestID: apiErr.ErrRequestID(err)}).Warnf("associate EIP to %s failed, %s", privateIP, err.Error())

			return err
		}
		l.WithFields(map[string]interface{}{
			LogFieldRequestID: resp.RequestId}).Infof("associate EIP to %s", privateIP)
		return nil
	})
}

// UnAssociateEIPAddress un-bind eip
func (a *OpenAPI) UnAssociateEIPAddress(eipID, eniID, eniIP string) error {
	req := vpc.CreateUnassociateEipAddressRequest()
	req.AllocationId = eipID
	req.InstanceId = eniID
	req.PrivateIpAddress = eniIP
	req.InstanceType = EIPInstanceTypeNetworkInterface

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "UnassociateEipAddress",
		LogFieldEIPID: eipID,
	})

	return retry.OnError(backoff.Backoff(backoff.DefaultKey), func(err error) bool {
		return apiErr.ErrAssert(apiErr.ErrTaskConflict, err)
	}, func() error {
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
	})
}

// ReleaseEIPAddress delete EIP
func (a *OpenAPI) ReleaseEIPAddress(eipID string) error {
	req := vpc.CreateReleaseEipAddressRequest()
	req.AllocationId = eipID

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "ReleaseEipAddress",
		LogFieldEIPID: eipID,
	})

	return retry.OnError(backoff.Backoff(backoff.DefaultKey), func(err error) bool {
		return apiErr.ErrAssert(apiErr.ErrTaskConflict, err)
	}, func() error {
		start := time.Now()
		resp, err := a.ClientSet.VPC().ReleaseEipAddress(req)
		metric.OpenAPILatency.WithLabelValues("ReleaseEipAddress", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
		if err != nil {
			l.WithFields(map[string]interface{}{
				LogFieldRequestID: apiErr.ErrRequestID(err)}).Warnf("release EIP failed, %s", err.Error())
			return err
		}
		l.WithFields(map[string]interface{}{
			LogFieldRequestID: resp.RequestId}).Info("release EIP")
		return nil
	})
}

// AddCommonBandwidthPackageIP add EIP to bandwidth package
func (a *OpenAPI) AddCommonBandwidthPackageIP(eipID, packageID string) error {
	req := vpc.CreateAddCommonBandwidthPackageIpRequest()
	req.BandwidthPackageId = packageID
	req.IpInstanceId = eipID

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "AddCommonBandwidthPackageIp",
		LogFieldEIPID: eipID,
	})
	return retry.OnError(backoff.Backoff(backoff.DefaultKey), func(err error) bool {
		return apiErr.ErrAssert(apiErr.ErrTaskConflict, err)
	}, func() error {
		start := time.Now()
		resp, err := a.ClientSet.VPC().AddCommonBandwidthPackageIp(req)
		metric.OpenAPILatency.WithLabelValues("AddCommonBandwidthPackageIp", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
		if err != nil {
			l.WithFields(map[string]interface{}{
				LogFieldRequestID: apiErr.ErrRequestID(err)}).Warnf("add eip failed, %s", err.Error())
			return err
		}
		l.WithFields(map[string]interface{}{
			LogFieldRequestID: resp.RequestId}).Info("add eip success")
		return nil
	})
}

// RemoveCommonBandwidthPackageIP remove EIP from bandwidth package
func (a *OpenAPI) RemoveCommonBandwidthPackageIP(eipID, packageID string) error {
	req := vpc.CreateRemoveCommonBandwidthPackageIpRequest()
	req.BandwidthPackageId = packageID
	req.IpInstanceId = eipID

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "RemoveCommonBandwidthPackageIp",
		LogFieldEIPID: eipID,
	})

	return retry.OnError(backoff.Backoff(backoff.DefaultKey), func(err error) bool {
		return apiErr.ErrAssert(apiErr.ErrTaskConflict, err)
	}, func() error {
		start := time.Now()
		resp, err := a.ClientSet.VPC().RemoveCommonBandwidthPackageIp(req)
		metric.OpenAPILatency.WithLabelValues("RemoveCommonBandwidthPackageIp", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
		if err != nil {
			l.WithFields(map[string]interface{}{
				LogFieldRequestID: apiErr.ErrRequestID(err)}).Warnf("remove eip failed, %s", err.Error())
			return err
		}
		l.WithFields(map[string]interface{}{
			LogFieldRequestID: resp.RequestId}).Info("remove eip success")
		return nil
	})
}
