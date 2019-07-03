package driver

import (
	"fmt"
	"net"

	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
)

type ipvlanDriver struct {
}

//type Namespace interface {
//}

//const (
//	IPVLAN_L2  string = "nsL2"
//	IPVLAN_L3S string = "nsL3S"
//)

//func createNamespace(nsType string) (Namespace, error) {
//	var namespace Namespace

//	switch nsType {
//	case IPVLAN_L2:
//	case IPVLAN_L3S:
//	default:
//		return nil, errors.Wrapf(err, "failed IPVlan Type %v", nsType)
//	}
//}

const (
	ipvlanRouteMetric = 2000
)

func (driver *ipvlanDriver) Setup(
	hostIPVlan string,
	containerIPVlan string,
	ipv4Addr *net.IPNet,
	primaryIpv4Addr *net.IPNet,
	gateway net.IP,
	extraRoutes []*types.Route,
	deviceID int,
	ingress uint64,
	egress uint64,
	netNS ns.NetNS) error {
	var err error

	parentLink, err := netlink.LinkByIndex(deviceID)
	if err != nil {
		return errors.Wrapf(err, "IPVLAN get parent Link[%+v] error.", deviceID)
	}

	err = driver.ensureParent(parentLink, primaryIpv4Addr, gateway)
	if err != nil {
		return errors.Wrapf(err, "IPVLAN configure parent Link[%+v] error.", deviceID)
	}

	slaveIPVlan := netlink.IPVlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:        hostIPVlan,
			ParentIndex: deviceID,
			MTU:         MTU,
		},
		Mode: netlink.IPVLAN_MODE_L3S,
	}

	if err := netlink.LinkAdd(&slaveIPVlan); err != nil {
		return errors.Wrapf(err, "IPVLAN Link add error.")
	}
	defer func() {
		if err != nil {
			deferErr := netlink.LinkDel(&slaveIPVlan)
			if deferErr != nil {
				err = errors.Wrapf(err, "ns3Add netlink.LinkDel ipv err %+v", deferErr)
			}
		}
	}()

	slaveLink, err := netlink.LinkByName(hostIPVlan)
	if err != nil {
		return errors.Wrapf(err, "IPVLAN Link by name error.")
	}

	if err = netlink.LinkSetNsFd(slaveLink, int(netNS.Fd())); err != nil {
		return errors.Wrapf(err, "IPVLAN Link set ns fd error.")
	}
	//////////////////////////////////////////////////////////////////////
	// 2. setup addr and default route
	err = netNS.Do(func(netNS ns.NetNS) error {
		// remove equal ip net
		var linkList []netlink.Link
		linkList, err = netlink.LinkList()
		if err != nil {
			return errors.Wrapf(err, "IPVLAN Link list in ns error.")
		}

		for _, link := range linkList {
			var addrList []netlink.Addr
			addrList, err = netlink.AddrList(link, netlink.FAMILY_ALL)
			if err != nil {
				return errors.Wrapf(err, "IPVLAN Link addrlist in ns error.")
			}
			for _, addr := range addrList {
				if ipNetEqual(addr.IPNet, ipv4Addr) {
					err = netlink.AddrDel(link, &netlink.Addr{IPNet: ipv4Addr})
					if err != nil {
						return errors.Wrapf(err, "IPVLAN Link addrdel in ns error.")
					}
				}
			}
		}

		// 2.1 setup addr
		err = netlink.LinkSetName(slaveLink, containerIPVlan)
		if err != nil {
			return errors.Wrapf(err, "setup ipvlan link name failed in ns")
		}

		err = netlink.LinkSetUp(slaveLink)
		if err != nil {
			return errors.Wrapf(err, "setup set ipvlan link up in ns")
		}

		err = netlink.AddrAdd(slaveLink, &netlink.Addr{
			IPNet: ipv4Addr,
		})
		if err != nil {
			return errors.Wrapf(err, "setup add addr to link in ns")
		}

		var defaultRoutes []netlink.Route
		defaultRoutes, err = netlink.RouteListFiltered(netlink.FAMILY_ALL,
			&netlink.Route{
				Dst:   nil,
				Scope: netlink.SCOPE_UNIVERSE,
			}, netlink.RT_FILTER_DST|netlink.RT_FILTER_SCOPE)
		if err != nil {
			return errors.Wrapf(err, "error found conflict route for ipvlan")
		}
		for _, route := range defaultRoutes {
			err = netlink.RouteDel(&route)
			if err != nil {
				return errors.Wrapf(err, "error delete conflict route for nic")
			}
		}

		// 2.2 setup default route
		err = netlink.RouteAdd(&netlink.Route{
			LinkIndex: slaveLink.Attrs().Index,
			Scope:     netlink.SCOPE_UNIVERSE,
			Flags:     int(netlink.FLAG_ONLINK),
			Dst:       defaultRoute,
			Gw:        gateway,
		})
		if err != nil {
			return errors.Wrap(err, "error add route for ipvlan")
		}
		return nil
	})

	if err != nil {
		return errors.Wrapf(err, "cannot set ipvlan addr and default route")
	}

	return nil
}

