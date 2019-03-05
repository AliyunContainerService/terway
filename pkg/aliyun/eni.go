package aliyun

import (
	"encoding/json"
	"fmt"
	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/types"
	"github.com/denverdino/aliyungo/common"
	"github.com/denverdino/aliyungo/ecs"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"net"
)

type ENIInfoGetter interface {
	GetENIConfigByMac(mac string) (*types.ENI, error)
	GetENIConfigById(eniId string) (*types.ENI, error)
	GetENIPrivateAddresses(eniId string) ([]net.IP, error)
	GetAttachedENIs(instanceId string, containsMainENI bool) ([]*types.ENI, error)
}

type ENIMetadata struct {
}

func (e *ENIMetadata) GetENIConfigByMac(mac string) (*types.ENI, error) {
	var (
		eni types.ENI
		err error
	)

	eni.ID, err = metadataValue(fmt.Sprintf(metadataBase+eniIDPath, mac))
	if err != nil {
		errors.Wrapf(err, "error get eni id from metaserver, mac: %s", mac)
	}
	eni.MAC = mac

	ipAddr, err := metadataValue(fmt.Sprintf(metadataBase+eniAddrPath, mac))
	if err != nil {
		errors.Wrapf(err, "error get eni address from metaserver, mac: %s", mac)
	}
	netmask, err := metadataValue(fmt.Sprintf(metadataBase+eniNetmaskPath, mac))
	if err != nil {
		errors.Wrapf(err, "error get eni netmask from metaserver, mac: %s", mac)
	}
	ip := net.ParseIP(ipAddr)
	if ip == nil {
		return nil, errors.Errorf("error parse eni address: %s from metadata", ipAddr)
	}
	mask := net.ParseIP(netmask)
	if mask == nil {
		return nil, errors.Errorf("error parse eni mask: %s from metadata", ipAddr)
	}
	eni.Address = net.IPNet{
		IP: ip,
		// fixme: dual stack support
		Mask: net.IPv4Mask(mask[12], mask[13], mask[14], mask[15]),
	}
	gw, err := metadataValue(fmt.Sprintf(metadataBase+eniGatewayPath, mac))
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni gateway from metaserver, mac: %s", mac)
	}
	gateway := net.ParseIP(gw)
	if mask == nil {
		return nil, errors.Errorf("error parse eni mask: %s from metadata", ipAddr)
	}
	eni.Gateway = gateway

	eni.Name, err = link.GetDeviceName(mac)
	if err != nil {
		logrus.Warnf("error get device name for eni: %v", err)
	}

	eni.DeviceNumber, err = link.GetDeviceNumber(mac)
	if err != nil {
		logrus.Warnf("error get device number for eni: %v", err)
	}

	return &eni, nil
}

func (e *ENIMetadata) GetENIConfigById(eniId string) (*types.ENI, error) {
	macs, err := e.getAttachMACList()
	if err != nil {
		return nil, err
	}
	for _, mac := range macs {
		id, err := metadataValue(fmt.Sprintf(metadataBase+eniIDPath, mac))
		if err != nil {
			return nil, errors.Wrapf(err, "error get eni id for mac: %s from metadata", mac)
		}
		if eniId == id {
			return e.GetENIConfigByMac(mac)
		}
	}
	return nil, errors.Errorf("not found eni id: %s", eniId)
}

func (e *ENIMetadata) GetENIPrivateAddresses(eniId string) ([]net.IP, error) {
	var addressList []net.IP
	eni, err := e.GetENIConfigById(eniId)
	if err != nil {
		return addressList, err
	}

	addressStrList := &[]string{}
	ipsStr, err := metadataValue(fmt.Sprintf(metadataBase+eniPrivateIPs, eni.MAC))
	err = json.Unmarshal([]byte(ipsStr), addressStrList)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni private address for eni: %s from metadata", eniId)
	}
	for _, ipStr := range *addressStrList {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			return nil, errors.Errorf("error parse private ip: %v", ip)
		}
		addressList = append(addressList, ip)
	}
	return addressList, err
}

func (e *ENIMetadata) getAttachMACList() ([]string, error) {
	macs, err := metadataArray(metadataBase + enisPath)
	return macs, errors.Wrapf(err, "error get eni list from metadata")
}

func (e *ENIMetadata) GetAttachedENIs(instanceId string, containsMainENI bool)  ([]*types.ENI, error) {
	var enis []*types.ENI

	mainENIMac, err := metadataValue(metadataBase + mainEniPath)
	if err != nil {
		return enis, errors.Wrapf(err, "error get main eni form metadata")
	}

	macs, err := e.getAttachMACList()
	if err != nil {
		return nil, err
	}
	for _, mac := range macs {
		if !containsMainENI {
			if mac == mainENIMac {
				continue
			}
		}
		eni, err := e.GetENIConfigByMac(mac)
		if err != nil {
			return nil, errors.Wrapf(err, "error get eni info for mac: %s from metadata", mac)
		}
		enis = append(enis, eni)
	}
	return enis, nil
}

type ENIOpenAPI struct {
	clientSet      *ClientMgr
	region         common.Region
}

func (*ENIOpenAPI) GetAttachedENIs(instanceId string, containsMainENI bool) ([]*types.ENI, error) {
	panic("implement me")
}

func (eoa *ENIOpenAPI) GetENIPrivateAddresses(eniId string) ([]net.IP, error) {
	describeNetworkInterfacesArgs := &ecs.DescribeNetworkInterfacesArgs{
		RegionId: eoa.region,
		NetworkInterfaceId: []string{eniId},
	}
	resp, err := eoa.clientSet.ecs.DescribeNetworkInterfaces(describeNetworkInterfacesArgs)
	if err != nil {
		return nil, errors.Wrapf(err, "error get info from openapi: eniid: %s", eniId)
	}

	if len(resp.NetworkInterfaceSets.NetworkInterfaceSet) != 1 {
		return nil, fmt.Errorf("unexpect number of eni of id: %s", eniId)
	}

	eni := resp.NetworkInterfaceSets.NetworkInterfaceSet[0]
	privateIpList := make([]net.IP, 0)
	for _, ipStr := range eni.PrivateIpSets.PrivateIpSet {
		ip := net.ParseIP(ipStr.PrivateIpAddress)
		if ip != nil {
			privateIpList = append(privateIpList, ip)
		}
	}
	return privateIpList, nil
}

func (*ENIOpenAPI) GetENIConfigByMac(mac string) (*types.ENI, error) {
	panic("implement me")
}

func (*ENIOpenAPI) GetENIConfigById(eniId string) (*types.ENI, error) {
	panic("implement me")
}
