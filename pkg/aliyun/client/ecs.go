package client

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/flowcontrol"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
	"github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/metric"
)

var _ VPC = &OpenAPI{}
var _ ECS = &OpenAPI{}

type OpenAPI struct {
	ClientSet        credential.Client
	IdempotentKeyGen IdempotentKeyGen

	ReadOnlyRateLimiter flowcontrol.RateLimiter
	MutatingRateLimiter flowcontrol.RateLimiter
}

func New(c credential.Client, readOnly, mutating flowcontrol.RateLimiter) (*OpenAPI, error) {
	return &OpenAPI{
		ClientSet:           c,
		IdempotentKeyGen:    NewIdempotentKeyGenerator(),
		ReadOnlyRateLimiter: readOnly,
		MutatingRateLimiter: mutating,
	}, nil
}

func (a *OpenAPI) CreateNetworkInterface(ctx context.Context, opts ...CreateNetworkInterfaceOption) (*NetworkInterface, error) {
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
		a.MutatingRateLimiter.Accept()
		start := time.Now()
		resp, innerErr = a.ClientSet.ECS().CreateNetworkInterface(req)
		metric.OpenAPILatency.WithLabelValues("CreateNetworkInterface", fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))
		if innerErr != nil {
			innerErr = apiErr.WarpError(innerErr)
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(innerErr)).Error(innerErr, "failed")

			if apiErr.ErrorIs(innerErr, apiErr.IsURLError, apiErr.WarpFn(apiErr.ErrThrottling, apiErr.ErrInternalError)) {
				return false, nil
			}

			return true, innerErr
		}

		return true, nil
	})

	if err != nil {
		rollBackFunc()
	}

	return FromCreateResp(resp), err
}

// DescribeNetworkInterface list eni
func (a *OpenAPI) DescribeNetworkInterface(ctx context.Context, vpcID string, eniID []string, instanceID string, instanceType string, status string, tags map[string]string) ([]*NetworkInterface, error) {
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

		l := logf.FromContext(ctx).WithValues(
			LogFieldAPI, "DescribeNetworkInterfaces",
			LogFieldENIID, eniID,
			LogFieldInstanceID, instanceID)

		a.ReadOnlyRateLimiter.Accept()
		start := time.Now()
		resp, err := a.ClientSet.ECS().DescribeNetworkInterfaces(req)
		metric.OpenAPILatency.WithLabelValues("DescribeNetworkInterfaces", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
		if err != nil {
			err = apiErr.WarpError(err)
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "error describe eni")
			return nil, err
		}
		for _, r := range resp.NetworkInterfaceSets.NetworkInterfaceSet {
			result = append(result, FromDescribeResp(&r))
		}

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
func (a *OpenAPI) AttachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	req := ecs.CreateAttachNetworkInterfaceRequest()
	req.NetworkInterfaceId = eniID
	req.InstanceId = instanceID
	req.TrunkNetworkInstanceId = trunkENIID

	l := logf.FromContext(ctx).WithValues(LogFieldAPI, "AttachNetworkInterface",
		LogFieldENIID, eniID,
		LogFieldInstanceID, instanceID)

	a.MutatingRateLimiter.Accept()
	start := time.Now()
	resp, err := a.ClientSet.ECS().AttachNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues("AttachNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "attach ENI failed")
		return err
	}
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("attach eni")
	return nil
}

// DetachNetworkInterface detach eni
func (a *OpenAPI) DetachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	req := ecs.CreateDetachNetworkInterfaceRequest()
	req.NetworkInterfaceId = eniID
	req.InstanceId = instanceID
	req.TrunkNetworkInstanceId = trunkENIID

	l := logf.FromContext(ctx).WithValues(
		LogFieldAPI, "DetachNetworkInterface",
		LogFieldENIID, eniID,
		LogFieldInstanceID, instanceID,
	)
	a.MutatingRateLimiter.Accept()
	start := time.Now()
	resp, err := a.ClientSet.ECS().DetachNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues("DetachNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		if apiErr.ErrorCodeIs(err, apiErr.ErrInvalidENINotFound) {
			return nil
		}
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "detach ENI failed")
		return err
	}
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("detach eni")
	return nil
}

// DeleteNetworkInterface del eni by id
func (a *OpenAPI) DeleteNetworkInterface(ctx context.Context, eniID string) error {
	req := ecs.CreateDeleteNetworkInterfaceRequest()
	req.NetworkInterfaceId = eniID

	l := logf.FromContext(ctx).WithValues(
		LogFieldAPI, "DeleteNetworkInterface",
		LogFieldENIID, eniID,
	)
	a.MutatingRateLimiter.Accept()
	start := time.Now()
	resp, err := a.ClientSet.ECS().DeleteNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues("DeleteNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "delete eni failed")
		return err
	}
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("delete eni")
	return nil
}