func (driver *ipvlanDriver) Teardown(hostIPVlan string, containerIPVlan string, netNS ns.NetNS) error {
	err := netNS.Do(func(netNS ns.NetNS) error {
		var nicLink netlink.Link
		nicLink, err := netlink.LinkByName(containerIPVlan)
		if err == nil {
			err := netlink.LinkSetDown(nicLink)
			if err != nil {
				return errors.Wrapf(err, "error set link down")
			}
			err = netlink.LinkDel(nicLink)
			if err != nil {
				return errors.Wrapf(err, "error del ipvlan link: %v", containerIPVlan)
			}
		}
		return nil
	})
	if err != nil {
		return errors.Wrapf(err, "NicDriver, error move nic out")
	}
	return nil
}

func (driver *ipvlanDriver) ensureParent(link netlink.Link,
	ipv4Addr *net.IPNet, gateway net.IP) error {

	if ipv4Addr == nil || ipv4Addr.IP == nil || ipv4Addr.Mask == nil {
		return fmt.Errorf("invalid address to vlan parent, %+v", ipv4Addr)
	}
	if link.Attrs().OperState != netlink.OperUp {
		err := netlink.LinkSetUp(link)
		if err != nil {
			return err
		}
	}

	addrList, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return err
	}

	foundIP := false
	for _, addr := range addrList {
		if addr.Equal(netlink.Addr{IPNet: ipv4Addr}) {
			foundIP = true
		} else {
			err = netlink.AddrDel(link, &addr)
			if err != nil {
				return err
			}
		}
	}
	//set master addr which equal with slave net device
	if !foundIP {
		err = netlink.AddrAdd(link, &netlink.Addr{IPNet: ipv4Addr})
		if err != nil {
			return err
		}
	}
	//delete link old default route
	var defaultRoutes []netlink.Route
	metric := int(ipvlanRouteMetric) + link.Attrs().Index

	defaultRoutes, err = netlink.RouteListFiltered(netlink.FAMILY_ALL,
		&netlink.Route{
			LinkIndex: link.Attrs().Index,
		}, netlink.RT_FILTER_OIF)
	if err != nil {
		return errors.Wrapf(err, "error found conflict route for master nic")
	}
	foundRoute := false
	for _, route := range defaultRoutes {
		if route.LinkIndex == link.Attrs().Index && route.Priority != metric {
			var metricRoute netlink.Route = route
			if err = netlink.RouteDel(&route); err != nil {
				return errors.Wrapf(err, "error del route priority for ipvlan parent: %+v", route)
			}
			metricRoute.Priority = metric
			if err = netlink.RouteAdd(&metricRoute); err != nil {
				return errors.Wrapf(err, "error add route priority for ipvlan parent: %+v", metricRoute)
			}
		}
		if route.LinkIndex == link.Attrs().Index &&
			(ipNetEqual(route.Dst, defaultRoute) || route.Dst == nil) {
			foundRoute = true
		}
	} //set master route
	if !foundRoute {
		err = netlink.RouteAdd(&netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_UNIVERSE,
			Flags:     int(netlink.FLAG_ONLINK),
			Dst:       defaultRoute,
			Gw:        gateway,
			Priority:  metric,
		})
		if err != nil {
			return errors.Wrapf(err, "error add route for master nic %+v,%+v",
				link.Attrs().Index, defaultRoutes)
		}
	}
	return nil
}
