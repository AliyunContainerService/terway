package driver

import (
	"encoding/binary"
	"fmt"
	"net"

	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

type ipvlanDriver struct {
	name string
}

func newIPVlanDriver() *ipvlanDriver {
	return &ipvlanDriver{
		name: "IPVLanL2",
	}
}

func (driver *ipvlanDriver) Setup(
	hostIPVlan string,
	containerIPVlan string,
	ipv4Addr *net.IPNet,
	primaryIpv4Addr net.IP,
	serviceCIDR *net.IPNet,
	gateway net.IP,
	extraRoutes []*types.Route,
	deviceID int,
	ingress uint64,
	egress uint64,
	netNS ns.NetNS) error {
	var err error

	parentLink, err := netlink.LinkByIndex(deviceID)
	if err != nil {
		return errors.Wrapf(err, "%s, get device by index %d error.", driver.name, deviceID)
	}
	if parentLink.Attrs().OperState != netlink.OperUp {
		if err := netlink.LinkSetUp(parentLink); err != nil {
			return errors.Wrapf(err, "%s, set device %s up error", driver.name, parentLink.Attrs().Name)
		}
	}

	slaveIPVlan := &netlink.IPVlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:        hostIPVlan,
			ParentIndex: deviceID,
			MTU:         MTU,
		},
		Mode: netlink.IPVLAN_MODE_L2,
	}

	if err := netlink.LinkAdd(slaveIPVlan); err != nil {
		return errors.Wrapf(err, "%s, add ipvlan slave device %s error.", driver.name, hostIPVlan)
	}

	slaveLink, err := netlink.LinkByName(hostIPVlan)
	if err != nil {
		return errors.Wrapf(err, "%s, get link for %s error.", driver.name, hostIPVlan)
	}

	if err = netlink.LinkSetNsFd(slaveLink, int(netNS.Fd())); err != nil {
		return errors.Wrapf(err, "%s, set %s to constainer error.", driver.name, slaveLink.Attrs().Name)
	}

	// 2. setup addr and default route
	err = netNS.Do(func(netNS ns.NetNS) error {
		name := fmt.Sprintf("%s.%s", driver.name, "container")
		// remove equal ip net
		linkList, err := netlink.LinkList()
		if err != nil {
			return errors.Wrapf(err, "%s, list devices error.", name)
		}

		for _, link := range linkList {
			if link.Attrs().Name == hostIPVlan {
				if err := netlink.LinkSetName(link, containerIPVlan); err != nil {
					return errors.Wrapf(err, "%s, rename device name from %s to %s error", name, hostIPVlan, containerIPVlan)
				}
			}

			addrList, err := netlink.AddrList(link, netlink.FAMILY_ALL)
			if err != nil {
				return errors.Wrapf(err, "%s, list addr for device %s error", name, link.Attrs().Name)
			}
			for _, addr := range addrList {
				if addr.IP.Equal(ipv4Addr.IP) {
					if err = netlink.AddrDel(link, &netlink.Addr{IPNet: ipv4Addr}); err != nil {
						return errors.Wrapf(err, "%s, delete old addaress %s error.", name, addr.IP)
					}
				}
			}
		}

		defLink, err := netlink.LinkByName(containerIPVlan)
		if err != nil {
			return errors.Wrapf(err, "%s, get device %s error", name, defLink.Attrs().Name)
		}

		if err := netlink.LinkSetUp(defLink); err != nil {
			return errors.Wrapf(err, "%s, set device %s up error", name, defLink.Attrs().Name)
		}

		if err := netlink.AddrAdd(slaveLink, &netlink.Addr{IPNet: ipv4Addr}); err != nil {
			return errors.Wrapf(err, "add address %v to device %s error", ipv4Addr, defLink.Attrs().Name)
		}

		// 2.2 setup default route
		err = netlink.RouteReplace(&netlink.Route{
			LinkIndex: slaveLink.Attrs().Index,
			Scope:     netlink.SCOPE_UNIVERSE,
			Flags:     int(netlink.FLAG_ONLINK),
			Dst:       defaultRoute,
			Gw:        gateway,
		})
		if err != nil {
			return errors.Wrapf(err, "%s, add default route via %s error", name, gateway)
		}
		return nil
	})

	if err != nil {
		return errors.Wrapf(err, "%s, set container link/address/route error", driver.name)
	}

	if err := driver.setupInitNamespace(parentLink, primaryIpv4Addr, ipv4Addr, serviceCIDR); err != nil {
		return errors.Wrapf(err, "%s, configure init namespace error.", driver.name)
	}

	return nil
}

