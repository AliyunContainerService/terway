package datapath

import (
	"encoding/binary"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"syscall"

	"github.com/AliyunContainerService/terway/plugin/driver/ipvlan"
	"github.com/AliyunContainerService/terway/plugin/driver/nic"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
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

	defaultMAC, _ = net.ParseMAC("ee:ff:ff:ff:ff:ff")
)

type IPvlanDriver struct{}

func NewIPVlanDriver() *IPvlanDriver {
	return &IPvlanDriver{}
}

func generateContCfgForIPVlan(cfg *types.SetupConfig, link netlink.Link) *nic.Conf {
	var addrs []*netlink.Addr
	var routes []*netlink.Route
	var rules []*netlink.Rule

	var neighs []*netlink.Neigh
	var sysctl map[string][]string

	if cfg.MultiNetwork {
		table := utils.GetRouteTableID(link.Attrs().Index)

		ruleIf := netlink.NewRule()
		ruleIf.OifName = cfg.ContainerIfName
		ruleIf.Table = table
		ruleIf.Priority = toContainerPriority

		rules = append(rules, ruleIf)
	}

	if cfg.ContainerIPNet.IPv4 != nil {
		if cfg.StripVlan {
			addrs = append(addrs, &netlink.Addr{IPNet: utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv4)})
		} else {
			addrs = append(addrs, &netlink.Addr{IPNet: cfg.ContainerIPNet.IPv4})
		}

		// add default route
		if cfg.DefaultRoute {
			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Scope:     netlink.SCOPE_UNIVERSE,
				Dst:       defaultRoute,
				Gw:        cfg.GatewayIP.IPv4,
				Flags:     int(netlink.FLAG_ONLINK),
			})
		}
		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst:       utils.NewIPNetWithMaxMask(cfg.HostIPSet.IPv4),
		})

		neighs = append(neighs, &netlink.Neigh{
			LinkIndex:    link.Attrs().Index,
			IP:           cfg.HostIPSet.IPv4.IP,
			HardwareAddr: link.Attrs().HardwareAddr,
			State:        netlink.NUD_PERMANENT,
		})

		if cfg.StripVlan {
			neighs = append(neighs, &netlink.Neigh{
				LinkIndex:    link.Attrs().Index,
				IP:           cfg.GatewayIP.IPv4,
				HardwareAddr: defaultMAC,
				State:        netlink.NUD_PERMANENT,
			})
		}

		if cfg.MultiNetwork {
			table := utils.GetRouteTableID(link.Attrs().Index)

			v4 := utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv4)

			ruleSrc := netlink.NewRule()
			ruleSrc.Src = v4
			ruleSrc.Table = table
			ruleSrc.Priority = toContainerPriority

			rules = append(rules, ruleSrc)

			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Scope:     netlink.SCOPE_UNIVERSE,
				Dst:       defaultRoute,
				Gw:        cfg.GatewayIP.IPv4,
				Flags:     int(netlink.FLAG_ONLINK),
				Table:     table,
			})
		}
	}
	if cfg.ContainerIPNet.IPv6 != nil {
		if cfg.StripVlan {
			addrs = append(addrs, &netlink.Addr{IPNet: utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv6)})
		} else {
			addrs = append(addrs, &netlink.Addr{IPNet: cfg.ContainerIPNet.IPv6})
		}

		// add default route
		if cfg.DefaultRoute {
			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Scope:     netlink.SCOPE_UNIVERSE,
				Dst:       defaultRouteIPv6,
				Gw:        cfg.GatewayIP.IPv6,
				Flags:     int(netlink.FLAG_ONLINK),
			})
		}
		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst:       utils.NewIPNetWithMaxMask(cfg.HostIPSet.IPv6),
		})

		neighs = append(neighs, &netlink.Neigh{
			LinkIndex:    link.Attrs().Index,
			IP:           cfg.HostIPSet.IPv6.IP,
			HardwareAddr: link.Attrs().HardwareAddr,
			State:        netlink.NUD_PERMANENT,
		})
		if cfg.StripVlan {
			neighs = append(neighs, &netlink.Neigh{
				LinkIndex:    link.Attrs().Index,
				IP:           cfg.GatewayIP.IPv6,
				HardwareAddr: defaultMAC,
				State:        netlink.NUD_PERMANENT,
			})
		}

		if cfg.MultiNetwork {
			table := utils.GetRouteTableID(link.Attrs().Index)

			v6 := utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv6)

			ruleSrc := netlink.NewRule()
			ruleSrc.Src = v6
			ruleSrc.Table = table
			ruleSrc.Priority = toContainerPriority

			rules = append(rules, ruleSrc)

			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Scope:     netlink.SCOPE_UNIVERSE,
				Dst:       defaultRouteIPv6,
				Gw:        cfg.GatewayIP.IPv6,
				Flags:     int(netlink.FLAG_ONLINK),
				Table:     table,
			})
		}
		sysctl = utils.GenerateIPv6Sysctl(cfg.ContainerIfName, true, false)
	}

	contCfg := &nic.Conf{
		IfName:    cfg.ContainerIfName,
		MTU:       cfg.MTU,
		Addrs:     addrs,
		Routes:    routes,
		Rules:     rules,
		Neighs:    neighs,
		SysCtl:    sysctl,
		StripVlan: false,
	}

	return contCfg
}

