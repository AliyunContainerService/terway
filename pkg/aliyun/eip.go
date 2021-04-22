package aliyun

import (
	"fmt"
	"net"
	"strconv"
	"time"

	terwayErr "github.com/AliyunContainerService/terway/pkg/aliyun/errors"
	"github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
)

func (e *ecsImpl) AllocateEipAddress(bandwidth int, chargeType types.InternetChargeType, eipID, eniID string, eniIP net.IP, allowRob bool) (*types.EIP, error) {
	var (
		eipInfo *types.EIP
		err     error
	)
	var eni *ecs.NetworkInterfaceSet
	eni, err = e.WaitForNetworkInterface(eniID, "", eniOpBackoff)
	if err != nil {
		return nil, err
	}
	eipBindedPrivateIP := eniIP.String()
	// get privateIP form eip , if privateIP is eni's primary ip , it's empty
	if eni.PrivateIpAddress == eniIP.String() {
		eipBindedPrivateIP = ""
	}

	if eipID == "" {
		var eips []vpc.EipAddress
		eips, err = e.describeEipAddresses("", eniID)
		if err != nil {
			return nil, err
		}

		// 1. check if eni have bind eip already,if true, use it directly
		if len(eips) > 0 {
			for _, eip := range eips {
				if eip.PrivateIpAddress == eipBindedPrivateIP {
					publicIP, err := ip.ToIP(eip.IpAddress)
					if err != nil {
						return nil, fmt.Errorf("eip %s, %w", eip.AllocationId, err)
					}
					log.WithFields(map[string]interface{}{
						LogFieldEIPID:     eip.AllocationId,
						LogFieldENIID:     eniID,
						LogFieldPrivateIP: eniIP,
					}).Infof("resuse binded eip")
					return &types.EIP{
						ID:             eip.AllocationId,
						Address:        publicIP,
						AssociateENI:   eniID,
						AssociateENIIP: eniIP,
					}, nil
				}
			}
		}
		// 2. create eip and bind to eni
		var resp *vpc.AllocateEipAddressResponse
		resp, err = e.allocateEIPAddress(strconv.Itoa(bandwidth), string(chargeType))
		if err != nil {
			return nil, err
		}
		eipAddress := net.ParseIP(resp.EipAddress)
		if eipAddress == nil {
			return nil, fmt.Errorf("invalid eip address %s, %s", resp.AllocationId, resp.EipAddress)
		}
		eipInfo = &types.EIP{
			ID:             resp.AllocationId,
			Address:        eipAddress,
			Delete:         true,
			AssociateENI:   eniID,
			AssociateENIIP: eniIP,
		}

		defer func() {
			if err != nil {
				err = e.ReleaseEipAddress(eipInfo.ID, eniID, eniIP)
				if err != nil {
					log.Errorf("error rollback eip: %+v, %+v, may cause eip leak...", resp.AllocationId, resp.EipAddress)
				}
			}
		}()
	} else {
		var eips []vpc.EipAddress
		eips, err = e.describeEipAddresses(eipID, "")
		if err != nil {
			return nil, err
		}
		if len(eips) == 0 {
			return nil, fmt.Errorf("can not found eip %s", eipID)
		}
		// 1. check this eip is bind as expected
		eip := eips[0]
		var eipAddress net.IP
		eipAddress, err = ip.ToIP(eip.IpAddress)
		if err != nil {
			return nil, fmt.Errorf("eip %s, %w", eip.AllocationId, err)
		}

		eipInfo = &types.EIP{
			ID:             eipID,
			Address:        eipAddress,
			AssociateENI:   eniID,
			AssociateENIIP: eniIP,
		}

		//  check eni is already bind the same eip
		if eip.InstanceId == eniID &&
			eip.PrivateIpAddress == eipBindedPrivateIP {
			return eipInfo, nil
		}

		if allowRob && eip.Status == eipStatusInUse {
			err = e.unassociateEIPAddress(eipID, "", "")
			if err != nil {
				return nil, fmt.Errorf("error unassocicate previous eip address, %v", err)
			}
			time.Sleep(3 * time.Second)

			start := time.Now()
			_, err = e.WaitForEIP(eipID, eipStatusAvailable, eniStateBackoff)
			metric.OpenAPILatency.WithLabelValues("UnassociateEipAddress/Async", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
			if err != nil {
				return nil, fmt.Errorf("error wait for eip to status Available: %v", err)
			}
		} else if eip.Status != eipStatusAvailable {
			err = fmt.Errorf("eip id: %v status is not Available", eipID)
			return nil, err
		}
	}
	logrus.Debugf("get eip info: %+v", eipInfo)

	// bind eip to eni/secondary address
	err = e.associateEIPAddress(eipInfo.ID, eniID, eniIP.String())
	if err != nil {
		err = fmt.Errorf("error associate eip:%v to eni:%v.%v, err: %v", eipInfo, eniID, eniIP, err)
		return nil, err
	}
	time.Sleep(3 * time.Second)

	start := time.Now()
	_, err = e.WaitForEIP(eipInfo.ID, "InUse", eniStateBackoff)
	metric.OpenAPILatency.WithLabelValues("AssociateEipAddress/Async", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, fmt.Errorf("wait for eip error: %v", err)
	}
	return eipInfo, nil
}

// UnassociateEipAddress
// 1. if eni is deleted eip auto unassociated
// 2. if eip is deleted , return code is InvalidAllocationId.NotFound
// 3. if eip is not bind ,return code is IncorrectEipStatus
func (e *ecsImpl) UnassociateEipAddress(eipID, eniID, eniIP string) error {
	var innerErr error
	err := wait.ExponentialBackoff(eniOpBackoff,
		func() (done bool, err error) {
			// we check eip binding is not changed
			var eips []vpc.EipAddress
			eips, innerErr = e.describeEipAddresses(eipID, "")
			if innerErr != nil {
				return false, nil
			}
			if len(eips) == 0 {
				return true, nil
			}
			eip := eips[0]
			// eip is bind to other ins
			if eip.InstanceId != eniID {
				return true, nil
			}
			// eip bind to eni primary address, the eip.PrivateIpAddress is empty
			if eip.PrivateIpAddress != "" {
				if eniIP != eip.PrivateIpAddress {
					return true, nil
				}
			}

			innerErr = e.unassociateEIPAddress(eipID, eniID, eniIP)
			if innerErr != nil {
				return false, nil
			}
			return true, nil
		},
	)
	if err != nil {
		return fmt.Errorf("error unassociate eip address: %v, %v, %v,%v,%w", eipID, eniID, eniIP, innerErr, err)
	}
	return nil
}

func (e *ecsImpl) ReleaseEipAddress(eipID, eniID string, eniIP net.IP) error {
	eip, err := e.WaitForEIP(eipID, "", eniStateBackoff)
	if err != nil {
		return fmt.Errorf("error release eip: %w", err)
	}
	// eip already released
	if eip == nil {
		return nil
	}
	logrus.Infof("got eip info to release: %+v", eip)
	if eip.Status != eipStatusAvailable {
		// detach eip from specify eni
		err = e.UnassociateEipAddress(eipID, eniID, eniIP.String())
		if err == nil {
			time.Sleep(3 * time.Second)
			start := time.Now()
			eip, err = e.WaitForEIP(eipID, eipStatusAvailable, eniStateBackoff)
			metric.OpenAPILatency.WithLabelValues("UnassociateEipAddress/Async", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
			if err != nil {
				logrus.Errorf("wait timeout UnassociateEipAddress for eni: %v, %v, %v", eniID, eniIP, err)
			}
		} else {
			logrus.Errorf("error UnassociateEipAddress for eni: %v, %v, %v", eniID, eniIP, err)
		}
	}
	var innerErr error
	if eip.Status == eipStatusAvailable {
		err = wait.ExponentialBackoff(eniReleaseBackoff, func() (done bool, err error) {
			innerErr = e.releaseEIPAddress(eip.AllocationId)
			if innerErr != nil {
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			return fmt.Errorf("%w, %s", err, innerErr)
		}
	}
	return err
}

// WaitForEIP wait status of eni, ignore status if is empty
func (e *ecsImpl) WaitForEIP(eipID string, status string, backoff wait.Backoff) (*vpc.EipAddress, error) {
	var eip *vpc.EipAddress
	var innerErr error
	err := wait.ExponentialBackoff(backoff,
		func() (done bool, err error) {
			var eips []vpc.EipAddress
			eips, innerErr = e.describeEipAddresses(eipID, "")
			if innerErr != nil {
				return false, nil
			}
			if len(eips) == 0 {
				return false, terwayErr.ErrNotFound
			}
			eip = &eips[0]
			if status != "" {
				if eip.Status != status {
					return false, nil
				}
			}
			return true, nil
		},
	)
	if err != nil {
		err = fmt.Errorf("failed to wait eip %s status %s error: %v %w", eipID, status, innerErr, err)
	}

	return eip, err
}

func (e *ecsImpl) describeEipAddresses(eipID, eniID string) ([]vpc.EipAddress, error) {
	req := vpc.CreateDescribeEipAddressesRequest()
	req.AllocationId = eipID
	if eniID != "" {
		req.AssociatedInstanceType = string(types.EIPInstanceTypeNetworkInterface)
		req.AssociatedInstanceId = eniID
	}
	req.PageSize = requests.NewInteger(100)

	l := log.WithFields(map[string]interface{}{LogFieldAPI: "DescribeEipAddresses", LogFieldEIPID: eipID, LogFieldENIID: eniID})
	start := time.Now()
	resp, err := e.clientSet.VPC().DescribeEipAddresses(req)
	metric.OpenAPILatency.WithLabelValues("DescribeEipAddresses", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithFields(map[string]interface{}{LogFieldRequestID: terwayErr.ErrRequestID(err)}).Warn(err)
		return nil, err
	}
	l.WithFields(map[string]interface{}{LogFieldRequestID: resp.RequestId}).Debugf("get eip len %d", len(resp.EipAddresses.EipAddress))
	return resp.EipAddresses.EipAddress, nil
}

func (e *ecsImpl) allocateEIPAddress(bandwidth, chargeType string) (*vpc.AllocateEipAddressResponse, error) {
	req := vpc.CreateAllocateEipAddressRequest()
	req.Bandwidth = bandwidth
	req.InternetChargeType = chargeType

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI: "AllocateEipAddress",
	})
	start := time.Now()
	resp, err := e.clientSet.VPC().AllocateEipAddress(req)
	metric.OpenAPILatency.WithLabelValues("AllocateEipAddress", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithFields(map[string]interface{}{
			LogFieldRequestID: terwayErr.ErrRequestID(err)}).Errorf("alloc EIP faild, %s", err.Error())
		return nil, fmt.Errorf("error create EIP, bandwidth %s, %w", bandwidth, err)
	}
	l.WithFields(map[string]interface{}{
		LogFieldEIPID:     resp.AllocationId,
		LogFieldRequestID: resp.RequestId}).Infof("alloc EIP %s", resp.EipAddress)
	return resp, nil
}

func (e *ecsImpl) unassociateEIPAddress(eipID, eniID, eniIP string) error {
	req := vpc.CreateUnassociateEipAddressRequest()
	req.AllocationId = eipID
	req.InstanceId = eniID
	req.PrivateIpAddress = eniIP
	req.InstanceType = string(types.EIPInstanceTypeNetworkInterface)

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "UnassociateEipAddress",
		LogFieldEIPID: eipID,
	})
	start := time.Now()
	resp, err := e.clientSet.VPC().UnassociateEipAddress(req)
	metric.OpenAPILatency.WithLabelValues("UnassociateEipAddress", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithFields(map[string]interface{}{
			LogFieldRequestID: terwayErr.ErrRequestID(err)}).Warnf("unassociate EIP failed, %s", err.Error())
		if terwayErr.ErrAssert(terwayErr.ErrInvalidAllocationIDNotFound, err) ||
			terwayErr.ErrAssert(terwayErr.ErrIncorrectEIPStatus, err) {
			return nil
		}
		return fmt.Errorf("error unassociate EIP %s, %w", eipID, err)
	}
	l.WithFields(map[string]interface{}{
		LogFieldRequestID: resp.RequestId}).Info("unassociate EIP")
	return nil
}

