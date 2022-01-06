package aliyun

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/errors"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	"github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/ipam"
	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/types"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
)

var log = logger.DefaultLogger.WithField("subSys", "openAPI")

var _ ipam.API = &Impl{}

type Impl struct {
	privateIPMutex sync.RWMutex
	metadata       ENIInfoGetter

	// fixme remove when metadata support eni type field
	eniTypeAttr bool

	ipFamily *types.IPFamily

	*OpenAPI
}

// NewAliyunImpl return new API implement object
func NewAliyunImpl(openAPI *OpenAPI, needENITypeAttr bool, ipFamily *types.IPFamily) ipam.API {
	return &Impl{
		metadata:    NewENIMetadata(ipFamily),
		ipFamily:    ipFamily,
		eniTypeAttr: needENITypeAttr,
		OpenAPI:     openAPI,
	}
}

// AllocateENI for instance
func (e *Impl) AllocateENI(ctx context.Context, vSwitch string, securityGroups []string, instanceID string, trunk bool, ipCount int, eniTags map[string]string) (*types.ENI, error) {
	if vSwitch == "" || len(securityGroups) == 0 || instanceID == "" {
		return nil, fmt.Errorf("invalid eni args for allocate")
	}

	var instanceType ENIType
	if trunk {
		instanceType = ENITypeTrunk
	}
	ipv4Count, ipv6Count := 0, 0
	if e.ipFamily.IPv4 {
		ipv4Count = ipCount
	}
	if e.ipFamily.IPv6 {
		ipv6Count = ipCount
	}

	resp, err := e.CreateNetworkInterface(ctx, instanceType, vSwitch, securityGroups, ipv4Count, ipv6Count, eniTags)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			eniDestroy := &types.ENI{
				ID: resp.NetworkInterfaceId,
			}
			if err = e.destroyInterface(ctx, eniDestroy.ID, instanceID, ""); err != nil {
				fmtErr := fmt.Sprintf("error rollback interface, may cause eni leak: %+v", err)
				_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
					tracing.AllocResourceFailed, fmtErr)
				logrus.Error(fmtErr)
			}
		}
	}()

	var innerErr error
	err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.WaitENIStatus), func() (bool, error) {
		innerErr = e.AttachNetworkInterface(ctx, resp.NetworkInterfaceId, instanceID, "")
		if innerErr != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		fmtErr := fmt.Sprintf("error attach ENI, %v", innerErr)
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
			tracing.AllocResourceFailed, fmtErr)
		return nil, fmt.Errorf("%s, %w", fmtErr, err)
	}

	logrus.Debugf("wait network interface attach: %v, %v, %v", resp.NetworkInterfaceId, resp.RequestId, instanceID)

	start := time.Now()
	// bind status is async api, sleep for first bind status inspect
	time.Sleep(backoff.Backoff(backoff.WaitENIStatus).Duration)
	eniStatus, err := e.WaitForNetworkInterface(ctx, resp.NetworkInterfaceId, ENIStatusInUse, backoff.Backoff(backoff.WaitENIStatus), false)
	metric.OpenAPILatency.WithLabelValues("WaitForNetworkInterfaceBind/"+string(ENIStatusInUse), fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

	if err != nil {
		return nil, err
	}

	var eni *types.ENI
	// backoff get eni config
	err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.WaitENIStatus),
		func() (done bool, err error) {
			l, ok := GetLimit(GetInstanceMeta().InstanceType)
			if !ok {
				return true, fmt.Errorf("failed to get instance type")
			}

			eni, innerErr = e.metadata.GetENIByMac(eniStatus.MacAddress)
			if innerErr != nil || eni.ID != resp.NetworkInterfaceId {
				logrus.Warnf("error get eni config by mac: %v, retrying...", innerErr)
				return false, nil
			}

			eni.MaxIPs = l.IPv4PerAdapter
			eni.Trunk = ENIType(eniStatus.Type) == ENITypeTrunk
			return true, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error get eni config, %v, %w", innerErr, err)
	}

	return eni, nil
}

func (e *Impl) FreeENI(ctx context.Context, eniID, instanceID string) error {
	return e.destroyInterface(ctx, eniID, instanceID, "")
}

