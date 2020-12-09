package aliyun

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/types"

	"github.com/denverdino/aliyungo/common"
	"github.com/denverdino/aliyungo/ecs"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	// NetworkInterfaceTagCreatorKey denotes the creator tag's key of network interface
	NetworkInterfaceTagCreatorKey = "creator"
	// NetworkInterfaceTagCreatorValue denotes the creator tag's value of network interface
	NetworkInterfaceTagCreatorValue = "terway"
)

// ECS the interface of ecs operation set
type ECS interface {
	AllocateENI(vSwitch string, securityGroup string, instanceID string, ipCount int, eniTags map[string]string) (*types.ENI, error)
	GetAttachedENIs(instanceID string, containsMainENI bool) ([]*types.ENI, error)
	GetENIByID(instanceID, eniID string) (*types.ENI, error)
	GetENIByMac(instanceID, mac string) (*types.ENI, error)
	FreeENI(eniID string, instanceID string) error
	GetENIIPs(eniID string) ([]net.IP, error)
	AssignIPForENI(eniID string) (net.IP, error)
	AssignNIPsForENI(eniID string, count int) ([]net.IP, error)
	UnAssignIPsForENI(eniID string, ips []net.IP) error
	GetInstanceMaxENI(instanceID string) (int, error)
	GetInstanceMaxPrivateIP(intanceID string) (int, error)
	GetInstanceMaxENIByType(instanceType string) (int, error)
	GetInstanceMaxPrivateIPByType(instanceType string) (int, error)
	GetENIMaxIP(instanceID string, eniID string) (int, error)
	GetAttachedSecurityGroup(instanceID string) (string, error)
	DescribeVSwitch(vSwitch string) (availIPCount int, err error)
	// EIP
	AllocateEipAddress(bandwidth int, chargeType common.InternetChargeType, eipID, eniID string, eniIP net.IP, allowRob bool) (*types.EIP, error)
	UnassociateEipAddress(eipID, eniID, eniIP string) error
	ReleaseEipAddress(eipID, eniID string, eniIP net.IP) error
	QueryEniIDByIP(address net.IP) (string, error)
}

type ecsImpl struct {
	privateIPMutex sync.RWMutex
	clientSet      *ClientMgr
	eniInfoGetter  ENIInfoGetter
	// avoid conflict on ecs
	openapiInfoGetter ENIInfoGetter
	region            common.Region
	vpcID             string
}

// NewECS return new ECS implement object
func NewECS(ak, sk, credentialPath string, region common.Region, ignoreLinkNotExist bool) (ECS, error) {
	clientSet, err := NewClientMgr(ak, sk, credentialPath)
	if err != nil {
		return nil, errors.Wrapf(err, "error get clientset")
	}
	if region == "" {
		regionStr, err := clientSet.MetaData().Region()
		if err != nil {
			return nil, errors.Wrapf(err, "error get regionid")
		}
		region = common.Region(regionStr)
		//RegionId = region
	}
	vpcID, err := clientSet.MetaData().VpcID()
	if err != nil {
		return nil, errors.Wrapf(err, "error get vpcID")
	}

	openapiENIInfoGetter := eniOpenAPI{
		clientSet: clientSet,
		region:    region,
	}

	return &ecsImpl{
		privateIPMutex: sync.RWMutex{},
		clientSet:      clientSet,
		eniInfoGetter: &eniMetadata{
			ignoreLinkNotExist: ignoreLinkNotExist,
		},
		openapiInfoGetter: &openapiENIInfoGetter,
		region:            region,
		vpcID:             vpcID,
	}, nil
}

