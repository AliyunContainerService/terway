package aliyun

import (
	"context"
	"net/netip"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/aliyun/eni"
	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	"github.com/AliyunContainerService/terway/pkg/factory"
	vswpool "github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

const (
	metadataPollInterval = time.Second * 1
	metadataWaitTimeout  = time.Second * 10
)

var _ factory.Factory = &Aliyun{}

// Aliyun the local eni factory impl for aliyun.
type Aliyun struct {
	ctx context.Context

	enableIPv4, enableIPv6 bool

	instanceID string
	zoneID     string

	openAPI interface {
		client.ECS
		client.VPC
	}
	getter eni.ENIInfoGetter

	vsw             *vswpool.SwitchPool
	selectionPolicy vswpool.SelectionPolicy

	vSwitchOptions   []string
	securityGroupIDs []string
	resourceGroupID  string

	eniTags map[string]string

	eniTypeAttr  types.Feat
	eniTagFilter map[string]string
}

func NewAliyun(ctx context.Context, openAPI *client.OpenAPI, getter eni.ENIInfoGetter, vsw *vswpool.SwitchPool, cfg *types.ENIConfig) *Aliyun {

	return &Aliyun{
		ctx:              ctx,
		openAPI:          openAPI,
		getter:           getter,
		vsw:              vsw,
		enableIPv4:       cfg.EnableIPv4,
		enableIPv6:       cfg.EnableIPv6,
		instanceID:       cfg.InstanceID,
		zoneID:           cfg.ZoneID,
		vSwitchOptions:   cfg.VSwitchOptions,
		securityGroupIDs: cfg.SecurityGroupIDs,
		resourceGroupID:  cfg.ResourceGroupID,
		eniTags:          cfg.ENITags,
		eniTypeAttr:      cfg.EniTypeAttr,
		selectionPolicy:  cfg.VSwitchSelectionPolicy,
	}
}

func (a *Aliyun) CreateNetworkInterface(ipv4, ipv6 int, eniType string) (*daemon.ENI, []netip.Addr, []netip.Addr, error) {
	ctx, cancel := context.WithTimeout(a.ctx, time.Second*60)
	defer cancel()

	// 1. create eni
	var eni *client.NetworkInterface
	var vswID string
	var (
		trunk bool
		erdma bool
	)
	if strings.ToLower(eniType) == "trunk" {
		trunk = true
	}
	if strings.ToLower(eniType) == "erdma" {
		erdma = true
	}
	err := wait.ExponentialBackoffWithContext(a.ctx, backoff.Backoff(backoff.ENICreate), func(ctx context.Context) (bool, error) {
		vsw, innerErr := a.vsw.GetOne(ctx, a.openAPI, a.zoneID, a.vSwitchOptions, &vswpool.SelectOptions{
			VSwitchSelectPolicy: a.selectionPolicy,
		})
		if innerErr != nil {
			return false, innerErr
		}
		vswID = vsw.ID

		bo := backoff.Backoff(backoff.ENICreate)
		option := &client.CreateNetworkInterfaceOptions{
			NetworkInterfaceOptions: &client.NetworkInterfaceOptions{
				Trunk:            trunk,
				ERDMA:            erdma,
				SecurityGroupIDs: a.securityGroupIDs,
				IPv6Count:        ipv6,
				IPCount:          ipv4,
				VSwitchID:        vswID,
				InstanceID:       a.instanceID,
				Tags:             a.eniTags,
				ResourceGroupID:  a.resourceGroupID,
			},
			Backoff: &bo,
		}

		eni, innerErr = a.openAPI.CreateNetworkInterface(ctx, option)
		if innerErr != nil {
			if apiErr.ErrorCodeIs(innerErr, apiErr.InvalidVSwitchIDIPNotEnough) {
				a.vsw.Block(vswID)
				return false, nil
			}
			return true, innerErr
		}
		return true, nil
	})

	if err != nil {
		return nil, nil, nil, err
	}

	r := &daemon.ENI{
		ID:        eni.NetworkInterfaceID,
		MAC:       eni.MacAddress,
		VSwitchID: eni.VSwitchID,
		Trunk:     trunk,
		ERdma:     erdma,
	}

	r.PrimaryIP.SetIP(eni.PrivateIPAddress)

	v4Set, err := func() ([]netip.Addr, error) {
		var ips []netip.Addr
		for _, v := range eni.PrivateIPSets {
			addr, err := netip.ParseAddr(v.PrivateIpAddress)
			if err != nil {
				return nil, err
			}
			ips = append(ips, addr)
		}
		return ips, nil
	}()
	if err != nil {
		return r, nil, nil, err
	}

	v6Set, err := func() ([]netip.Addr, error) {
		var ips []netip.Addr
		for _, v := range eni.IPv6Set {
			addr, err := netip.ParseAddr(v.Ipv6Address)
			if err != nil {
				return nil, err
			}
			ips = append(ips, addr)
		}
		return ips, nil
	}()
	if err != nil {
		return r, nil, nil, err
	}

	// 2. attach eni
	err = a.openAPI.AttachNetworkInterface(ctx, eni.NetworkInterfaceID, a.instanceID, "")
	if err != nil {
		return r, nil, nil, err
	}

	// 3. wait metadata ready & update cidr
	err = validateIPInMetadata(ctx, v4Set, func() []netip.Addr {
		exists, err := metadata.GetIPv4ByMac(r.MAC)
		if err != nil {
			klog.Errorf("metadata: error get eni private ip: %v", err)
		}
		return exists
	})
	if err != nil {
		return r, nil, nil, err
	}

	// wait mac
	err = wait.PollUntilContextTimeout(ctx, metadataPollInterval, metadataWaitTimeout, true, func(ctx context.Context) (bool, error) {
		macs, err := metadata.GetENIsMAC()
		if err != nil {
			klog.Errorf("metadata: error get mac: %v", err)
			return false, nil
		}
		return sets.NewString(macs...).Has(r.MAC), nil
	})
	if err != nil {
		return r, nil, nil, err
	}

	prefix, err := metadata.GetVSwitchCIDR(eni.MacAddress)
	if err != nil {
		return r, nil, nil, err
	}
	r.VSwitchCIDR.SetIPNet(prefix.String())

	gw, err := metadata.GetENIGatewayAddr(eni.MacAddress)
	if err != nil {
		return r, nil, nil, err
	}
	r.GatewayIP.SetIP(gw.String())

	if ipv6 > 0 {
		err = validateIPInMetadata(ctx, v6Set, func() []netip.Addr {
			exists, err := metadata.GetIPv6ByMac(r.MAC)
			if err != nil {
				klog.Errorf("metadata: error get eni private ip: %v", err)
			}
			return exists
		})
		if err != nil {
			return r, nil, nil, err
		}

		prefix, err = metadata.GetVSwitchIPv6CIDR(eni.MacAddress)
		if err != nil {
			return r, nil, nil, err
		}
		r.VSwitchCIDR.SetIPNet(prefix.String())

		gw, err = metadata.GetENIV6GatewayAddr(eni.MacAddress)
		if err != nil {
			return r, nil, nil, err
		}
		r.GatewayIP.SetIP(gw.String())
	}

	return r, v4Set, v6Set, nil
}

func (a *Aliyun) AssignNIPv4(eniID string, count int, mac string) ([]netip.Addr, error) {
	// 1. assign ip
	var ips []netip.Addr
	var err error

	bo := backoff.Backoff(backoff.ENIIPOps)
	option := &client.AssignPrivateIPAddressOptions{
		Backoff: &bo,
		NetworkInterfaceOptions: &client.NetworkInterfaceOptions{
			NetworkInterfaceID: eniID,
			IPCount:            count,
		}}

	ips, err = a.openAPI.AssignPrivateIPAddress(a.ctx, option)
	if err != nil {
		return nil, err
	}

	// 2. wait ip ready in metadata
	// TODO: support rollback single ip
	err = validateIPInMetadata(a.ctx, ips, func() []netip.Addr {
		exists, err := metadata.GetIPv4ByMac(mac)
		if err != nil {
			klog.Errorf("metadata: error get eni private ip: %v", err)
		}
		return exists
	})

	return ips, err
}

func (a *Aliyun) AssignNIPv6(eniID string, count int, mac string) ([]netip.Addr, error) {
	// 1. assign ip
	var ips []netip.Addr
	var err error

	bo := backoff.Backoff(backoff.ENIIPOps)
	option := &client.AssignIPv6AddressesOptions{
		Backoff: &bo,
		NetworkInterfaceOptions: &client.NetworkInterfaceOptions{
			NetworkInterfaceID: eniID,
			IPv6Count:          count,
		}}

	ips, err = a.openAPI.AssignIpv6Addresses(a.ctx, option)
	if err != nil {
		return nil, err
	}

	// 2. wait ip ready in metadata
	// TODO: support rollback single ip
	err = validateIPInMetadata(a.ctx, ips, func() []netip.Addr {
		exists, err := metadata.GetIPv6ByMac(mac)
		if err != nil {
			klog.Errorf("metadata: error get eni private ip: %v", err)
		}
		return exists
	})

	return ips, err
}

func (a *Aliyun) UnAssignNIPv4(eniID string, ips []netip.Addr, mac string) error {
	var err, innerErr error

	err = wait.ExponentialBackoffWithContext(a.ctx, backoff.Backoff(backoff.ENIIPOps), func(ctx context.Context) (bool, error) {
		innerErr = a.openAPI.UnAssignPrivateIPAddresses(ctx, eniID, ips)
		if innerErr != nil {
			if apiErr.ErrAssert(apiErr.ErrForbidden, innerErr) {
				return true, innerErr
			}
			return false, nil
		}
		return true, nil
	})

	if err != nil {
		if innerErr != nil {
			return innerErr
		}
		return err
	}

	err = validateIPNotInMetadata(a.ctx, ips, func() []netip.Addr {
		var exists []netip.Addr
		exists, innerErr = metadata.GetIPv4ByMac(mac)
		if innerErr != nil {
			klog.Errorf("metadata: error get eni private ip: %v", innerErr)
			return ips
		}
		return exists
	})

	return err
}

func (a *Aliyun) UnAssignNIPv6(eniID string, ips []netip.Addr, mac string) error {
	var err, innerErr error

	err = wait.ExponentialBackoffWithContext(a.ctx, backoff.Backoff(backoff.ENIIPOps), func(ctx context.Context) (bool, error) {
		innerErr = a.openAPI.UnAssignIpv6Addresses(ctx, eniID, ips)
		if innerErr != nil {
			if apiErr.ErrAssert(apiErr.ErrForbidden, innerErr) {
				return true, innerErr
			}
			return false, nil
		}
		return true, nil
	})

	if err != nil {
		if innerErr != nil {
			return innerErr
		}
		return err
	}

	err = validateIPNotInMetadata(a.ctx, ips, func() []netip.Addr {
		var exists []netip.Addr
		exists, innerErr = metadata.GetIPv6ByMac(mac)
		if innerErr != nil {
			klog.Errorf("metadata: error get eni private ip: %v", innerErr)
			return ips
		}
		return exists
	})

	return err
}

func (a *Aliyun) DeleteNetworkInterface(eniID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	err := a.openAPI.DetachNetworkInterface(ctx, eniID, a.instanceID, "")
	if err != nil {
		return err
	}
	time.Sleep(time.Second * 5)
	err = a.openAPI.DeleteNetworkInterface(ctx, eniID)
	return err
}

func (a *Aliyun) LoadNetworkInterface(mac string) (ipv4Set []netip.Addr, ipv6Set []netip.Addr, err error) {

	if a.enableIPv4 {
		ipv4Set, err = a.getter.GetENIPrivateAddressesByMACv2(mac)
		if err != nil {
			return
		}
	}

	if a.enableIPv6 {
		ipv6Set, err = a.getter.GetENIPrivateIPv6AddressesByMACv2(mac)
	}

	return
}

func (a *Aliyun) GetAttachedNetworkInterface(trunkENIID string) ([]*daemon.ENI, error) {
	enis, err := a.getter.GetENIs(false)
	if err != nil {
		return nil, err
	}

	if len(enis) == 0 {
		return nil, err
	}

	feat := a.eniTypeAttr

	var eniIDs []string
	idMap := map[string]*daemon.ENI{}
	for _, eni := range enis {
		eniIDs = append(eniIDs, eni.ID)
		idMap[eni.ID] = eni

		if trunkENIID == eni.ID {
			eni.Trunk = true

			types.DisableFeature(&feat, types.FeatTrunk)
		}
	}

	var result []*daemon.ENI

	if feat > 0 || len(a.eniTags) > 0 {
		var innerErr error
		var eniSet []*client.NetworkInterface
		err = wait.ExponentialBackoffWithContext(a.ctx, backoff.Backoff(backoff.ENIIPOps), func(ctx context.Context) (bool, error) {
			eniSet, innerErr = a.openAPI.DescribeNetworkInterface(ctx, "", eniIDs, "", "", "", a.eniTagFilter)
			if innerErr == nil {
				return true, nil
			}
			if apiErr.ErrAssert(apiErr.ErrForbidden, innerErr) {
				return true, innerErr
			}
			return false, nil
		})
		if err != nil {
			if innerErr != nil {
				return nil, innerErr
			}
			return nil, err
		}
		for _, eni := range eniSet {
			e, ok := idMap[eni.NetworkInterfaceID]
			if !ok {
				continue
			}
			e.Trunk = eni.Type == client.ENITypeTrunk
			e.ERdma = eni.NetworkInterfaceTrafficMode == client.ENITrafficModeRDMA

			// take to intersect
			result = append(result, e)
		}

	} else {
		result = enis
	}
	return result, nil
}

func validateIPInMetadata(ctx context.Context, expect []netip.Addr, getExist func() []netip.Addr) error {
	return wait.PollUntilContextTimeout(ctx, metadataPollInterval, metadataWaitTimeout, false, func(ctx context.Context) (bool, error) {
		exists := getExist()
		return sets.New[netip.Addr](exists...).HasAll(expect...), nil
	})
}

func validateIPNotInMetadata(ctx context.Context, gone []netip.Addr, getExist func() []netip.Addr) error {
	return wait.PollUntilContextTimeout(ctx, metadataPollInterval, metadataWaitTimeout, false, func(ctx context.Context) (bool, error) {
		exists := getExist()

		return !sets.New[netip.Addr](exists...).HasAny(gone...), nil
	})
}
