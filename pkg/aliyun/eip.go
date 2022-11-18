package aliyun

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	"github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/types"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
)

func (e *Impl) AllocateEipAddress(ctx context.Context, bandwidth int, chargeType types.InternetChargeType, eipID, eniID string, eniIP net.IP, allowRob bool, isp, bandwidthPackageID, eipPoolID string) (*types.EIP, error) {
	var (
		eipInfo *types.EIP
		err     error
	)
	var eni *client.NetworkInterface
	eni, err = e.WaitForNetworkInterface(ctx, eniID, "", backoff.Backoff(backoff.ENIOps), false)
	if err != nil {
		return nil, err
	}
	eipBindedPrivateIP := eniIP.String()
	// get privateIP form eip , if privateIP is eni's primary ip , it's empty
	if eni.PrivateIPAddress == eniIP.String() {
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
						client.LogFieldEIPID:     eip.AllocationId,
						client.LogFieldENIID:     eniID,
						client.LogFieldPrivateIP: eniIP,
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
		resp, err = e.AllocateEIPAddress(strconv.Itoa(bandwidth), string(chargeType), isp)
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
				err = e.ReleaseEipAddress(ctx, eipInfo.ID, eniID, eniIP)
				if err != nil {
					log.Errorf("error rollback eip: %+v, %+v, may cause eip leak...", resp.AllocationId, resp.EipAddress)
				}
			}
		}()

		// add eip to bandwidth package
		if bandwidthPackageID != "" {
			err = e.AddCommonBandwidthPackageIP(eipInfo.ID, bandwidthPackageID)
			if err != nil {
				return nil, err
			}
		}

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
			err = e.UnAssociateEIPAddress(eipID, "", "")
			if err != nil {
				return nil, fmt.Errorf("error unassocicate previous eip address, %v", err)
			}
			time.Sleep(3 * time.Second)

			start := time.Now()
			_, err = e.WaitForEIP(eipID, eipStatusAvailable, backoff.Backoff(backoff.WaitENIStatus))
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
	err = e.AssociateEIPAddress(eipInfo.ID, eniID, eniIP.String())
	if err != nil {
		err = fmt.Errorf("error associate eip:%v to eni:%v.%v, err: %v", eipInfo, eniID, eniIP, err)
		return nil, err
	}
	time.Sleep(3 * time.Second)

	start := time.Now()
	_, err = e.WaitForEIP(eipInfo.ID, "InUse", backoff.Backoff(backoff.WaitENIStatus))
	metric.OpenAPILatency.WithLabelValues("AssociateEipAddress/Async", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, fmt.Errorf("wait for eip error: %v", err)
	}
	return eipInfo, nil
}

// UnassociateEipAddress un associate eip
// 1. if eni is deleted eip auto unassociated
// 2. if eip is deleted , return code is InvalidAllocationId.NotFound
// 3. if eip is not bind ,return code is IncorrectEipStatus
func (e *Impl) UnassociateEipAddress(ctx context.Context, eipID, eniID, eniIP string) error {
	var innerErr error
	err := wait.ExponentialBackoff(backoff.Backoff(backoff.ENIOps),
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
			// eip bind to eni primary address, the eip.PrivateIPAddress is empty
			if eip.PrivateIpAddress != "" {
				if eniIP != eip.PrivateIpAddress {
					return true, nil
				}
			}

			innerErr = e.UnAssociateEIPAddress(eipID, eniID, eniIP)
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

func (e *Impl) ReleaseEipAddress(ctx context.Context, eipID, eniID string, eniIP net.IP) error {
	eip, err := e.WaitForEIP(eipID, "", backoff.Backoff(backoff.WaitENIStatus))
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
		err = e.UnassociateEipAddress(ctx, eipID, eniID, eniIP.String())
		if err == nil {
			time.Sleep(3 * time.Second)
			start := time.Now()
			eip, err = e.WaitForEIP(eipID, eipStatusAvailable, backoff.Backoff(backoff.WaitENIStatus))
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
		if eip.BandwidthPackageId != "" {
			err = e.RemoveCommonBandwidthPackageIP(eip.AllocationId, eip.BandwidthPackageId)
			if err != nil {
				if !apiErr.ErrAssert(apiErr.ErrIPNotInCbwp, err) {
					return err
				}
			}
		}
		err = wait.ExponentialBackoff(backoff.Backoff(backoff.ENIRelease), func() (done bool, err error) {
			innerErr = e.ReleaseEIPAddress(eip.AllocationId)
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
func (e *Impl) WaitForEIP(eipID string, status string, backoff wait.Backoff) (*vpc.EipAddress, error) {
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
				return false, apiErr.ErrNotFound
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

func (e *Impl) describeEipAddresses(eipID, eniID string) ([]vpc.EipAddress, error) {
	req := vpc.CreateDescribeEipAddressesRequest()
	req.AllocationId = eipID
	if eniID != "" {
		req.AssociatedInstanceType = client.EIPInstanceTypeNetworkInterface
		req.AssociatedInstanceId = eniID
	}
	req.PageSize = requests.NewInteger(100)

	l := log.WithFields(map[string]interface{}{client.LogFieldAPI: "DescribeEipAddresses", client.LogFieldEIPID: eipID, client.LogFieldENIID: eniID})
	start := time.Now()
	resp, err := e.ClientSet.VPC().DescribeEipAddresses(req)
	metric.OpenAPILatency.WithLabelValues("DescribeEipAddresses", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithFields(map[string]interface{}{client.LogFieldRequestID: apiErr.ErrRequestID(err)}).Warn(err)
		return nil, err
	}
	l.WithFields(map[string]interface{}{client.LogFieldRequestID: resp.RequestId}).Debugf("get eip len %d", len(resp.EipAddresses.EipAddress))
	return resp.EipAddresses.EipAddress, nil
}

const (
	eipStatusInUse     = "InUse"
	eipStatusAvailable = "Available"
)
