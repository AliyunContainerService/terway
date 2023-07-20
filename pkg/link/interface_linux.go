//go:build linux

package link

import (
	"fmt"
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
		return 0, fmt.Errorf("error get link list from netlink, %w", err)
	}

	for _, link := range linkList {
		// ignore virtual nic type. eg. ipvlan veth bridge
		if _, ok := link.(*netlink.Device); !ok {
			continue
		}
		if link.Attrs().HardwareAddr.String() == mac {
			return int32(link.Attrs().Index), nil
		}
	}
	return 0, errors.Wrapf(ErrNotFound, "can't found dev by mac %s", mac)
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
