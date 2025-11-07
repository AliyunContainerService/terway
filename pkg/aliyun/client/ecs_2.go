package client

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/util/wait"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/metric"
)

// DescribeNetworkInterface2 list eni
func (a *ECSService) DescribeNetworkInterface2(ctx context.Context, opts ...DescribeNetworkInterfaceOption) ([]*NetworkInterface, error) {
	ctx, span := a.Tracer.Start(ctx, APIDescribeNetworkInterfaces)
	defer span.End()

	var result []*NetworkInterface
	nextToken := ""

	for {
		err := a.RateLimiter.Wait(ctx, APIDescribeNetworkInterfaces)
		if err != nil {
			return nil, err
		}

		options := &DescribeNetworkInterfaceOptions{}
		for _, opt := range opts {
			opt.ApplyTo(options)
		}
		req := options.ECS()
		req.NextToken = nextToken
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

		l.WithValues(LogFieldRequestID, resp.RequestId, "count", len(result)).Info("describe enis")

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

func (a *ECSService) AssignPrivateIPAddress2(ctx context.Context, opts ...AssignPrivateIPAddressOption) ([]IPSet, error) {
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
		if !apiErr.ErrorCodeIs(err, apiErr.ErrIdempotentFailed) {
			rollBackFunc()
		}
		return nil, err
	}

	ips, err := ip.ToIPAddrs(resp.AssignedPrivateIpAddressesSet.PrivateIpSet.PrivateIpAddress)
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("assign private ip", "ips", ips)

	return lo.Map(ips, func(item netip.Addr, _ int) IPSet {
		return IPSet{
			IPAddress: item.String(),
			IPStatus:  "",
			Primary:   false,
		}
	}), err
}

func (a *ECSService) UnAssignPrivateIPAddresses2(ctx context.Context, eniID string, ips []IPSet) error {
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

	str := lo.Map(ips, func(item IPSet, _ int) string {
		return item.IPAddress
	})
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

// AssignIpv6Addresses2 assign ipv6 address
func (a *ECSService) AssignIpv6Addresses2(ctx context.Context, opts ...AssignIPv6AddressesOption) ([]IPSet, error) {
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
		if !apiErr.ErrorCodeIs(err, apiErr.ErrIdempotentFailed) {
			rollBackFunc()
		}
		return nil, err
	}

	ips := lo.Map(resp.Ipv6Sets.Ipv6Address, func(item string, _ int) IPSet {
		return IPSet{
			IPAddress: item,
			IPStatus:  "",
			Primary:   false,
		}
	})
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("assign ipv6", "ips", ips)

	return ips, nil
}

// UnAssignIpv6Addresses2 remove ip from eni
// return ok if 1. eni is released 2. ip is already released 3. release success
func (a *ECSService) UnAssignIpv6Addresses2(ctx context.Context, eniID string, ips []IPSet) error {
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
	str := lo.Map(ips, func(item IPSet, _ int) string {
		return item.IPAddress
	})
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

// DetachNetworkInterface2 detaches an ENI using the new option-based interface.
func (a *ECSService) DetachNetworkInterface2(ctx context.Context, opts ...DetachNetworkInterfaceOption) error {
	ctx, span := a.Tracer.Start(ctx, APIDetachNetworkInterface)
	defer span.End()

	option := &DetachNetworkInterfaceOptions{}
	for _, opt := range opts {
		opt.ApplyTo(option)
	}

	req, err := option.ECS()
	if err != nil {
		return err
	}
	l := LogFields(logf.FromContext(ctx), req)

	err = a.RateLimiter.Wait(ctx, APIDetachNetworkInterface)
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
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("detach eni success")
	return nil
}
