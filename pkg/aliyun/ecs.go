package aliyun

import (
	"context"
	"fmt"
	"net"
	"time"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/errors"
	"github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/metric"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/flowcontrol"
)

const maxSinglePageSize = 100

type OpenAPI struct {
	ClientSet Client

	ReadOnlyRateLimiter flowcontrol.RateLimiter
	MutatingRateLimiter flowcontrol.RateLimiter
}

func New(c Client, readOnly, mutating flowcontrol.RateLimiter) (*OpenAPI, error) {
	return &OpenAPI{
		ClientSet:           c,
		ReadOnlyRateLimiter: readOnly,
		MutatingRateLimiter: mutating,
	}, nil
}

func NewClientSet(ak, sk, regionID, credentialPath, secretNamespace, secretName string) (Client, error) {
	if regionID == "" {
		return nil, fmt.Errorf("regionID unset")
	}
	clientSet, err := NewClientMgr(ak, sk, credentialPath, regionID, secretNamespace, secretName)
	if err != nil {
		return nil, fmt.Errorf("error get clientset, %w", err)
	}
	return clientSet, nil
}

func NewAliyun(ak, sk, regionID, credentialPath, secretNamespace, secretName string) (*OpenAPI, error) {
	if regionID == "" {
		return nil, fmt.Errorf("regionID unset")
	}
	clientSet, err := NewClientMgr(ak, sk, credentialPath, regionID, secretNamespace, secretName)
	if err != nil {
		return nil, fmt.Errorf("error get clientset, %w", err)
	}
	return &OpenAPI{
		ClientSet:           clientSet,
		ReadOnlyRateLimiter: flowcontrol.NewTokenBucketRateLimiter(8, 10),
		MutatingRateLimiter: flowcontrol.NewTokenBucketRateLimiter(4, 5),
	}, nil
}

// CreateNetworkInterface instanceType Secondary Trunk
func (a *OpenAPI) CreateNetworkInterface(ctx context.Context, instanceType ENIType, vSwitch string, securityGroups []string, ipCount, ipv6Count int, eniTags map[string]string) (*ecs.CreateNetworkInterfaceResponse, error) {
	req := ecs.CreateCreateNetworkInterfaceRequest()
	req.VSwitchId = vSwitch
	req.InstanceType = string(instanceType)
	req.SecurityGroupIds = &securityGroups
	req.NetworkInterfaceName = generateEniName()
	req.Description = eniDescription
	if ipCount > 1 {
		req.SecondaryPrivateIpAddressCount = requests.NewInteger(ipCount - 1)
	}
	if ipv6Count > 0 {
		req.Ipv6AddressCount = requests.NewInteger(ipv6Count)
	}

	var tags []ecs.CreateNetworkInterfaceTag
	for k, v := range eniTags {
		tags = append(tags, ecs.CreateNetworkInterfaceTag{
			Key:   k,
			Value: v,
		})
	}
	req.Tag = &tags

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:       "CreateNetworkInterface",
		LogFieldVSwitchID: vSwitch,
	})
	a.MutatingRateLimiter.Accept()
	start := time.Now()
	resp, err := a.ClientSet.ECS().CreateNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues("CreateNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Errorf("error create ENI, %s", err.Error())
		return nil, err
	}

	l.WithFields(map[string]interface{}{
		LogFieldENIID:     resp.NetworkInterfaceId,
		LogFieldRequestID: resp.RequestId,
	}).Info("create ENI")
	return resp, err
}

