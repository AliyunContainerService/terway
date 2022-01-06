package link

import (
	"net"

	"github.com/pkg/errors"

	"github.com/AliyunContainerService/terway/pkg/windows/iface"
	"github.com/AliyunContainerService/terway/pkg/windows/ipforward"
)

// GetDeviceNumber get interface device number by mac address
func GetDeviceNumber(mac string) (int32, error) {
	macIface, err := iface.GetInterfaceByMAC(mac, true)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to get dev by MAC %s", mac)
	}
	return int32(macIface.Index), nil
}

// GetDeviceName get interface device name by mac address
func GetDeviceName(mac string) (string, error) {
	macIface, err := iface.GetInterfaceByMAC(mac, true)
	if err != nil {
		return "", errors.Wrapf(err, "failed to get interface by MAC %s", mac)
	}
	return macIface.Name, nil
}

// DeleteIPRulesByIP delete all ip rule related to the addr
func DeleteIPRulesByIP(addr *net.IPNet) error {
	var routes, err = ipforward.GetNetRoutes()
	if err != nil {
		return errors.Wrapf(err, "failed to get all routes")
	}
	for _, r := range routes {
		if ipNetEqual(addr, r.DestinationSubnet) ||
			ipNetEqual(addr, &net.IPNet{IP: r.NextHop, Mask: net.IPv4Mask(255, 255, 255, 255)}) {
			err = ipforward.RemoveNetRoutesForInterface(r.LinkIndex, r.DestinationSubnet, &r.NextHop)
			if err != nil {
				return errors.Wrapf(err, "failed to remove ip route %s(%s) on interface %d",
					r.DestinationSubnet, r.NextHop, r.LinkIndex)
			}
		}
	}
	return nil
}

// DeleteRouteByIP delete all route related to the addr
func DeleteRouteByIP(addr *net.IPNet) error {
	var routes, err = ipforward.GetNetRoutes()
	if err != nil {
		return errors.Wrapf(err, "failed to get all routes")
	}
	for _, r := range routes {
		if ipNetEqual(addr, r.DestinationSubnet) {
			err = ipforward.RemoveNetRoutesForInterface(r.LinkIndex, r.DestinationSubnet, &r.NextHop)
			if err != nil {
				return errors.Wrapf(err, "failed to remove route %s(%s) on interface %d",
					r.DestinationSubnet, r.NextHop, r.LinkIndex)
			}
		}
	}
	return nil
}