func generateENICfgForIPVlan(cfg *types.SetupConfig, link netlink.Link) *nic.Conf {
	var routes []*netlink.Route
	var sysctl map[string][]string

	if cfg.ContainerIPNet.IPv6 != nil {
		sysctl = utils.GenerateIPv6Sysctl(link.Attrs().Name, true, true)
	}

	contCfg := &nic.Conf{
		MTU:       cfg.MTU,
		Routes:    routes,
		SysCtl:    sysctl,
		StripVlan: cfg.StripVlan, // if trunk enabled, will remote vlan tag
	}

	return contCfg
}

// for ipvl_x
func generateSlaveLinkCfgForIPVlan(cfg *types.SetupConfig, link netlink.Link) *nic.Conf {
	var addrs []*netlink.Addr
	var routes []*netlink.Route
	var sysctl map[string][]string

	if cfg.ContainerIPNet.IPv4 != nil {
		addrs = append(addrs, &netlink.Addr{IPNet: utils.NewIPNetWithMaxMask(cfg.HostIPSet.IPv4), Scope: int(netlink.SCOPE_HOST)})

		// add route to container
		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst:       utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv4),
		})
	}
	if cfg.ContainerIPNet.IPv6 != nil {
		addrs = append(addrs, &netlink.Addr{IPNet: utils.NewIPNetWithMaxMask(cfg.HostIPSet.IPv6), Flags: unix.IFA_F_NODAD})

		// add route to container
		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst:       utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv6),
		})
	}

	contCfg := &nic.Conf{
		MTU:    cfg.MTU,
		Addrs:  addrs,
		Routes: routes,
		SysCtl: sysctl,
	}

	return contCfg
}

func (d *IPvlanDriver) Setup(cfg *types.SetupConfig, netNS ns.NetNS) error {
	var err error

	parentLink, err := netlink.LinkByIndex(cfg.ENIIndex)
	if err != nil {
		return fmt.Errorf("error get eni by index %d, %w", cfg.ENIIndex, err)
	}
	eniCfg := generateENICfgForIPVlan(cfg, parentLink)
	err = nic.Setup(parentLink, eniCfg)
	if err != nil {
		return err
	}

	if cfg.EnableNetworkPriority {
		err = utils.SetEgressPriority(parentLink, cfg.NetworkPriority, cfg.ContainerIPNet)
		if err != nil {
			return err
		}
	}

	if cfg.StripVlan {
		err = utils.EnsureVlanTag(parentLink, cfg.ContainerIPNet, uint16(cfg.Vid))
		if err != nil {
			return err
		}
	}

	err = ipvlan.Setup(&ipvlan.IPVlan{
		Parent:  parentLink.Attrs().Name,
		PreName: cfg.HostVETHName,
		IfName:  cfg.ContainerIfName,
		MTU:     cfg.MTU,
	}, netNS)
	if err != nil {
		return err
	}

	// 2. setup addr and default route
	err = netNS.Do(func(netNS ns.NetNS) error {
		contLink, err := netlink.LinkByName(cfg.ContainerIfName)
		if err != nil {
			return fmt.Errorf("error find link %s in container, %w", cfg.ContainerIfName, err)
		}
		contCfg := generateContCfgForIPVlan(cfg, contLink)
		err = nic.Setup(contLink, contCfg)
		if err != nil {
			return err
		}
		if cfg.Egress == 0 && cfg.Ingress == 0 {
			return nil
		}
		if cfg.BandwidthMode == "edt" {
			return ensureFQ(contLink)
		}
		return utils.SetupTC(contLink, cfg.Egress)
	})
	if err != nil {
		return fmt.Errorf("error set container link/address/route, %w", err)
	}

	if err := d.setupInitNamespace(parentLink, cfg); err != nil {
		return fmt.Errorf("error set init namespace, %w", err)
	}

	return nil
}