// DescribeNetworkInterface list eni
func (a *OpenAPI) DescribeNetworkInterface(ctx context.Context, vpcID string, eniID []string, instanceID string, instanceType ENIType, status ENIStatus) ([]ecs.NetworkInterfaceSet, error) {
	var result []ecs.NetworkInterfaceSet
	for i := 1; ; {
		req := ecs.CreateDescribeNetworkInterfacesRequest()
		req.VpcId = vpcID

		req.NetworkInterfaceId = &eniID

		req.InstanceId = instanceID
		req.Type = string(instanceType)
		req.Status = string(status)

		req.PageNumber = requests.NewInteger(i)
		req.PageSize = requests.NewInteger(maxSinglePageSize)

		l := log.WithFields(map[string]interface{}{
			LogFieldAPI:        "DescribeNetworkInterfaces",
			LogFieldENIID:      eniID,
			LogFieldInstanceID: instanceID,
		})
		a.ReadOnlyRateLimiter.Accept()
		start := time.Now()
		resp, err := a.ClientSet.ECS().DescribeNetworkInterfaces(req)
		metric.OpenAPILatency.WithLabelValues("DescribeNetworkInterfaces", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
		if err != nil {
			l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Warn(err)
			return nil, err
		}
		result = append(result, resp.NetworkInterfaceSets.NetworkInterfaceSet...)

		if resp.TotalCount < resp.PageNumber*resp.PageSize {
			break
		}
		i++
	}
	return result, nil
}

// AttachNetworkInterface attach eni
func (a *OpenAPI) AttachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	req := ecs.CreateAttachNetworkInterfaceRequest()
	req.NetworkInterfaceId = eniID
	req.InstanceId = instanceID
	req.TrunkNetworkInstanceId = trunkENIID

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:        "AttachNetworkInterface",
		LogFieldENIID:      eniID,
		LogFieldInstanceID: instanceID,
	})
	a.MutatingRateLimiter.Accept()
	start := time.Now()
	resp, err := a.ClientSet.ECS().AttachNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues("AttachNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Warnf("attach ENI failed, %s", err.Error())
		return err
	}
	l.WithField(LogFieldRequestID, resp.RequestId).Infof("attach eni")
	return nil
}

// DetachNetworkInterface detach eni
func (a *OpenAPI) DetachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	req := ecs.CreateDetachNetworkInterfaceRequest()
	req.NetworkInterfaceId = eniID
	req.InstanceId = instanceID
	req.TrunkNetworkInstanceId = trunkENIID

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:        "DetachNetworkInterface",
		LogFieldENIID:      eniID,
		LogFieldInstanceID: instanceID,
	})
	a.MutatingRateLimiter.Accept()
	start := time.Now()
	resp, err := a.ClientSet.ECS().DetachNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues("DetachNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		if apiErr.ErrAssert(apiErr.ErrInvalidENINotFound, err) {
			return nil
		}
		l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Errorf("detach eni failed, %v", err)
		return err
	}
	l.WithField(LogFieldRequestID, resp.RequestId).Infof("detach eni")
	return nil
}

// DeleteNetworkInterface del eni by id
func (a *OpenAPI) DeleteNetworkInterface(ctx context.Context, eniID string) error {
	req := ecs.CreateDeleteNetworkInterfaceRequest()
	req.NetworkInterfaceId = eniID

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "DeleteNetworkInterface",
		LogFieldENIID: eniID,
	})
	a.MutatingRateLimiter.Accept()
	start := time.Now()
	resp, err := a.ClientSet.ECS().DeleteNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues("DeleteNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Errorf("delete eni failed, %v", err)
		return err
	}
	l.WithField(LogFieldRequestID, resp.RequestId).Infof("delete eni")
	return nil
}