// DescribeVSwitch for vswitch
func (e *ecsImpl) DescribeVSwitch(vSwitch string) (availIPCount int, err error) {
	vSwitchArgs := &ecs.DescribeVSwitchesArgs{
		RegionId:  e.region,
		VSwitchId: vSwitch,
	}
	vsw, _, err := e.clientSet.Ecs().DescribeVSwitches(vSwitchArgs)
	// For systems without RAM policy for VPC API permission, result is:
	// vsw is an empty slice, err is nil.
	// For systems which have RAM policy for VPC API permission,
	// (1) if vswitch indeed exists, result is:
	// vsw is a slice with a single element, err is nil.
	// (2) if vswitch doesn't exist, result is:
	// vsw is an empty slice, err is not nil.
	logrus.Debugf("result for DescribeVSwitches: vsw slice = %+v, err = %v", vsw, err)
	if len(vsw) > 0 {
		return vsw[0].AvailableIpAddressCount, nil
	}
	return 0, err

}

// AllocateENI for instance
func (e *ecsImpl) AllocateENI(vSwitch string, securityGroup string, instanceID string, ipCount int, eniTags map[string]string) (*types.ENI, error) {
	if vSwitch == "" || len(securityGroup) == 0 || instanceID == "" {
		return nil, errors.Errorf("invalid eni args for allocate")
	}
	var (
		start = time.Now()
		err   error
	)
	createNetworkInterfaceArgs := &ecs.CreateNetworkInterfaceArgs{
		RegionId:                       e.region,
		VSwitchId:                      vSwitch,
		SecurityGroupId:                securityGroup,
		NetworkInterfaceName:           generateEniName(),
		Description:                    eniDescription,
		SecondaryPrivateIpAddressCount: ipCount - 1,
		Tag: map[string]string{
			NetworkInterfaceTagCreatorKey: NetworkInterfaceTagCreatorValue,
		},
	}
	// append extra eni tags
	for k, v := range eniTags {
		createNetworkInterfaceArgs.Tag[k] = v
	}
	createNetworkInterfaceResponse, err := e.clientSet.Ecs().CreateNetworkInterface(createNetworkInterfaceArgs)
	metric.OpenAPILatency.WithLabelValues("CreateNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			eniDestroy := &types.ENI{
				ID: createNetworkInterfaceResponse.NetworkInterfaceId,
			}
			if err = e.destroyInterface(eniDestroy.ID, instanceID, true); err != nil {
				fmtErr := fmt.Sprintf("error rollback interface, may cause eni leak: %+v", err)
				_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
					tracing.AllocResourceFailed, fmtErr)
				logrus.Error(fmtErr)
			}
		}
	}()

	start = time.Now()
	attachNetworkInterfaceArgs := &ecs.AttachNetworkInterfaceArgs{
		RegionId:           e.region,
		NetworkInterfaceId: createNetworkInterfaceResponse.NetworkInterfaceId,
		InstanceId:         instanceID,
	}
	var innerErr error
	err = wait.ExponentialBackoff(eniOpBackoff, func() (bool, error) {
		innerErr = e.clientSet.Ecs().AttachNetworkInterface(attachNetworkInterfaceArgs)
		if innerErr != nil {
			logrus.Warnf("attach Network Interface failed: %v, retry...", innerErr)
			return false, nil
		}
		return true, nil
	})

	metric.OpenAPILatency.WithLabelValues("AttachNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		fmtErr := fmt.Sprintf("error attach network interface， %v", innerErr)
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
			tracing.AllocResourceFailed, fmtErr)
		return nil, errors.Wrap(err, fmtErr)
	}

	logrus.Debugf("wait network interface attach: %v, %v, %v", createNetworkInterfaceResponse.NetworkInterfaceId, createNetworkInterfaceResponse.RequestId, attachNetworkInterfaceArgs.InstanceId)

	start = time.Now()
	// bind status is async api, sleep for first bind status inspect
	time.Sleep(eniStateBackoff.Duration)
	eniStatus, err := e.WaitForNetworkInterface(createNetworkInterfaceResponse.NetworkInterfaceId, eniStatusInUse, eniStateBackoff)
	metric.OpenAPILatency.WithLabelValues("WaitForNetworkInterfaceBind/"+eniStatusInUse, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

	if err != nil {
		return nil, err
	}

	var eni *types.ENI
	// backoff get eni config
	err = wait.ExponentialBackoff(
		eniStateBackoff,
		func() (done bool, err error) {
			eni, innerErr = e.eniInfoGetter.GetENIConfigByMac(eniStatus.MacAddress)
			if innerErr != nil || eni.ID != createNetworkInterfaceResponse.NetworkInterfaceId {
				logrus.Warnf("error get eni config by mac: %v, retrying...", innerErr)
				return false, nil
			}

			eni.MaxIPs, innerErr = e.GetENIMaxIP(instanceID, eni.ID)
			if innerErr != nil {
				logrus.Warnf("error get eni max ips : %v, retrying...", innerErr)
				return false, nil
			}
			return true, nil
		},
	)
	return eni, errors.Wrapf(err, "error get eni config, %v", innerErr)
}

