package aliyun

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/types"
	"github.com/denverdino/aliyungo/common"
	"github.com/denverdino/aliyungo/ecs"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// ENIInfoGetter interface to get eni information
type ENIInfoGetter interface {
	GetENIConfigByMac(mac string) (*types.ENI, error)
	GetENIConfigByID(eniID string) (*types.ENI, error)
	GetENIPrivateAddresses(eniID string) ([]net.IP, error)
	GetAttachedENIs(instanceID string, containsMainENI bool) ([]*types.ENI, error)
}

type eniMetadata struct {
}

func (e *eniMetadata) GetENIConfigByMac(mac string) (*types.ENI, error) {
	var (
		eni types.ENI
		err error
	)

	eni.ID, err = metadataValue(fmt.Sprintf(metadataBase+eniIDPath, mac))
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni id from metaserver, mac: %s", mac)
	}
	eni.MAC = mac

	ipAddr, err := metadataValue(fmt.Sprintf(metadataBase+eniAddrPath, mac))
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni address from metaserver, mac: %s", mac)
	}
	netmask, err := metadataValue(fmt.Sprintf(metadataBase+eniNetmaskPath, mac))
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni netmask from metaserver, mac: %s", mac)
	}
	ip := net.ParseIP(ipAddr)
	if ip == nil {
		return nil, errors.Errorf("error parse eni address: %s from metadata", ipAddr)
	}
	mask := net.ParseIP(netmask)
	if mask == nil {
		return nil, errors.Errorf("error parse eni mask: %s from metadata", netmask)
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
	if gateway == nil {
		return nil, errors.Errorf("error parse eni gateway: %s from metadata", gw)
	}
	eni.Gateway = gateway

	vswitch, err := metadataValue(fmt.Sprintf(metadataBase+eniVSwitchPath, mac))
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni vswitch from metaserver, mac: %s", mac)
	}
	eni.VSwitch = vswitch

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

func (e *eniMetadata) GetENIConfigByID(eniID string) (*types.ENI, error) {
	macs, err := e.getAttachMACList()
	if err != nil {
		return nil, err
	}
	for _, mac := range macs {
		id, err := metadataValue(fmt.Sprintf(metadataBase+eniIDPath, mac))
		if err != nil {
			return nil, errors.Wrapf(err, "error get eni id for mac: %s from metadata", mac)
		}
		if eniID == id {
			return e.GetENIConfigByMac(mac)
		}
	}
	return nil, errors.Errorf("not found eni id: %s", eniID)
}

