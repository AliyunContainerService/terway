//go:build privileged

package datapath

import (
	"net"

	terwayTypes "github.com/AliyunContainerService/terway/types"
	"github.com/vishvananda/netlink"
)

var containerIPNet = &net.IPNet{
	IP:   net.ParseIP("169.10.0.10"),
	Mask: net.CIDRMask(24, 32),
}

var containerIPNetIPv6 = &net.IPNet{
	IP:   net.ParseIP("fd00:10::10"),
	Mask: net.CIDRMask(120, 128),
}

var eth0IPNet = &net.IPNet{
	IP:   net.ParseIP("169.20.0.10"),
	Mask: net.CIDRMask(24, 32),
}

var eth0IPNetIPv6 = &net.IPNet{
	IP:   net.ParseIP("fd00:20::10"),
	Mask: net.CIDRMask(120, 128),
}

var ipv4GW = net.ParseIP("169.10.0.253")
var ipv6GW = net.ParseIP("fd00:10::fffd")

var serviceCIDR = &net.IPNet{
	IP:   net.ParseIP("169.30.0.0"),
	Mask: net.CIDRMask(24, 32),
}

var serviceCIDRIPv6 = &net.IPNet{
	IP:   net.ParseIP("fd00:30::0"),
	Mask: net.CIDRMask(120, 128),
}

func FindIP(link netlink.Link, ipNetSet *terwayTypes.IPNetSet) (bool, error) {
	exec := func(ipNet *net.IPNet, family int) (bool, error) {
		addrList, err := netlink.AddrList(link, family)
		if err != nil {
			return false, err
		}
		for _, addr := range addrList {
			if addr.IPNet.String() == ipNet.String() {
				return true, nil
			}
		}
		return false, nil
	}
	if ipNetSet.IPv4 != nil {
		ok, err := exec(ipNetSet.IPv4, netlink.FAMILY_V4)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	if ipNetSet.IPv6 != nil {
		ok, err := exec(ipNetSet.IPv6, netlink.FAMILY_V6)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func FindNeigh(link netlink.Link, ip net.IP, lladdr net.HardwareAddr) (bool, error) {
	neighs, err := netlink.NeighList(link.Attrs().Index, netlink.FAMILY_ALL)
	if err != nil {
		return false, err
	}

	for _, neigh := range neighs {
		if neigh.IP.Equal(ip) && neigh.HardwareAddr.String() == lladdr.String() {
			return true, nil
		}
	}
	return false, nil
}