func (e *ecsImpl) destroyInterface(eniID string, instanceID string, force bool) error {
	var (
		retryErr error
	)

	var (
		start = time.Now()
		err   error
	)

	detachNetworkInterfaceArgs := &ecs.DetachNetworkInterfaceArgs{
		RegionId:           e.region,
		NetworkInterfaceId: eniID,
		InstanceId:         instanceID,
	}

	// backoff detach eni
	err = wait.ExponentialBackoff(
		eniReleaseBackoff,
		func() (done bool, err error) {
			start = time.Now()
			_, err = e.clientSet.Ecs().DetachNetworkInterface(detachNetworkInterfaceArgs)
			metric.OpenAPILatency.WithLabelValues("DetachNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
			if err != nil {
				retryErr = err
				logrus.Warnf("error detach eni: %v, retrying...", err)
				return false, nil
			}
			return true, nil
		},
	)
	if err != nil && !force {
		fmtErr := fmt.Sprintf("cannot detach eni,  %+v", retryErr)
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
			tracing.DisposeResourceFailed, fmtErr)
		return errors.Wrap(err, fmtErr)
	}

	start = time.Now()
	// detach status is async api, sleep for first detach status inspect
	time.Sleep(eniReleaseBackoff.Duration)
	_, err = e.WaitForNetworkInterface(eniID, eniStatusAvailable, eniReleaseBackoff)
	metric.OpenAPILatency.WithLabelValues("WaitForNetworkInterfaceDestroy/"+eniStatusAvailable, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

	if err != nil && !force {
		fmtErr := fmt.Sprintf("cannot wait detach network interface %+v", err)
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
			tracing.DisposeResourceFailed, fmtErr)
		return errors.Wrap(err, fmtErr)
	}

	deleteNetworkInterfaceArgs := &ecs.DeleteNetworkInterfaceArgs{
		RegionId:           e.region,
		NetworkInterfaceId: eniID,
	}
	// backoff delete network interface
	err = wait.ExponentialBackoff(
		eniReleaseBackoff,
		func() (done bool, err error) {
			start = time.Now()
			_, err = e.clientSet.Ecs().DeleteNetworkInterface(deleteNetworkInterfaceArgs)
			metric.OpenAPILatency.WithLabelValues("DeleteNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
			if err != nil {
				retryErr = err
				logrus.Warnf("error delete eni: %v, retrying...", err)
				return false, nil
			}
			return true, nil
		},
	)
	if err != nil {
		fmtErr := fmt.Sprintf("cannot detach eni: %v", retryErr)
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
			tracing.DisposeResourceFailed, fmtErr)
		return errors.Wrap(err, fmtErr)
	}
	return nil
}

// WaitForNetworkInterface wait status of eni
func (e *ecsImpl) WaitForNetworkInterface(eniID, status string, backoff wait.Backoff) (*ecs.NetworkInterfaceType, error) {
	var eniInfo *ecs.NetworkInterfaceType
	var innerErr error
	err := wait.ExponentialBackoff(backoff,
		func() (done bool, err error) {
			eniIds := []string{eniID}

			describeNetworkInterfacesArgs := ecs.DescribeNetworkInterfacesArgs{
				RegionId:           e.region,
				NetworkInterfaceId: eniIds,
			}
			var nisResponse *ecs.DescribeNetworkInterfacesResponse
			nisResponse, innerErr = e.clientSet.Ecs().DescribeNetworkInterfaces(&describeNetworkInterfacesArgs)
			if innerErr != nil {
				logrus.Warnf("Failed to describe network interface %v: %v", eniID, innerErr)
				return false, nil
			}

			if len(nisResponse.NetworkInterfaceSets.NetworkInterfaceSet) > 0 && nisResponse.NetworkInterfaceSets.NetworkInterfaceSet[0].Status == status {
				eniInfo = &nisResponse.NetworkInterfaceSets.NetworkInterfaceSet[0]
				return true, nil
			}
			return false, nil
		},
	)
	return eniInfo, errors.Wrapf(err, "error wait for network interface: %v, %v", eniID, innerErr)
}

