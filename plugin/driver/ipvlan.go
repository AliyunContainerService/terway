package driver

import (
	"encoding/binary"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"syscall"

	terwaySysctl "github.com/AliyunContainerService/terway/pkg/sysctl"
	terwayTypes "github.com/AliyunContainerService/terway/types"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

const (
	ipVlanRequirementMajor = 4
	ipVlanRequirementMinor = 19
)

var (
	regexKernelVersion = regexp.MustCompile(`^(\d+)\.(\d+)`)
)

type IPvlanDriver struct {
	name string
	ipv4 bool
	ipv6 bool
}

func NewIPVlanDriver(ipv4, ipv6 bool) *IPvlanDriver {
	return &IPvlanDriver{
		name: "IPVLanL2",
		ipv4: ipv4,
		ipv6: ipv6,
	}
}

func (d *IPvlanDriver) Setup(cfg *SetupConfig, netNS ns.NetNS) error {
	var err error

	parentLink, err := netlink.LinkByIndex(cfg.ENIIndex)
	if err != nil {
		return fmt.Errorf("error get eni by index %d, %w", cfg.ENIIndex, err)
	}
	_, err = EnsureLinkUp(parentLink)
	if err != nil {
		return err
	}
	_, err = EnsureLinkMTU(parentLink, cfg.MTU)
	if err != nil {
		return err
	}

	if d.ipv6 {
		_, err = EnsureRoute(&netlink.Route{
			LinkIndex: parentLink.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst: &net.IPNet{
				IP:   cfg.GatewayIP.IPv6,
				Mask: net.CIDRMask(128, 128),
			},
		})
		if err != nil {
			return err
		}
		_ = terwaySysctl.EnsureConf(fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/accept_ra", parentLink.Attrs().Name), "0")
		_ = terwaySysctl.EnsureConf(fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/forwarding", parentLink.Attrs().Name), "1")
	}

	err = LinkAdd(&netlink.IPVlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:        cfg.HostVETHName,
			ParentIndex: cfg.ENIIndex,
			MTU:         cfg.MTU,
		},
		Mode: netlink.IPVLAN_MODE_L2,
	})
	if err != nil {
		return err
	}

	slaveLink, err := netlink.LinkByName(cfg.HostVETHName)
	if err != nil {
		return fmt.Errorf("error find ipvlan link %s, %w", cfg.HostVETHName, err)
	}

	err = LinkSetNsFd(slaveLink, netNS)
	if err != nil {
		return err
	}

	// 2. setup addr and default route
	err = netNS.Do(func(netNS ns.NetNS) error {
		if d.ipv6 {
			err := EnableIPv6()
			if err != nil {
				return err
			}
		}

		linkList, err := netlink.LinkList()
		if err != nil {
			return fmt.Errorf("error list links, %w", err)
		}

		// accept_ra
		for _, link := range linkList {
			if link.Attrs().Name != cfg.HostVETHName {
				continue
			}
			err = SetupLink(link, cfg)
			if err != nil {
				return err
			}

			if d.ipv6 {
				_ = terwaySysctl.EnsureConf(fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/accept_ra", cfg.ContainerIfName), "0")
			}

			_, err = EnsureDefaultRoute(link, cfg.GatewayIP, unix.RT_TABLE_MAIN)
			if err != nil {
				return err
			}

			// setup route to host ipvlan interface
			_, err = EnsureHostToContainerRoute(link, cfg.HostIPSet)
			if err != nil {
				return fmt.Errorf("add route to host %s error, %w", cfg.HostIPSet, err)
			}

			// set host ipvlan interface mac in ARP table
			_, err = EnsureNeighbor(link, cfg.HostIPSet)
			return err
		}
		return err
	})

	if err != nil {
		return fmt.Errorf("error set container link/address/route, %w", err)
	}

	if err := d.setupInitNamespace(parentLink, cfg); err != nil {
		return fmt.Errorf("error set init namespace, %w", err)
	}

	return nil
}

func (d *IPvlanDriver) Teardown(cfg *TeardownCfg, netNS ns.NetNS) error {
	parents := make(map[int]struct{})
	link, err := netlink.LinkByName(cfg.HostVETHName)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); !ok {
			return fmt.Errorf("error get link %s, %w", cfg.HostVETHName, err)
		}
	} else {
		parents[link.Attrs().ParentIndex] = struct{}{}
		_ = LinkSetDown(link)
		err = LinkDel(link)
		if err != nil {
			return fmt.Errorf("error del link, %w", err)
		}
	}
	err = netNS.Do(func(netNS ns.NetNS) error {
		for _, ifName := range []string{cfg.HostVETHName, cfg.ContainerIfName} {
			link, err := netlink.LinkByName(ifName)
			if err != nil {
				if _, ok := err.(netlink.LinkNotFoundError); !ok {
					return fmt.Errorf("error get link %s, %w", ifName, err)
				}
				continue
			}

			parents[link.Attrs().ParentIndex] = struct{}{}
			_ = LinkSetDown(link)
			err = LinkDel(link)
			if err != nil {
				return fmt.Errorf("error del link, %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("error teardown container, %w", err)
	}

	delete(parents, 0)
	return d.teardownInitNamespace(parents, cfg.ContainerIPNet)
}

func (d *IPvlanDriver) Check(cfg *CheckConfig) error {
	parentLinkIndex := 0
	// 1. check addr and default route
	err := cfg.NetNS.Do(func(netNS ns.NetNS) error {
		link, err := netlink.LinkByName(cfg.ContainerIFName)
		if err != nil {
			return err
		}
		parentLinkIndex = link.Attrs().ParentIndex
		changed, err := EnsureLinkUp(link)
		if err != nil {
			return err
		}

		if changed {
			cfg.RecordPodEvent(fmt.Sprintf("link %s set to up", cfg.ContainerIFName))
		}

		changed, err = EnsureLinkMTU(link, cfg.MTU)
		if err != nil {
			return err
		}

		if changed {
			cfg.RecordPodEvent(fmt.Sprintf("link %s set mtu to %v", cfg.ContainerIFName, cfg.MTU))
		}

		changed, err = EnsureDefaultRoute(link, cfg.GatewayIP, unix.RT_TABLE_MAIN)
		if err != nil {
			return err
		}
		if changed {
			Log.Debugf("route is changed")
			cfg.RecordPodEvent("default route is updated")
		}

		return EnsureNetConfSet(true, false)
	})
	if err != nil {
		if _, ok := err.(ns.NSPathNotExistErr); ok {
			return nil
		}
		return err
	}
	// 2. check parent link ( this is called in every setup it is safe)
	Log.Debugf("parent link is %d", parentLinkIndex)
	parentLink, err := netlink.LinkByIndex(parentLinkIndex)
	if err != nil {
		return errors.Wrapf(err, "%s, get device by index %d error.", d.name, cfg.ENIIndex)
	}
	changed, err := EnsureLinkUp(parentLink)
	if err != nil {
		return err
	}
	if changed {
		cfg.RecordPodEvent(fmt.Sprintf("parent link id %d set to up", int(cfg.ENIIndex)))
	}
	changed, err = EnsureLinkMTU(parentLink, cfg.MTU)
	if err != nil {
		return err
	}

	if changed {
		cfg.RecordPodEvent(fmt.Sprintf("link %s set mtu to %v", parentLink.Attrs().Name, cfg.MTU))
	}

	return err
}

func (d *IPvlanDriver) createSlaveIfNotExist(parentLink netlink.Link, slaveName string, mtu int) (netlink.Link, error) {
	slaveLink, err := netlink.LinkByName(slaveName)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); !ok {
			return nil, errors.Wrapf(err, "%s, get device %s error", d.name, slaveName)
		}
	} else {
		_, err = EnsureLinkMTU(slaveLink, mtu)
		if err != nil {
			return nil, err
		}
		return slaveLink, nil
	}

	err = LinkAdd(&netlink.IPVlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:        slaveName,
			ParentIndex: parentLink.Attrs().Index,
			MTU:         mtu,
		},
		Mode: netlink.IPVLAN_MODE_L2,
	})
	if err != nil {
		return nil, err
	}
	link, err := netlink.LinkByName(slaveName)
	if err != nil {
		return nil, fmt.Errorf("error get ipvlan link %s", slaveName)
	}

	return link, nil
}

