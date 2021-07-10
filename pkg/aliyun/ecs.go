package aliyun

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/errors"
	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	"github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/types"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	errors2 "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
)

var log = logger.DefaultLogger.WithField("subSys", "openAPI")

// ECS the interface of ecs operation set
type ECS interface {
	AllocateENI(vSwitch, securityGroup, instanceID string, trunk bool, ipCount int, eniTags map[string]string) (*types.ENI, error)
	GetAttachedENIs(containsMainENI bool) ([]*types.ENI, error)
	GetSecondaryENIMACs() ([]string, error)
	GetPrivateIPv4ByMAC(mac string) ([]net.IP, error)
	GetENIByMac(mac string) (*types.ENI, error)
	FreeENI(eniID string, instanceID string) error
	GetENIIPs(mac string) ([]net.IP, []net.IP, error)
	AssignIPForENI(eniID string) (net.IP, error)
	AssignNIPsForENI(eniID string, count int) ([]net.IP, error)
	UnAssignIPsForENI(eniID string, ips []net.IP) error
	GetAttachedSecurityGroups(instanceID string) ([]string, error)
	DescribeVSwitchByID(vSwitch string) (*vpc.VSwitch, error)
	CheckEniSecurityGroup(sgIDs []string) error
	// EIP
	AllocateEipAddress(bandwidth int, chargeType types.InternetChargeType, eipID, eniID string, eniIP net.IP, allowRob bool) (*types.EIP, error)
	UnassociateEipAddress(eipID, eniID, eniIP string) error
	ReleaseEipAddress(eipID, eniID string, eniIP net.IP) error
	QueryEniIDByIP(address net.IP) (string, error)
}

type ecsImpl struct {
	privateIPMutex sync.RWMutex
	metadata       ENIInfoGetter
	vpcID          string

	// fixme remove when metadata support eni type field
	eniTypeAttr bool

	ipFamily *types.IPFamily

	*OpenAPI
}

// NewECS return new ECS implement object
func NewECS(ak, sk, credentialPath string, needENITypeAttr, ignoreLinkNotExist bool, vpcID, regionID string, ipFamily *types.IPFamily) (ECS, error) {
	openAPI, err := NewAliyun(ak, sk, regionID, credentialPath)
	if err != nil {
		return nil, fmt.Errorf("error get clientset, %w", err)
	}
	e := &ecsImpl{
		privateIPMutex: sync.RWMutex{},
		metadata:       NewENIMetadata(ignoreLinkNotExist, ipFamily),
		vpcID:          vpcID,
		ipFamily:       ipFamily,
		eniTypeAttr:    needENITypeAttr,
		OpenAPI:        openAPI,
	}

	err = UpdateFromAPI(openAPI.clientSet.ECS(), GetInstanceMeta().InstanceType)
	return e, err
}