// WaitForNetworkInterface wait status of eni
func (a *OpenAPI) WaitForNetworkInterface(ctx context.Context, eniID string, status string, backoff wait.Backoff, ignoreNotExist bool) (*NetworkInterface, error) {
	var eniInfo *NetworkInterface
	if eniID == "" {
		return nil, fmt.Errorf("eniID not set")
	}
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
		return nil, fmt.Errorf("error wait for eni %v to status %s, %w", eniID, status, err)
	}
	return eniInfo, nil
}

func (a *OpenAPI) AssignPrivateIPAddress(ctx context.Context, opts ...AssignPrivateIPAddressOption) ([]netip.Addr, error) {
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
		a.MutatingRateLimiter.Accept()
		start := time.Now()
		resp, innerErr = a.ClientSet.ECS().AssignPrivateIpAddresses(req)
		metric.OpenAPILatency.WithLabelValues("AssignPrivateIpAddresses", fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))
		if innerErr != nil {
			innerErr = apiErr.WarpError(innerErr)
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(innerErr)).Error(innerErr, "failed")

			if apiErr.ErrorIs(innerErr, apiErr.IsURLError, apiErr.WarpFn(apiErr.ErrThrottling, apiErr.ErrInternalError)) {
				return false, nil
			}

			return true, innerErr
		}

		return true, nil
	})
	if err != nil {
		rollBackFunc()
	}

	ips, err := ip.ToIPAddrs(resp.AssignedPrivateIpAddressesSet.PrivateIpSet.PrivateIpAddress)
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("assign private ip", "ips", ips)

	return ips, err
}

// UnAssignPrivateIPAddresses remove ip from eni
// return ok if 1. eni is released 2. ip is already released 3. release success
// for primaryIP err is InvalidIp.IpUnassigned
func (a *OpenAPI) UnAssignPrivateIPAddresses(ctx context.Context, eniID string, ips []netip.Addr) error {
	if len(ips) == 0 {
		return nil
	}
	req := ecs.CreateUnassignPrivateIpAddressesRequest()
	req.NetworkInterfaceId = eniID
	str := ip.IPAddrs2str(ips)
	req.PrivateIpAddress = &str

	l := logf.FromContext(ctx).WithValues(
		LogFieldAPI, "UnassignPrivateIpAddresses",
		LogFieldENIID, eniID,
		LogFieldIPs, strings.Join(str, ","),
	)
	start := time.Now()
	resp, err := a.ClientSet.ECS().UnassignPrivateIpAddresses(req)
	metric.OpenAPILatency.WithLabelValues("UnassignPrivateIpAddresses", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

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
func (a *OpenAPI) AssignIpv6Addresses(ctx context.Context, opts ...AssignIPv6AddressesOption) ([]netip.Addr, error) {
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
		a.MutatingRateLimiter.Accept()
		start := time.Now()
		resp, innerErr = a.ClientSet.ECS().AssignIpv6Addresses(req)
		metric.OpenAPILatency.WithLabelValues("AssignIpv6Addresses", fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))
		if innerErr != nil {
			innerErr = apiErr.WarpError(innerErr)
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(innerErr)).Error(innerErr, "failed")

			if apiErr.ErrorIs(innerErr, apiErr.IsURLError, apiErr.WarpFn(apiErr.ErrThrottling, apiErr.ErrInternalError)) {
				return false, nil
			}

			return true, innerErr
		}

		return true, nil
	})
	if err != nil {
		rollBackFunc()
	}

	ips, err := ip.ToIPAddrs(resp.Ipv6Sets.Ipv6Address)
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("assign ipv6", "ips", ips)

	return ips, err
}

