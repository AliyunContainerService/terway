package driver

import (
	"fmt"
	"github.com/containernetworking/plugins/pkg/utils/sysctl"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
	"net"
)

func deleteRoutesForAddr(addr *net.IPNet, tableID int) error {
	routeList, err := netlink.RouteListFiltered(netlink.FAMILY_ALL, &netlink.Route{
		Dst:   addr,
		Table: tableID,
	}, netlink.RT_FILTER_DST|netlink.RT_FILTER_TABLE)
	if err != nil {
		return errors.Wrapf(err, "error get route list")
	}

	for _, route := range routeList {
		err = netlink.RouteDel(&route)
		if err != nil {
			return errors.Wrapf(err, "error cleanup route: %v", route)
		}
	}
	return nil
}

// add 1000 to link index to avoid route table conflict
func getRouteTableID(linkIndex int) int {
	return 1000 + linkIndex
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

const rpFilterSysctl = "net.ipv4.conf.%s.rp_filter"
// EnsureHostNsConfig setup host namespace configs
func EnsureHostNsConfig() error {
	existInterfaces, err := net.Interfaces()
	if err != nil {
		return errors.Wrapf(err, "error get exist interfaces on system")
	}

	for _, key := range []string{"default", "all"} {
		sysctlName := fmt.Sprintf(rpFilterSysctl, key)
		if _, err = sysctl.Sysctl(sysctlName, "0"); err != nil {
			return errors.Wrapf(err, "error set: %s sysctl value to 0", sysctlName)
		}
	}

	for _, existIf := range existInterfaces {
		sysctlName := fmt.Sprintf(rpFilterSysctl, existIf.Name)
		sysctlValue, err := sysctl.Sysctl(sysctlName)
		if err != nil {
			continue
		}
		if sysctlValue != "0" {
			if _, err = sysctl.Sysctl(sysctlName, "0"); err != nil {
				return errors.Wrapf(err, "error set: %s sysctl value to 0", sysctlName)
			}
		}
	}
	return nil
}