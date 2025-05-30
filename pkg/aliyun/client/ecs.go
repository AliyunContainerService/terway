package client

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"k8s.io/apimachinery/pkg/util/wait"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/metric"
)

const (
	APICreateNetworkInterface     = "CreateNetworkInterface"
	APIDescribeNetworkInterfaces  = "DescribeNetworkInterfaces"
	APIAttachNetworkInterface     = "AttachNetworkInterface"
	APIDetachNetworkInterface     = "DetachNetworkInterface"
	APIDeleteNetworkInterface     = "DeleteNetworkInterface"
	APIAssignPrivateIPAddress     = "AssignPrivateIpAddresses"
	APIUnAssignPrivateIPAddresses = "UnAssignPrivateIpAddresses"
	APIAssignIPv6Addresses        = "AssignIpv6Addresses"
	APIUnAssignIpv6Addresses      = "UnAssignIpv6Addresses"
	APIDescribeInstanceTypes      = "DescribeInstanceTypes"
)

func (a *ECSService) CreateNetworkInterface(ctx context.Context, opts ...CreateNetworkInterfaceOption) (*NetworkInterface, error) {
	ctx, span := a.Tracer.Start(ctx, APICreateNetworkInterface)
	defer span.End()

	option := &CreateNetworkInterfaceOptions{}
	for _, opt := range opts {
		opt.ApplyCreateNetworkInterface(option)
	}

	req, rollBackFunc, err := option.Finish(a.IdempotentKeyGen)
	if err != nil {
		return nil, err
	}
	l := LogFields(logf.FromContext(ctx), req)

	var (
		resp     *ecs.CreateNetworkInterfaceResponse
		innerErr error
	)

	err = wait.ExponentialBackoffWithContext(ctx, *option.Backoff, func(ctx context.Context) (bool, error) {
		err = a.RateLimiter.Wait(ctx, APICreateNetworkInterface)
		if err != nil {
			return false, err
		}
		start := time.Now()
		resp, innerErr = a.ClientSet.ECS().CreateNetworkInterface(req)
		metric.OpenAPILatency.WithLabelValues(APICreateNetworkInterface, fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))
		if innerErr != nil {
			innerErr = apiErr.WarpError(innerErr)
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(innerErr)).Error(innerErr, "failed")

			if apiErr.ErrorIs(innerErr, apiErr.IsURLError, apiErr.WarpFn(apiErr.ErrThrottling, apiErr.ErrInternalError)) {
				return false, nil
			}

			return true, innerErr
		}
		l.WithValues(LogFieldRequestID, resp.RequestId, LogFieldENIID, resp.NetworkInterfaceId).Info("eni created")
		return true, nil
	})

	if err != nil {
		rollBackFunc()
		return nil, err
	}

	return FromCreateResp(resp), err
}

// DescribeNetworkInterface list eni
func (a *ECSService) DescribeNetworkInterface(ctx context.Context, vpcID string, eniID []string, instanceID string, instanceType string, status string, tags map[string]string) ([]*NetworkInterface, error) {
	ctx, span := a.Tracer.Start(ctx, APIDescribeNetworkInterfaces)
	defer span.End()

	var result []*NetworkInterface
	nextToken := ""

	var ecsTags []ecs.DescribeNetworkInterfacesTag
	for k, v := range tags {
		ecsTags = append(ecsTags, ecs.DescribeNetworkInterfacesTag{
			Key:   k,
			Value: v,
		})
	}

	for {
		err := a.RateLimiter.Wait(ctx, APIDescribeNetworkInterfaces)
		if err != nil {
			return nil, err
		}

		req := ecs.CreateDescribeNetworkInterfacesRequest()
		req.NextToken = nextToken
		req.VpcId = vpcID
		if len(ecsTags) > 0 {
			req.Tag = &ecsTags
		}
		req.NetworkInterfaceId = &eniID
		req.InstanceId = instanceID
		req.Type = instanceType
		req.Status = status

		req.MaxResults = requests.NewInteger(maxSinglePageSize)

		l := LogFields(logf.FromContext(ctx), req)

		start := time.Now()
		resp, err := a.ClientSet.ECS().DescribeNetworkInterfaces(req)
		metric.OpenAPILatency.WithLabelValues(APIDescribeNetworkInterfaces, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
		if err != nil {
			err = apiErr.WarpError(err)
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "error describe eni")
			return nil, err
		}
		for _, r := range resp.NetworkInterfaceSets.NetworkInterfaceSet {
			result = append(result, FromDescribeResp(&r))
		}

		l.WithValues(LogFieldRequestID, resp.RequestId).Info("describe enis")

		if len(resp.NetworkInterfaceSets.NetworkInterfaceSet) < maxSinglePageSize {
			break
		}
		if resp.NextToken == "" {
			break
		}
		nextToken = resp.NextToken
	}
	return result, nil
}

