//+build linux

package link

import (
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
)

// GetDeviceNumber get interface device number by mac address
func GetDeviceNumber(mac string) (int32, error) {
	linkList, err := netlink.LinkList()
	if err != nil {
		return 0, errors.Wrapf(err, "error get link list from netlink")
	}

	for _, link := range linkList {
		if link.Attrs().HardwareAddr.String() == mac {
			return int32(link.Attrs().Index), nil
		}
	}
	return 0, errors.Wrapf(ErrNotFound, "can't found dev by mac %s", mac)
}

// GetDeviceName get interface device name by mac address
func GetDeviceName(mac string) (string, error) {
	linkList, err := netlink.LinkList()
	if err != nil {
		return "", errors.Wrapf(err, "error get link list from netlink")
	}

	for _, link := range linkList {
		if link.Attrs().HardwareAddr.String() == mac {
			return link.Attrs().Name, nil
		}
	}
	return "", errors.Wrapf(ErrNotFound, "can't found dev by mac %s", mac)
}
