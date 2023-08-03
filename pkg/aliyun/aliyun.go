package aliyun

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
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

	tagFilter map[string]string // eg.      "TagKey": "creator", "TagValue": "terway"

	*client.OpenAPI
}

// NewAliyunImpl return new API implement object
func NewAliyunImpl(openAPI *client.OpenAPI, needENITypeAttr bool, ipFamily *types.IPFamily, tagFilter map[string]string) ipam.API {
	return &Impl{
		metadata:    NewENIMetadata(ipFamily),
		ipFamily:    ipFamily,
		eniTypeAttr: needENITypeAttr,
		OpenAPI:     openAPI,
		tagFilter:   tagFilter,
	}
}

// AllocateENI for instance
func (e *Impl) AllocateENI(ctx context.Context, vSwitch string, securityGroups []string, instanceID string, trunk bool, ipCount int, eniTags map[string]string) (*types.ENI, error) {
	if vSwitch == "" || len(securityGroups) == 0 || instanceID == "" {
		return nil, fmt.Errorf("invalid eni args for allocate")
	}

	ipv4Count, ipv6Count := 0, 0
	if e.ipFamily.IPv4 {
		ipv4Count = ipCount
	}
	if e.ipFamily.IPv6 {
		ipv6Count = ipCount
	}

	resp, err := e.CreateNetworkInterface(ctx, trunk, vSwitch, securityGroups, "", ipv4Count, ipv6Count, eniTags)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			rollBackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			eniDestroy := &types.ENI{
				ID: resp.NetworkInterfaceID,
			}
			if err = e.destroyInterface(rollBackCtx, eniDestroy.ID, instanceID, ""); err != nil {
				fmtErr := fmt.Sprintf("error rollback interface, may cause eni leak: %+v", err)
				_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
					tracing.AllocResourceFailed, fmtErr)
				logrus.Error(fmtErr)
			}
		}
	}()

	var innerErr error
	err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.WaitENIStatus), func(ctx context.Context) (bool, error) {
		innerErr = e.AttachNetworkInterface(ctx, resp.NetworkInterfaceID, instanceID, "")
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

	logrus.Debugf("wait network interface attach: %v, %v", resp.NetworkInterfaceID, instanceID)

	start := time.Now()
	// bind status is async api, sleep for first bind status inspect
	time.Sleep(backoff.Backoff(backoff.WaitENIStatus).Duration)
	eniStatus, err := e.WaitForNetworkInterface(ctx, resp.NetworkInterfaceID, client.ENIStatusInUse, backoff.Backoff(backoff.WaitENIStatus), false)
	metric.OpenAPILatency.WithLabelValues("WaitForNetworkInterfaceBind/"+string(client.ENIStatusInUse), fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

	if err != nil {
		return nil, err
	}

	var eni *types.ENI
	// backoff get eni config
	err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.WaitENIStatus),
		func(ctx context.Context) (done bool, err error) {
			eni, innerErr = e.metadata.GetENIByMac(eniStatus.MacAddress)
			if innerErr != nil || eni.ID != resp.NetworkInterfaceID {
				logrus.Warnf("error get eni config by mac: %v, retrying...", innerErr)
				return false, nil
			}
			eni.Trunk = eniStatus.Type == client.ENITypeTrunk
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
		func(ctx context.Context) (done bool, err error) {
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
		func(ctx context.Context) (done bool, err error) {
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
func (e *Impl) GetAttachedENIs(ctx context.Context, containsMainENI bool, trunkENIID string) ([]*types.ENI, error) {
	enis, err := e.metadata.GetENIs(containsMainENI)
	if err != nil {
		return nil, fmt.Errorf("error get eni config by mac, %w", err)
	}

	var eniIDs []string
	enisMap := map[string]*types.ENI{}
	for _, eni := range enis {
		eniIDs = append(eniIDs, eni.ID)
		enisMap[eni.ID] = eni

		if trunkENIID == eni.ID {
			eni.Trunk = true
		}
	}

	var result []*types.ENI
	if (e.eniTypeAttr || len(e.tagFilter) > 0) && len(eniIDs) > 0 {
		if trunkENIID == "" || len(e.tagFilter) > 0 {
			eniSet, err := e.DescribeNetworkInterface(ctx, "", eniIDs, "", "", "", e.tagFilter)
			if err != nil {
				return nil, err
			}
			for _, eni := range eniSet {
				e, ok := enisMap[eni.NetworkInterfaceID]
				if !ok {
					continue
				}
				e.Trunk = eni.Type == client.ENITypeTrunk

				// take to intersect
				result = append(result, e)
			}
		} else {
			result = enis
		}
	} else {
		result = enis
	}
	return result, nil
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

func (e *Impl) AssignNIPsForENI(ctx context.Context, eniID, mac string, count int) ([]net.IP, []net.IP, error) {
	if eniID == "" || mac == "" || count <= 0 {
		return nil, nil, fmt.Errorf("args error")
	}
	e.privateIPMutex.Lock()
	defer e.privateIPMutex.Unlock()

	var wg sync.WaitGroup
	var ipv4s, ipv6s []net.IP
	var err, v4Err, v6Err error

	wrap := func(e error) error {
		err = e
		return err
	}

	defer func() {
		if err == nil {
			return
		}
		fmtErr := fmt.Errorf("error assign %d address for eniID: %v, %w", count, eniID, err)
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
			tracing.AllocResourceFailed, fmtErr.Error())

		// rollback ips
		rollBackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		roleBackErr := e.unAssignIPsForENIUnSafe(rollBackCtx, eniID, mac, ipv4s, ipv6s)
		if roleBackErr != nil {
			fmtErr = fmt.Errorf("roll back failed %s, %w", fmtErr, roleBackErr)
			log.Error(fmtErr.Error())
			_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
				tracing.AllocResourceFailed, fmtErr.Error())
		}
	}()

	if e.ipFamily.IPv4 {
		var innerErr error
		idempotentKey := string(uuid.NewUUID())
		err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.ENIOps), func(ctx context.Context) (bool, error) {
			ipv4s, innerErr = e.AssignPrivateIPAddress(ctx, eniID, count, idempotentKey)
			if innerErr != nil {
				if apiErr.ErrAssert(apiErr.InvalidVSwitchIDIPNotEnough, innerErr) {
					return false, innerErr
				}
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			return nil, nil, wrap(fmt.Errorf("%w, innerErr %v", err, innerErr))
		}
		if len(ipv4s) != count {
			return nil, nil, wrap(fmt.Errorf("openAPI return IP error.Want %d got %d", count, len(ipv4s)))
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			v4Err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.MetaAssignPrivateIP),
				func(ctx context.Context) (bool, error) {
					var remoteIPs []net.IP
					remoteIPs, innerErr = e.metadata.GetENIPrivateAddressesByMAC(mac)
					if innerErr != nil {
						return false, nil
					}
					if !ip.IPsIntersect(remoteIPs, ipv4s) {
						innerErr = fmt.Errorf("ip is not present in metadataAPI,expect %s got %s", ipv4s, remoteIPs)
						return false, nil
					}
					return true, nil
				},
			)
			if v4Err != nil {
				v4Err = fmt.Errorf("%w, metadataAPI %v", v4Err, innerErr)
			}
		}()
	}

	if e.ipFamily.IPv6 {
		var innerErr error
		idempotentKey := string(uuid.NewUUID())
		err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.ENIOps), func(ctx context.Context) (bool, error) {
			ipv6s, innerErr = e.AssignIpv6Addresses(ctx, eniID, count, idempotentKey)
			if innerErr != nil {
				if apiErr.ErrAssert(apiErr.InvalidVSwitchIDIPNotEnough, innerErr) {
					return false, innerErr
				}
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			return ipv4s, ipv6s, wrap(fmt.Errorf("%w, innerErr %v", err, innerErr))
		}
		if len(ipv6s) != count {
			return ipv4s, ipv6s, wrap(fmt.Errorf("openAPI return IP error.Want %d got %d", count, len(ipv6s)))
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			v6Err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.MetaAssignPrivateIP),
				func(ctx context.Context) (bool, error) {
					var remoteIPs []net.IP
					remoteIPs, innerErr = e.metadata.GetENIPrivateIPv6AddressesByMAC(mac)
					if innerErr != nil {
						return false, nil
					}
					if !ip.IPsIntersect(remoteIPs, ipv6s) {
						innerErr = fmt.Errorf("ip is not present in metadataAPI,expect %s got %s", ipv6s, remoteIPs)
						return false, nil
					}
					return true, nil
				},
			)
			if v6Err != nil {
				v6Err = fmt.Errorf("%w, metadataAPI %v", v6Err, innerErr)
			}
		}()
	}
	wg.Wait()

	err = k8sErr.NewAggregate([]error{v4Err, v6Err})

	return ipv4s, ipv6s, err
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

		err := wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.ENIOps), func(ctx context.Context) (bool, error) {
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

		err := wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.ENIOps), func(ctx context.Context) (bool, error) {
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
		func(ctx context.Context) (bool, error) {
			if len(ipv4s) > 0 {
				var remoteIPs []net.IP
				remoteIPs, innerErr = e.metadata.GetENIPrivateAddressesByMAC(mac)
				if innerErr != nil {
					return false, nil
				}
				if ip.IPsIntersect(remoteIPs, ipv4s) {
					innerErr = fmt.Errorf("ip is present in metadataAPI,expect %s be removed, got %s", ipv4s, remoteIPs)
					return false, nil
				}
			}
			if len(ipv6s) > 0 {
				var remoteIPs []net.IP
				remoteIPs, innerErr = e.metadata.GetENIPrivateIPv6AddressesByMAC(mac)
				if innerErr != nil {
					return false, nil
				}
				if ip.IPsIntersect(remoteIPs, ipv6s) {
					innerErr = fmt.Errorf("ip is present in metadataAPI,expect %s be removed, got %s", ipv6s, remoteIPs)
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
	eniList, err := e.DescribeNetworkInterface(ctx, "", nil, instanceID, "", "", nil)
	if err != nil {
		logrus.WithField(client.LogFieldInstanceID, instanceID).Warn(err)
		return nil
	}
	sgSet := sets.NewString(sg...)

	var errs []error
	for _, eni := range eniList {
		if eni.Type == string(client.ENITypePrimary) {
			continue
		}
		eniSgSet := sets.NewString(eni.SecurityGroupIDs...)
		if sgSet.Intersection(eniSgSet).Len() > 0 {
			continue
		}
		err := fmt.Errorf("found eni %s security group [%s] mismatch witch ecs security group [%s]."+
			"If you can confirm config is correct, you can ignore this", eni.NetworkInterfaceID,
			strings.Join(eni.SecurityGroupIDs, ","), strings.Join(sg, ","))
		logrus.WithField("instance", instanceID).Warn(err)

		errs = append(errs, err)
	}
	return k8sErr.NewAggregate(errs)
}