// AttachNetworkInterface attach eni
func (a *ECSService) AttachNetworkInterface(ctx context.Context, opts ...AttachNetworkInterfaceOption) error {
	ctx, span := a.Tracer.Start(ctx, APIAttachNetworkInterface)
	defer span.End()

	err := a.RateLimiter.Wait(ctx, APIAttachNetworkInterface)
	if err != nil {
		return err
	}

	option := &AttachNetworkInterfaceOptions{}
	for _, opt := range opts {
		opt.ApplyTo(option)
	}

	req, err := option.ECS()
	if err != nil {
		return err
	}
	l := LogFields(logf.FromContext(ctx), req)

	start := time.Now()
	resp, err := a.ClientSet.ECS().AttachNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues(APIAttachNetworkInterface, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "attach eni failed")
		return err
	}
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("attach eni")
	return nil
}

// DetachNetworkInterface detach eni
func (a *ECSService) DetachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	ctx, span := a.Tracer.Start(ctx, APIDetachNetworkInterface)
	defer span.End()

	req := ecs.CreateDetachNetworkInterfaceRequest()
	req.NetworkInterfaceId = eniID
	req.InstanceId = instanceID
	req.TrunkNetworkInstanceId = trunkENIID

	l := LogFields(logf.FromContext(ctx), req)

	err := a.RateLimiter.Wait(ctx, APIDetachNetworkInterface)
	if err != nil {
		return err
	}
	start := time.Now()
	resp, err := a.ClientSet.ECS().DetachNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues(APIDetachNetworkInterface, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		if apiErr.ErrorCodeIs(err, apiErr.ErrInvalidENINotFound, apiErr.ErrInvalidEcsIDNotFound) {
			return nil
		}
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "detach eni failed")
		return err
	}
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("detach eni")
	return nil
}

// DeleteNetworkInterface del eni by id
func (a *ECSService) DeleteNetworkInterface(ctx context.Context, eniID string) error {
	ctx, span := a.Tracer.Start(ctx, APIDeleteNetworkInterface)
	defer span.End()

	req := ecs.CreateDeleteNetworkInterfaceRequest()
	req.NetworkInterfaceId = eniID

	l := LogFields(logf.FromContext(ctx), req)

	err := a.RateLimiter.Wait(ctx, APIDeleteNetworkInterface)
	if err != nil {
		return err
	}
	start := time.Now()
	resp, err := a.ClientSet.ECS().DeleteNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues(APIDeleteNetworkInterface, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "delete eni failed")
		return err
	}
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("delete eni")
	return nil
}