// UnAssignIpv6Addresses remove ip from eni
// return ok if 1. eni is released 2. ip is already released 3. release success
func (a *OpenAPI) UnAssignIpv6Addresses(ctx context.Context, eniID string, ips []netip.Addr) error {
	if len(ips) == 0 {
		return nil
	}
	req := ecs.CreateUnassignIpv6AddressesRequest()
	req.NetworkInterfaceId = eniID
	str := ip.IPAddrs2str(ips)
	req.Ipv6Address = &str

	l := logf.FromContext(ctx).WithValues(
		LogFieldAPI, "UnassignIpv6Addresses",
		LogFieldENIID, eniID,
		LogFieldIPs, strings.Join(str, ","),
	)
	start := time.Now()
	resp, err := a.ClientSet.ECS().UnassignIpv6Addresses(req)
	metric.OpenAPILatency.WithLabelValues("UnassignIpv6Addresses", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

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

func (a *OpenAPI) DescribeInstanceTypes(ctx context.Context, types []string) ([]ecs.InstanceType, error) {
	var result []ecs.InstanceType

	nextToken := ""
	for {
		req := ecs.CreateDescribeInstanceTypesRequest()
		req.NextToken = nextToken
		// nb(l1b0k): see https://help.aliyun.com/practice_detail/461278.
		req.MaxResults = requests.NewInteger(100)
		if types != nil {
			req.InstanceTypes = &types
		}
		start := time.Now()
		resp, err := a.ClientSet.ECS().DescribeInstanceTypes(req)
		metric.OpenAPILatency.WithLabelValues("DescribeInstanceTypes", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

		l := logf.FromContext(ctx).WithValues(
			LogFieldAPI, "DescribeInstanceTypes",
		)
		if err != nil {
			err = apiErr.WarpError(err)
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "describe instance types failed")
			return nil, err
		}

		result = append(result, resp.InstanceTypes.InstanceType...)

		if resp.NextToken == "" {
			break
		}
		nextToken = resp.NextToken
	}

	return result, nil
}

func (a *OpenAPI) ModifyNetworkInterfaceAttribute(ctx context.Context, eniID string, securityGroupIDs []string) error {
	req := ecs.CreateModifyNetworkInterfaceAttributeRequest()
	req.NetworkInterfaceId = eniID
	req.SecurityGroupId = &securityGroupIDs
	start := time.Now()
	resp, err := a.ClientSet.ECS().ModifyNetworkInterfaceAttribute(req)
	metric.OpenAPILatency.WithLabelValues("ModifyNetworkInterfaceAttribute", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

	l := logf.FromContext(ctx).WithValues(
		LogFieldAPI, "ModifyNetworkInterfaceAttribute",
	)
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "modify securityGroup failed")
		return err
	}
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("modify securityGroup", "ids", strings.Join(securityGroupIDs, ","))
	return nil
}

// DefaultGetLimit returns the instance limits of a particular instance type. // https://www.alibabacloud.com/help/doc-detail/25620.htm
// if instanceType is empty will list all instanceType and warm the cache, no error and Limits will return
func DefaultGetLimit(client interface{}, instanceType string) (*Limits, error) {
	a, ok := client.(*OpenAPI)
	if !ok {
		return nil, fmt.Errorf("unsupported client")
	}

	v, ok := limits.Load(instanceType)
	if ok {
		return v.(*Limits), nil
	}
	var req []string
	if instanceType != "" {
		req = append(req, instanceType)
	}
	ins, err := a.DescribeInstanceTypes(context.Background(), req)
	if err != nil {
		return nil, err
	}

	for _, instanceTypeInfo := range ins {
		instanceTypeID := instanceTypeInfo.InstanceTypeId
		adapterLimit := instanceTypeInfo.EniQuantity
		ipv4PerAdapter := instanceTypeInfo.EniPrivateIpAddressQuantity
		ipv6PerAdapter := instanceTypeInfo.EniIpv6AddressQuantity
		memberAdapterLimit := instanceTypeInfo.EniTotalQuantity - instanceTypeInfo.EniQuantity
		eRdmaLimit := instanceTypeInfo.EriQuantity
		// exclude eth0 eth1
		maxMemberAdapterLimit := instanceTypeInfo.EniTotalQuantity - 2
		if !instanceTypeInfo.EniTrunkSupported {
			memberAdapterLimit = 0
			maxMemberAdapterLimit = 0
		}
		limits.Store(instanceTypeID, &Limits{
			Adapters:              adapterLimit,
			TotalAdapters:         instanceTypeInfo.EniTotalQuantity,
			IPv4PerAdapter:        max(ipv4PerAdapter, 0),
			IPv6PerAdapter:        max(ipv6PerAdapter, 0),
			MemberAdapterLimit:    max(memberAdapterLimit, 0),
			MaxMemberAdapterLimit: max(maxMemberAdapterLimit, 0),
			ERdmaAdapters:         max(eRdmaLimit, 0),
			InstanceBandwidthRx:   instanceTypeInfo.InstanceBandwidthRx,
			InstanceBandwidthTx:   instanceTypeInfo.InstanceBandwidthTx,
		})
		logf.Log.WithValues(
			"instance-type", instanceType,
			"adapters", adapterLimit,
			"total-adapters", instanceTypeInfo.EniTotalQuantity,
			"ipv4", ipv4PerAdapter,
			"ipv6", ipv6PerAdapter,
			"member-adapters", memberAdapterLimit,
			"erdma-adapters", eRdmaLimit,
			"max-member-adapters", maxMemberAdapterLimit,
			"bandwidth-rx", instanceTypeInfo.InstanceBandwidthRx,
			"bandwidth-tx", instanceTypeInfo.InstanceBandwidthTx,
		).Info("instance limit")

	}
	if instanceType == "" {
		return nil, nil
	}
	v, ok = limits.Load(instanceType)
	if !ok {
		return nil, fmt.Errorf("unexpected error")
	}

	return v.(*Limits), nil
}