func (driver *ipvlanDriver) Teardown(hostIPVlan string,
	containerIPVlan string,
	netNS ns.NetNS,
	containerIP net.IP) error {
	parents := make(map[int]struct{})

	if link, err := netlink.LinkByName(hostIPVlan); err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); !ok {
			return errors.Wrapf(err, "%s, list device %s error", driver.name, hostIPVlan)
		}
	} else {
		parents[link.Attrs().ParentIndex] = struct{}{}
		err := netlink.LinkSetDown(link)
		if e := netlink.LinkDel(link); e != nil {
			return errors.Wrapf(e, "%s, delete device %s error with another down device error %v",
				driver.name, hostIPVlan, err)
		}
	}

	err := netNS.Do(func(netNS ns.NetNS) error {
		name := fmt.Sprintf("%s.%s", driver.name, "container")

		for _, ifName := range []string{hostIPVlan, containerIPVlan} {
			link, err := netlink.LinkByName(ifName)
			if err != nil {
				if _, ok := err.(netlink.LinkNotFoundError); !ok {
					return errors.Wrapf(err, "%s, list device %s error.", name, ifName)
				}
				continue
			}

			parents[link.Attrs().ParentIndex] = struct{}{}
			err = netlink.LinkSetDown(link)
			if e := netlink.LinkDel(link); e != nil {
				return errors.Wrapf(e, "%s, delete device %s error with another down device error %v.",
					name, ifName, err)
			}
		}
		return nil
	})
	if err != nil {
		return errors.Wrapf(err, "%s, clear container network error", driver.name)
	}

	delete(parents, 0)
	return driver.teardownInitNamespace(parents, containerIP)
}

func (driver *ipvlanDriver) createSlaveIfNotExist(parentLink netlink.Link, slaveName string) (netlink.Link, error) {
	slaveLink, err := netlink.LinkByName(slaveName)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); !ok {
			return nil, errors.Wrapf(err, "%s, get device %s error", driver.name, slaveName)
		}
	} else {
		return slaveLink, nil
	}

	err = netlink.LinkAdd(&netlink.IPVlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:        slaveName,
			ParentIndex: parentLink.Attrs().Index,
			MTU:         MTU,
		},
		Mode: netlink.IPVLAN_MODE_L2,
	})
	link, e := netlink.LinkByName(slaveName)
	if e != nil {
		return nil, errors.Wrapf(err, "%s, add device %s error with another list device error %v",
			driver.name, slaveName, e)
	}
	return link, nil
}

func (driver *ipvlanDriver) setAddressIfNotExist(link netlink.Link, ip *net.IPNet, scope netlink.Scope) error {
	new := &netlink.Addr{
		IPNet: ip,
		Scope: int(scope),
	}

	addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return errors.Wrapf(err, "%s, list address for device %s error", driver.name, link.Attrs().Name)
	}

	found := false
	for _, old := range addrs {
		if ipNetEqual(old.IPNet, new.IPNet) && old.Scope == new.Scope {
			found = true
			continue
		}
		if err := netlink.AddrDel(link, &old); err != nil {
			return errors.Wrapf(err, "%s, delete address %s of device %s error",
				driver.name, old.IPNet, link.Attrs().Name)
		}
	}

	if found {
		return nil
	}

	if err := netlink.AddrReplace(link, new); err != nil {
		return errors.Wrapf(err, "%s, replace addr %s for device %s error",
			driver.name, new.IPNet, link.Attrs().Name)
	}
	return nil
}

func (driver *ipvlanDriver) setupRouteIfNotExist(dst *net.IPNet, link netlink.Link) error {
	new := &netlink.Route{
		Protocol:  netlink.FAMILY_V4,
		LinkIndex: link.Attrs().Index,
		Scope:     netlink.SCOPE_LINK,
		Dst:       dst,
	}

	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, new, netlink.RT_FILTER_DST|netlink.RT_FILTER_OIF)
	if err != nil {
		return errors.Wrapf(err, "%s, filter route dst %s dev %s error",
			driver.name, dst, link.Attrs().Name)
	}

	if len(routes) != 0 {
		return nil
	}

	if err := netlink.RouteReplace(new); err != nil {
		return errors.Wrapf(err, "%s, replace route %s dev %s error",
			driver.name, dst, link.Attrs().Name)
	}
	return nil
}