func (e *Impl) destroyInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	var innerErr error
	err := wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.ENIRelease),
		func() (done bool, err error) {
			innerErr = e.DetachNetworkInterface(ctx, eniID, instanceID, trunkENIID)
			if innerErr != nil {
				return false, nil
			}
			return true, nil
		},
	)
	if err != nil {
		fmtErr := fmt.Sprintf("cannot detach eni,  %+v", innerErr)
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
			tracing.DisposeResourceFailed, fmtErr)
	}

	time.Sleep(backoff.Backoff(backoff.WaitENIStatus).Duration)

	// backoff delete network interface
	err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.ENIOps),
		func() (done bool, err error) {
			innerErr = e.DeleteNetworkInterface(context.Background(), eniID)
			if innerErr != nil {
				return false, nil
			}
			return true, nil
		},
	)
	if err != nil {
		fmtErr := fmt.Sprintf("cannot delete eni: %v %v", err, innerErr)
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
			tracing.DisposeResourceFailed, fmtErr)
		return fmt.Errorf("%s, %w", fmtErr, err)
	}
	return nil
}

// GetAttachedENIs of instanceId
// containsMainENI is contains the main interface(eth0) of instance
func (e *Impl) GetAttachedENIs(ctx context.Context, containsMainENI bool) ([]*types.ENI, error) {
	enis, err := e.metadata.GetENIs(containsMainENI)
	if err != nil {
		return nil, fmt.Errorf("error get eni config by mac, %w", err)
	}
	l, ok := GetLimit(GetInstanceMeta().InstanceType)
	if !ok {
		return nil, fmt.Errorf("failed to get instance type")
	}
	var eniIDs []string
	enisMap := map[string]*types.ENI{}
	for _, eni := range enis {
		eni.MaxIPs = l.IPv4PerAdapter
		eniIDs = append(eniIDs, eni.ID)
		enisMap[eni.ID] = eni
	}
	if e.eniTypeAttr && len(eniIDs) > 0 {
		eniSet, err := e.DescribeNetworkInterface(ctx, "", eniIDs, "", "", "")
		if err != nil {
			return nil, err
		}
		for _, eni := range eniSet {
			enisMap[eni.NetworkInterfaceId].Trunk = ENIType(eni.Type) == ENITypeTrunk
		}
	}
	return enis, nil
}

func (e *Impl) GetSecondaryENIMACs(ctx context.Context) ([]string, error) {
	return e.metadata.GetSecondaryENIMACs()
}

func (e *Impl) GetENIIPs(ctx context.Context, mac string) ([]net.IP, []net.IP, error) {
	e.privateIPMutex.RLock()
	defer e.privateIPMutex.RUnlock()

	var ipv4, ipv6 []net.IP
	var err error
	if e.ipFamily.IPv4 {
		ipv4, err = e.metadata.GetENIPrivateAddressesByMAC(mac)
		if err != nil {
			return nil, nil, err
		}
	}
	if e.ipFamily.IPv6 {
		ipv6, err = e.metadata.GetENIPrivateIPv6AddressesByMAC(mac)
		if err != nil {
			return nil, nil, err
		}
	}
	return ipv4, ipv6, nil
}

