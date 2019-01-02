package driver

import (
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
	"net"
)

func deleteRoutesForAddr(addr *net.IPNet, tableId int) error {
	routeList, err := netlink.RouteListFiltered(netlink.FAMILY_ALL, &netlink.Route{
		Dst: addr,
		Table: tableId,
	}, netlink.RT_FILTER_DST | netlink.RT_FILTER_TABLE)
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
func getRouteTableId(linkIndex int) int {
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