// GetAttachedENIs of instanceId
// containsMainENI is contains the main interface(eth0) of instance
func (e *ecsImpl) GetAttachedENIs(instanceID string, containsMainENI bool) ([]*types.ENI, error) {
	enis, err := e.eniInfoGetter.GetAttachedENIs(instanceID, containsMainENI)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni config by mac")
	}
	for _, eni := range enis {
		eni.MaxIPs, err = e.GetENIMaxIP(instanceID, eni.ID)
		if err != nil {
			logrus.Warnf("error get eni max ips %v", err)
			return nil, errors.Wrapf(err, "error get eni max ip")
		}
	}
	return enis, nil
}

func (e *ecsImpl) FreeENI(eniID, instanceID string) error {
	return e.destroyInterface(eniID, instanceID, true)
}

func (e *ecsImpl) GetENIIPs(eniID string) ([]net.IP, error) {
	e.privateIPMutex.RLock()
	defer e.privateIPMutex.RUnlock()
	return e.eniInfoGetter.GetENIPrivateAddresses(eniID)
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

	assignPrivateIPAddressesArgs := &ecs.AssignPrivateIpAddressesArgs{
		RegionId:                       e.region,
		NetworkInterfaceId:             eniID,
		SecondaryPrivateIpAddressCount: count,
	}

	start := time.Now()
	var innerErr error
	var assignPrivateIPAddressesResponse *ecs.AssignPrivateIpAddressesResponse
	err := wait.ExponentialBackoff(eniOpBackoff, func() (bool, error) {
		assignPrivateIPAddressesResponse, innerErr = e.clientSet.Ecs().AssignPrivateIpAddresses(assignPrivateIPAddressesArgs)
		if innerErr != nil {
			logrus.Warnf("Assign private ip address failed: %+v, retrying", innerErr)
			if strings.Contains(innerErr.Error(), InvalidVSwitchIDIPNotEnough) {
				return false, errors.Errorf("Assign private ip address failed: %+v", innerErr)
			}
			return false, nil
		}
		return true, nil
	})
	metric.OpenAPILatency.WithLabelValues("AssignPrivateIpAddresses", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		fmtErr := fmt.Sprintf("error assign address for eniID: %v, %v", eniID, innerErr)
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
			tracing.AllocResourceFailed, fmtErr)
		return nil, errors.Wrap(err, fmtErr)
	}

	var newIPList []net.IP

	for _, ipStr := range assignPrivateIPAddressesResponse.AssignedPrivateIpAddressesSet.PrivateIpSet.PrivateIpAddress {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		newIPList = append(newIPList, ip)
	}
	if count != len(newIPList) {
		return nil, fmt.Errorf("incorrect count,want %d got %d", count, len(newIPList))
	}
	return newIPList, err
}

