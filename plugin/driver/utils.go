package driver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/pkg/errors"

	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	terwaySysctl "github.com/AliyunContainerService/terway/pkg/sysctl"
	terwayTypes "github.com/AliyunContainerService/terway/types"

	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	k8snet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	fileLockTimeOut = 11 * time.Second
)

// Log for default log
var Log = DefaultLogger.WithField("subSys", "terway-cni")

// DefaultLogger default log
var DefaultLogger = NewDefaultLogger()

// Hook for log
var Hook = &PodInfoHook{ExtraInfo: make(map[string]string)}

func NewDefaultLogger() *logrus.Logger {
	logger := logrus.New()
	logger.Formatter = &logrus.TextFormatter{
		DisableTimestamp: true,
		DisableColors:    true,
		DisableQuote:     true,
	}
	logger.SetLevel(logrus.InfoLevel)
	logger.AddHook(Hook)
	return logger
}

type PodInfoHook struct {
	ExtraInfo map[string]string
}

func (p *PodInfoHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (p *PodInfoHook) Fire(e *logrus.Entry) error {
	for k, v := range p.ExtraInfo {
		e.Data[k] = v
	}
	return nil
}

func (p *PodInfoHook) AddExtraInfo(k, v string) {
	p.ExtraInfo[k] = v
}

func (p *PodInfoHook) AddExtraInfos(e map[string]string) {
	for k, v := range e {
		p.ExtraInfo[k] = v
	}
}

func SetLogDebug() {
	DefaultLogger.SetLevel(logrus.DebugLevel)
	logrus.SetOutput(os.Stderr)
}

// JSONStr json to str
func JSONStr(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

type Locker struct {
	FD *os.File
}

// Close close
func (l *Locker) Close() error {
	if l.FD != nil {
		return l.FD.Close()
	}
	return nil
}

// GrabFileLock get file lock with timeout 11seconds
func GrabFileLock(lockfilePath string) (*Locker, error) {
	var success bool
	var err error
	l := &Locker{}
	defer func(l *Locker) {
		if !success {
			_ = l.Close()
		}
	}(l)

	l.FD, err = os.OpenFile(lockfilePath, os.O_CREATE, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock %s: %v", lockfilePath, err)
	}
	if err := wait.PollImmediate(200*time.Millisecond, fileLockTimeOut, func() (bool, error) {
		if err := grabFileLock(l.FD); err != nil {
			return false, nil
		}
		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to acquire new iptables lock: %v", err)
	}
	success = true
	return l, nil
}

func grabFileLock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

// add 1000 to link index to avoid route table conflict
func getRouteTableID(linkIndex int) int {
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

// EnsureAddrWithPrefix take the ipNet set and ensure only one IP for each family is present on link
// it will remove other unmatched IPs
func EnsureAddrWithPrefix(link netlink.Link, ipNetSet *terwayTypes.IPNetSet, prefixRoute bool) (bool, error) {
	var changed bool

	if ipNetSet.IPv4 != nil {
		newAddr := &netlink.Addr{IPNet: ipNetSet.IPv4}
		if !prefixRoute {
			newAddr.Flags = unix.IFA_F_NOPREFIXROUTE
		}
		c, err := EnsureAddr(link, newAddr)
		if err != nil {
			return c, err
		}
		if c {
			changed = true
		}
	}
	if ipNetSet.IPv6 != nil {
		newAddr := &netlink.Addr{IPNet: ipNetSet.IPv6}
		if !prefixRoute {
			newAddr.Flags = unix.IFA_F_NOPREFIXROUTE
		}
		c, err := EnsureAddr(link, newAddr)
		if err != nil {
			return c, err
		}
		if c {
			changed = true
		}
	}
	return changed, nil
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

func EnsureDefaultRoute(link netlink.Link, gw *terwayTypes.IPSet, table int) (bool, error) {
	var changed bool
	if gw.IPv4 != nil {
		c, err := EnsureRoute(&netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_UNIVERSE,
			Dst:       defaultRoute,
			Gw:        gw.IPv4,
			Table:     table,
			Flags:     int(netlink.FLAG_ONLINK),
		})
		if err != nil {
			return changed, err
		}
		changed = changed || c
	}
	if gw.IPv6 != nil {
		c, err := EnsureRoute(&netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_UNIVERSE,
			Dst:       defaultRouteIPv6,
			Gw:        gw.IPv6,
			Table:     table,
			Flags:     int(netlink.FLAG_ONLINK),
		})
		if err != nil {
			return changed, err
		}
		changed = changed || c
	}
	return changed, nil
}

// EnsureHostToContainerRoute create host to container route
func EnsureHostToContainerRoute(link netlink.Link, ipNetSet *terwayTypes.IPNetSet) (bool, error) {
	var changed bool
	linkIndex := link.Attrs().Index

	exec := func(expect *netlink.Route) error {
		routes, err := netlink.RouteListFiltered(NetlinkFamily(expect.Dst.IP), &netlink.Route{
			Table: unix.RT_TABLE_MAIN,
			Scope: netlink.SCOPE_LINK,
		}, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_SCOPE)
		if err != nil {
			return fmt.Errorf("error list route: %v", err)
		}

		found := false
		for _, r := range routes {
			if r.Dst == nil {
				continue
			}
			if !r.Dst.IP.Equal(expect.Dst.IP) {
				continue
			}
			if r.LinkIndex != linkIndex || !bytes.Equal(r.Dst.Mask, expect.Dst.Mask) {
				err := RouteDel(&r)
				if err != nil {
					if os.IsNotExist(err) {
						continue
					}
				}
				changed = true
			}
			found = true
		}
		if !found {
			err := RouteReplace(expect)
			if err != nil {
				return err
			}
			changed = true
		}
		return nil
	}
	if ipNetSet.IPv4 != nil {
		err := exec(&netlink.Route{
			LinkIndex: linkIndex,
			Scope:     netlink.SCOPE_LINK,
			Dst:       NewIPNetWithMaxMask(ipNetSet.IPv4),
		})
		if err != nil {
			return changed, err
		}
	}
	if ipNetSet.IPv6 != nil {
		err := exec(&netlink.Route{
			LinkIndex: linkIndex,
			Scope:     netlink.SCOPE_LINK,
			Dst:       NewIPNetWithMaxMask(ipNetSet.IPv6),
		})
		if err != nil {
			return changed, err
		}
	}
	return changed, nil
}

func PodInfoKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

// SetupLink is a common setup for all links
// 1. set link name
// 2. set link up
// 3. set link ip address
func SetupLink(link netlink.Link, cfg *SetupConfig) error {
	_, err := EnsureLinkName(link, cfg.ContainerIfName)
	if err != nil {
		return err
	}
	_, err = EnsureLinkUp(link)
	if err != nil {
		return fmt.Errorf("error set link %s up , %w", link.Attrs().Name, err)
	}

	_, err = EnsureAddrWithPrefix(link, cfg.ContainerIPNet, !cfg.TrunkENI)
	return err
}

// AddNeigh add arp for link
func AddNeigh(link netlink.Link, mac net.HardwareAddr, ip *terwayTypes.IPSet) error {
	exec := func(ip net.IP) error {
		family := syscall.AF_INET
		if terwayIP.IPv6(ip) {
			family = syscall.AF_INET6
		}
		err := NeighAdd(&netlink.Neigh{
			LinkIndex:    link.Attrs().Index,
			IP:           ip,
			HardwareAddr: mac,
			State:        netlink.NUD_PERMANENT,
			Family:       family,
		})
		if err != nil {
			return err
		}
		return nil
	}
	if ip.IPv4 != nil {
		err := exec(ip.IPv4)
		if err != nil {
			return err
		}
	}
	if ip.IPv6 != nil {
		err := exec(ip.IPv6)
		if err != nil {
			return err
		}
	}
	return nil
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

func IPNetToMaxMask(ipNet *terwayTypes.IPNetSet) {
	if ipNet.IPv4 != nil {
		ipNet.IPv4 = NewIPNetWithMaxMask(ipNet.IPv4)
	}
	if ipNet.IPv6 != nil {
		ipNet.IPv6 = NewIPNetWithMaxMask(ipNet.IPv6)
	}
}

// FindIPRule look up ip rules in config
func FindIPRule(rule *netlink.Rule) ([]netlink.Rule, error) {
	var filterMask uint64
	family := netlink.FAMILY_V4
	if rule.Src == nil && rule.Dst == nil {
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

	return netlink.RuleListFiltered(family, rule, filterMask)
}

func EnsureIPRule(link netlink.Link, ipNetSet *terwayTypes.IPNetSet, tableID int) (bool, error) {
	changed := false

	exec := func(ipNet *net.IPNet, expected *netlink.Rule) error {
		// 1. clean exist rules if needed
		ruleList, err := FindIPRule(expected)
		if err != nil {
			return err
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
			if del {
				changed = true
				err = RuleDel(&rule)
				if err != nil {
					return err
				}
			} else {
				found = true
			}
		}
		if found {
			return nil
		}
		changed = true
		return RuleAdd(expected)
	}

	if ipNetSet.IPv4 != nil {
		v4 := NewIPNetWithMaxMask(ipNetSet.IPv4)
		// 2. add host to container rule
		toContainerRule := netlink.NewRule()
		toContainerRule.Dst = v4
		toContainerRule.Table = mainRouteTable
		toContainerRule.Priority = toContainerPriority

		err := exec(v4, toContainerRule)
		if err != nil {
			return changed, err
		}

		// 3. add from container rule
		fromContainerRule := netlink.NewRule()
		fromContainerRule.IifName = link.Attrs().Name
		fromContainerRule.Src = v4
		fromContainerRule.Table = tableID
		fromContainerRule.Priority = fromContainerPriority

		err = exec(v4, fromContainerRule)
		if err != nil {
			return changed, err
		}
	}
	if ipNetSet.IPv6 != nil {
		v6 := NewIPNetWithMaxMask(ipNetSet.IPv6)

		// 2. add host to container rule
		toContainerRule := netlink.NewRule()
		toContainerRule.Dst = v6
		toContainerRule.Table = mainRouteTable
		toContainerRule.Priority = toContainerPriority

		err := exec(v6, toContainerRule)
		if err != nil {
			return changed, err
		}

		// 3. add from container rule
		fromContainerRule := netlink.NewRule()
		fromContainerRule.IifName = link.Attrs().Name
		fromContainerRule.Src = v6
		fromContainerRule.Table = tableID
		fromContainerRule.Priority = fromContainerPriority

		err = exec(v6, fromContainerRule)
		if err != nil {
			return changed, err
		}
	}
	return changed, nil
}

func DelIPRulesByIP(ipNet *net.IPNet) error {
	var ruleList []netlink.Rule
	var err error
	if terwayIP.IPv6(ipNet.IP) {
		ruleList, err = netlink.RuleList(netlink.FAMILY_V6)
	} else {
		ruleList, err = netlink.RuleList(netlink.FAMILY_V4)
	}
	if err != nil {
		return fmt.Errorf("error get ip rule, %w", err)
	}

	for _, rule := range ruleList {
		if terwayIP.NetEqual(ipNet, rule.Src) || terwayIP.NetEqual(ipNet, rule.Dst) {
			innerErr := RuleDel(&rule)
			if innerErr != nil {
				rule.IifName = ""
				err = errors.Wrap(RuleDel(&rule), "error de")
			}
		}
	}
	return err
}

func EnableIPv6() error {
	err := terwaySysctl.EnsureConf("/proc/sys/net/ipv6/conf/all/disable_ipv6", "0")
	if err != nil {
		return err
	}
	err = terwaySysctl.EnsureConf("/proc/sys/net/ipv6/conf/default/disable_ipv6", "0")
	if err != nil {
		return err
	}
	return nil
}

func GetHostIP(ipv4, ipv6 bool) (*terwayTypes.IPNetSet, error) {
	var nodeIPv4, nodeIPv6 *net.IPNet

	if ipv4 {
		v4, err := k8snet.ResolveBindAddress(net.ParseIP("127.0.0.1"))
		if err != nil {
			return nil, err
		}
		if terwayIP.IPv6(v4) {
			return nil, fmt.Errorf("error get node ipv4 address.This may dure to 1. no ipv4 address 2. no ipv4 default route")
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
			return nil, fmt.Errorf("error get node ipv6 address.This may dure to 1. no ipv6 address 2. no ipv6 default route")
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

func EnsureNeighbor(link netlink.Link, hostIPSet *terwayTypes.IPNetSet) (bool, error) {
	var changed bool
	var err error

	if hostIPSet.IPv4 != nil {
		err = NeighSet(&netlink.Neigh{
			IP:           hostIPSet.IPv4.IP,
			Family:       netlink.FAMILY_V4,
			LinkIndex:    link.Attrs().Index,
			HardwareAddr: link.Attrs().HardwareAddr,
			Type:         netlink.NDA_DST,
			State:        netlink.NUD_PERMANENT,
		})
		if err != nil {
			return false, fmt.Errorf("add host ipvlan interface %s mac %s to ARP table error, %w", hostIPSet.IPv4, link.Attrs().HardwareAddr, err)
		}
		changed = true
	}
	if hostIPSet.IPv6 != nil {
		err = NeighSet(&netlink.Neigh{
			IP:           hostIPSet.IPv6.IP,
			Family:       netlink.FAMILY_V6,
			LinkIndex:    link.Attrs().Index,
			HardwareAddr: link.Attrs().HardwareAddr,
			Type:         netlink.NDA_DST,
			State:        netlink.NUD_PERMANENT,
		})
		if err != nil {
			return false, fmt.Errorf("add host ipvlan interface %s mac %s to ARP table error, %w", hostIPSet.IPv4, link.Attrs().HardwareAddr, err)
		}
		changed = true
	}
	return changed, nil
}

func EnsureVlanUntagger(link netlink.Link) error {
	if err := EnsureClsActQdsic(link); err != nil {
		return errors.Wrapf(err, "error ensure cls act qdisc for %s vlan untag", link.Attrs().Name)
	}
	filters, err := netlink.FilterList(link, netlink.HANDLE_MIN_INGRESS)
	if err != nil {
		return errors.Wrapf(err, "list ingress filter for %s error", link.Attrs().Name)
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
		return errors.Wrapf(err, "error add filter for vlan untag")
	}
	return nil
}

func EnsureClsActQdsic(link netlink.Link) error {
	qds, err := netlink.QdiscList(link)
	if err != nil {
		return errors.Wrapf(err, "list qdisc for dev %s error", link.Attrs().Name)
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
		return errors.Wrapf(err, "replace clsact qdisc for dev %s error", link.Attrs().Name)
	}
	return nil
}