func (d *IPvlanDriver) Teardown(cfg *types.TeardownCfg, netNS ns.NetNS) error {
	err := utils.DelLinkByName(cfg.HostVETHName)
	if err != nil {
		return err
	}

	if cfg.EnableNetworkPriority {
		link, err := netlink.LinkByIndex(cfg.ENIIndex)
		if err != nil {
			if _, ok := err.(netlink.LinkNotFoundError); !ok {
				return err
			}
		} else {
			err = utils.DelEgressPriority(link, cfg.ContainerIPNet)
			if err != nil {
				return err
			}
		}
	}

	err = func() error {
		link, err := netlink.LinkByIndex(cfg.ENIIndex)
		if err != nil {
			if _, ok := err.(netlink.LinkNotFoundError); !ok {
				return err
			}
			return nil
		}
		return utils.DelFilter(link, netlink.HANDLE_MIN_EGRESS, cfg.ContainerIPNet)
	}()
	if err != nil {
		return err
	}

	// del route to container
	return d.teardownInitNamespace(cfg.ContainerIPNet)
}

func (d *IPvlanDriver) Check(cfg *types.CheckConfig) error {
	parentLinkIndex := 0
	// 1. check addr and default route
	err := cfg.NetNS.Do(func(netNS ns.NetNS) error {
		link, err := netlink.LinkByName(cfg.ContainerIfName)
		if err != nil {
			return err
		}
		parentLinkIndex = link.Attrs().ParentIndex
		changed, err := utils.EnsureLinkUp(link)
		if err != nil {
			return err
		}

		if changed {
			cfg.RecordPodEvent(fmt.Sprintf("link %s set to up", cfg.ContainerIfName))
		}

		changed, err = utils.EnsureLinkMTU(link, cfg.MTU)
		if err != nil {
			return err
		}

		if changed {
			cfg.RecordPodEvent(fmt.Sprintf("link %s set mtu to %v", cfg.ContainerIfName, cfg.MTU))
		}

		return utils.EnsureNetConfSet(true, false)
	})
	if err != nil {
		if _, ok := err.(ns.NSPathNotExistErr); ok {
			return nil
		}
		return err
	}
	// 2. check parent link ( this is called in every setup it is safe)
	utils.Log.Debugf("parent link is %d", parentLinkIndex)
	parentLink, err := netlink.LinkByIndex(parentLinkIndex)
	if err != nil {
		return fmt.Errorf("error get parent link, %w", err)
	}
	changed, err := utils.EnsureLinkUp(parentLink)
	if err != nil {
		return err
	}
	if changed {
		cfg.RecordPodEvent(fmt.Sprintf("parent link id %d set to up", int(cfg.ENIIndex)))
	}
	changed, err = utils.EnsureLinkMTU(parentLink, cfg.MTU)
	if err != nil {
		return err
	}

	if changed {
		cfg.RecordPodEvent(fmt.Sprintf("link %s set mtu to %v", parentLink.Attrs().Name, cfg.MTU))
	}

	return nil
}