func (e *ecsImpl) UnAssignIPsForENI(eniID string, ips []net.IP) error {
	e.privateIPMutex.Lock()
	defer e.privateIPMutex.Unlock()

	unAssignPrivateIPAddressesArgs := &ecs.UnassignPrivateIpAddressesArgs{
		RegionId:           e.region,
		NetworkInterfaceId: eniID,
		PrivateIpAddress:   ips2str(ips),
	}

	var innerErr error
	start := time.Now()
	err := wait.ExponentialBackoff(
		eniOpBackoff,
		func() (bool, error) {
			_, innerErr = e.clientSet.Ecs().UnassignPrivateIpAddresses(unAssignPrivateIPAddressesArgs)
			if innerErr != nil {
				if ErrAssert(ErrInvalidIPIPUnassigned, innerErr) {
					return true, innerErr
				}
				logrus.Warnf("error unassign private ip address: %v, retry...", innerErr)
				return false, nil
			}
			return true, nil
		},
	)

	metric.OpenAPILatency.WithLabelValues("UnassignPrivateIpAddresses", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		if ErrAssert(ErrInvalidIPIPUnassigned, innerErr) {
			return nil
		}
		fmtErr := fmt.Sprintf("error unassign address for eniID: %v, %v", eniID, innerErr)
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning,
			tracing.DisposeResourceFailed, fmtErr)
		return errors.Wrap(err, fmtErr)
	}

	start = time.Now()
	var addressesAfter []net.IP
	// UnassignPrivateIpAddresses is async api, sleep for first ip addr inspect
	time.Sleep(eniStateBackoff.Duration)
	// backoff get interface addresses
	err = wait.ExponentialBackoff(
		eniStateBackoff,
		func() (done bool, err error) {
			addressesAfter, innerErr = e.openapiInfoGetter.GetENIPrivateAddresses(eniID)
			if innerErr != nil {
				logrus.Errorf("error get after eni private address for %s, err: %v", eniID, innerErr)
				return false, nil
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
		return errors.Wrap(err, fmtErr)
	}
	return nil
}

func (e *ecsImpl) GetInstanceMaxENI(instanceID string) (int, error) {
	eniCap := 0
	var innerErr error
	err := wait.ExponentialBackoff(
		eniStateBackoff,
		func() (done bool, err error) {
			var insType *ecs.InstanceAttributesType
			insType, innerErr = e.GetInstanceAttributesType(instanceID)
			if innerErr != nil {
				// fallback to deprecated DescribeInstanceAttribute
				start := time.Now()
				insType, innerErr = e.clientSet.Ecs().DescribeInstanceAttribute(instanceID)
				metric.OpenAPILatency.WithLabelValues("DescribeInstanceAttribute", fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))
				if innerErr != nil {
					logrus.Warnf("error get instance info: %s: %v， retry...", instanceID, innerErr)
					return false, nil
				}
			}

			start := time.Now()
			var instanceTypeItems []ecs.InstanceTypeItemType
			instanceTypeItems, innerErr = e.clientSet.Ecs().DescribeInstanceTypesNew(&ecs.DescribeInstanceTypesArgs{
				InstanceTypes: []string{insType.InstanceType},
			})
			metric.OpenAPILatency.WithLabelValues("DescribeInstanceTypesNew", fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))

			if innerErr != nil {
				// fallback to preload eni quota
				if quota, ok := ecsEniMatix[insType.InstanceType]; ok {
					eniCap = quota
					return true, nil
				}
				logrus.Warnf("error get instance types info: %v， retry...", innerErr)
				return false, nil
			}

			for _, instanceTypeSpec := range instanceTypeItems {
				if instanceTypeSpec.InstanceTypeId == insType.InstanceType {
					eniCap = instanceTypeSpec.EniQuantity
					break
				}
			}

			if eniCap == 0 {
				logrus.Warnf("error get instance type info: %v", insType.InstanceType)
				return false, errors.Errorf("error get instance type info: %v", insType.InstanceType)
			}
			return true, nil
		})

	return eniCap, errors.Wrapf(err, "error get instance max eni: %v, %v", instanceID, innerErr)
}

func (e *ecsImpl) GetInstanceMaxPrivateIP(instanceID string) (int, error) {
	maxEni, err := e.GetInstanceMaxENI(instanceID)
	if err != nil {
		return 0, errors.Wrapf(err, "error get instance max eni: %v", instanceID)
	}
	maxIP, err := e.GetENIMaxIP(instanceID, "")
	if err != nil {
		return 0, errors.Wrapf(err, "error get eni max ip: %v", instanceID)
	}
	maxIPForInstance := (maxEni - 1) * maxIP
	if maxIPForInstance <= 0 {
		return 0, errors.Errorf("instance not support multi ip address: %v ", instanceID)
	}
	return maxIPForInstance, nil
}