func (d *IPvlanDriver) setupRouteIfNotExist(link netlink.Link, dst *terwayTypes.IPNetSet) error {
	exec := func(ipNet *net.IPNet) error {
		family := NetlinkFamily(ipNet.IP)
		route := &netlink.Route{
			Protocol:  netlink.RouteProtocol(family),
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst:       ipNet,
		}
		routes, err := netlink.RouteListFiltered(family, route, netlink.RT_FILTER_DST|netlink.RT_FILTER_OIF)
		if err != nil {
			return fmt.Errorf("error list route %s, %w", route.String(), err)
		}
		if len(routes) != 0 {
			return nil
		}

		err = RouteReplace(route)
		if err != nil {
			return err
		}
		return nil
	}
	if dst.IPv4 != nil {
		err := exec(NewIPNetWithMaxMask(dst.IPv4))
		if err != nil {
			return err
		}
	}
	if dst.IPv6 != nil {
		err := exec(NewIPNetWithMaxMask(dst.IPv6))
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *IPvlanDriver) setupFilters(link netlink.Link, cidrs []*net.IPNet, dstIndex int) error {
	parent := uint32(netlink.HANDLE_CLSACT&0xffff0000 | netlink.HANDLE_MIN_EGRESS&0x0000ffff)
	filters, err := netlink.FilterList(link, parent)
	if err != nil {
		return errors.Wrapf(err, "%s, list egress filter for %s error", d.name, link.Attrs().Name)
	}

	ruleInFilter := make(map[*redirectRule]bool)
	for _, v := range cidrs {
		rule, err := dstIPRule(link.Attrs().Index, v, dstIndex, netlink.TCA_INGRESS_REDIR)
		if err != nil {
			return errors.Wrapf(err, "%s, create redirect rule error", d.name)
		}
		ruleInFilter[rule] = false
	}

	for _, filter := range filters {
		matchAny := false
		for rule := range ruleInFilter {
			if rule.isMatch(filter) {
				ruleInFilter[rule] = true
				matchAny = true
				break
			}
		}
		if matchAny {
			continue
		}
		if err := netlink.FilterDel(filter); err != nil {
			return errors.Wrapf(err, "%s, delete filter of %s error", d.name, link.Attrs().Name)
		}
	}

	for rule, in := range ruleInFilter {
		if !in {
			u32 := rule.toU32Filter()
			u32.Parent = parent
			if err := netlink.FilterAdd(u32); err != nil {
				return errors.Wrapf(err, "%s, add filter for %s error", d.name, link.Attrs().Name)
			}
		}
	}
	return nil
}

func (d *IPvlanDriver) setupInitNamespace(parentLink netlink.Link, cfg *SetupConfig) error {
	// setup slave nic
	slaveName := d.initSlaveName(parentLink.Attrs().Index)
	slaveLink, err := d.createSlaveIfNotExist(parentLink, slaveName, cfg.MTU)
	if err != nil {
		return err
	}
	_, err = EnsureLinkUp(slaveLink)
	if err != nil {
		return err
	}

	if slaveLink.Attrs().Flags&unix.IFF_NOARP == 0 {
		if err := netlink.LinkSetARPOff(slaveLink); err != nil {
			return errors.Wrapf(err, "%s, set device %s noarp error", d.name, slaveLink.Attrs().Name)
		}
	}

	if cfg.HostIPSet.IPv4 != nil {
		_, err = EnsureAddr(slaveLink, &netlink.Addr{
			IPNet: cfg.HostIPSet.IPv4,
			Scope: int(netlink.SCOPE_HOST),
		})
		if err != nil {
			return err
		}
	}
	if cfg.HostIPSet.IPv6 != nil {
		_, err = EnsureAddr(slaveLink, &netlink.Addr{
			IPNet: cfg.HostIPSet.IPv6,
			Flags: unix.IFA_F_NODAD,
			Scope: int(netlink.SCOPE_LINK),
		})
		if err != nil {
			return err
		}
	}

	// check tc rule
	err = EnsureClsActQdsic(parentLink)
	if err != nil {
		return err
	}

	if cfg.TrunkENI {
		err = EnsureVlanUntagger(parentLink)
		if err != nil {
			return err
		}
	}

	redirectCIDRs := append(cfg.HostStackCIDRs, cfg.ServiceCIDR.IPv4)
	err = d.setupFilters(parentLink, redirectCIDRs, slaveLink.Attrs().Index)
	if err != nil {
		return err
	}

	err = d.setupRouteIfNotExist(slaveLink, cfg.ContainerIPNet)
	if err != nil {
		return err
	}

	return nil
}

func (d *IPvlanDriver) teardownInitNamespace(parents map[int]struct{}, containerIP *terwayTypes.IPNetSet) error {
	if containerIP == nil {
		return nil
	}

	exec := func(link netlink.Link, ipNet *net.IPNet) error {
		rt := &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Dst:       NewIPNetWithMaxMask(ipNet),
		}

		routes, err := netlink.RouteListFiltered(NetlinkFamily(ipNet.IP), rt, netlink.RT_FILTER_DST|netlink.RT_FILTER_OIF)
		if err != nil {
			return fmt.Errorf("error get route by filter %s, %w", rt.String(), err)
		}
		for _, route := range routes {
			err = RouteDel(&route)
			if err != nil {
				return err
			}
		}
		return nil
	}
	// get slave link
	for index := range parents {
		initLink, err := d.initSlaveLink(index)
		if err != nil {
			if _, ok := err.(netlink.LinkNotFoundError); !ok {
				return fmt.Errorf("error get link by index %d, %w", index, err)
			}
			continue
		}
		if containerIP.IPv4 != nil {
			err = exec(initLink, NewIPNetWithMaxMask(containerIP.IPv4))
			if err != nil {
				return err
			}
		}
		if containerIP.IPv6 != nil {
			err = exec(initLink, NewIPNetWithMaxMask(containerIP.IPv6))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *IPvlanDriver) initSlaveName(parentIndex int) string {
	return fmt.Sprintf("ipvl_%d", parentIndex)
}

func (d *IPvlanDriver) initSlaveLink(parentIndex int) (netlink.Link, error) {
	return netlink.LinkByName(d.initSlaveName(parentIndex))
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

func int8ToString(arr []int8) string {
	var bytes []byte
	for _, v := range arr {
		if v == 0 {
			break
		}

		bytes = append(bytes, byte(v))
	}

	return string(bytes)
}

// CheckIPVLanAvailable checks if current kernel version meet the requirement (>= 4.19)
func CheckIPVLanAvailable() (bool, error) {
	var uts syscall.Utsname
	err := syscall.Uname(&uts)
	if err != nil {
		return false, err
	}

	result := regexKernelVersion.FindStringSubmatch(int8ToString(uts.Release[:]))
	if len(result) != 3 {
		return false, errors.New("can't determine linux kernel version")
	}

	major, err := strconv.Atoi(result[1])
	if err != nil {
		return false, err
	}

	minor, err := strconv.Atoi(result[2])
	if err != nil {
		return false, err
	}

	return (major == ipVlanRequirementMajor && minor >= ipVlanRequirementMinor) ||
		major > ipVlanRequirementMajor, nil
}
