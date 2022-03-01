//go:build !linux && !windows
// +build !linux,!windows

package link

import (
	"net"
)

// GetDeviceNumber get interface device number by mac address
func GetDeviceNumber(mac string) (int32, error) {
	return 0, ErrUnsupported
}

// GetDeviceName get interface device name by mac address
func GetDeviceName(mac string) (string, error) {
	return "", ErrUnsupported
}

// DeleteIPRulesByIP delete all ip rule related to the addr
func DeleteIPRulesByIP(addr *net.IPNet) error {
	return ErrUnsupported
}

// DeleteRouteByIP delete all route related to the addr
func DeleteRouteByIP(addr *net.IPNet) error {
	return ErrUnsupported
}
