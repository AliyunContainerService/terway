package utils

import (
	"errors"
	"fmt"
	"net"

	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	terwaySysctl "github.com/AliyunContainerService/terway/pkg/sysctl"
	"github.com/AliyunContainerService/terway/pkg/tc"
	terwayTypes "github.com/AliyunContainerService/terway/types"

	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	k8sErr "k8s.io/apimachinery/pkg/util/errors"
	k8snet "k8s.io/apimachinery/pkg/util/net"
)

// GetRouteTableID add 1000 to link index to avoid route table conflict
func GetRouteTableID(linkIndex int) int {
	return 1000 + linkIndex
}

// EnsureHostNsConfig setup host namespace configs
func EnsureHostNsConfig(ipv4, ipv6 bool) error {
	for _, key := range []string{"default", "all"} {
		for _, cfg := range ipv4NetConfig {
			err := terwaySysctl.EnsureConf(fmt.Sprintf(cfg[0], key), cfg[1])
			if err != nil {
				return err
			}
		}
	}

	return EnsureNetConfSet(ipv4, ipv6)
}

// EnsureLinkUp set link up,return changed and err
func EnsureLinkUp(link netlink.Link) (bool, error) {
	if link.Attrs().Flags&net.FlagUp != 0 {
		return false, nil
	}
	return true, LinkSetUp(link)
}

// EnsureLinkMTU set link mtu,return changed and err
func EnsureLinkMTU(link netlink.Link, mtu int) (bool, error) {
	if link.Attrs().MTU == mtu {
		return false, nil
	}
	return true, LinkSetMTU(link, mtu)
}

func EnsureLinkName(link netlink.Link, name string) (bool, error) {
	if link.Attrs().Name == name {
		return false, nil
	}
	return true, LinkSetName(link, name)
}

// DelLinkByName del by name and ignore if link not present
func DelLinkByName(ifName string) error {
	contLink, err := netlink.LinkByName(ifName)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); ok { //nolint
			return nil
		}
	}
	return LinkDel(contLink)
}

// EnsureAddr ensure only one IP for each family is present on link
func EnsureAddr(link netlink.Link, expect *netlink.Addr) (bool, error) {
	var changed bool

	addrList, err := netlink.AddrList(link, NetlinkFamily(expect.IP))
	if err != nil {
		return false, fmt.Errorf("error list address from if %s, %w", link.Attrs().Name, err)
	}

	found := false
	for _, addr := range addrList {
		if !addr.IP.IsGlobalUnicast() {
			continue
		}

		if (addr.IPNet.String() == expect.IPNet.String()) && (addr.Scope == expect.Scope) {
			found = true
			continue
		}

		err := AddrDel(link, &addr)
		if err != nil {
			return false, err
		}
		changed = true
	}
	if found {
		return changed, nil
	}
	return true, AddrReplace(link, expect)
}

// FoundRoutes look up routes
func FoundRoutes(expected *netlink.Route) ([]netlink.Route, error) {
	family := NetlinkFamily(expected.Dst.IP)
	routeFilter := netlink.RT_FILTER_DST
	if expected.Dst == nil {
		return nil, fmt.Errorf("dst in route expect not nil")
	}
	find := *expected

	if find.Dst.String() == "::/0" || find.Dst.String() == "0.0.0.0/0" {
		find.Dst = nil
	}
	if find.LinkIndex > 0 {
		routeFilter = routeFilter | netlink.RT_FILTER_OIF
	}
	if find.Scope > 0 {
		routeFilter = routeFilter | netlink.RT_FILTER_SCOPE
	}
	if find.Gw != nil {
		routeFilter = routeFilter | netlink.RT_FILTER_GW
	}
	if find.Table > 0 {
		routeFilter = routeFilter | netlink.RT_FILTER_TABLE
	}
	return netlink.RouteListFiltered(family, &find, routeFilter)
}

// EnsureRoute will call ip route replace if route is not found
func EnsureRoute(expected *netlink.Route) (bool, error) {
	routes, err := FoundRoutes(expected)
	if err != nil {
		return false, fmt.Errorf("error list expected: %v", err)
	}
	if len(routes) > 0 {
		return false, nil
	}

	return true, RouteReplace(expected)
}