func (e *Impl) AssignNIPsForENI(ctx context.Context, eniID, mac string, count int) (ipSet []types.IPSet, err error) {
	if eniID == "" || mac == "" || count <= 0 {
		return nil, fmt.Errorf("args error")
	}

	e.privateIPMutex.Lock()
	defer e.privateIPMutex.Unlock()

	var wg sync.WaitGroup
	var ipv4s, ipv6s []net.IP
	var v4Err, v6Err error

	defer func() {
		if err == nil {
			return
		}
		fmtErr := fmt.Errorf("error assign %d address for eniID: %v, %w", count, eniID, err)
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
			tracing.AllocResourceFailed, fmtErr.Error())

		// rollback ips
		roleBackErr := e.unAssignIPsForENIUnSafe(ctx, eniID, mac, ipv4s, ipv6s)
		if roleBackErr != nil {
			fmtErr = fmt.Errorf("roll back failed %s, %w", fmtErr, roleBackErr)
			log.Error(fmtErr.Error())
			_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
				tracing.AllocResourceFailed, fmtErr.Error())
		}
	}()

	if e.ipFamily.IPv4 {
		var innerErr error
		err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.ENIOps), func() (bool, error) {
			ipv4s, innerErr = e.AssignPrivateIPAddress(ctx, eniID, count)
			if innerErr != nil {
				if apiErr.ErrAssert(apiErr.InvalidVSwitchIDIPNotEnough, innerErr) {
					return false, innerErr
				}
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			return
		}
		if len(ipv4s) != count {
			return nil, fmt.Errorf("openAPI return IP error.Want %d got %d", count, len(ipv4s))
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			v4Err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.MetaAssignPrivateIP),
				func() (done bool, err error) {
					remoteIPs, err := e.metadata.GetENIPrivateAddressesByMAC(mac)
					if err != nil {
						return false, nil
					}
					if !ip.IPsIntersect(remoteIPs, ipv4s) {
						return false, nil
					}
					return true, nil
				},
			)
		}()
	}

	if e.ipFamily.IPv6 {
		var innerErr error
		err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.ENIOps), func() (bool, error) {
			ipv6s, innerErr = e.AssignIpv6Addresses(ctx, eniID, count)
			if innerErr != nil {
				if apiErr.ErrAssert(apiErr.InvalidVSwitchIDIPNotEnough, innerErr) {
					return false, innerErr
				}
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			return
		}
		if len(ipv6s) != count {
			return nil, fmt.Errorf("openAPI return IP error.Want %d got %d", count, len(ipv6s))
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			v6Err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.MetaAssignPrivateIP),
				func() (done bool, err error) {
					remoteIPs, err := e.metadata.GetENIPrivateIPv6AddressesByMAC(mac)
					if err != nil {
						return false, nil
					}
					if !ip.IPsIntersect(remoteIPs, ipv6s) {
						return false, nil
					}
					return true, nil
				},
			)
		}()
	}
	wg.Wait()

	return types.MergeIPs(ipv4s, ipv6s), k8sErr.NewAggregate([]error{v4Err, v6Err})
}

func (e *Impl) UnAssignIPsForENI(ctx context.Context, eniID, mac string, ipv4s []net.IP, ipv6s []net.IP) error {
	e.privateIPMutex.Lock()
	defer e.privateIPMutex.Unlock()

	return e.unAssignIPsForENIUnSafe(ctx, eniID, mac, ipv4s, ipv6s)
}

func (e *Impl) unAssignIPsForENIUnSafe(ctx context.Context, eniID, mac string, ipv4s []net.IP, ipv6s []net.IP) error {
	if eniID == "" || mac == "" {
		return fmt.Errorf("args error")
	}

	var errs []error

	if len(ipv4s) > 0 {
		var innerErr error

		err := wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.ENIOps), func() (bool, error) {
			innerErr = e.UnAssignPrivateIPAddresses(ctx, eniID, ipv4s)
			if innerErr != nil {
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			errs = append(errs, err, innerErr)
		}
	}

	if len(ipv6s) > 0 {
		var innerErr error

		err := wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.ENIOps), func() (bool, error) {
			innerErr = e.UnAssignIpv6Addresses(ctx, eniID, ipv6s)
			if innerErr != nil {
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			errs = append(errs, err, innerErr)
		}
	}

	if len(errs) > 0 {
		fmtErr := fmt.Sprintf("error unassign address for eniID: %v, %v", eniID, k8sErr.NewAggregate(errs))
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
			tracing.DisposeResourceFailed, fmtErr)
		return k8sErr.NewAggregate(errs)
	}

	start := time.Now()

	// unassignPrivateIpAddresses is async api, sleep for first ip addr inspect
	time.Sleep(backoff.Backoff(backoff.MetaUnAssignPrivateIP).Duration)
	// backoff get interface addresses
	var innerErr error

	err := wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.MetaUnAssignPrivateIP),
		func() (done bool, err error) {
			if len(ipv4s) > 0 {
				remoteIPs, err := e.metadata.GetENIPrivateAddressesByMAC(mac)
				if err != nil {
					return false, nil
				}
				if ip.IPsIntersect(remoteIPs, ipv4s) {
					return false, nil
				}
			}
			if len(ipv6s) > 0 {
				remoteIPs, err := e.metadata.GetENIPrivateIPv6AddressesByMAC(mac)
				if err != nil {
					return false, nil
				}
				if ip.IPsIntersect(remoteIPs, ipv6s) {
					return false, nil
				}
			}
			return true, nil
		},
	)
	metric.OpenAPILatency.WithLabelValues("UnassignPrivateIpAddressesAsync", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		fmtErr := fmt.Sprintf("error unassign eni private address for %s, %v", eniID, innerErr)
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
			tracing.DisposeResourceFailed, fmtErr)
		return fmt.Errorf("%s, %w", fmtErr, err)
	}
	return nil
}