// WaitForNetworkInterface wait status of eni
func (a *ECSService) WaitForNetworkInterface(ctx context.Context, eniID string, status string, backoff wait.Backoff, ignoreNotExist bool) (*NetworkInterface, error) {
	ctx, span := a.Tracer.Start(ctx, "WaitForNetworkInterface")
	defer span.End()

	var eniInfo *NetworkInterface
	if eniID == "" {
		return nil, fmt.Errorf("eniID not set")
	}
	start := time.Now()
	err := wait.ExponentialBackoff(backoff,
		func() (done bool, err error) {
			eni, err := a.DescribeNetworkInterface(ctx, "", []string{eniID}, "", "", "", nil)
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
		return nil, fmt.Errorf("error wait for eni %v to status %s,took %s, %w", eniID, status, time.Since(start).String(), err)
	}
	return eniInfo, nil
}

func (a *ECSService) AssignPrivateIPAddress(ctx context.Context, opts ...AssignPrivateIPAddressOption) ([]netip.Addr, error) {
	ctx, span := a.Tracer.Start(ctx, APIAssignPrivateIPAddress)
	defer span.End()

	option := &AssignPrivateIPAddressOptions{}
	for _, opt := range opts {
		opt.ApplyAssignPrivateIPAddress(option)
	}

	req, rollBackFunc, err := option.Finish(a.IdempotentKeyGen)
	if err != nil {
		return nil, err
	}
	l := LogFields(logf.FromContext(ctx), req)

	var (
		resp     *ecs.AssignPrivateIpAddressesResponse
		innerErr error
	)

	err = wait.ExponentialBackoffWithContext(ctx, *option.Backoff, func(ctx context.Context) (bool, error) {
		innerErr = a.RateLimiter.Wait(ctx, APIAssignPrivateIPAddress)
		if innerErr != nil {
			return true, innerErr
		}
		start := time.Now()
		resp, innerErr = a.ClientSet.ECS().AssignPrivateIpAddresses(req)
		metric.OpenAPILatency.WithLabelValues(APIAssignPrivateIPAddress, fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))
		if innerErr != nil {
			innerErr = apiErr.WarpError(innerErr)
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(innerErr)).Error(innerErr, "failed")

			if apiErr.ErrorIs(innerErr, apiErr.IsURLError, apiErr.WarpFn(apiErr.ErrThrottling, apiErr.ErrInternalError, apiErr.ErrOperationConflict)) {
				return false, nil
			}

			return true, innerErr
		}

		return true, nil
	})
	if err != nil {
		rollBackFunc()
		return nil, err
	}

	ips, err := ip.ToIPAddrs(resp.AssignedPrivateIpAddressesSet.PrivateIpSet.PrivateIpAddress)
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("assign private ip", "ips", ips)

	return ips, err
}

// UnAssignPrivateIPAddresses remove ip from eni
// return ok if 1. eni is released 2. ip is already released 3. release success
// for primaryIP err is InvalidIp.IpUnassigned
func (a *ECSService) UnAssignPrivateIPAddresses(ctx context.Context, eniID string, ips []netip.Addr) error {
	if len(ips) == 0 {
		return nil
	}

	ctx, span := a.Tracer.Start(ctx, APIUnAssignPrivateIPAddresses)
	defer span.End()

	err := a.RateLimiter.Wait(ctx, APIUnAssignPrivateIPAddresses)
	if err != nil {
		return err
	}

	req := ecs.CreateUnassignPrivateIpAddressesRequest()
	req.NetworkInterfaceId = eniID
	str := ip.IPAddrs2str(ips)
	req.PrivateIpAddress = &str

	l := LogFields(logf.FromContext(ctx), req)

	start := time.Now()
	resp, err := a.ClientSet.ECS().UnassignPrivateIpAddresses(req)
	metric.OpenAPILatency.WithLabelValues(APIUnAssignPrivateIPAddresses, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

	if err != nil {
		err = apiErr.WarpError(err)
		if apiErr.ErrorCodeIs(err, apiErr.ErrInvalidIPIPUnassigned, apiErr.ErrInvalidENINotFound) {
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Info("success")
			return nil
		}

		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "unassign private ip failed")
		return err
	}
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("success")
	return nil
}