func NewIPNetWithMaxMask(ipNet *net.IPNet) *net.IPNet {
	if ipNet.IP.To4() == nil {
		return &net.IPNet{
			IP:   ipNet.IP,
			Mask: net.CIDRMask(128, 128),
		}
	}
	return &net.IPNet{
		IP:   ipNet.IP,
		Mask: net.CIDRMask(32, 32),
	}
}

func NewIPNet1(ipNet *terwayTypes.IPNetSet) []*netlink.Addr {
	var addrs []*netlink.Addr
	if ipNet.IPv4 != nil {
		addrs = append(addrs, &netlink.Addr{IPNet: ipNet.IPv4})
	}
	if ipNet.IPv6 != nil {
		addrs = append(addrs, &netlink.Addr{IPNet: ipNet.IPv6})
	}
	return addrs
}

func NewIPNetToMaxMask(ipNet *terwayTypes.IPNetSet) []*netlink.Addr {
	var addrs []*netlink.Addr
	if ipNet.IPv4 != nil {
		addrs = append(addrs, &netlink.Addr{IPNet: NewIPNetWithMaxMask(ipNet.IPv4)})
	}
	if ipNet.IPv6 != nil {
		addrs = append(addrs, &netlink.Addr{IPNet: NewIPNetWithMaxMask(ipNet.IPv6)})
	}
	return addrs
}

func NewIPNet(ipNet *terwayTypes.IPNetSet) *terwayTypes.IPNetSet {
	ipNetSet := &terwayTypes.IPNetSet{}
	if ipNet.IPv4 != nil {
		ipNetSet.IPv4 = NewIPNetWithMaxMask(ipNet.IPv4)
	}
	if ipNet.IPv6 != nil {
		ipNetSet.IPv6 = NewIPNetWithMaxMask(ipNet.IPv6)
	}
	return ipNetSet
}

// FindIPRule look up ip rules in config
func FindIPRule(rule *netlink.Rule) ([]netlink.Rule, error) {
	var filterMask uint64
	family := netlink.FAMILY_V4

	if rule.Src == nil && rule.Dst == nil && rule.OifName == "" {
		return nil, errors.New("both src and dst is nil")
	}

	if rule.Src != nil {
		filterMask = filterMask | netlink.RT_FILTER_SRC
		family = NetlinkFamily(rule.Src.IP)
	}
	if rule.Dst != nil {
		filterMask = filterMask | netlink.RT_FILTER_DST
		family = NetlinkFamily(rule.Dst.IP)
	}
	if rule.OifName != "" {
		filterMask = filterMask | netlink.RT_FILTER_OIF
		family = netlink.FAMILY_V4
	}

	if rule.Priority >= 0 {
		filterMask = filterMask | netlink.RT_FILTER_PRIORITY
	}
	return netlink.RuleListFiltered(family, rule, filterMask)
}

func EnsureIPRule(expected *netlink.Rule) (bool, error) {
	changed := false

	// 1. clean exist rules if needed
	ruleList, err := FindIPRule(expected)
	if err != nil {
		return false, err
	}
	found := false
	for _, rule := range ruleList {
		del := false
		if rule.Table != expected.Table {
			del = true
		}
		if rule.Priority != expected.Priority {
			del = true
		}
		if rule.IifName != expected.IifName {
			del = true
		}
		if del {
			changed = true
			err = RuleDel(&rule)
			if err != nil {
				return changed, err
			}
		} else {
			found = true
		}
	}
	if found {
		return changed, nil
	}
	return true, RuleAdd(expected)
}

func GenerateIPv6Sysctl(ifName string, disableRA, enableForward bool) map[string][]string {
	result := map[string][]string{}
	for _, name := range []string{"lo", "all", "default"} {
		result[fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/disable_ipv6", name)] = []string{fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/disable_ipv6", name), "0"}
	}

	if ifName != "" {
		result[fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/disable_ipv6", ifName)] = []string{fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/disable_ipv6", ifName), "0"}

		if disableRA {
			result[fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/accept_ra", ifName)] = []string{fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/accept_ra", ifName), "0"}
		}
		if enableForward {
			result[fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/forwarding", ifName)] = []string{fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/forwarding", ifName), "1"}
		}
	}
	return result
}