func (driver *ipvlanDriver) setupClsActQsic(link netlink.Link) error {
	qds, err := netlink.QdiscList(link)
	if err != nil {
		return errors.Wrapf(err, "%s, list qdisc for dev %s error", driver.name, link.Attrs().Name)
	}
	for _, q := range qds {
		if q.Type() == "clsact" {
			return nil
		}
	}

	qdisc := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: link.Attrs().Index,
			Parent:    netlink.HANDLE_CLSACT,
			Handle:    netlink.HANDLE_CLSACT & 0xffff0000,
		},
		QdiscType: "clsact",
	}
	if err := netlink.QdiscReplace(qdisc); err != nil {
		return errors.Wrapf(err, "%s, replace clsact qdisc for dev %s error", driver.name, link.Attrs().Name)
	}
	return nil
}

type redirectRule struct {
	index    int
	proto    uint16
	offset   int32
	value    uint32
	mask     uint32
	redir    netlink.MirredAct
	dstIndex int
}

func dstIPRule(index int, ip *net.IPNet, dstIndex int, redir netlink.MirredAct) (*redirectRule, error) {
	v4 := ip.IP.Mask(ip.Mask).To4()
	if v4 == nil {
		return nil, fmt.Errorf("only support ipv4")
	}

	v4Mask := net.IP(ip.Mask).To4()
	if v4Mask == nil {
		return nil, fmt.Errorf("only support ipv4")
	}

	return &redirectRule{
		index:    index,
		proto:    unix.ETH_P_IP,
		offset:   16,
		value:    binary.BigEndian.Uint32(v4),
		mask:     binary.BigEndian.Uint32(v4Mask),
		redir:    redir,
		dstIndex: dstIndex,
	}, nil
}

func (rule *redirectRule) isMatch(filter netlink.Filter) bool {
	u32, ok := filter.(*netlink.U32)
	if !ok {
		return false
	}
	if u32.Attrs().LinkIndex != rule.index || u32.Attrs().Protocol != rule.proto {
		return false
	}

	if len(u32.Sel.Keys) != 1 {
		return false
	}

	key := u32.Sel.Keys[0]
	if key.Mask != rule.mask || key.Off != rule.offset || key.Val != rule.value {
		return false
	}

	return rule.isMatchActions(u32.Actions)
}

func (rule *redirectRule) isMatchActions(acts []netlink.Action) bool {
	if len(acts) != 3 {
		return false
	}

	tun, ok := acts[0].(*netlink.TunnelKeyAction)
	if !ok {
		return false
	}
	if tun.Attrs().Action != netlink.TC_ACT_PIPE {
		return false
	}
	if tun.Action != netlink.TCA_TUNNEL_KEY_UNSET {
		return false
	}

	skbedit, ok := acts[1].(*netlink.SkbEditAction)
	if !ok {
		return false
	}
	if skbedit.Attrs().Action != netlink.TC_ACT_PIPE {
		return false
	}
	if skbedit.PType == nil || *skbedit.PType != uint16(unix.PACKET_HOST) {
		return false
	}

	mirred, ok := acts[2].(*netlink.MirredAction)
	if !ok {
		return false
	}
	if mirred.Attrs().Action != netlink.TC_ACT_STOLEN {
		return false
	}
	if mirred.MirredAction != rule.redir {
		return false
	}
	if mirred.Ifindex != rule.dstIndex {
		return false
	}

	return true
}

func (rule *redirectRule) toActions() []netlink.Action {
	mirredAct := netlink.NewMirredAction(rule.dstIndex)
	mirredAct.MirredAction = netlink.TCA_INGRESS_REDIR

	tunAct := netlink.NewTunnelKeyAction()
	tunAct.Action = netlink.TCA_TUNNEL_KEY_UNSET

	skbedit := netlink.NewSkbEditAction()
	ptype := uint16(unix.PACKET_HOST)
	skbedit.PType = &ptype

	return []netlink.Action{tunAct, skbedit, mirredAct}
}

func (rule *redirectRule) toU32Filter() *netlink.U32 {
	return &netlink.U32{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: rule.index,
			Priority:  40000,
			Protocol:  rule.proto,
		},
		Sel: &netlink.TcU32Sel{
			Nkeys: 1,
			Flags: nl.TC_U32_TERMINAL,
			Keys: []netlink.TcU32Key{
				{
					Mask: rule.mask,
					Val:  rule.value,
					Off:  rule.offset,
				},
			},
		},
		Actions: rule.toActions(),
	}
}