func (e *ecsImpl) GetENIMaxIP(instanceID string, eniID string) (int, error) {
	// fixme: the eniid must bind on specified instanceID
	eniIPCap := 0
	var innerErr error
	err := wait.ExponentialBackoff(
		eniStateBackoff,
		func() (done bool, err error) {
			var insType *ecs.InstanceAttributesType
			insType, innerErr = e.GetInstanceAttributesType(instanceID)
			if innerErr != nil {
				// fallback to deprecated DescribeInstanceAttribute
				start := time.Now()
				insType, innerErr = e.clientSet.Ecs().DescribeInstanceAttribute(instanceID)
				metric.OpenAPILatency.WithLabelValues("DescribeInstanceAttribute", fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))
				if innerErr != nil {
					logrus.Warnf("error get instance info: %s: %v， retry...", instanceID, innerErr)
					return false, nil
				}
			}

			start := time.Now()
			var instanceTypeItems []ecs.InstanceTypeItemType
			instanceTypeItems, innerErr = e.clientSet.Ecs().DescribeInstanceTypesNew(&ecs.DescribeInstanceTypesArgs{
				InstanceTypes: []string{insType.InstanceType},
			})
			metric.OpenAPILatency.WithLabelValues("DescribeInstanceTypesNew", fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))

			if innerErr != nil {
				logrus.Warnf("error get instance info: %v， retry...", innerErr)
				return false, nil
			}
			for _, instanceTypeSpec := range instanceTypeItems {
				if instanceTypeSpec.InstanceTypeId == insType.InstanceType {
					eniIPCap = instanceTypeSpec.EniPrivateIpAddressQuantity
					break
				}
			}

			if eniIPCap == 0 {
				logrus.Warnf("error get instance type info: %v", insType.InstanceType)
				return false, errors.Errorf("error get instance type info: %v", insType.InstanceType)
			}
			return true, nil
		})

	return eniIPCap, errors.Wrapf(err, "error get instance max eni ip: %v, %v", instanceID, innerErr)
}

func (e *ecsImpl) GetENIByID(instanceID, eniID string) (*types.ENI, error) {
	eni, err := e.eniInfoGetter.GetENIConfigByID(eniID)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni config by mac")
	}
	eni.MaxIPs, err = e.GetENIMaxIP(instanceID, eni.ID)
	if err != nil {
		logrus.Warnf("error get eni max ips %v", err)
		return nil, errors.Wrapf(err, "error get eni max ip")
	}
	return eni, nil
}

func (e *ecsImpl) GetENIByMac(instanceID, mac string) (*types.ENI, error) {
	eni, err := e.eniInfoGetter.GetENIConfigByMac(mac)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni config by mac")
	}
	eni.MaxIPs, err = e.GetENIMaxIP(instanceID, eni.ID)
	if err != nil {
		logrus.Warnf("error get eni max ips %v", err)
		return nil, errors.Wrapf(err, "error get eni max ip")
	}
	return eni, nil
}

