package aliyun

import (
	"fmt"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/types"
	"github.com/denverdino/aliyungo/common"
	"github.com/denverdino/aliyungo/ecs"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
	"net"
	"sync"
	"time"
)

type ECS interface {
	AllocateENI(vSwitch string, securityGroup string, instanceId string) (*types.ENI, error)
	GetAttachedENIs(instanceId string, containsMainENI bool) ([]*types.ENI, error)
	GetENIById(instanceId, eniId string) (*types.ENI, error)
	GetENIByMac(instanceId, mac string) (*types.ENI, error)
	FreeENI(eniId string, instanceId string) error
	GetENIIPs(eniId string) ([]net.IP, error)
	AssignIPForENI(eniId string) (net.IP, error)
	UnAssignIPForENI(eniid string, ip net.IP) error
	GetInstanceMaxENI(instanceId string) (int, error)
	GetInstanceMaxPrivateIP(intanceId string) (int, error)
	GetENIMaxIP(instanceId string, eniId string) (int, error)
}

type ecsImpl struct {
	privateIpMutex sync.RWMutex
	clientSet      *ClientMgr
	eniInfoGetter  ENIInfoGetter
	// avoid conflict on ecs
	openapiInfoGetter ENIInfoGetter
	region            common.Region
}

func NewECS(ak, sk string, region common.Region) (ECS, error) {
	clientSet, err := NewClientMgr(ak, sk)
	if err != nil {
		return nil, errors.Wrapf(err, "error get clientset")
	}
	if region == "" {
		regionStr, err := clientSet.meta.Region()
		if err != nil {
			return nil, errors.Wrapf(err, "error get regionid")
		}
		region = common.Region(regionStr)
		//RegionId = region
	}

	openapiENIInfoGetter := ENIOpenAPI{
		clientSet: clientSet,
		region:    region,
	}

	return &ecsImpl{
		privateIpMutex:    sync.RWMutex{},
		clientSet:         clientSet,
		eniInfoGetter:     &ENIMetadata{},
		openapiInfoGetter: &openapiENIInfoGetter,
		region:            region,
	}, nil
}

func (e ecsImpl) AllocateENI(vSwitch string, securityGroup string, instanceId string) (*types.ENI, error) {
	if vSwitch == "" || len(securityGroup) == 0 || instanceId == "" {
		return nil, errors.Errorf("invalid eni args for allocate")
	}
	var (
		start = time.Now()
		err   error
	)
	createNetworkInterfaceArgs := &ecs.CreateNetworkInterfaceArgs{
		RegionId:             common.Region(e.region),
		VSwitchId:            vSwitch,
		SecurityGroupId:      securityGroup,
		NetworkInterfaceName: generateEniName(),
		Description:          eniDescription,
	}
	createNetworkInterfaceResponse, err := e.clientSet.ecs.CreateNetworkInterface(createNetworkInterfaceArgs)
	metric.OpenAPILatency.WithLabelValues("CreateNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			eniDestroy := &types.ENI{
				ID: createNetworkInterfaceResponse.NetworkInterfaceId,
			}
			e.destroyInterface(eniDestroy.ID, instanceId, true)
		}
	}()

	start = time.Now()
	err = e.clientSet.ecs.WaitForNetworkInterface(createNetworkInterfaceArgs.RegionId,
		createNetworkInterfaceResponse.NetworkInterfaceId, eniStatusAvailable, eniCreateTimeout)
	metric.OpenAPILatency.WithLabelValues("WaitForNetworkInterfaceCreate/"+eniStatusAvailable, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, err
	}

	start = time.Now()
	attachNetworkInterfaceArgs := &ecs.AttachNetworkInterfaceArgs{
		RegionId:           common.Region(e.region),
		NetworkInterfaceId: createNetworkInterfaceResponse.NetworkInterfaceId,
		InstanceId:         instanceId,
	}
	err = e.clientSet.ecs.AttachNetworkInterface(attachNetworkInterfaceArgs)
	metric.OpenAPILatency.WithLabelValues("AttachNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, err
	}

	start = time.Now()
	err = e.clientSet.ecs.WaitForNetworkInterface(createNetworkInterfaceArgs.RegionId,
		createNetworkInterfaceResponse.NetworkInterfaceId, eniStatusInUse, eniBindTimeout)
	metric.OpenAPILatency.WithLabelValues("WaitForNetworkInterfaceBind/"+eniStatusInUse, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

	if err != nil {
		return nil, err
	}

	describeNetworkInterfacesArgs := &ecs.DescribeNetworkInterfacesArgs{
		RegionId:           createNetworkInterfaceArgs.RegionId,
		NetworkInterfaceId: []string{createNetworkInterfaceResponse.NetworkInterfaceId},
	}
	var describeNetworkInterfacesResp *ecs.DescribeNetworkInterfacesResponse
	start = time.Now()
	describeNetworkInterfacesResp, err = e.clientSet.ecs.DescribeNetworkInterfaces(describeNetworkInterfacesArgs)
	metric.OpenAPILatency.WithLabelValues("DescribeNetworkInterfaces", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, err
	}

	if len(describeNetworkInterfacesResp.NetworkInterfaceSets.NetworkInterfaceSet) != 1 {
		err = fmt.Errorf("error get ENIInfoGetter interface: %s", createNetworkInterfaceResponse.NetworkInterfaceId)
		return nil, err
	}
	var eni *types.ENI
	// backoff get eni config
	err = wait.ExponentialBackoff(
		wait.Backoff{
			Duration: time.Second,
			Factor:   2,
			Jitter:   0,
			Steps:    5,
		},
		func() (done bool, err error) {
			eni, err = e.eniInfoGetter.GetENIConfigByMac(describeNetworkInterfacesResp.NetworkInterfaceSets.NetworkInterfaceSet[0].MacAddress)
			if err != nil || eni.ID != createNetworkInterfaceResponse.NetworkInterfaceId {
				logrus.Warnf("error get eni config by mac: %v, retrying...", err)
				return false, nil
			}

			eni.MaxIPs, err = e.GetENIMaxIP(instanceId, eni.ID)
			if err != nil {
				logrus.Warnf("error get eni max ips : %v, retrying...", err)
				return false, nil
			}
			return true, nil
		},
	)
	return eni, err
}