func (driver *ipvlanDriver) setupFilters(link netlink.Link, ip *net.IPNet, dstIndex int) error {
	parent := uint32(netlink.HANDLE_CLSACT&0xffff0000 | netlink.HANDLE_MIN_EGRESS&0x0000ffff)
	filters, err := netlink.FilterList(link, parent)
	if err != nil {
		return errors.Wrapf(err, "%s, list egress filter for %s error", driver.name, link.Attrs().Name)
	}

	rule, err := dstIPRule(link.Attrs().Index, ip, dstIndex, netlink.TCA_INGRESS_REDIR)
	if err != nil {
		return errors.Wrapf(err, "%s, create redirect rule error", driver.name)
	}

	found := false
	for _, filter := range filters {
		if rule.isMatch(filter) {
			found = true
			continue
		}
		if err := netlink.FilterDel(filter); err != nil {
			return errors.Wrapf(err, "%s, delete filter of %s error", driver.name, link.Attrs().Name)
		}
	}

	if found {
		return nil
	}

	u32 := rule.toU32Filter()
	u32.Parent = parent
	if err := netlink.FilterAdd(u32); err != nil {
		return errors.Wrapf(err, "%s, add filter for %s error", driver.name, link.Attrs().Name)
	}

	return nil
}

func (driver *ipvlanDriver) setupInitNamespace(
	parentLink netlink.Link,
	hostIP net.IP,
	containerIP *net.IPNet,
	serviceCIDR *net.IPNet,
) error {
	// setup slave nic
	slaveName := driver.initSlaveName(parentLink.Attrs().Index)
	slaveLink, err := driver.createSlaveIfNotExist(parentLink, slaveName)
	if err != nil {
		return err
	}
	if slaveLink.Attrs().OperState != netlink.OperUp {
		if err := netlink.LinkSetUp(slaveLink); err != nil {
			return errors.Wrapf(err, "%s, set device %s up error", driver.name, slaveLink.Attrs().Name)
		}
	}

	ipNet := &net.IPNet{
		IP:   hostIP,
		Mask: net.CIDRMask(32, 32),
	}
	if err := driver.setAddressIfNotExist(slaveLink, ipNet, netlink.SCOPE_HOST); err != nil {
		return err
	}

	// check tc rule
	if err := driver.setupClsActQsic(parentLink); err != nil {
		return err
	}
	if err := driver.setupFilters(parentLink, serviceCIDR, slaveLink.Attrs().Index); err != nil {
		return err
	}

	dst := &net.IPNet{
		IP:   containerIP.IP,
		Mask: net.CIDRMask(32, 32),
	}
	if err := driver.setupRouteIfNotExist(dst, slaveLink); err != nil {
		return err
	}

	return nil
}

func (driver *ipvlanDriver) teardownInitNamespace(parents map[int]struct{}, containerIP net.IP) error {
	if containerIP == nil {
		return nil
	}
	// get slave link
	for index := range parents {
		initLink, err := driver.initSlaveLink(index)
		if err != nil {
			if _, ok := err.(netlink.LinkNotFoundError); !ok {
				return errors.Wrapf(err, "%s, get slave device %s error", driver.name, driver.initSlaveName(index))
			}
			continue
		}

		rt := &netlink.Route{
			LinkIndex: initLink.Attrs().Index,
			Dst: &net.IPNet{
				IP:   containerIP,
				Mask: net.CIDRMask(32, 32),
			},
		}
		routes, err := netlink.RouteListFiltered(netlink.FAMILY_ALL, rt, netlink.RT_FILTER_DST|netlink.RT_FILTER_OIF)
		if err != nil {
			return errors.Wrapf(err, "%s, list route %s dev %s error", driver.name, containerIP, initLink.Attrs().Name)
		}
		for _, route := range routes {
			if err := netlink.RouteDel(&route); err != nil {
				return errors.Wrapf(err, "%s, delete route %s dev %s error", driver.name, containerIP, initLink.Attrs().Name)
			}
		}
	}
	return nil
}

func (driver *ipvlanDriver) initSlaveName(parentIndex int) string {
	return fmt.Sprintf("ipvl_%d", parentIndex)
}

func (driver *ipvlanDriver) initSlaveLink(parentIndex int) (netlink.Link, error) {
	return netlink.LinkByName(driver.initSlaveName(parentIndex))
}