func GetHostIP(ipv4, ipv6 bool) (*terwayTypes.IPNetSet, error) {
	var nodeIPv4, nodeIPv6 *net.IPNet

	if ipv4 {
		v4, err := k8snet.ResolveBindAddress(net.ParseIP("127.0.0.1"))
		if err != nil {
			return nil, err
		}
		if terwayIP.IPv6(v4) {
			return nil, fmt.Errorf("error get node ipv4 address.This may due to 1. no ipv4 address 2. no ipv4 default route")
		}
		nodeIPv4 = &net.IPNet{
			IP:   v4,
			Mask: net.CIDRMask(32, 32),
		}
	}

	if ipv6 {
		v6, err := k8snet.ResolveBindAddress(net.ParseIP("::1"))
		if err != nil {
			return nil, err
		}
		if !terwayIP.IPv6(v6) {
			return nil, fmt.Errorf("error get node ipv6 address.This may due to 1. no ipv6 address 2. no ipv6 default route")
		}
		nodeIPv6 = &net.IPNet{
			IP:   v6,
			Mask: net.CIDRMask(128, 128),
		}
	}
	return &terwayTypes.IPNetSet{
		IPv4: nodeIPv4,
		IPv6: nodeIPv6,
	}, nil
}

func EnsureNeigh(neigh *netlink.Neigh) (bool, error) {
	var neighs []netlink.Neigh
	var err error
	if terwayIP.IPv6(neigh.IP) {
		neighs, err = netlink.NeighList(neigh.LinkIndex, netlink.FAMILY_V6)
	} else {
		neighs, err = netlink.NeighList(neigh.LinkIndex, netlink.FAMILY_V4)
	}
	if err != nil {
		return false, err
	}
	found := false
	for _, n := range neighs {
		if n.IP.Equal(neigh.IP) && n.HardwareAddr.String() == neigh.HardwareAddr.String() {
			found = true
			break
		}
	}
	if !found {
		return true, NeighSet(neigh)
	}
	return false, err
}

var ipv4NetConfig = [][]string{
	{"/proc/sys/net/ipv4/conf/%s/forwarding", "1"},
	{"/proc/sys/net/ipv4/conf/%s/rp_filter", "0"},
}

var ipv6NetConfig = [][]string{
	{"/proc/sys/net/ipv6/conf/%s/forwarding", "1"},
	{"/proc/sys/net/ipv6/conf/%s/disable_ipv6", "0"},
}

// EnsureNetConfSet will set net config to all link
func EnsureNetConfSet(ipv4, ipv6 bool) error {
	links, err := netlink.LinkList()
	if err != nil {
		return err
	}

	for _, link := range links {
		if ipv4 {
			for _, cfg := range ipv4NetConfig {
				innerErr := terwaySysctl.EnsureConf(fmt.Sprintf(cfg[0], link.Attrs().Name), cfg[1])
				if innerErr != nil {
					err = fmt.Errorf("%v, %w", err, innerErr)
				}
			}
		}
		if ipv6 {
			for _, cfg := range ipv6NetConfig {
				innerErr := terwaySysctl.EnsureConf(fmt.Sprintf(cfg[0], link.Attrs().Name), cfg[1])
				if innerErr != nil {
					err = fmt.Errorf("%v, %w", err, innerErr)
				}
			}
		}
	}
	return err
}

