package eni

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/AliyunContainerService/terway/pkg/aliyun/instance"
	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

// ENIInfoGetter interface to get eni information
type ENIInfoGetter interface {
	GetENIPrivateAddressesByMACv2(mac string) ([]netip.Addr, error)
	GetENIPrivateIPv6AddressesByMACv2(mac string) ([]netip.Addr, error)

	GetENIs(containsMainENI bool) ([]*daemon.ENI, error)
}

type ENIMetadata struct {
	ipv4, ipv6 bool
}

func NewENIMetadata(ipv4, ipv6 bool) *ENIMetadata {
	return &ENIMetadata{
		ipv4: ipv4,
		ipv6: ipv6,
	}
}

func (e *ENIMetadata) GetENIByMac(mac string) (*daemon.ENI, error) {
	eni := daemon.ENI{
		MAC: mac,
	}
	var err error

	eni.ID, err = metadata.GetENIID(mac)
	if err != nil {
		return nil, fmt.Errorf("error get eni by id %s, %w", eni.ID, err)
	}

	ip, err := metadata.GetENIPrimaryIP(mac)
	if err != nil {
		return nil, fmt.Errorf("error get eni primary ip by %s, %w", mac, err)
	}

	eni.PrimaryIP = types.IPSet{
		IPv4: ip,
	}
	gw, err := metadata.GetENIGateway(mac)
	if err != nil {
		return nil, fmt.Errorf("error get eni gateway by mac %s, %w", mac, err)
	}

	vSwitchCIDR, err := metadata.GetVSwitchCIDR(mac)
	if err != nil {
		return nil, fmt.Errorf("error get eni vSwitchCIDR from metaserver, mac: %s, %w", mac, err)
	}

	var v6gw net.IP
	var vSwitchIPv6CIDR *net.IPNet
	if e.ipv6 {
		v6gw, err = metadata.GetENIV6Gateway(mac)
		if err != nil {
			return nil, fmt.Errorf("error get eni ipv6 gateway from metaserver, mac: %s, %w", mac, err)
		}

		vSwitchIPv6CIDR, err = metadata.GetVSwitchIPv6CIDR(mac)
		if err != nil {
			return nil, fmt.Errorf("error get eni vSwitchIPv6CIDR from metaserver, mac: %s, %w", mac, err)
		}
	}

	eni.VSwitchCIDR = types.IPNetSet{
		IPv4: vSwitchCIDR,
		IPv6: vSwitchIPv6CIDR,
	}

	eni.GatewayIP = types.IPSet{
		IPv4: gw,
		IPv6: v6gw,
	}

	vswitch, err := metadata.GetENIVSwitchID(mac)
	if err != nil {
		return nil, fmt.Errorf("error get eni vswitch from metaserver, mac: %s, %w", mac, err)
	}
	eni.VSwitchID = vswitch

	return &eni, nil
}

func (e *ENIMetadata) GetENIPrivateAddressesByMACv2(mac string) ([]netip.Addr, error) {
	return metadata.GetIPv4ByMac(mac)
}

func (e *ENIMetadata) GetENIPrivateIPv6AddressesByMACv2(mac string) ([]netip.Addr, error) {
	return metadata.GetIPv6ByMac(mac)
}

func (e *ENIMetadata) GetENIs(containsMainENI bool) ([]*daemon.ENI, error) {
	var enis []*daemon.ENI

	mainENIMac, err := instance.GetInstanceMeta().GetPrimaryMAC()
	if err != nil {
		return nil, err
	}

	macs, err := metadata.GetENIsMAC()
	if err != nil {
		return nil, err
	}
	for _, mac := range macs {
		if !containsMainENI {
			if mac == mainENIMac {
				continue
			}
		}
		eni, err := e.GetENIByMac(mac)
		if err != nil {
			return nil, err
		}
		enis = append(enis, eni)
	}
	return enis, nil
}
