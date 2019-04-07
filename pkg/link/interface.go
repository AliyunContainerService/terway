//+build linux

package link

import (
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
)

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
	return 0, errors.Errorf("cannot found mac address: %s", mac)
}

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
	return "", errors.Errorf("cannot found mac address: %s", mac)
}