func (e *Impl) GetENIByMac(ctx context.Context, mac string) (*types.ENI, error) {
	eni, err := e.metadata.GetENIByMac(mac)
	if err != nil {
		return nil, fmt.Errorf("error get eni config by mac, %w", err)
	}
	l, ok := GetLimit(GetInstanceMeta().InstanceType)
	if !ok {
		return nil, fmt.Errorf("failed to get instance type")
	}
	eni.MaxIPs = l.IPv4PerAdapter
	return eni, nil
}

func (e *Impl) GetAttachedSecurityGroups(ctx context.Context, instanceID string) ([]string, error) {
	var ids []string
	insType, err := e.GetInstanceAttributesType(ctx, instanceID)
	if err != nil {
		// fallback to deprecated DescribeInstanceAttribute
		start := time.Now()
		req := ecs.CreateDescribeInstanceAttributeRequest()
		req.InstanceId = instanceID
		resp, err := e.ClientSet.ECS().DescribeInstanceAttribute(req)
		metric.OpenAPILatency.WithLabelValues("DescribeInstanceAttribute", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
		if err != nil {
			return nil, fmt.Errorf("error describe instance attribute for security group: %s,%w", instanceID, err)
		}
		ids = resp.SecurityGroupIds.SecurityGroupId
	} else {
		ids = insType.SecurityGroupIds.SecurityGroupId
	}
	if len(ids) > 0 {
		return ids, nil
	}
	return nil, fmt.Errorf("error get instance security groups: %s", instanceID)
}

func (e *Impl) GetInstanceAttributesType(ctx context.Context, instanceID string) (*ecs.Instance, error) {
	req := ecs.CreateDescribeInstancesRequest()
	req.InstanceIds = fmt.Sprintf("[%q]", instanceID)

	start := time.Now()
	resp, err := e.ClientSet.ECS().DescribeInstances(req)
	metric.OpenAPILatency.WithLabelValues("DescribeInstances", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, err
	}
	if len(resp.Instances.Instance) != 1 {
		return nil, fmt.Errorf("error get instanceAttributesType with instanceID %s: expected 1 but got %d", instanceID, len(resp.Instances.Instance))
	}
	return &resp.Instances.Instance[0], nil
}

func (e *Impl) QueryEniIDByIP(ctx context.Context, vpcID string, address net.IP) (string, error) {
	req := ecs.CreateDescribeNetworkInterfacesRequest()
	req.VpcId = vpcID
	req.PrivateIpAddress = &[]string{address.String()}

	resp, err := e.ClientSet.ECS().DescribeNetworkInterfaces(req)
	if err != nil || len(resp.NetworkInterfaceSets.NetworkInterfaceSet) != 1 {
		return "", fmt.Errorf("error describe network interfaces from ip: %v, %v, %v", address, err, resp)
	}
	return resp.NetworkInterfaceSets.NetworkInterfaceSet[0].NetworkInterfaceId, nil
}

// CheckEniSecurityGroup will sync eni's security with ecs's security group
func (e *Impl) CheckEniSecurityGroup(ctx context.Context, sg []string) error {
	instanceID := GetInstanceMeta().InstanceID

	// get all attached eni
	eniList, err := e.DescribeNetworkInterface(ctx, "", nil, instanceID, "", "")
	if err != nil {
		logrus.WithField(LogFieldInstanceID, instanceID).Warn(err)
		return nil
	}
	sgSet := sets.NewString(sg...)

	var errs []error
	for _, eni := range eniList {
		if eni.Type == string(ENITypePrimary) {
			continue
		}
		eniSgSet := sets.NewString(eni.SecurityGroupIds.SecurityGroupId...)
		if sgSet.Intersection(eniSgSet).Len() > 0 {
			continue
		}
		err := fmt.Errorf("found eni %s security group [%s] mismatch witch ecs security group [%s]."+
			"If you can confirm config is correct, you can ignore this", eni.NetworkInterfaceId,
			strings.Join(eni.SecurityGroupIds.SecurityGroupId, ","), strings.Join(sg, ","))
		logrus.WithField("instance", instanceID).Warn(err)

		errs = append(errs, err)
	}
	return k8sErr.NewAggregate(errs)
}
