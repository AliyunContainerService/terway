//+build linux

package link

import (
	"net"
	"os"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
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

// DeleteIPRulesByIP delete all ip rule related to the addr
func DeleteIPRulesByIP(addr *net.IPNet) error {
	family := netlink.FAMILY_V4
	if addr.IP.To4() == nil {
		family = netlink.FAMILY_V6
	}
	rules, err := netlink.RuleList(family)
	if err != nil {
		return err
	}
	for _, r := range rules {
		if ipNetEqual(addr, r.Src) || ipNetEqual(addr, r.Dst) {
			log.Infof("del ip rule %s", r.String())
			err := netlink.RuleDel(&r)
			if err == nil {
				continue
			}
			if os.IsNotExist(err) {
				// keep the old behave
				r.IifName = ""
				_ = netlink.RuleDel(&r)
			}
			return err
		}
	}
	return nil
}

// DeleteRouteByIP delete all route related to the addr
func DeleteRouteByIP(addr *net.IPNet) error {
	family := netlink.FAMILY_V4
	if addr.IP.To4() == nil {
		family = netlink.FAMILY_V6
	}
	routes, err := netlink.RouteList(nil, family)
	if err != nil {
		return err
	}
	for _, r := range routes {
		if r.Dst != nil && r.Dst.IP.Equal(addr.IP) {
			log.Infof("del route %s", r.String())
			err := netlink.RouteDel(&r)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// ipNetEqual returns true iff both IPNet are equal
func ipNetEqual(ipn1 *net.IPNet, ipn2 *net.IPNet) bool {
	if ipn1 == ipn2 {
		return true
	}
	if ipn1 == nil || ipn2 == nil {
		return false
	}
	m1, _ := ipn1.Mask.Size()
	m2, _ := ipn2.Mask.Size()
	return m1 == m2 && ipn1.IP.Equal(ipn2.IP)
}