func (d *IPvlanDriver) createSlaveIfNotExist(parentLink netlink.Link, slaveName string, mtu int) (netlink.Link, error) {
	slaveLink, err := netlink.LinkByName(slaveName)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); !ok {
			return nil, fmt.Errorf("get device %s error, %w", slaveName, err)
		}
	} else {
		_, err = utils.EnsureLinkMTU(slaveLink, mtu)
		if err != nil {
			return nil, err
		}
		return slaveLink, nil
	}

	err = utils.LinkAdd(&netlink.IPVlan{
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

func (d *IPvlanDriver) setupFilters(link netlink.Link, cidrs []*net.IPNet, dstIndex int) error {
	parent := uint32(netlink.HANDLE_CLSACT&0xffff0000 | netlink.HANDLE_MIN_EGRESS&0x0000ffff)
	filters, err := netlink.FilterList(link, parent)
	if err != nil {
		return fmt.Errorf("list egress filter for %s error, %w", link.Attrs().Name, err)
	}

	ruleInFilter := make(map[*redirectRule]bool)
	for _, v := range cidrs {
		rule, err := dstIPRule(link.Attrs().Index, v, dstIndex, netlink.TCA_INGRESS_REDIR)
		if err != nil {
			return fmt.Errorf("create redirect rule error, %w", err)
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
		if filter.Attrs() != nil && filter.Attrs().Priority != 4000 {
			continue
		}
		if err := utils.FilterDel(filter); err != nil {
			return fmt.Errorf("delete filter of %s error, %w", link.Attrs().Name, err)
		}
	}

	for rule, in := range ruleInFilter {
		if !in {
			u32 := rule.toU32Filter()
			u32.Parent = parent
			if err := utils.FilterAdd(u32); err != nil {
				return fmt.Errorf("add filter for %s error, %w", link.Attrs().Name, err)
			}
		}
	}
	return nil
}

func (d *IPvlanDriver) setupInitNamespace(parentLink netlink.Link, cfg *types.SetupConfig) error {
	// setup slave nic
	slaveName := d.initSlaveName(parentLink.Attrs().Index)
	slaveLink, err := d.createSlaveIfNotExist(parentLink, slaveName, cfg.MTU)
	if err != nil {
		return err
	}

	if slaveLink.Attrs().Flags&unix.IFF_NOARP == 0 {
		if err := netlink.LinkSetARPOff(slaveLink); err != nil {
			return fmt.Errorf("set device %s noarp error, %w", slaveLink.Attrs().Name, err)
		}
	}
	slaveCfg := generateSlaveLinkCfgForIPVlan(cfg, slaveLink)
	err = nic.Setup(slaveLink, slaveCfg)
	if err != nil {
		return err
	}

	// check tc rule
	err = utils.EnsureClsActQdsic(parentLink)
	if err != nil {
		return err
	}

	redirectCIDRs := append(cfg.HostStackCIDRs, cfg.ServiceCIDR.IPv4)
	err = d.setupFilters(parentLink, redirectCIDRs, slaveLink.Attrs().Index)
	if err != nil {
		return err
	}

	return nil
}

func (d *IPvlanDriver) teardownInitNamespace(containerIP *terwayTypes.IPNetSet) error {
	if containerIP == nil {
		return nil
	}

	exec := func(ipNet *net.IPNet) error {
		routes, err := utils.FoundRoutes(&netlink.Route{
			Dst: ipNet,
		})
		if err != nil {
			return err
		}
		for _, route := range routes {
			err = utils.RouteDel(&route)
			if err != nil {
				return err
			}
		}
		return nil
	}

	if containerIP.IPv4 != nil {
		err := exec(utils.NewIPNetWithMaxMask(containerIP.IPv4))
		if err != nil {
			return err
		}
	}
	if containerIP.IPv6 != nil {
		err := exec(utils.NewIPNetWithMaxMask(containerIP.IPv6))
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *IPvlanDriver) initSlaveName(parentIndex int) string {
	return fmt.Sprintf("ipvl_%d", parentIndex)
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

func ensureFQ(link netlink.Link) error {
	fq := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: link.Attrs().Index,
			Parent:    netlink.HANDLE_ROOT,
			Handle:    netlink.MakeHandle(1, 0),
		},
		QdiscType: "fq",
	}
	qds, err := netlink.QdiscList(link)
	if err != nil {
		return err
	}
	found := false
	for _, qd := range qds {
		if qd.Attrs().LinkIndex != link.Attrs().Index {
			continue
		}
		if qd.Type() != fq.Type() {
			continue
		}
		if qd.Attrs().Parent != fq.Parent {
			continue
		}
		if qd.Attrs().Handle != fq.Handle {
			continue
		}
		found = true
	}
	if found {
		return nil
	}
	return utils.QdiscReplace(fq)
}