// AssignIpv6Addresses assign ipv6 address
func (a *ECSService) AssignIpv6Addresses(ctx context.Context, opts ...AssignIPv6AddressesOption) ([]netip.Addr, error) {
	ctx, span := a.Tracer.Start(ctx, APIAssignIPv6Addresses)
	defer span.End()

	option := &AssignIPv6AddressesOptions{}
	for _, opt := range opts {
		opt.ApplyAssignIPv6Addresses(option)
	}

	req, rollBackFunc, err := option.Finish(a.IdempotentKeyGen)
	if err != nil {
		return nil, err
	}
	l := LogFields(logf.FromContext(ctx), req)

	var (
		resp     *ecs.AssignIpv6AddressesResponse
		innerErr error
	)

	err = wait.ExponentialBackoffWithContext(ctx, *option.Backoff, func(ctx context.Context) (bool, error) {
		innerErr = a.RateLimiter.Wait(ctx, APIAssignIPv6Addresses)
		if err != nil {
			return true, innerErr
		}

		start := time.Now()
		resp, innerErr = a.ClientSet.ECS().AssignIpv6Addresses(req)
		metric.OpenAPILatency.WithLabelValues(APIAssignIPv6Addresses, fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))
		if innerErr != nil {
			innerErr = apiErr.WarpError(innerErr)
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(innerErr)).Error(innerErr, "failed")

			if apiErr.ErrorIs(innerErr, apiErr.IsURLError, apiErr.WarpFn(apiErr.ErrThrottling, apiErr.ErrInternalError, apiErr.ErrOperationConflict)) {
				return false, nil
			}

			return true, innerErr
		}

		return true, nil
	})
	if err != nil {
		rollBackFunc()
		return nil, err
	}

	ips, err := ip.ToIPAddrs(resp.Ipv6Sets.Ipv6Address)
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("assign ipv6", "ips", ips)

	return ips, err
}

// UnAssignIpv6Addresses remove ip from eni
// return ok if 1. eni is released 2. ip is already released 3. release success
func (a *ECSService) UnAssignIpv6Addresses(ctx context.Context, eniID string, ips []netip.Addr) error {
	ctx, span := a.Tracer.Start(ctx, APIUnAssignIpv6Addresses)
	defer span.End()

	if len(ips) == 0 {
		return nil
	}

	err := a.RateLimiter.Wait(ctx, APIUnAssignIpv6Addresses)
	if err != nil {
		return err
	}

	req := ecs.CreateUnassignIpv6AddressesRequest()
	req.NetworkInterfaceId = eniID
	str := ip.IPAddrs2str(ips)
	req.Ipv6Address = &str

	l := LogFields(logf.FromContext(ctx), req)

	start := time.Now()
	resp, err := a.ClientSet.ECS().UnassignIpv6Addresses(req)
	metric.OpenAPILatency.WithLabelValues(APIUnAssignIpv6Addresses, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

	if err != nil {
		err = apiErr.WarpError(err)
		if apiErr.ErrorCodeIs(err, apiErr.ErrInvalidIPIPUnassigned, apiErr.ErrInvalidENINotFound) {
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Info("success")
			return nil
		}

		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "unassign ipv6 ip failed")
		return err
	}
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("success")
	return nil
}

func (a *ECSService) DescribeInstanceTypes(ctx context.Context, types []string) ([]ecs.InstanceType, error) {
	ctx, span := a.Tracer.Start(ctx, APIDescribeInstanceTypes)
	defer span.End()

	var result []ecs.InstanceType

	nextToken := ""
	for {
		err := a.RateLimiter.Wait(ctx, APIDescribeInstanceTypes)
		if err != nil {
			return nil, err
		}

		req := ecs.CreateDescribeInstanceTypesRequest()
		req.NextToken = nextToken
		// nb(l1b0k): see https://help.aliyun.com/practice_detail/461278.
		req.MaxResults = requests.NewInteger(100)
		if types != nil {
			req.InstanceTypes = &types
		}
		start := time.Now()
		resp, err := a.ClientSet.ECS().DescribeInstanceTypes(req)
		metric.OpenAPILatency.WithLabelValues(APIDescribeInstanceTypes, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

		l := LogFields(logf.FromContext(ctx), req)

		if err != nil {
			err = apiErr.WarpError(err)
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "describe instance types failed")
			return nil, err
		}
		l.WithValues(LogFieldRequestID, resp.RequestId).Info("success")

		result = append(result, resp.InstanceTypes.InstanceType...)

		if resp.NextToken == "" {
			break
		}
		nextToken = resp.NextToken
	}

	return result, nil
}