func EnsureVlanUntagger(link netlink.Link) error {
	if err := EnsureClsActQdsic(link); err != nil {
		return fmt.Errorf("error ensure cls act qdisc for %s vlan untag, %w", link.Attrs().Name, err)
	}
	filters, err := netlink.FilterList(link, netlink.HANDLE_MIN_INGRESS)
	if err != nil {
		return fmt.Errorf("list ingress filter for %s error, %w", link.Attrs().Name, err)
	}
	for _, filter := range filters {
		if u32, ok := filter.(*netlink.U32); ok {
			if u32.Attrs().LinkIndex == link.Attrs().Index &&
				u32.Attrs().Protocol == uint16(netlink.VLAN_PROTOCOL_8021Q) &&
				len(u32.Sel.Keys) == 1 && u32.Sel.Keys[0].Mask == 0x0 && u32.Sel.Keys[0].Off == 0x0 && u32.Sel.Keys[0].Val == 0x0 &&
				len(u32.Actions) == 1 {
				if action, ok := u32.Actions[0].(*netlink.VlanAction); ok {
					if action.Action == netlink.TCA_VLAN_KEY_POP {
						return nil
					}
				}
			}
		}
	}

	vlanAct := netlink.NewVlanKeyAction()
	vlanAct.Action = netlink.TCA_VLAN_KEY_POP
	u32 := &netlink.U32{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: link.Attrs().Index,
			Parent:    netlink.HANDLE_MIN_INGRESS,
			Priority:  20000,
			Protocol:  uint16(netlink.VLAN_PROTOCOL_8021Q),
		},
		Sel: &netlink.TcU32Sel{
			Nkeys: 1,
			Flags: netlink.TC_U32_TERMINAL,
			Keys: []netlink.TcU32Key{
				{
					Mask: 0x0,
					Val:  0x0,
					Off:  0x0,
				},
			},
		},
		Actions: []netlink.Action{vlanAct},
	}
	err = netlink.FilterAdd(u32)
	if err != nil {
		return fmt.Errorf("error add filter for vlan untag, %w", err)
	}
	return nil
}

// EnsureVlanTag use tc-vlan set vlan tag
func EnsureVlanTag(link netlink.Link, ipNetSet *terwayTypes.IPNetSet, vid uint16) error {
	err := EnsureClsActQdsic(link)
	if err != nil {
		return fmt.Errorf("error ensure cls act qdisc for %s vlan tag, %w", link.Attrs().Name, err)
	}

	filters, err := netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
	if err != nil {
		return fmt.Errorf("list ingress filter for %s error, %w", link.Attrs().Name, err)
	}

	exec := func(ipNet *net.IPNet) error {
		vlanAct := netlink.NewVlanKeyAction()
		vlanAct.Attrs().Action = netlink.TC_ACT_PIPE
		vlanAct.Action = netlink.TCA_VLAN_KEY_PUSH
		vlanAct.Vid = vid
		expect := &netlink.U32{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: link.Attrs().Index,
				Parent:    netlink.HANDLE_MIN_EGRESS,
				Priority:  50001,
				Protocol:  uint16(unix.ETH_P_IP),
			},
			Actions: []netlink.Action{vlanAct},
		}
		tc.MatchSrc(expect, ipNet)

		for _, filter := range filters {
			u32, ok := filter.(*netlink.U32)
			if !ok {
				continue
			}
			if u32.Attrs().LinkIndex != link.Attrs().Index || u32.Attrs().Protocol != unix.ETH_P_IP || len(u32.Actions) == 0 || u32.Sel == nil {
				continue
			}
			act, ok := u32.Actions[0].(*netlink.VlanAction)
			if !ok {
				continue
			}
			if act.Action != netlink.TCA_VLAN_KEY_PUSH || act.Attrs().Action != netlink.TC_ACT_PIPE {
				continue
			}
			if !tc.Contain(u32.Sel.Keys, expect.Sel.Keys) {
				continue
			}
			if act.Vid != vid {
				err = FilterDel(u32)
				if err != nil {
					return err
				}
				continue
			}
			return nil
		}

		return FilterAdd(expect)
	}

	if ipNetSet.IPv4 != nil {
		err = exec(NewIPNetWithMaxMask(ipNetSet.IPv4))
		if err != nil {
			return err
		}
	}
	if ipNetSet.IPv6 != nil {
		err = exec(NewIPNetWithMaxMask(ipNetSet.IPv6))
	}
	return err
}

