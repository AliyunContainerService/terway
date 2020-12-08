package aliyun

import (
	"fmt"
	"net"
	"time"

	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/types"
	"github.com/denverdino/aliyungo/common"
	"github.com/denverdino/aliyungo/ecs"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
)

func (e *ecsImpl) AllocateEipAddress(bandwidth int, chargeType common.InternetChargeType, eipID, eniID string, eniIP net.IP, allowRob bool) (*types.EIP, error) {
	var (
		eipInfo *types.EIP
		err     error
	)
	// get eip info
	if eipID == "" {
		start := time.Now()
		var eipAddressStr, allocationID string
		eipAddressStr, allocationID, err = e.clientSet.Vpc().AllocateEipAddress(
			&ecs.AllocateEipAddressArgs{RegionId: e.region, Bandwidth: bandwidth, InternetChargeType: chargeType},
		)
		metric.OpenAPILatency.WithLabelValues("AllocateEipAddress", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
		logrus.Debugf("AllocateEipAddress: %v, %v, %v", eipAddressStr, allocationID, err)
		if err != nil {
			return nil, errors.Wrapf(err, "error create eip address, bandwidth %v", bandwidth)
		}
		defer func() {
			if err != nil {
				err = e.ReleaseEipAddress(allocationID, eniID, eniIP)
				if err != nil {
					logrus.Errorf("error rollback eip: %+v, %+v, may cause eip leak...", allocationID, eipAddressStr)
				}
			}
		}()
		eipAddress := net.ParseIP(eipAddressStr)
		if eipAddress == nil {
			err = errors.Errorf("invalid eip address: %v, %v", allocationID, eipAddressStr)
			return nil, err
		}
		eipInfo = &types.EIP{
			ID:             allocationID,
			Address:        eipAddress,
			Delete:         true,
			AssociateENI:   eniID,
			AssociateENIIP: eniIP,
		}
	} else {
		start := time.Now()
		var eipList []ecs.EipAddressSetType
		eipList, _, err = e.clientSet.Vpc().DescribeEipAddresses(&ecs.DescribeEipAddressesArgs{
			RegionId:     e.region,
			AllocationId: eipID,
		})
		metric.OpenAPILatency.WithLabelValues("DescribeEipAddresses", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
		if len(eipList) == 0 || err != nil || eipList[0].AllocationId != eipID {
			err = errors.Errorf("error describe eip: %v, %v, %v", eipID, eipList, err)
			return nil, err
		}
		if allowRob && eipList[0].Status == ecs.EipStatusInUse {

			// Unassociate By Id
			start := time.Now()
			err = e.clientSet.Vpc().NewUnassociateEipAddress(&ecs.UnallocateEipAddressArgs{
				AllocationId: eipID,
			})
			metric.OpenAPILatency.WithLabelValues("UnallocateEipAddress", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
			if err != nil {
				return nil, errors.Errorf("error unassocicate previous eip address, %v", err)
			}

			start = time.Now()
			_, err = e.WaitForEip(eipID, ecs.EipStatusAvailable, eniStateBackoff)
			metric.OpenAPILatency.WithLabelValues("UnAssociateEipAddress/Async", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
			if err != nil {
				logrus.Errorf("wait for eip error: %v", err)
				return nil, errors.Errorf("wait for eip error: %v", err)
			}
		} else if eipList[0].Status != ecs.EipStatusAvailable {
			err = errors.Errorf("eip id: %v status is not Available", eipID)
			return nil, err
		}
		eipAddress := net.ParseIP(eipList[0].IpAddress)
		if eipAddress == nil {
			err = errors.Errorf("invalid eip address: %v, %v", eipID, eipList[0].IpAddress)
			return nil, err
		}
		eipInfo = &types.EIP{
			ID:             eipID,
			Address:        eipAddress,
			AssociateENI:   eniID,
			AssociateENIIP: eniIP,
		}
	}
	logrus.Debugf("get eip info: %+v", eipInfo)
	// bind eip to eni/secondary address
	start := time.Now()
	err = e.clientSet.Vpc().NewAssociateEipAddress(&ecs.AssociateEipAddressArgs{
		AllocationId:     eipInfo.ID,
		InstanceId:       eniID,
		PrivateIpAddress: eniIP.String(),
		InstanceType:     ecs.NetworkInterface,
	})
	metric.OpenAPILatency.WithLabelValues("AssociateEipAddress", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = errors.Errorf("error associate eip:%v to eni:%v.%v, err: %v", eipInfo, eniID, eniIP, err)
		logrus.Errorf("associate error: %v", err)
		return nil, err
	}
	start = time.Now()
	_, err = e.WaitForEip(eipInfo.ID, ecs.EipStatusInUse, eniStateBackoff)
	metric.OpenAPILatency.WithLabelValues("AssociateEipAddress/Async", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		logrus.Errorf("wait for eip error: %v", err)
		return nil, errors.Errorf("wait for eip error: %v", err)
	}
	return eipInfo, nil
}

func (e *ecsImpl) UnassociateEipAddress(eipID, eniID, eniIP string) error {
	logrus.Infof("UnassociateEipAddress: %v, %v, %v", eipID, eniID, eniIP)
	var innerErr error
	err := wait.ExponentialBackoff(eniOpBackoff,
		func() (done bool, err error) {
			var eniResponse *ecs.DescribeNetworkInterfacesResponse
			eniResponse, innerErr = e.clientSet.Ecs().DescribeNetworkInterfaces(&ecs.DescribeNetworkInterfacesArgs{
				RegionId:           e.region,
				NetworkInterfaceId: []string{eniID},
			})
			if innerErr != nil {
				logrus.Errorf("error describe network interface: %v", err)
			}
			if len(eniResponse.NetworkInterfaceSets.NetworkInterfaceSet) == 0 {
				// eni already released
				return true, nil
			}
			logrus.Debugf("describe network interface on un associate eip: %+v", eniResponse.NetworkInterfaceSets.NetworkInterfaceSet[0])

			describeEipAddressesArgs := ecs.DescribeEipAddressesArgs{
				AllocationId: eipID,
				RegionId:     e.region,
			}
			start := time.Now()
			var eipResponse []ecs.EipAddressSetType
			eipResponse, _, innerErr = e.clientSet.Vpc().DescribeEipAddresses(&describeEipAddressesArgs)
			metric.OpenAPILatency.WithLabelValues("DescribeEipAddresses", fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))
			logrus.Debugf("unassociate eip status result: %+v, %v", eipResponse, innerErr)
			if innerErr != nil {
				logrus.Warnf("Failed to get eip info: %v", innerErr)
				return false, nil
			} else if len(eipResponse) == 0 {
				logrus.Debugf("eip already deleted")
				// eip already deleted
				return true, nil
			} else {
				if eipResponse[0].PrivateIpAddress == "" {
					logrus.Debugf("%v Bind private ip is ENI %v primary ip, set private ip to ENI primary ip %v",
						eipID, eniID, eniResponse.NetworkInterfaceSets.NetworkInterfaceSet[0].PrivateIpAddress)
					eipResponse[0].PrivateIpAddress = eniResponse.NetworkInterfaceSets.NetworkInterfaceSet[0].PrivateIpAddress
				}
				// eip allocated to other resources
				if eipResponse[0].InstanceType != ecs.NetworkInterface ||
					eipResponse[0].InstanceId != eniID ||
					eipResponse[0].PrivateIpAddress != eniIP {
					return true, nil
				}
			}

			start = time.Now()
			innerErr := e.clientSet.Vpc().NewUnassociateEipAddress(&ecs.UnallocateEipAddressArgs{
				AllocationId:     eipID,
				InstanceId:       eniID,
				InstanceType:     ecs.NetworkInterface,
				PrivateIpAddress: eniIP,
			})
			metric.OpenAPILatency.WithLabelValues("UnassociateEipAddress", fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))
			if innerErr != nil {
				logrus.Warnf("Failed to unassociate eip address: %v", err)
				return false, nil
			}
			return true, nil
		},
	)
	if err != nil {
		return errors.Errorf("error unassociate eip address: %v, %v, %v, innerErr: %v", eipID, eniID, eniIP, innerErr)
	}
	return nil
}

func (e *ecsImpl) ReleaseEipAddress(eipID, eniID string, eniIP net.IP) error {
	var (
		innerErr error
		eipInfo  *ecs.EipAddressSetType
	)
	err := wait.ExponentialBackoff(eniStateBackoff, func() (done bool, err error) {
		describeEipAddressesArgs := ecs.DescribeEipAddressesArgs{
			AllocationId: eipID,
			RegionId:     e.region,
		}
		var eipResponse []ecs.EipAddressSetType
		eipResponse, _, innerErr = e.clientSet.Ecs().DescribeEipAddresses(&describeEipAddressesArgs)
		if innerErr == nil {
			if len(eipResponse) != 0 {
				eipInfo = &eipResponse[0]
			}
			return true, nil
		}
		logrus.Errorf("error describe eip address: %v", innerErr)
		return false, nil
	})
	if err != nil {
		return errors.Errorf("error release eip: %v, %v", err, innerErr)
	}
	// eip already released
	if eipInfo == nil {
		return nil
	}
	logrus.Infof("got eip info to release: %+v", eipInfo)
	if eipInfo.Status != ecs.EipStatusAvailable {
		// detach eip from specify eni
		err = e.UnassociateEipAddress(eipID, eniID, eniIP.String())
		if err == nil {
			start := time.Now()
			eipInfo, err = e.WaitForEip(eipID, ecs.EipStatusAvailable, eniStateBackoff)
			metric.OpenAPILatency.WithLabelValues("UnassociateEipAddress/Async", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
			if err != nil {
				logrus.Errorf("wait timeout UnassociateEipAddress for eni: %v, %v, %v", eniID, eniIP, err)
			}
		} else {
			logrus.Errorf("error UnassociateEipAddress for eni: %v, %v, %v", eniID, eniIP, err)
		}
	}
	if eipInfo.Status == ecs.EipStatusAvailable {
		err = wait.ExponentialBackoff(eniReleaseBackoff, func() (done bool, err error) {
			start := time.Now()
			innerErr = e.clientSet.Ecs().ReleaseEipAddress(eipID)
			metric.OpenAPILatency.WithLabelValues("ReleaseEipAddress", fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))
			if innerErr != nil {
				logrus.Errorf("error Release eip: %v, %v", eipID, innerErr)
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			logrus.Errorf("error release eip: %v, %v, %v", eipID, err, innerErr)
			return errors.Errorf("error release eip: %v, %v, %v", eipID, err, innerErr)
		}
	}
	return nil
}

// WaitForNetworkInterface wait status of eni
func (e *ecsImpl) WaitForEip(eipID string, status ecs.EipStatus, backoff wait.Backoff) (*ecs.EipAddressSetType, error) {
	var eipInfo *ecs.EipAddressSetType
	var innerErr error
	err := wait.ExponentialBackoff(backoff,
		func() (done bool, err error) {
			describeEipAddressesArgs := ecs.DescribeEipAddressesArgs{
				AllocationId: eipID,
				RegionId:     e.region,
			}
			eipResponse, _, innerErr := e.clientSet.Ecs().DescribeEipAddresses(&describeEipAddressesArgs)
			if innerErr != nil {
				logrus.Warnf("Failed to describe eip %v: %v", eipID, err)
				return false, nil
			}
			if len(eipResponse) > 0 && eipResponse[0].Status == status {
				eipInfo = &eipResponse[0]
				return true, nil
			}
			return false, nil
		},
	)
	if err != nil {
		err = fmt.Errorf("describe eip error: %v, innerErr: %v", err, innerErr)
	}

	return eipInfo, err
}