// AllocateENI for instance
func (e *ecsImpl) AllocateENI(vSwitch, securityGroup, instanceID string, trunk bool, ipCount int, eniTags map[string]string) (*types.ENI, error) {
	if vSwitch == "" || len(securityGroup) == 0 || instanceID == "" {
		return nil, fmt.Errorf("invalid eni args for allocate")
	}

	var instanceType ENIType
	if trunk {
		instanceType = ENITypeTrunk
	}
	resp, err := e.CreateNetworkInterface(instanceType, vSwitch, []string{securityGroup}, ipCount, eniTags)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			eniDestroy := &types.ENI{
				ID: resp.NetworkInterfaceId,
			}
			if err = e.destroyInterface(eniDestroy.ID, instanceID, ""); err != nil {
				fmtErr := fmt.Sprintf("error rollback interface, may cause eni leak: %+v", err)
				_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
					tracing.AllocResourceFailed, fmtErr)
				logrus.Error(fmtErr)
			}
		}
	}()

	var innerErr error
	err = wait.ExponentialBackoff(ENIOpBackoff, func() (bool, error) {
		innerErr = e.AttachNetworkInterface(resp.NetworkInterfaceId, instanceID, "")
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
	time.Sleep(eniStateBackoff.Duration)
	eniStatus, err := e.WaitForNetworkInterface(resp.NetworkInterfaceId, ENIStatusInUse, eniStateBackoff)
	metric.OpenAPILatency.WithLabelValues("WaitForNetworkInterfaceBind/"+string(ENIStatusInUse), fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

	if err != nil {
		return nil, err
	}

	var eni *types.ENI
	// backoff get eni config
	err = wait.ExponentialBackoff(
		eniStateBackoff,
		func() (done bool, err error) {
			l, ok := GetLimit(GetInstanceMeta().InstanceType)
			if !ok {
				return true, fmt.Errorf("failed to get instance type")
			}

			eni, innerErr = e.metadata.GetENIConfigByMac(eniStatus.MacAddress)
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

func (e *ecsImpl) destroyInterface(eniID, instanceID, trunkENIID string) error {
	var innerErr error
	err := wait.ExponentialBackoff(
		eniReleaseBackoff,
		func() (done bool, err error) {
			innerErr = e.DetachNetworkInterface(eniID, instanceID, trunkENIID)
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

	time.Sleep(eniStateBackoff.Duration)

	// backoff delete network interface
	err = wait.ExponentialBackoff(
		eniReleaseBackoff,
		func() (done bool, err error) {
			innerErr = e.DeleteNetworkInterface(eniID)
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
func (e *ecsImpl) GetAttachedENIs(containsMainENI bool) ([]*types.ENI, error) {
	enis, err := e.metadata.GetAttachedENIs(containsMainENI)
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
		eniSet, err := e.DescribeNetworkInterface("", eniIDs, "", "", "")
		if err != nil {
			return nil, err
		}
		for _, eni := range eniSet {
			enisMap[eni.NetworkInterfaceId].Trunk = ENIType(eni.Type) == ENITypeTrunk
		}
	}
	return enis, nil
}

func (e *ecsImpl) GetSecondaryENIMACs() ([]string, error) {
	return e.metadata.GetSecondaryENIMACs()
}

func (e *ecsImpl) GetPrivateIPv4ByMAC(mac string) ([]net.IP, error) {
	return metadata.GetENIPrivateIPs(mac)
}

func (e *ecsImpl) FreeENI(eniID, instanceID string) error {
	return e.destroyInterface(eniID, instanceID, "")
}

func (e *ecsImpl) GetENIIPs(mac string) ([]net.IP, []net.IP, error) {
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

func (e *ecsImpl) AssignIPForENI(eniID string) (net.IP, error) {
	ipList, err := e.AssignNIPsForENI(eniID, 1)
	if err != nil || len(ipList) != 1 {
		return nil, fmt.Errorf("error assign ip for eni: %s, ipList: %v, err: %v", eniID, ipList, err)
	}
	return ipList[0], nil
}

func (e *ecsImpl) AssignNIPsForENI(eniID string, count int) ([]net.IP, error) {
	e.privateIPMutex.Lock()
	defer e.privateIPMutex.Unlock()

	var innerErr error
	var ips []net.IP
	err := wait.ExponentialBackoff(ENIOpBackoff, func() (bool, error) {
		ips, innerErr = e.assignPrivateIPAddresses(eniID, count)
		if innerErr != nil {
			if apiErr.ErrAssert(apiErr.InvalidVSwitchIDIPNotEnough, innerErr) {
				return false, innerErr
			}
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		fmtErr := fmt.Sprintf("error assign address for eniID: %v, %v", eniID, innerErr)
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
			tracing.AllocResourceFailed, fmtErr)
		return nil, fmt.Errorf("%s, %w", fmtErr, err)
	}

	if count != len(ips) {
		return nil, fmt.Errorf("incorrect count,want %d got %d", count, len(ips))
	}
	return ips, nil
}

func (e *ecsImpl) UnAssignIPsForENI(eniID string, ips []net.IP) error {
	e.privateIPMutex.Lock()
	defer e.privateIPMutex.Unlock()

	var innerErr error
	err := wait.ExponentialBackoff(
		ENIOpBackoff,
		func() (bool, error) {
			innerErr = e.unAssignPrivateIPAddresses(eniID, ips)
			if innerErr != nil {
				return false, nil
			}
			return true, nil
		},
	)
	if err != nil {
		fmtErr := fmt.Sprintf("error unassign address for eniID: %v, %v", eniID, innerErr)
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
			tracing.DisposeResourceFailed, fmtErr)
		return fmt.Errorf("%s, %w", fmtErr, err)
	}

	start := time.Now()

	// unassignPrivateIpAddresses is async api, sleep for first ip addr inspect
	time.Sleep(eniStateBackoff.Duration)
	// backoff get interface addresses
	err = wait.ExponentialBackoff(
		eniStateBackoff,
		func() (done bool, err error) {
			var enis []ecs.NetworkInterfaceSet
			enis, innerErr = e.DescribeNetworkInterface("", []string{eniID}, "", "", "")
			if innerErr != nil {
				return false, nil
			}
			if len(enis) == 0 {
				return false, nil
			}
			var addressesAfter []net.IP
			for _, ipStr := range enis[0].PrivateIpSets.PrivateIpSet {
				i, err := ip.ToIP(ipStr.PrivateIpAddress)
				if err != nil {
					return false, err
				}
				addressesAfter = append(addressesAfter, i)
			}
			if ipIntersect(addressesAfter, ips) {
				return false, nil
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

func (e *ecsImpl) GetENIByMac(mac string) (*types.ENI, error) {
	eni, err := e.metadata.GetENIConfigByMac(mac)
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

func (e *ecsImpl) GetAttachedSecurityGroups(instanceID string) ([]string, error) {
	var ids []string
	insType, err := e.GetInstanceAttributesType(instanceID)
	if err != nil {
		// fallback to deprecated DescribeInstanceAttribute
		start := time.Now()
		req := ecs.CreateDescribeInstanceAttributeRequest()
		req.InstanceId = instanceID
		resp, err := e.clientSet.ECS().DescribeInstanceAttribute(req)
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

func (e *ecsImpl) GetInstanceAttributesType(instanceID string) (*ecs.Instance, error) {
	req := ecs.CreateDescribeInstancesRequest()
	req.InstanceIds = fmt.Sprintf("[%q]", instanceID)

	start := time.Now()
	resp, err := e.clientSet.ECS().DescribeInstances(req)
	metric.OpenAPILatency.WithLabelValues("DescribeInstances", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, err
	}
	if len(resp.Instances.Instance) != 1 {
		return nil, fmt.Errorf("error get instanceAttributesType with instanceID %s: expected 1 but got %d", instanceID, len(resp.Instances.Instance))
	}
	return &resp.Instances.Instance[0], nil
}

func (e *ecsImpl) QueryEniIDByIP(address net.IP) (string, error) {
	req := ecs.CreateDescribeNetworkInterfacesRequest()
	req.VpcId = e.vpcID
	req.PrivateIpAddress = &[]string{address.String()}

	resp, err := e.clientSet.ECS().DescribeNetworkInterfaces(req)
	if err != nil || len(resp.NetworkInterfaceSets.NetworkInterfaceSet) != 1 {
		return "", fmt.Errorf("error describe network interfaces from ip: %v, %v, %v", address, err, resp)
	}
	return resp.NetworkInterfaceSets.NetworkInterfaceSet[0].NetworkInterfaceId, nil
}

// CheckEniSecurityGroup will sync eni's security with ecs's security group
func (e *ecsImpl) CheckEniSecurityGroup(sg []string) error {
	instanceID := GetInstanceMeta().InstanceID

	// get all attached eni
	eniList, err := e.DescribeNetworkInterface("", nil, instanceID, "", "")
	if err != nil {
		logrus.WithField(LogFieldInstanceID, instanceID).Warn(err)
		return nil
	}
	sgSet := sets.NewString(sg...)

	var errs []error
	for _, eni := range eniList {
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
	return errors2.NewAggregate(errs)
}

// unAssignPrivateIPAddresses
// return ok if 1. eni is released 2. ip is already released 3. release success
func (e *ecsImpl) unAssignPrivateIPAddresses(eniID string, ips []net.IP) error {
	req := ecs.CreateUnassignPrivateIpAddressesRequest()
	req.NetworkInterfaceId = eniID
	str := ips2str(ips)
	req.PrivateIpAddress = &str

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "UnassignPrivateIpAddresses",
		LogFieldENIID: eniID,
	})
	start := time.Now()
	resp, err := e.clientSet.ECS().UnassignPrivateIpAddresses(req)
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

func (e *ecsImpl) assignPrivateIPAddresses(eniID string, count int) ([]net.IP, error) {
	req := ecs.CreateAssignPrivateIpAddressesRequest()
	req.NetworkInterfaceId = eniID
	req.SecondaryPrivateIpAddressCount = requests.NewInteger(count)

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:              "AssignPrivateIpAddresses",
		LogFieldENIID:            eniID,
		LogFieldSecondaryIPCount: count,
	})
	start := time.Now()
	resp, err := e.clientSet.ECS().AssignPrivateIpAddresses(req)
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