func (e *ecsImpl) associateEIPAddress(eipID, eniID, privateIP string) error {
	req := vpc.CreateAssociateEipAddressRequest()
	req.AllocationId = eipID
	req.InstanceId = eniID
	req.PrivateIpAddress = privateIP
	req.InstanceType = string(types.EIPInstanceTypeNetworkInterface)

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "AssociateEipAddress",
		LogFieldEIPID: eipID,
		LogFieldENIID: eniID,
	})
	start := time.Now()
	resp, err := e.clientSet.VPC().AssociateEipAddress(req)
	metric.OpenAPILatency.WithLabelValues("AssociateEipAddress", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithFields(map[string]interface{}{
			LogFieldRequestID: terwayErr.ErrRequestID(err)}).Warnf("associate EIP to %s failed, %s", privateIP, err.Error())

		return fmt.Errorf("error associate EIP %s, %w", eipID, err)
	}
	l.WithFields(map[string]interface{}{
		LogFieldRequestID: resp.RequestId}).Infof("associate EIP to %s", privateIP)
	return nil
}

func (e *ecsImpl) releaseEIPAddress(eipID string) error {
	req := vpc.CreateReleaseEipAddressRequest()
	req.AllocationId = eipID

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:   "ReleaseEipAddress",
		LogFieldEIPID: eipID,
	})

	start := time.Now()
	resp, err := e.clientSet.VPC().ReleaseEipAddress(req)
	metric.OpenAPILatency.WithLabelValues("ReleaseEipAddress", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithFields(map[string]interface{}{
			LogFieldRequestID: terwayErr.ErrRequestID(err)}).Warnf("release EIP failed, %s", err.Error())
		return fmt.Errorf("error release EIP %s, %w", eipID, err)
	}
	l.WithFields(map[string]interface{}{
		LogFieldRequestID: resp.RequestId}).Info("release EIP")
	return nil
}
