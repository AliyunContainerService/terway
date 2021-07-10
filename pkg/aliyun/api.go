package aliyun

import (
	"fmt"
	"time"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/errors"
	"github.com/AliyunContainerService/terway/pkg/metric"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/flowcontrol"
)

type OpenAPI struct {
	clientSet *ClientMgr

	ReadOnlyRateLimiter flowcontrol.RateLimiter
	MutatingRateLimiter flowcontrol.RateLimiter
}

func NewAliyun(ak, sk, regionID, credentialPath string) (*OpenAPI, error) {
	if regionID == "" {
		return nil, fmt.Errorf("regionID unset")
	}
	clientSet, err := NewClientMgr(ak, sk, credentialPath, regionID)
	if err != nil {
		return nil, fmt.Errorf("error get clientset, %w", err)
	}
	return &OpenAPI{
		clientSet:           clientSet,
		ReadOnlyRateLimiter: flowcontrol.NewTokenBucketRateLimiter(8, 10),
		MutatingRateLimiter: flowcontrol.NewTokenBucketRateLimiter(4, 5),
	}, nil
}

// CreateNetworkInterface instanceType Secondary Trunk
func (a *OpenAPI) CreateNetworkInterface(instanceType ENIType, vSwitch string, securityGroups []string, ipCount int, eniTags map[string]string) (*ecs.CreateNetworkInterfaceResponse, error) {
	req := ecs.CreateCreateNetworkInterfaceRequest()
	req.VSwitchId = vSwitch
	req.InstanceType = string(instanceType)
	req.SecurityGroupIds = &securityGroups
	req.NetworkInterfaceName = generateEniName()
	req.Description = eniDescription
	if ipCount > 1 {
		req.SecondaryPrivateIpAddressCount = requests.NewInteger(ipCount - 1)
	}

	tags := []ecs.CreateNetworkInterfaceTag{}
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
	resp, err := a.clientSet.ECS().CreateNetworkInterface(req)
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
func (a *OpenAPI) DescribeNetworkInterface(vpcID string, eniID []string, instanceID string, instanceType ENIType, status ENIStatus) ([]ecs.NetworkInterfaceSet, error) {
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
		resp, err := a.clientSet.ECS().DescribeNetworkInterfaces(req)
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
func (a *OpenAPI) AttachNetworkInterface(eniID, instanceID, trunkENIID string) error {
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
	resp, err := a.clientSet.ECS().AttachNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues("AttachNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Warnf("attach ENI failed, %s", err.Error())
		return err
	}
	l.WithField(LogFieldRequestID, resp.RequestId).Infof("attach eni")
	return nil
}

// DetachNetworkInterface detach eni
func (a *OpenAPI) DetachNetworkInterface(eniID, instanceID, trunkENIID string) error {
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
	resp, err := a.clientSet.ECS().DetachNetworkInterface(req)
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
func (a *OpenAPI) DeleteNetworkInterface(eniID string) error {
	req := ecs.CreateDeleteNetworkInterfaceRequest()
	req.NetworkInterfaceId = eniID

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "DeleteNetworkInterface",
		LogFieldENIID: eniID,
	})
	a.MutatingRateLimiter.Accept()
	start := time.Now()
	resp, err := a.clientSet.ECS().DeleteNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues("DeleteNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Errorf("delete eni failed, %v", err)
		return err
	}
	l.WithField(LogFieldRequestID, resp.RequestId).Infof("delete eni")
	return nil
}

// WaitForNetworkInterface wait status of eni
func (a *OpenAPI) WaitForNetworkInterface(eniID string, status ENIStatus, backoff wait.Backoff) (*ecs.NetworkInterfaceSet, error) {
	var eniInfo *ecs.NetworkInterfaceSet

	err := wait.ExponentialBackoff(backoff,
		func() (done bool, err error) {
			eni, err := a.DescribeNetworkInterface("", []string{eniID}, "", "", status)
			if err != nil {
				return false, nil
			}
			if len(eni) == 1 {
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