func (e *eniMetadata) GetENIPrivateAddresses(eniID string) ([]net.IP, error) {
	var addressList []net.IP
	eni, err := e.GetENIConfigByID(eniID)
	if err != nil {
		return addressList, err
	}

	addressStrList := &[]string{}
	ipsStr, err := metadataValue(fmt.Sprintf(metadataBase+eniPrivateIPs, eni.MAC))
	if err != nil {
		return nil, errors.Wrapf(err, "error get private ips from metadata")
	}
	err = json.Unmarshal([]byte(ipsStr), addressStrList)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni private address for eni: %s from metadata", eniID)
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

func (e *eniMetadata) getAttachMACList() ([]string, error) {
	macs, err := metadataArray(metadataBase + enisPath)
	return macs, errors.Wrapf(err, "error get eni list from metadata")
}

func (e *eniMetadata) GetAttachedENIs(instanceID string, containsMainENI bool) ([]*types.ENI, error) {
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

type eniOpenAPI struct {
	clientSet *ClientMgr
	region    common.Region
}

func (*eniOpenAPI) GetAttachedENIs(instanceID string, containsMainENI bool) ([]*types.ENI, error) {
	panic("implement me")
}

func (eoa *eniOpenAPI) GetENIPrivateAddresses(eniID string) ([]net.IP, error) {
	describeNetworkInterfacesArgs := &ecs.DescribeNetworkInterfacesArgs{
		RegionId:           eoa.region,
		NetworkInterfaceId: []string{eniID},
	}
	resp, err := eoa.clientSet.ecs.DescribeNetworkInterfaces(describeNetworkInterfacesArgs)
	if err != nil {
		return nil, errors.Wrapf(err, "error get info from openapi: eniid: %s", eniID)
	}

	if len(resp.NetworkInterfaceSets.NetworkInterfaceSet) != 1 {
		return nil, fmt.Errorf("unexpect number of eni of id: %s", eniID)
	}

	eni := resp.NetworkInterfaceSets.NetworkInterfaceSet[0]
	privateIPList := make([]net.IP, 0)
	for _, ipStr := range eni.PrivateIpSets.PrivateIpSet {
		ip := net.ParseIP(ipStr.PrivateIpAddress)
		if ip != nil {
			privateIPList = append(privateIPList, ip)
		}
	}
	return privateIPList, nil
}

func (*eniOpenAPI) GetENIConfigByMac(mac string) (*types.ENI, error) {
	panic("implement me")
}

func (*eniOpenAPI) GetENIConfigByID(eniID string) (*types.ENI, error) {
	panic("implement me")
}

var ecsEniMatix = map[string]int{
	"ecs.t1.xsmall":              1,
	"ecs.t1.small":               1,
	"ecs.s2.small":               1,
	"ecs.s3.medium":              2,
	"ecs.c1.small":               2,
	"ecs.c2.medium":              2,
	"ecs.s1.small":               1,
	"ecs.s2.large":               1,
	"ecs.s3.large":               2,
	"ecs.c1.large":               2,
	"ecs.c2.large":               2,
	"ecs.s1.medium":              1,
	"ecs.s2.xlarge":              1,
	"ecs.m1.medium":              2,
	"ecs.m1.xlarge":              2,
	"ecs.c2.xlarge":              2,
	"ecs.s1.large":               1,
	"ecs.s2.2xlarge":             1,
	"ecs.m2.medium":              2,
	"ecs.m2.xlarge":              2,
	"ecs.n1.tiny":                1,
	"ecs.n1.small":               1,
	"ecs.n1.medium":              2,
	"ecs.n1.large":               2,
	"ecs.n1.xlarge":              2,
	"ecs.n1.3xlarge":             2,
	"ecs.n1.7xlarge":             2,
	"ecs.n2.small":               1,
	"ecs.n2.medium":              2,
	"ecs.n2.large":               2,
	"ecs.n2.xlarge":              2,
	"ecs.n2.3xlarge":             2,
	"ecs.n2.7xlarge":             2,
	"ecs.e3.small":               1,
	"ecs.e3.medium":              2,
	"ecs.e3.large":               2,
	"ecs.e3.xlarge":              2,
	"ecs.e3.3xlarge":             2,
	"ecs.e3.7xlarge":             2,
	"ecs.sn1.medium":             2,
	"ecs.sn2.medium":             2,
	"ecs.sn1.large":              3,
	"ecs.sn1.xlarge":             4,
	"ecs.sn1.3xlarge":            8,
	"ecs.sn1.7xlarge":            8,
	"ecs.sn2.large":              3,
	"ecs.sn2.xlarge":             4,
	"ecs.sn2.3xlarge":            8,
	"ecs.sn2.7xlarge":            8,
	"ecs.n4.small":               1,
	"ecs.n4.large":               1,
	"ecs.n4.xlarge":              2,
	"ecs.n4.2xlarge":             2,
	"ecs.n4.4xlarge":             2,
	"ecs.n4.8xlarge":             2,
	"ecs.mn4.small":              1,
	"ecs.mn4.large":              1,
	"ecs.mn4.xlarge":             2,
	"ecs.mn4.2xlarge":            3,
	"ecs.mn4.4xlarge":            8,
	"ecs.mn4.8xlarge":            8,
	"ecs.xn4.small":              1,
	"ecs.e4.large":               1,
	"ecs.e4.xlarge":              2,
	"ecs.e4.2xlarge":             3,
	"ecs.e4.4xlarge":             8,
	"ecs.gn3.2xlarge":            4,
	"ecs.sn2.13xlarge":           8,
	"ecs.e4.small":               1,
	"ecs.se1.large":              2,
	"ecs.se1.xlarge":             3,
	"ecs.se1.2xlarge":            4,
	"ecs.se1.4xlarge":            8,
	"ecs.se1.8xlarge":            8,
	"ecs.se1.14xlarge":           8,
	"ecs.ga1.4xlarge":            8,
	"ecs.ga1.8xlarge":            8,
	"ecs.ga1.14xlarge":           8,
	"ecs.gn4.8xlarge":            8,
	"ecs.gn4.14xlarge":           8,
	"ecs.i1.xlarge":              3,
	"ecs.i1.2xlarge":             4,
	"ecs.i1.4xlarge":             8,
	"ecs.i1.8xlarge":             8,
	"ecs.i1.14xlarge":            8,
	"ecs.cm4.xlarge":             3,
	"ecs.cm4.2xlarge":            4,
	"ecs.cm4.4xlarge":            8,
	"ecs.ce4.xlarge":             3,
	"ecs.c4.xlarge":              3,
	"ecs.c4.2xlarge":             4,
	"ecs.c4.4xlarge":             8,
	"ecs.cm4.6xlarge":            8,
	"ecs.sc1.4xlarge":            8,
	"ecs.d1.2xlarge":             4,
	"ecs.d1.4xlarge":             8,
	"ecs.d1.6xlarge":             8,
	"ecs.d1.8xlarge":             8,
	"ecs.d1.14xlarge":            8,
	"ecs.ga1.2xlarge":            4,
	"ecs.ga1.xlarge":             3,
	"ecs.sn1ne.large":            2,
	"ecs.sn1ne.xlarge":           3,
	"ecs.sn1ne.2xlarge":          4,
	"ecs.sn1ne.4xlarge":          8,
	"ecs.sn1ne.8xlarge":          8,
	"ecs.sn2ne.large":            2,
	"ecs.sn2ne.xlarge":           3,
	"ecs.sn2ne.2xlarge":          4,
	"ecs.sn2ne.4xlarge":          8,
	"ecs.sn2ne.8xlarge":          8,
	"ecs.sn2ne.14xlarge":         8,
	"ecs.se1ne.large":            2,
	"ecs.se1ne.xlarge":           3,
	"ecs.se1ne.2xlarge":          4,
	"ecs.se1ne.4xlarge":          8,
	"ecs.se1ne.8xlarge":          8,
	"ecs.se1ne.14xlarge":         8,
	"ecs.i1-c10d1.8xlarge":       8,
	"ecs.i1-c5d1.4xlarge":        8,
	"ecs.f1-c8f1.2xlarge":        2,
	"ecs.f1-c8f1.4xlarge":        2,
	"ecs.gn5-c4g1.xlarge":        3,
	"ecs.gn5-c8g1.2xlarge":       4,
	"ecs.gn5-c4g1.2xlarge":       4,
	"ecs.gn5-c8g1.4xlarge":       8,
	"ecs.gn5-c8g1.8xlarge":       8,
	"ecs.gn5-c8g1.14xlarge":      8,
	"ecs.gn4-c4g1.xlarge":        3,
	"ecs.gn4-c8g1.2xlarge":       4,
	"ecs.gn4-c4g1.2xlarge":       4,
	"ecs.gn4-c8g1.4xlarge":       8,
	"ecs.d1ne.2xlarge":           4,
	"ecs.d1ne.4xlarge":           8,
	"ecs.d1ne.6xlarge":           8,
	"ecs.d1ne.8xlarge":           8,
	"ecs.d1ne.14xlarge":          8,
	"ecs.d1-c8d3.8xlarge":        8,
	"ecs.d1-c14d3.14xlarge":      8,
	"ecs.c5.large":               2,
	"ecs.c5.xlarge":              3,
	"ecs.c5.2xlarge":             4,
	"ecs.c5.4xlarge":             8,
	"ecs.c5.6xlarge":             8,
	"ecs.c5.8xlarge":             8,
	"ecs.c5.16xlarge":            8,
	"ecs.g5.large":               2,
	"ecs.g5.xlarge":              3,
	"ecs.g5.2xlarge":             4,
	"ecs.g5.4xlarge":             8,
	"ecs.g5.6xlarge":             8,
	"ecs.g5.8xlarge":             8,
	"ecs.g5.16xlarge":            8,
	"ecs.r5.large":               2,
	"ecs.r5.xlarge":              3,
	"ecs.r5.2xlarge":             4,
	"ecs.r5.4xlarge":             8,
	"ecs.r5.6xlarge":             8,
	"ecs.r5.8xlarge":             8,
	"ecs.r5.16xlarge":            8,
	"ecs.r5.22xlarge":            15,
	"ecs.hfc5.large":             2,
	"ecs.hfc5.xlarge":            3,
	"ecs.hfc5.2xlarge":           4,
	"ecs.hfc5.4xlarge":           8,
	"ecs.hfc5.6xlarge":           8,
	"ecs.hfc5.8xlarge":           8,
	"ecs.hfg5.large":             2,
	"ecs.hfg5.xlarge":            3,
	"ecs.hfg5.2xlarge":           4,
	"ecs.hfg5.4xlarge":           8,
	"ecs.hfg5.6xlarge":           8,
	"ecs.hfg5.8xlarge":           8,
	"ecs.hfg5.14xlarge":          8,
	"ecs.i2.xlarge":              3,
	"ecs.i2.2xlarge":             4,
	"ecs.i2.4xlarge":             8,
	"ecs.i2.8xlarge":             8,
	"ecs.i2.16xlarge":            8,
	"ecs.gn5-c28g1.7xlarge":      8,
	"ecs.gn5-c28g1.14xlarge":     8,
	"ecs.re4.20xlarge":           2,
	"ecs.re4.40xlarge":           2,
	"ecs.gn5i-c2g1.large":        2,
	"ecs.gn5i-c4g1.xlarge":       3,
	"ecs.gn5i-c8g1.2xlarge":      4,
	"ecs.gn5i-c16g1.4xlarge":     8,
	"ecs.gn5i-c28g1.14xlarge":    8,
	"ecs.gn5t.7xlarge":           8,
	"ecs.gn5t.14xlarge":          8,
	"ecs.t5-c1m1.large":          1,
	"ecs.t5-c1m1.xlarge":         2,
	"ecs.t5-c1m1.2xlarge":        2,
	"ecs.t5-c1m1.4xlarge":        2,
	"ecs.t5-c1m2.large":          1,
	"ecs.t5-c1m2.xlarge":         2,
	"ecs.t5-c1m2.2xlarge":        2,
	"ecs.t5-c1m2.4xlarge":        2,
	"ecs.t5-c1m4.large":          1,
	"ecs.t5-c1m4.xlarge":         2,
	"ecs.t5-c1m4.2xlarge":        2,
	"ecs.t5-lc2m1.nano":          1,
	"ecs.t5-lc1m1.small":         1,
	"ecs.t5-lc1m2.small":         1,
	"ecs.t5-lc1m2.large":         1,
	"ecs.t5-lc1m4.large":         1,
	"ecs.f1-c28f1.7xlarge":       2,
	"ecs.f1-c28f1.14xlarge":      2,
	"ecs.sccg5.24xlarge":         32,
	"ecs.scch5.16xlarge":         32,
	"ecs.sccgn5d.16xlarge":       2,
	"ecs.sccgn5.16xlarge":        2,
	"ecs.ebmg5.24xlarge":         32,
	"ecs.ebmr5.24xlarge":         32,
	"ecs.f2-c8f1.2xlarge":        2,
	"ecs.f2-c28f1.7xlarge":       2,
	"ecs.f2-c8f1.4xlarge":        2,
	"ecs.f2-c28f1.14xlarge":      2,
	"ecs.gn5i-c40g1.10xlarge":    8,
	"ecs.gn5i-c48g1.12xlarge":    2,
	"ecs.ebmr5.2xlarge":          6,
	"ecs.ebmr4.4xlarge":          12,
	"ecs.ebmg4.8xlarge":          12,
	"ecs.ebmg4.16xlarge":         12,
	"ecs.ebma1.24xlarge":         1,
	"ecs.gn6p-c8g1.4xlarge":      2,
	"ecs.gn5i-c16g1.8xlarge":     8,
	"ecs.sn1ne.3xlarge":          6,
	"ecs.sn1ne.6xlarge":          4,
	"ecs.sn2ne.3xlarge":          6,
	"ecs.sn2ne.6xlarge":          4,
	"ecs.se1ne.3xlarge":          6,
	"ecs.se1ne.6xlarge":          4,
	"ecs.c4.3xlarge":             6,
	"ecs.cm4.3xlarge":            6,
	"ecs.ce4.2xlarge":            4,
	"ecs.c5.3xlarge":             6,
	"ecs.hfc5.3xlarge":           6,
	"ecs.g5.3xlarge":             6,
	"ecs.g5.22xlarge":            15,
	"ecs.hfg5.3xlarge":           6,
	"ecs.r5.3xlarge":             6,
	"ecs.i1.3xlarge":             6,
	"ecs.i1.6xlarge":             4,
	"ecs.d1.3xlarge":             6,
	"ecs.g5se.large":             1,
	"ecs.g5se.xlarge":            3,
	"ecs.g5se.2xlarge":           3,
	"ecs.g5se.4xlarge":           8,
	"ecs.g5se.6xlarge":           8,
	"ecs.g5se.8xlarge":           8,
	"ecs.g5se.16xlarge":          8,
	"ecs.g5se.18xlarge":          8,
	"ecs.gn5i-c12g1.6xlarge":     8,
	"ecs.gn5t.4xlarge":           2,
	"ecs.ebmgn5t.16xlarge":       1,
	"ecs.sccgn5t.16xlarge":       1,
	"ecs.sccgn6.24xlarge":        32,
	"ecs.ebmgn5.16xlarge":        1,
	"ecs.ebmhfg5.2xlarge":        6,
	"ecs.ebmhfg4.4xlarge":        12,
	"ecs.ebmc4.8xlarge":          16,
	"ecs.ic5.large":              2,
	"ecs.ic5.xlarge":             3,
	"ecs.ic5.2xlarge":            4,
	"ecs.ic5.3xlarge":            6,
	"ecs.ic5.4xlarge":            8,
	"ecs.ic5.6xlarge":            8,
	"ecs.ic5.8xlarge":            8,
	"ecs.ic5.16xlarge":           8,
	"ecs.gn5d-c8g1.8xlarge":      8,
	"ecs.gn5-c48g1.12xlarge":     8,
	"ecs.ebmgn5i.24xlarge":       31,
	"ecs.ebmi2.24xlarge":         31,
	"ecs.d1ne-c8d3.8xlarge":      8,
	"ecs.d1ne-c14d3.14xlarge":    8,
	"ecs.f3-c4f1.xlarge":         3,
	"ecs.f3-c8f1.2xlarge":        4,
	"ecs.f3-c12f1.3xlarge":       6,
	"ecs.f3-c16f1.4xlarge":       8,
	"ecs.f3-c24f1.6xlarge":       8,
	"ecs.f3-c32f1.8xlarge":       8,
	"ecs.f3-c40f1.10xlarge":      8,
	"ecs.f3-c6f1.3xlarge":        6,
	"ecs.f3-c8f1.4xlarge":        8,
	"ecs.f3-c12f1.6xlarge":       8,
	"ecs.f3-c16f1.8xlarge":       8,
	"ecs.f3-c32f1.16xlarge":      8,
	"ecs.f3-c8f1.8xlarge":        8,
	"ecs.f3-c16f1.16xlarge":      8,
	"ecs.ebmgn5t.10xlarge":       32,
	"ecs.gn6v-c8g1.2xlarge":      4,
	"ecs.gn6v-c8g1.4xlarge":      8,
	"ecs.gn6v-c8g1.8xlarge":      8,
	"ecs.gn6v-c8g1.16xlarge":     8,
	"ecs.ebmi3.24xlarge":         32,
	"ecs.re5.15xlarge":           8,
	"ecs.re5.30xlarge":           15,
	"ecs.re5.45xlarge":           15,
	"ecs.i2g.2xlarge":            4,
	"ecs.i2g.4xlarge":            8,
	"ecs.i2g.8xlarge":            8,
	"ecs.i2g.16xlarge":           8,
	"ecs.r1.7xlarge":             4,
	"ecs.c5.22xlarge":            15,
	"ecs.d1ne-8tc14d3.14xlarge":  8,
	"ecs.re4e.40xlarge":          15,
	"ecs.d1ne-c14d3g.14xlarge":   8,
	"ecs.d1ne-8tc14d3g.14xlarge": 8,
	"ecs.ebmg5s.24xlarge":        32,
	"ecs.sn1ne.22xlarge":         15,
	"ecs.sn2ne.22xlarge":         15,
	"ecs.se1ne.22xlarge":         15,
	"ecs.ebmgn5ts.10xlarge":      32,
	"ecs.ebmg5s-eni.24xlarge":    250,
	"ecs.i2ne.xlarge":            3,
	"ecs.i2ne.2xlarge":           4,
	"ecs.i2ne.4xlarge":           8,
	"ecs.i2ne.8xlarge":           8,
	"ecs.i2ne.16xlarge":          8,
	"ecs.i2gne.2xlarge":          4,
	"ecs.i2gne.4xlarge":          8,
	"ecs.i2gne.8xlarge":          8,
	"ecs.i2gne.16xlarge":         8,
	"ecs.vgn5i-m1.large":         2,
	"ecs.vgn5i-m2.xlarge":        3,
	"ecs.vgn5i-m4.2xlarge":       4,
	"ecs.vgn5i-m8.4xlarge":       5,
	"ecs.ebmc5s.24xlarge":        32,
	"ecs.ebmgn6t.24xlarge":       32,
	"ecs.g6.26xlarge":            15,
	"ecs.gn6i-c4g1.xlarge":       2,
	"ecs.gn6i-c8g1.2xlarge":      2,
	"ecs.gn6i-c16g1.4xlarge":     3,
	"ecs.gn6i-c24g1.6xlarge":     4,
	"ecs.gn6i-c24g1.12xlarge":    6,
	"ecs.gn6i-c24g1.24xlarge":    8,
	"ecs.gn6i-c32g1.8xlarge":     3,
	"ecs.gn6i-c48g1.12xlarge":    6,
	"ecs.gn6i-c72g1.18xlarge":    8,
	"ecs.g6.13xlarge":            7,
	"ecs.g6.16xlarge":            8,
	"ecs.ebmg6.26xlarge":         32,
	"ecs.gn6v-c10g1.20xlarge":    8,
	"ecs.g5ne.2xlarge":           4,
	"ecs.g5ne.4xlarge":           6,
	"ecs.g5ne.8xlarge":           6,
	"ecs.g5nenl.4xlarge":         6,
	"ecs.g5nenl.2xlarge":         4,
	"ecs.ebmm5d.24xlarge":        32,
	"ecs.g6.2xlarge":             4,
	"ecs.ebmr5s.24xlarge":        32,
	"ecs.i2.20xlarge":            8,
	"ecs.g6.large":               2,
	"ecs.g6.xlarge":              3,
	"ecs.g6.3xlarge":             6,
	"ecs.g6.4xlarge":             8,
	"ecs.g6.6xlarge":             8,
	"ecs.g6.8xlarge":             8,
	"ecs.ebmhd1.10xlarge":        15,
	"ecs.ebma3.4xlarge":          1,
	"ecs.ebmgn6.24xlarge":        32,
	"ecs.ebmgn6i.24xlarge":       32,
	"ecs.ec1.xlarge":             3,
	"ecs.ec1.2xlarge":            4,
	"ecs.eg1.xlarge":             3,
	"ecs.eg1.2xlarge":            4,
	"ecs.ebmhd2.24xlarge":        32,
	"ecs.ebmhd3.24xlarge":        32,
	"ecs.ec1.large":              2,
	"ecs.ec1.4xlarge":            6,
	"ecs.ec1.7xlarge":            6,
	"ecs.eg1.large":              2,
	"ecs.eg1.4xlarge":            6,
	"ecs.eg1.7xlarge":            6,
	"ecs.er1.large":              2,
	"ecs.er1.xlarge":             3,
	"ecs.er1.2xlarge":            4,
	"ecs.c5.21xlarge":            24,
	"ecs.v5-c1m1.large":          2,
	"ecs.v5-c1m1.xlarge":         2,
	"ecs.v5-c1m1.2xlarge":        3,
	"ecs.v5-c1m1.3xlarge":        3,
	"ecs.v5-c1m1.4xlarge":        4,
	"ecs.v5-c1m2.xlarge":         2,
	"ecs.v5-c1m2.large":          2,
	"ecs.v5-c1m1.8xlarge":        4,
	"ecs.v5-c1m2.2xlarge":        3,
	"ecs.v5-c1m2.3xlarge":        3,
	"ecs.v5-c1m2.4xlarge":        4,
	"ecs.v5-c1m2.8xlarge":        4,
	"ecs.v5-c1m4.4xlarge":        4,
	"ecs.v5-c1m4.3xlarge":        3,
	"ecs.v5-c1m4.2xlarge":        3,
	"ecs.v5-c1m4.xlarge":         2,
	"ecs.v5-c1m4.large":          2,
	"ecs.v5-c1m4.8xlarge":        4,
	"ecs.v5-c1m8.large":          2,
	"ecs.v5-c1m8.xlarge":         2,
	"ecs.v5-c1m8.2xlarge":        3,
	"ecs.v5-c1m8.3xlarge":        3,
	"ecs.v5-c1m8.4xlarge":        4,
	"ecs.v5-c1m8.8xlarge":        4,
	"ecs.ebmi2-ant.24xlarge":     32,
	"ecs.ebmg6e.26xlarge":        32,
	"ecs.ebmc6e.26xlarge":        32,
	"ecs.ed2-2t.11xlarge":        8,
	"ecs.ebmi3s.24xlarge":        32,
	"ecs.sn1nec.large":           2,
	"ecs.sn1nec.xlarge":          3,
	"ecs.sn1nec.4xlarge":         6,
	"ecs.sn1nec.8xlarge":         8,
	"ecs.sn1nec.2xlarge":         4,
	"ecs.sn2nec.8xlarge":         8,
	"ecs.g6e.26xlarge":           15,
	"ecs.g6e.2xlarge":            4,
	"ecs.c6.16xlarge":            8,
	"ecs.c6.26xlarge":            15,
	"ecs.c6.13xlarge":            7,
	"ecs.c6.8xlarge":             8,
	"ecs.c6.6xlarge":             8,
	"ecs.c6.4xlarge":             8,
	"ecs.c6.3xlarge":             6,
	"ecs.c6.2xlarge":             4,
	"ecs.c6.xlarge":              3,
	"ecs.se1nec.large":           2,
	"ecs.se1nec.xlarge":          3,
	"ecs.se1nec.2xlarge":         4,
	"ecs.se1nec.4xlarge":         6,
	"ecs.se1nec.8xlarge":         8,
	"ecs.sn2nec.large":           2,
	"ecs.sn2nec.xlarge":          3,
	"ecs.sn2nec.2xlarge":         4,
	"ecs.sn2nec.4xlarge":         6,
	"ecs.r6.13xlarge":            7,
	"ecs.r6.8xlarge":             8,
	"ecs.r6.6xlarge":             8,
	"ecs.r6.2xlarge":             4,
	"ecs.r6.xlarge":              3,
	"ecs.r6.large":               2,
	"ecs.r6.26xlarge":            15,
	"ecs.c6.large":               2,
	"ecs.r6.16xlarge":            8,
	"ecs.r6.4xlarge":             8,
	"ecs.r6.3xlarge":             6,
	"ecs.ed1-4t.7xlarge":         8,
	"ecs.ed1-2t.7xlarge":         8,
	"ecs.g6e.13xlarge":           7,
	"ecs.g6e.8xlarge":            8,
	"ecs.g6e.4xlarge":            8,
	"ecs.g6e.large":              2,
	"ecs.g6e.xlarge":             3,
	"ecs.scch5s.16xlarge":        32,
	"ecs.sccg5s.24xlarge":        32,
	"ecs.t6-c4m1.large":          1,
	"ecs.t6-c2m1.large":          1,
	"ecs.t6-c1m1.large":          1,
	"ecs.t6-c1m2.large":          1,
	"ecs.t6-c1m4.xlarge":         2,
	"ecs.t6-c1m4.2xlarge":        2,
	"ecs.t6-c1m4.large":          1,
	"ecs.i2ne.20xlarge":          8,
	"ecs.ebmgn6e.24xlarge":       32,
	"ecs.hfc6.xlarge":            3,
	"ecs.hfg6.8xlarge":           8,
	"ecs.hfg6.6xlarge":           8,
	"ecs.hfg6.4xlarge":           8,
	"ecs.hfg6.3xlarge":           6,
	"ecs.hfg6.2xlarge":           4,
	"ecs.hfg6.xlarge":            2,
	"ecs.hfg6.large":             2,
	"ecs.hfc6.8xlarge":           8,
	"ecs.hfc6.6xlarge":           8,
	"ecs.hfc6.4xlarge":           8,
	"ecs.hfc6.3xlarge":           6,
	"ecs.hfc6.2xlarge":           4,
	"ecs.hfc6.large":             2,
	"ecs.ht6c.small":             1,
	"ecs.ht6c.large":             2,
	"ecs.ht6c.xlarge":            3,
	"ecs.ht6c.2xlarge":           4,
	"ecs.ht6c.3xlarge":           6,
	"ecs.ht6c.4xlarge":           8,
	"ecs.ht6c.6xlarge":           8,
	"ecs.ht6c.8xlarge":           8,
	"ecs.ht6g.small":             1,
	"ecs.ht6g.large":             2,
	"ecs.ht6r.3xlarge":           6,
	"ecs.ht6r.4xlarge":           8,
	"ecs.ht6r.6xlarge":           8,
	"ecs.ht6r.8xlarge":           8,
	"ecs.gn6e-c12g1.24xlarge":    8,
	"ecs.gn6e-c12g1.6xlarge":     8,
	"ecs.hfr6.large":             2,
	"ecs.hfr6.2xlarge":           4,
	"ecs.hfr6.xlarge":            2,
	"ecs.hfr6.3xlarge":           6,
	"ecs.hfr6.4xlarge":           8,
	"ecs.hfr6.6xlarge":           8,
	"ecs.hfr6.8xlarge":           8,
	"ecs.ht6r.xlarge":            3,
	"ecs.ht6r.large":             2,
	"ecs.ht6r.small":             1,
	"ecs.ht6r.2xlarge":           4,
	"ecs.ht6g.xlarge":            3,
	"ecs.ht6g.2xlarge":           4,
	"ecs.ht6g.3xlarge":           6,
	"ecs.ht6g.4xlarge":           8,
	"ecs.ht6g.6xlarge":           8,
	"ecs.ht6g.8xlarge":           8,
	"ecs.hfc6.10xlarge":          7,
	"ecs.hfc6.16xlarge":          8,
	"ecs.hfc6.20xlarge":          15,
	"ecs.hfg6.10xlarge":          7,
	"ecs.hfg6.16xlarge":          8,
	"ecs.hfg6.20xlarge":          15,
	"ecs.hfr6.10xlarge":          7,
	"ecs.hfr6.16xlarge":          8,
	"ecs.hfr6.20xlarge":          15,
	"ecs.gn6e-c12g1.3xlarge":     6,
	"ecs.ebmgn6v.24xlarge":       32,
	"ecs.gn6e-c12g1.12xlarge":    8,
	"ecs.ebman1.26xlarge":        32,
	"ecs.c5t.large":              2,
	"ecs.c5t.21xlarge":           24,
	"ecs.c5t.16xlarge":           8,
	"ecs.c5t.8xlarge":            8,
	"ecs.c5t.6xlarge":            8,
	"ecs.c5t.4xlarge":            8,
	"ecs.c5t.xlarge":             3,
	"ecs.c5t.2xlarge":            4,
	"ecs.c5t.3xlarge":            6,
	"ecs.d2s.8xlarge":            8,
	"ecs.d2s.16xlarge":           8,
	"ecs.sccgn6ne.24xlarge":      32,
	"ecs.ebmc6.26xlarge":         32,
	"ecs.ebmr6.26xlarge":         32,
	"ecs.ebmhd4.24xlarge":        32,
	"ecs.ebmi5-8nvme.24xlarge":   32,
	"ecs.ebmhfr6.20xlarge":       32,
	"ecs.ebmhfg6.20xlarge":       15,
	"ecs.ebmhfc6.20xlarge":       32,
	"ecs.gn6t.6xlarge":           8,
	"ecs.gn6t.3xlarge":           6,
	"ecs.ebmbz-c40m128.10xlarge": 32,
	"ecs.ebmbz-c56m128.14xlarge": 32,
	"ecs.ebmhd5.14xlarge":        32,
	"ecs.ebmre6-aep.26xlarge":    32,
	"ecs.ebmre6-6t.52xlarge":     32,
	"ecs.ebmre6-3t.52xlarge":     32,
	"ecs.ed3-8t.15xlarge":        8,
	"ecs.ed3-8t.7xlarge":         8,
	"ecs.vgn6iv-m4.xlarge":       3,
	"ecs.ebmgn6ex.24xlarge":      32,
	"ecs.c5t.11xlarge":           23,
	"ecs.c5t.10xlarge":           23,
	"ecs.c5v5.4xlarge":           8,
	"ecs.sn1nev4.4xlarge":        8,
	"ecs.sn1nev4.8xlarge":        8,
	"ecs.sn2nev4.4xlarge":        8,
	"ecs.sn1nev4.6xlarge":        4,
	"ecs.g5v6.8xlarge":           8,
	"ecs.g5v6.6xlarge":           8,
	"ecs.g5v6.4xlarge":           8,
	"ecs.g5v6.16xlarge":          8,
	"ecs.sn2nev5.4xlarge":        8,
	"ecs.sn1nev5.8xlarge":        8,
	"ecs.sn1nev5.6xlarge":        4,
	"ecs.sn1nev5.4xlarge":        8,
	"ecs.sn2nev4.8xlarge":        8,
	"ecs.sn2nev4.6xlarge":        4,
	"ecs.c5v6.8xlarge":           8,
	"ecs.c5v6.6xlarge":           8,
	"ecs.c5v6.4xlarge":           8,
	"ecs.c5v6.16xlarge":          8,
	"ecs.g5v5.8xlarge":           8,
	"ecs.g5v5.6xlarge":           8,
	"ecs.sn2nev5.6xlarge":        4,
	"ecs.sn2nev5.8xlarge":        8,
	"ecs.sn1nev6.6xlarge":        4,
	"ecs.g5v5.4xlarge":           8,
	"ecs.g5v5.16xlarge":          8,
	"ecs.sn1nev6.4xlarge":        8,
	"ecs.sn1nev6.8xlarge":        8,
	"ecs.sn2nev6.4xlarge":        8,
	"ecs.c5v5.8xlarge":           8,
	"ecs.c5v5.6xlarge":           8,
	"ecs.sn2nev6.6xlarge":        4,
	"ecs.sn2nev6.8xlarge":        8,
	"ecs.c5v5.16xlarge":          8,
	"ecs.ic6.large":              2,
	"ecs.ic6.4xlarge":            8,
	"ecs.ic6.6xlarge":            8,
	"ecs.ic6.3xlarge":            6,
	"ecs.ic6.8xlarge":            8,
	"ecs.ic6.xlarge":             3,
	"ecs.ic6.2xlarge":            4,
	"ecs.ebmi2s.24xlarge":        32,
	"ecs.ed3g-6t.7xlarge":        1,
	"ecs.ed3g-6t.15xlarge":       1,
	"ecs.ed3c-8t.15xlarge":       1,
	"ecs.ed3c-8t.7xlarge":        1,
	"ecs.sccgn6ex.24xlarge":      32,
	"ecs.g5ne-10g.2xlarge":       4,
	"ecs.g5ne-10g.4xlarge":       6,
	"ecs.g5ne-10g.8xlarge":       6,
	"ecs.g5nenl-10g.2xlarge":     4,
	"ecs.g5nenl-10g.4xlarge":     6,
	"ecs.d2s.2xlarge":            4,
	"ecs.d2s.20xlarge":           8,
	"ecs.d2s.4xlarge":            8,
	"ecs.gn6t.24xlarge":          8,
	"ecs.gn6t.12xlarge":          8,
	"ecs.d2s.5xlarge":            8,
	"ecs.d2s.10xlarge":           8,
	"ecs.faas-image.j2w.medium":  2,
	"ecs.faas-image.j2w.large":   8,
	"ecs.faas-image.j2j.large":   8,
	"ecs.faas-image.j2j.medium":  2,
	"ecs.ebmtest-pov.26xlarge":   32,
	"ecs.sccc6.26xlarge":         32,
	"ecs.scchfr6.20xlarge":       32,
	"ecs.scchfg6.20xlarge":       32,
	"ecs.scchfc6.20xlarge":       32,
	"ecs.sccr6.26xlarge":         32,
	"ecs.sccg6.26xlarge":         32,
	"ecs.ec3.large":              1,
	"ecs.ec3.xlarge":             1,
	"ecs.er3.15xlarge":           1,
	"ecs.er3.14xlarge":           1,
	"ecs.er3.8xlarge":            1,
	"ecs.ec3.2xlarge":            1,
	"ecs.ec3.3xlarge":            1,
	"ecs.ec3.4xlarge":            1,
	"ecs.ec3.7xlarge":            1,
	"ecs.ec3.8xlarge":            1,
	"ecs.ec3.14xlarge":           1,
	"ecs.ec3.15xlarge":           1,
	"ecs.eg3.large":              1,
	"ecs.eg3.xlarge":             1,
	"ecs.eg3.2xlarge":            1,
	"ecs.eg3.3xlarge":            1,
	"ecs.eg3.4xlarge":            1,
	"ecs.eg3.7xlarge":            1,
	"ecs.eg3.8xlarge":            1,
	"ecs.eg3.14xlarge":           1,
	"ecs.eg3.15xlarge":           1,
	"ecs.er3.large":              1,
	"ecs.er3.xlarge":             1,
	"ecs.er3.2xlarge":            1,
	"ecs.er3.3xlarge":            1,
	"ecs.er3.4xlarge":            1,
	"ecs.er3.6xlarge":            1,
	"ecs.ebmc6-aep.26xlarge":     32,
	"ecs.i2d.21xlarge":           16,
	"ecs.s6-c1m4.2xlarge":        2,
	"ecs.s6-c1m1.small":          2,
	"ecs.s6-c1m2.small":          2,
	"ecs.s6-c1m4.small":          2,
	"ecs.s6-c1m2.large":          2,
	"ecs.s6-c1m4.large":          2,
	"ecs.s6-c1m2.xlarge":         2,
	"ecs.s6-c1m4.xlarge":         2,
	"ecs.s6-c1m2.2xlarge":        2,
	"ecs.ebmi2g.24xlarge":        32,
	"ecs.vgn6i-m8.2xlarge":       4,
	"ecs.vgn6i-m4.xlarge":        3,
	"ecs.vgn6i-m2.large":         2,
	"ecs.g5ne.16xlarge":          6,
	"ecs.kvmtest-f41.14large":    16,
	"ecs.kvmtest-f45s1.14large":  16,
	"ecs.kvmtest-f43.14large":    16,
	"ecs.ebmr7.24xlarge":         64,
	"ecs.ebmg7.24xlarge":         64,
	"ecs.ebmc7.24xlarge":         64,
	"ecs.i2v-c1m2d1536.16xlarge": 4,
	"ecs.i2v-c1m2d100.xlarge":    2,
	"ecs.i2v-c1m4d1536.8xlarge":  4,
	"ecs.i2v-c1m2d1024.8xlarge":  4,
	"ecs.i2v-c1m2d1536.8xlarge":  4,
	"ecs.i2v-c1m4d1536.4xlarge":  4,
	"ecs.i2v-c1m4d1024.4xlarge":  4,
	"ecs.i2v-c1m2d1536.4xlarge":  4,
	"ecs.i2v-c1m2d1024.4xlarge":  4,
	"ecs.i2v-c1m2d500.4xlarge":   4,
	"ecs.i2v-c1m2d100.4xlarge":   4,
	"ecs.i2v-c1m4d1536.2xlarge":  2,
	"ecs.i2v-c1m4d1024.2xlarge":  2,
	"ecs.i2v-c1m4d100.2xlarge":   2,
	"ecs.i2v-c1m4d500.2xlarge":   2,
	"ecs.i2v-c1m2d1536.2xlarge":  2,
	"ecs.i2v-c1m2d500.2xlarge":   2,
	"ecs.i2v-c1m2d1024.2xlarge":  2,
	"ecs.i2v-c1m4d1536.xlarge":   2,
	"ecs.i2v-c1m2d100.2xlarge":   2,
	"ecs.i2v-c1m4d1024.xlarge":   2,
	"ecs.i2v-c1m4d500.xlarge":    2,
	"ecs.i2v-c1m4d100.xlarge":    2,
	"ecs.i2v-c1m2d1536.xlarge":   2,
	"ecs.i2v-c1m2d1024.xlarge":   2,
	"ecs.i2v-c1m2d500.xlarge":    2,
}