func (e *ecsImpl) GetAttachedSecurityGroup(instanceID string) (string, error) {
	insType, err := e.GetInstanceAttributesType(instanceID)
	if err != nil {
		// fallback to deprecated DescribeInstanceAttribute
		start := time.Now()
		insType, err = e.clientSet.Ecs().DescribeInstanceAttribute(instanceID)
		metric.OpenAPILatency.WithLabelValues("DescribeInstanceAttribute", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
		if err != nil {
			return "", errors.Wrapf(err, "error describe instance attribute for security group: %s", instanceID)
		}
	}
	if len(insType.SecurityGroupIds.SecurityGroupId) > 0 {
		return insType.SecurityGroupIds.SecurityGroupId[0], nil
	}
	return "", fmt.Errorf("error get instance security groups: %s", instanceID)
}

func (e *ecsImpl) GetInstanceAttributesType(instanceID string) (*ecs.InstanceAttributesType, error) {
	diArgs := &ecs.DescribeInstancesArgs{
		RegionId:    e.region,
		InstanceIds: fmt.Sprintf("[%q]", instanceID),
	}
	start := time.Now()
	instanceAttributesTypes, _, err := e.clientSet.Ecs().DescribeInstances(diArgs)
	metric.OpenAPILatency.WithLabelValues("DescribeInstances", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, err
	}
	if len(instanceAttributesTypes) != 1 {
		return nil, fmt.Errorf("error get instanceAttributesType with instanceID %s: expected 1 but got %d", instanceID, len(instanceAttributesTypes))
	}
	return &instanceAttributesTypes[0], nil
}

func (e *ecsImpl) QueryEniIDByIP(address net.IP) (string, error) {
	args := ecs.DescribeNetworkInterfacesArgs{
		RegionId:         e.region,
		PrivateIpAddress: []string{address.String()},
		VpcID:            e.vpcID,
	}
	resp, err := e.clientSet.Ecs().DescribeNetworkInterfaces(&args)
	if err != nil || len(resp.NetworkInterfaceSets.NetworkInterfaceSet) != 1 {
		return "", errors.Errorf("error describe network interfaces from ip: %v, %v, %v", address, err, resp)
	}
	return resp.NetworkInterfaceSets.NetworkInterfaceSet[0].NetworkInterfaceId, nil
}

func (e *ecsImpl) GetInstanceMaxENIByType(instanceType string) (int, error) {
	eniCap := 0
	var innerErr error
	err := wait.ExponentialBackoff(
		eniStateBackoff,
		func() (done bool, err error) {
			start := time.Now()
			var instanceTypeItems []ecs.InstanceTypeItemType
			instanceTypeItems, innerErr = e.clientSet.Ecs().DescribeInstanceTypesNew(&ecs.DescribeInstanceTypesArgs{
				InstanceTypes: []string{instanceType},
			})
			metric.OpenAPILatency.WithLabelValues("DescribeInstanceTypesNew", fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))

			if innerErr != nil {
				// fallback to preload eni quota
				if quota, ok := ecsEniMatix[instanceType]; ok {
					eniCap = quota
					return true, nil
				}
				logrus.Warnf("error get instance types info: %v， retry...", innerErr)
				return false, nil
			}

			for _, instanceTypeSpec := range instanceTypeItems {
				if instanceTypeSpec.InstanceTypeId == instanceType {
					eniCap = instanceTypeSpec.EniQuantity
					break
				}
			}

			if eniCap == 0 {
				logrus.Warnf("error get instance type info: %v", instanceType)
				return false, errors.Errorf("error get instance type info: %v", instanceType)
			}
			return true, nil
		})

	return eniCap, errors.Wrapf(err, "error get instance max eni: %v, %v", instanceType, innerErr)
}

func (e *ecsImpl) GetInstanceMaxPrivateIPByType(instanceType string) (int, error) {
	eniIPCap := 0
	var innerErr error
	err := wait.ExponentialBackoff(
		eniStateBackoff,
		func() (done bool, err error) {
			start := time.Now()
			var instanceTypeItems []ecs.InstanceTypeItemType
			instanceTypeItems, innerErr = e.clientSet.Ecs().DescribeInstanceTypesNew(&ecs.DescribeInstanceTypesArgs{
				InstanceTypes: []string{instanceType},
			})
			metric.OpenAPILatency.WithLabelValues("DescribeInstanceTypesNew", fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))

			if innerErr != nil {
				logrus.Warnf("error get instance info: %v， retry...", innerErr)
				return false, nil
			}
			for _, instanceTypeSpec := range instanceTypeItems {
				if instanceTypeSpec.InstanceTypeId == instanceType {
					eniIPCap = instanceTypeSpec.EniPrivateIpAddressQuantity
					break
				}
			}

			if eniIPCap == 0 {
				logrus.Warnf("error get instance type info: %v", instanceType)
				return false, errors.Errorf("error get instance type info: %v", instanceType)
			}
			return true, nil
		})

	return eniIPCap, errors.Wrapf(err, "error get instance max eni ip: %v, %v", instanceType, innerErr)
}