// WaitForNetworkInterface wait status of eni
func (a *OpenAPI) WaitForNetworkInterface(ctx context.Context, eniID string, status ENIStatus, backoff wait.Backoff, ignoreNotExist bool) (*ecs.NetworkInterfaceSet, error) {
	var eniInfo *ecs.NetworkInterfaceSet
	if eniID == "" {
		return nil, fmt.Errorf("eniID not set")
	}
	err := wait.ExponentialBackoff(backoff,
		func() (done bool, err error) {
			eni, err := a.DescribeNetworkInterface(ctx, "", []string{eniID}, "", "", "")
			if err != nil {
				return false, nil
			}
			if len(eni) == 0 && ignoreNotExist {
				return true, apiErr.ErrNotFound
			}
			if len(eni) == 1 {
				if string(status) != eni[0].Status {
					return false, nil
				}

				eniInfo = &eni[0]
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

// AssignPrivateIPAddress assign secondary ip
func (a *OpenAPI) AssignPrivateIPAddress(ctx context.Context, eniID string, count int) ([]net.IP, error) {
	req := ecs.CreateAssignPrivateIpAddressesRequest()
	req.NetworkInterfaceId = eniID
	req.SecondaryPrivateIpAddressCount = requests.NewInteger(count)

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:              "AssignPrivateIpAddresses",
		LogFieldENIID:            eniID,
		LogFieldSecondaryIPCount: count,
	})
	start := time.Now()
	resp, err := a.ClientSet.ECS().AssignPrivateIpAddresses(req)
	metric.OpenAPILatency.WithLabelValues("AssignPrivateIpAddresses", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Warnf("assign private ip failed, %s", err.Error())
		return nil, err
	}
	ips, err := ip.ToIPs(resp.AssignedPrivateIpAddressesSet.PrivateIpSet.PrivateIpAddress)
	if err != nil {
		l.WithField(LogFieldRequestID, resp.RequestId).Errorf("assign private ip, %v", resp.AssignedPrivateIpAddressesSet.PrivateIpSet.PrivateIpAddress)
		return nil, err
	}
	l.WithField(LogFieldRequestID, resp.RequestId).Infof("assign private ip, %v", resp.AssignedPrivateIpAddressesSet.PrivateIpSet.PrivateIpAddress)

	return ips, nil
}

// UnAssignPrivateIPAddresses remove ip from eni
// return ok if 1. eni is released 2. ip is already released 3. release success
func (a *OpenAPI) UnAssignPrivateIPAddresses(ctx context.Context, eniID string, ips []net.IP) error {
	req := ecs.CreateUnassignPrivateIpAddressesRequest()
	req.NetworkInterfaceId = eniID
	str := ip.IPs2str(ips)
	req.PrivateIpAddress = &str

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "UnassignPrivateIpAddresses",
		LogFieldENIID: eniID,
	})
	start := time.Now()
	resp, err := a.ClientSet.ECS().UnassignPrivateIpAddresses(req)
	metric.OpenAPILatency.WithLabelValues("UnassignPrivateIpAddresses", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

	if err != nil {
		if apiErr.ErrAssert(apiErr.ErrInvalidIPIPUnassigned, err) {
			l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Infof("unassign private ip ,%s", str)
			return nil
		}
		if apiErr.ErrAssert(apiErr.ErrInvalidENINotFound, err) {
			l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Infof("unassign private ip ,%s", str)
			return nil
		}
		l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Warnf("unassign private ip failed,%s %s", str, err.Error())
		return err
	}
	l.WithField(LogFieldRequestID, resp.RequestId).Infof("unassign private ip ,%s", str)
	return nil
}

// AssignIpv6Addresses assign ipv6 address
func (a *OpenAPI) AssignIpv6Addresses(ctx context.Context, eniID string, count int) ([]net.IP, error) {
	req := ecs.CreateAssignIpv6AddressesRequest()
	req.NetworkInterfaceId = eniID
	req.Ipv6AddressCount = requests.NewInteger(count)

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:              "AssignIpv6Addresses",
		LogFieldENIID:            eniID,
		LogFieldSecondaryIPCount: count,
	})
	start := time.Now()
	resp, err := a.ClientSet.ECS().AssignIpv6Addresses(req)
	metric.OpenAPILatency.WithLabelValues("AssignIpv6Addresses", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Warnf("assign private ip failed, %s", err.Error())
		return nil, err
	}
	ips, err := ip.ToIPs(resp.Ipv6Sets.Ipv6Address)
	if err != nil {
		l.WithField(LogFieldRequestID, resp.RequestId).Errorf("assign private ip, %v", resp.Ipv6Sets.Ipv6Address)
		return nil, err
	}
	l.WithField(LogFieldRequestID, resp.RequestId).Infof("assign ipv6 ip, %v", resp.Ipv6Sets.Ipv6Address)

	return ips, nil
}

// UnAssignIpv6Addresses remove ip from eni
// return ok if 1. eni is released 2. ip is already released 3. release success
func (a *OpenAPI) UnAssignIpv6Addresses(ctx context.Context, eniID string, ips []net.IP) error {
	req := ecs.CreateUnassignIpv6AddressesRequest()
	req.NetworkInterfaceId = eniID
	str := ip.IPs2str(ips)
	req.Ipv6Address = &str

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "UnassignIpv6Addresses",
		LogFieldENIID: eniID,
	})
	start := time.Now()
	resp, err := a.ClientSet.ECS().UnassignIpv6Addresses(req)
	metric.OpenAPILatency.WithLabelValues("UnassignIpv6Addresses", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

	if err != nil {
		if apiErr.ErrAssert(apiErr.ErrInvalidIPIPUnassigned, err) {
			l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Infof("unassign private ip ,%s", str)
			return nil
		}
		if apiErr.ErrAssert(apiErr.ErrInvalidENINotFound, err) {
			l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Infof("unassign private ip ,%s", str)
			return nil
		}
		l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Warnf("unassign private ipv6 failed,%s %s", str, err.Error())
		return err
	}
	l.WithField(LogFieldRequestID, resp.RequestId).Infof("unassign ipv6 ip ,%s", str)
	return nil
}