func EnsureClsActQdsic(link netlink.Link) error {
	qds, err := netlink.QdiscList(link)
	if err != nil {
		return fmt.Errorf("list qdisc for dev %s error, %w", link.Attrs().Name, err)
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
	if err := QdiscReplace(qdisc); err != nil {
		return fmt.Errorf("replace clsact qdisc for dev %s error, %w", link.Attrs().Name, err)
	}
	return nil
}

// EnsurePrioQdiscAt10 write qdisc  attach under mq
func EnsurePrioQdiscAt10(link netlink.Link) error {
	qds, err := netlink.QdiscList(link)
	if err != nil {
		return fmt.Errorf("list qdisc for dev %s error, %w", link.Attrs().Name, err)
	}
	for _, q := range qds {
		// only handle qd under mq at 1:
		major, minor := netlink.MajorMinor(q.Attrs().Parent)
		if major != 1 || minor == 0 {
			continue
		}

		_, ok := q.(*netlink.Prio)
		if !ok {
			err = QdiscReplace(netlink.NewPrio(netlink.QdiscAttrs{
				LinkIndex: link.Attrs().Index,
				Parent:    q.Attrs().Parent,
			}))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// EnsureMQQdisc write qdisc
func EnsureMQQdisc(link netlink.Link) error {
	qds, err := netlink.QdiscList(link)
	if err != nil {
		return fmt.Errorf("list qdisc for dev %s error, %w", link.Attrs().Name, err)
	}
	var prev netlink.Qdisc
	for _, q := range qds {
		_, minor := netlink.MajorMinor(q.Attrs().Handle)
		if q.Attrs().Parent == netlink.HANDLE_ROOT && minor == 0 {
			prev = q
		}

		if q.Type() == "mq" && q.Attrs().Parent == netlink.HANDLE_ROOT && q.Attrs().Handle == netlink.MakeHandle(1, 0) {
			return nil
		}
	}
	if prev != nil {
		_ = QdiscDel(prev)
	}

	return QdiscReplace(&netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: link.Attrs().Index,
			Parent:    netlink.HANDLE_ROOT,
			Handle:    netlink.MakeHandle(1, 0),
		},
		QdiscType: "mq",
	})
}

func FilterAdd(filter *netlink.U32) error {
	cmd := fmt.Sprintf("tc filter add %s", filter.String())
	Log.Info(cmd)
	err := netlink.FilterAdd(filter)
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}

func FilterDel(filter netlink.Filter) error {
	cmd := fmt.Sprintf("tc filter del %s", filter.Attrs().String())
	Log.Info(cmd)
	err := netlink.FilterDel(filter)
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}

// SetFilter write u32 filter
func SetFilter(link netlink.Link, parentID, classID uint32, ipNetSet *terwayTypes.IPNetSet) error {
	exec := func(ipNet *net.IPNet) error {
		found, err := tc.FilterBySrcIP(link, parentID, ipNet)
		if err != nil {
			return err
		}
		if found != nil && found.ClassId == classID {
			return nil
		}

		u32 := &netlink.U32{
			FilterAttrs: netlink.FilterAttrs{
				LinkIndex: link.Attrs().Index,
				Parent:    parentID,
				Priority:  1,
				Protocol:  unix.ETH_P_IP,
			},
			ClassId: classID,
		}
		tc.MatchSrc(u32, ipNet)

		return FilterAdd(u32)
	}

	if ipNetSet.IPv4 != nil {
		err := exec(NewIPNetWithMaxMask(ipNetSet.IPv4))
		if err != nil {
			return err
		}
	}
	if ipNetSet.IPv6 != nil {
		err := exec(NewIPNetWithMaxMask(ipNetSet.IPv6))
		if err != nil {
			return err
		}
	}
	return nil
}

// DelFilter del u32 filter by pod ip
func DelFilter(link netlink.Link, parentID uint32, ipNetSet *terwayTypes.IPNetSet) error {
	exec := func(ipNet *net.IPNet) error {
		found, err := tc.FilterBySrcIP(link, parentID, ipNet)
		if err != nil {
			return err
		}
		if found == nil {
			return nil
		}
		err = FilterDel(found)
		if err != nil {
			return err
		}
		return nil
	}

	if ipNetSet.IPv4 != nil {
		err := exec(NewIPNetWithMaxMask(ipNetSet.IPv4))
		if err != nil {
			return err
		}
	}
	if ipNetSet.IPv6 != nil {
		err := exec(NewIPNetWithMaxMask(ipNetSet.IPv6))
		if err != nil {
			return err
		}
	}

	return nil
}

// SetEgressPriority write egress priority rule for pod
func SetEgressPriority(link netlink.Link, classID uint32, ipNetSet *terwayTypes.IPNetSet) error {
	err := EnsureMQQdisc(link)
	if err != nil {
		return err
	}
	err = EnsurePrioQdiscAt10(link)
	if err != nil {
		return err
	}
	qds, err := netlink.QdiscList(link)
	if err != nil {
		return fmt.Errorf("list qdisc for dev %s error, %w", link.Attrs().Name, err)
	}
	for _, q := range qds {
		_, ok := q.(*netlink.Prio)
		if !ok {
			continue
		}

		err = SetFilter(link, q.Attrs().Handle, classID, ipNetSet)
		if err != nil {
			return err
		}
	}
	return nil
}

func DelEgressPriority(link netlink.Link, ipNetSet *terwayTypes.IPNetSet) error {
	qds, err := netlink.QdiscList(link)
	if err != nil {
		return err
	}
	for _, q := range qds {
		_, ok := q.(*netlink.Prio)
		if !ok {
			continue
		}
		err = DelFilter(link, q.Attrs().Handle, ipNetSet)
		if err != nil {
			return err
		}
	}
	return nil
}

func SetupTC(link netlink.Link, bandwidthInBytes uint64) error {
	rule := &tc.TrafficShapingRule{
		Rate: bandwidthInBytes,
	}
	return tc.SetRule(link, rule)
}

// GenericTearDown target to clean all related resource as much as possible
func GenericTearDown(netNS ns.NetNS) error {
	var errList []error
	hostNetNS, err := ns.GetCurrentNS()
	if err != nil {
		return fmt.Errorf("err get host net ns, %w", err)
	}
	err = netNS.Do(func(netNS ns.NetNS) error {
		linkList, err := netlink.LinkList()
		if err != nil {
			return fmt.Errorf("error get link list from netlink, %w", err)
		}
		for _, l := range linkList {
			if l.Attrs().Name == "lo" {
				continue
			}
			_ = LinkSetDown(l)
			switch l.(type) {
			case *netlink.IPVlan, *netlink.Vlan, *netlink.Veth, *netlink.Ifb, *netlink.Dummy:
				errList = append(errList, LinkDel(l))
			case *netlink.Device:
				name, err := ip.RandomVethName()
				if err != nil {
					errList = append(errList, err)
					continue
				}
				errList = append(errList, LinkSetName(l, name))
				errList = append(errList, LinkSetNsFd(l, hostNetNS))
			default:
				continue
			}
		}
		return nil
	})
	if err != nil {
		if _, ok := err.(ns.NSPathNotExistErr); !ok {
			errList = append(errList, err)
		}
	}
	errList = append(errList, CleanIPRules())
	return k8sErr.NewAggregate(errList)
}

// CleanIPRules del ip rule for detached devs
func CleanIPRules() (err error) {
	var rules []netlink.Rule
	rules, err = netlink.RuleList(netlink.FAMILY_ALL)
	if err != nil {
		return err
	}

	var ipNets []*net.IPNet
	defer func() {
		for _, r := range rules {
			if r.Priority != 512 && r.Priority != 2048 {
				continue
			}
			if r.IifName != "" || r.OifName != "" {
				continue
			}
			found := false

			for _, ipNet := range ipNets {
				if r.Dst != nil {
					if r.Dst.String() == ipNet.String() {
						found = true
						break
					}
				}
				if r.Src != nil {
					if r.Src.String() == ipNet.String() {
						found = true
						break
					}
				}
			}
			if !found {
				continue
			}
			_ = RuleDel(&r)
		}
	}()
	for _, r := range rules {
		if r.Priority != 512 && r.Priority != 2048 {
			continue
		}
		name := r.IifName
		if name == "" {
			name = r.OifName
		}
		if name == "" {
			continue
		}
		_, err = netlink.LinkByName(name)
		if err != nil {
			if _, ok := err.(netlink.LinkNotFoundError); !ok {
				return err
			}
			err = RuleDel(&r)
			if err != nil {
				return err
			}
			var ipNet *net.IPNet
			if r.Dst != nil {
				ipNet = r.Dst
			}
			if r.Src != nil {
				ipNet = r.Src
			}
			if ipNet != nil {
				ipNets = append(ipNets, ipNet)
			}
		}
	}

	return nil
}