func (e ecsImpl) destroyInterface(eniId string, instanceId string, force bool) error {
	var (
		retryErr error
	)

	var (
		start = time.Now()
		err   error
	)

	detachNetworkInterfaceArgs := &ecs.DetachNetworkInterfaceArgs{
		RegionId:           common.Region(e.region),
		NetworkInterfaceId: eniId,
		InstanceId:         instanceId,
	}

	// backoff get eni config
	err = wait.ExponentialBackoff(
		wait.Backoff{
			Duration: time.Second,
			Factor:   2,
			Jitter:   0,
			Steps:    5,
		},
		func() (done bool, err error) {
			start = time.Now()
			_, err = e.clientSet.ecs.DetachNetworkInterface(detachNetworkInterfaceArgs)
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
		return errors.Wrapf(err, "cannot detach eni: %v", retryErr)
	}

	start = time.Now()
	err = e.clientSet.ecs.WaitForNetworkInterface(detachNetworkInterfaceArgs.RegionId,
		eniId, eniStatusAvailable, eniBindTimeout)
	metric.OpenAPILatency.WithLabelValues("WaitForNetworkInterfaceDestroy/"+eniStatusAvailable, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

	if err != nil && !force {
		return errors.Wrapf(err, "cannot wait detach network interface")
	}

	deleteNetworkInterfaceArgs := &ecs.DeleteNetworkInterfaceArgs{
		RegionId:           e.region,
		NetworkInterfaceId: eniId,
	}
	// backoff delete network interface
	err = wait.ExponentialBackoff(
		wait.Backoff{
			Duration: time.Second,
			Factor:   2,
			Jitter:   0,
			Steps:    5,
		},
		func() (done bool, err error) {
			start = time.Now()
			_, err = e.clientSet.ecs.DeleteNetworkInterface(deleteNetworkInterfaceArgs)
			metric.OpenAPILatency.WithLabelValues("DeleteNetworkInterface", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
			if err != nil {
				logrus.Warnf("error delete eni: %v, retrying...", err)
				return false, nil
			}
			return true, nil
		},
	)
	return errors.Wrapf(err, "cannot detach eni: %v", retryErr)
}

func (e ecsImpl) GetAttachedENIs(instanceId string, containsMainENI bool) ([]*types.ENI, error) {
	enis, err := e.eniInfoGetter.GetAttachedENIs(instanceId, containsMainENI)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni config by mac")
	}
	for _, eni := range enis {
		eni.MaxIPs, err = e.GetENIMaxIP(instanceId, eni.ID)
		if err != nil {
			logrus.Warnf("error get eni max ips", err)
			return nil, errors.Wrapf(err, "error get eni max ip")
		}
	}
	return enis, nil
}

func (e ecsImpl) FreeENI(eniId, instanceId string) error {
	return e.destroyInterface(eniId, instanceId, true)
}

func (e ecsImpl) GetENIIPs(eniId string) ([]net.IP, error) {
	e.privateIpMutex.RLock()
	defer e.privateIpMutex.RUnlock()
	return e.eniInfoGetter.GetENIPrivateAddresses(eniId)
}

func (e ecsImpl) AssignIPForENI(eniId string) (net.IP, error) {
	e.privateIpMutex.Lock()
	defer e.privateIpMutex.Unlock()
	addressesBefore, err := e.openapiInfoGetter.GetENIPrivateAddresses(eniId)
	if err != nil {
		return nil, errors.Wrapf(err, "error get before address for eniId: %v", eniId)
	}

	assignPrivateIpAddressesArgs := &ecs.AssignPrivateIpAddressesArgs{
		RegionId:                       e.region,
		NetworkInterfaceId:             eniId,
		SecondaryPrivateIpAddressCount: 1,
	}

	start := time.Now()
	_, err = e.clientSet.ecs.AssignPrivateIpAddresses(assignPrivateIpAddressesArgs)
	metric.OpenAPILatency.WithLabelValues("AssignPrivateIpAddresses", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, errors.Wrapf(err, "error assign address for eniId: %v", eniId)
	}

	var addressesAfter []net.IP
	// backoff get interface addresses
	err = wait.ExponentialBackoff(
		wait.Backoff{
			Duration: time.Second,
			Factor:   2,
			Jitter:   0,
			Steps:    5,
		},
		func() (done bool, err error) {
			addressesAfter, err = e.openapiInfoGetter.GetENIPrivateAddresses(eniId)
			if err != nil {
				return false, errors.Wrapf(err, "error get after eni private address for %s", eniId)
			}

			if len(addressesAfter)-len(addressesBefore) != 1 {
				return false, nil
			} else {
				return true, nil
			}
		},
	)

	if err != nil {
		return nil, errors.Wrapf(err, "error allocate eni private address for %s", eniId)
	}
	var newIP net.IP
	mb := map[string]bool{}
	for _, beforeIP := range addressesBefore {
		mb[beforeIP.String()] = true
	}
	for _, afterIP := range addressesAfter {
		if _, ok := mb[afterIP.String()]; !ok {
			newIP = afterIP
			break
		}
	}
	return newIP, err
}

func (e ecsImpl) UnAssignIPForENI(eniId string, ip net.IP) error {
	e.privateIpMutex.Lock()
	defer e.privateIpMutex.Unlock()

	addressesBefore, err := e.openapiInfoGetter.GetENIPrivateAddresses(eniId)
	if err != nil {
		return errors.Wrapf(err, "error get before address for eniId: %v", eniId)
	}

	found := false
	for _, addr := range addressesBefore {
		if addr.Equal(ip) {
			found = true
		}
	}
	// ip not exist on eni
	if !found {
		return nil
	}

	unAssignPrivateIpAddressesArgs := &ecs.UnassignPrivateIpAddressesArgs{
		RegionId:           e.region,
		NetworkInterfaceId: eniId,
		PrivateIpAddress:   []string{ip.String()},
	}

	start := time.Now()
	_, err = e.clientSet.ecs.UnassignPrivateIpAddresses(unAssignPrivateIpAddressesArgs)
	metric.OpenAPILatency.WithLabelValues("UnassignPrivateIpAddresses", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return errors.Wrapf(err, "error unassign address for eniId: %v", eniId)
	}

	var addressesAfter []net.IP
	// backoff get interface addresses
	err = wait.ExponentialBackoff(
		wait.Backoff{
			Duration: time.Second,
			Factor:   2,
			Jitter:   0,
			Steps:    5,
		},
		func() (done bool, err error) {
			addressesAfter, err = e.openapiInfoGetter.GetENIPrivateAddresses(eniId)
			if err != nil {
				return false, errors.Wrapf(err, "error get after eni private address for %s", eniId)
			}

			if len(addressesBefore)-len(addressesAfter) != 1 {
				return false, nil
			} else {
				return true, nil
			}
		},
	)
	return errors.Wrapf(err, "error unassign eni private address for %s", eniId)
}

func (e ecsImpl) GetInstanceMaxENI(instanceId string) (int, error) {
	eniCap := 0
	err := wait.ExponentialBackoff(
		wait.Backoff{
			Duration: time.Second,
			Factor:   2,
			Jitter:   0,
			Steps:    5,
		}, func() (done bool, err error) {
			start := time.Now()
			insType, err := e.clientSet.ecs.DescribeInstanceAttribute(instanceId)
			metric.OpenAPILatency.WithLabelValues("DescribeInstanceAttribute", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
			if err != nil {
				logrus.Warnf("error get instance info: %s: %v， retry...", instanceId, err)
				return false, nil
			}

			start = time.Now()
			instanceTypeItems, err := e.clientSet.ecs.DescribeInstanceTypesNew(&ecs.DescribeInstanceTypesArgs{
				InstanceTypeFamily: insType.InstanceTypeFamily,
			})
			metric.OpenAPILatency.WithLabelValues("DescribeInstanceTypesNew", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

			if err != nil {
				logrus.Warnf("error get instance types info: %v， retry...", err)
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

	return eniCap, errors.Wrapf(err, "error get instance max eni: %v", instanceId)
}

func (e ecsImpl) GetInstanceMaxPrivateIP(instanceId string) (int, error) {
	maxEni, err := e.GetInstanceMaxENI(instanceId)
	if err != nil {
		return 0, errors.Wrapf(err, "error get instance max eni: %v", instanceId)
	}
	maxIP, err := e.GetENIMaxIP(instanceId, "")
	if err != nil {
		return 0, errors.Wrapf(err, "error get eni max ip: %v", instanceId)
	}
	maxIPForInstance := (maxEni - 1) * maxIP
	if maxIPForInstance <= 0 {
		return 0, errors.Errorf("instance not support multi ip address: %v ", instanceId)
	}
	return maxIPForInstance, nil
}

func (e ecsImpl) GetENIMaxIP(instanceId string, eniId string) (int, error) {
	// fixme: the eniid must bind on specified instanceId
	eniIPCap := 0
	err := wait.ExponentialBackoff(
		wait.Backoff{
			Duration: time.Second,
			Factor:   2,
			Jitter:   0,
			Steps:    5,
		}, func() (done bool, err error) {
			start := time.Now()
			insType, err := e.clientSet.ecs.DescribeInstanceAttribute(instanceId)
			metric.OpenAPILatency.WithLabelValues("DescribeInstanceAttribute", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
			if err != nil {
				return false, nil
			}

			start = time.Now()
			instanceTypeItems, err := e.clientSet.ecs.DescribeInstanceTypesNew(&ecs.DescribeInstanceTypesArgs{
				InstanceTypeFamily: insType.InstanceTypeFamily,
			})
			metric.OpenAPILatency.WithLabelValues("DescribeInstanceTypesNew", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))

			if err != nil {
				logrus.Warnf("error get instance info: %v， retry...", err)
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

	return eniIPCap, errors.Wrapf(err, "error get instance max eni ip: %v", instanceId)
}

func (e ecsImpl) GetENIById(instanceId, eniId string) (*types.ENI, error) {
	eni, err := e.eniInfoGetter.GetENIConfigById(eniId)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni config by mac")
	}
	eni.MaxIPs, err = e.GetENIMaxIP(instanceId, eni.ID)
	if err != nil {
		logrus.Warnf("error get eni max ips", err)
		return nil, errors.Wrapf(err, "error get eni max ip")
	}
	return eni, nil
}

func (e ecsImpl) GetENIByMac(instanceId, mac string) (*types.ENI, error) {
	eni, err := e.eniInfoGetter.GetENIConfigByMac(mac)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni config by mac")
	}
	eni.MaxIPs, err = e.GetENIMaxIP(instanceId, eni.ID)
	if err != nil {
		logrus.Warnf("error get eni max ips", err)
		return nil, errors.Wrapf(err, "error get eni max ip")
	}
	return eni, nil
}
