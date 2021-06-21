package driver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	terwaySysctl "github.com/AliyunContainerService/terway/pkg/sysctl"
	terwayTypes "github.com/AliyunContainerService/terway/types"

	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/plugins/pkg/ip"
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

func NewDefaultLogger() *logrus.Logger {
	logger := logrus.New()
	logger.Formatter = &logrus.TextFormatter{
		DisableTimestamp: true,
		DisableColors:    true,
		DisableQuote:     true,
	}
	logger.SetLevel(logrus.InfoLevel)
	return logger
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
func EnsureHostNsConfig() error {
	for _, key := range []string{"default", "all"} {
		for _, cfg := range ipv4NetConfig {
			err := terwaySysctl.EnsureConf(fmt.Sprintf(cfg[0], key), cfg[1])
			if err != nil {
				return err
			}
		}
	}

	return EnsureNetConfSet(true, false)
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

// EnsureAddr take the ipNet set and ensure only one IP for each family is present on link
// it will remove other unmatched IPs
func EnsureAddr(link netlink.Link, ipNetSet *terwayTypes.IPNetSet, scope int) (bool, error) {
	var changed bool

	exec := func(expect *net.IPNet) error {
		addrList, err := netlink.AddrList(link, NetlinkFamily(expect.IP))
		if err != nil {
			return fmt.Errorf("error list address from if %s, %w", link.Attrs().Name, err)
		}

		found := false
		for _, addr := range addrList {
			if !addr.IP.IsGlobalUnicast() {
				continue
			}

			if addr.IPNet.String() == expect.String() && (scope == -1 || addr.Scope == scope) {
				found = true
				continue
			}

			err := AddrDel(link, &addr)
			if err != nil {
				return err
			}
		}
		if found {
			return nil
		}
		changed = true

		newAddr := &netlink.Addr{IPNet: expect}
		if scope > 0 {
			newAddr.Scope = scope
		}
		return AddrReplace(link, newAddr)
	}

	if ipNetSet.IPv4 != nil {
		err := exec(ipNetSet.IPv4)
		if err != nil {
			return changed, err
		}
	}
	if ipNetSet.IPv6 != nil {
		err := exec(ipNetSet.IPv6)
		if err != nil {
			return changed, err
		}
	}
	return changed, nil
}

func EnsureDefaultRoute(link netlink.Link, gw *terwayTypes.IPSet) (bool, error) {
	var changed bool
	if gw.IPv4 != nil {
		ok, err := ensureRoute(link, defaultRoute, netlink.SCOPE_UNIVERSE, int(netlink.FLAG_ONLINK), gw.IPv4)
		if err != nil {
			return changed, err
		}
		if ok {
			changed = true
		}
	}
	if gw.IPv6 != nil {
		ok, err := ensureRoute(link, defaultRouteIPv6, netlink.SCOPE_UNIVERSE, int(netlink.FLAG_ONLINK), gw.IPv6)
		if err != nil {
			return changed, err
		}
		if ok {
			changed = true
		}
	}
	return changed, nil
}

func EnsureRoute(link netlink.Link, hostIPSet *terwayTypes.IPNetSet) (bool, error) {
	var changed bool
	if hostIPSet.IPv4 != nil {
		ok, err := ensureRoute(link, hostIPSet.IPv4, netlink.SCOPE_LINK, 0, nil)
		if err != nil {
			return changed, err
		}
		if ok {
			changed = true
		}
	}
	if hostIPSet.IPv6 != nil {
		ok, err := ensureRoute(link, hostIPSet.IPv6, netlink.SCOPE_LINK, 0, nil)
		if err != nil {
			return changed, err
		}
		if ok {
			changed = true
		}
	}
	return changed, nil
}

func ensureRoute(link netlink.Link, dst *net.IPNet, scope netlink.Scope, flags int, gw net.IP) (bool, error) {
	var err error
	if gw != nil {
		err = ip.ValidateExpectedRoute([]*types.Route{
			{
				Dst: *dst,
				GW:  gw,
			},
		})
		if err == nil {
			return false, nil
		}
		if !strings.Contains(err.Error(), "not found") {
			return false, err
		}
	}
	r := &netlink.Route{
		LinkIndex: link.Attrs().Index,
		Scope:     scope,
		Flags:     flags,
		Dst:       dst,
		Gw:        gw,
	}
	err = RouteReplace(r)
	if err != nil {
		return false, err
	}
	return true, nil
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

	_, err = EnsureAddr(link, cfg.ContainerIPNet, -1)
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

func FindIPRules(ipNet *net.IPNet, found func(rule *netlink.Rule) error) error {
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
	for i := range ruleList {
		Log.Debugf("get rule %s", ruleList[i].String())
		if terwayIP.NetEqual(ipNet, ruleList[i].Src) || terwayIP.NetEqual(ipNet, ruleList[i].Dst) {
			// need check copy
			err = found(&ruleList[i])
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func EnsurePolicyRule(link netlink.Link, ipNetSet *terwayTypes.IPNetSet, tableID int) (bool, error) {
	changed := false

	exec := func(ipNet *net.IPNet, expected *netlink.Rule) error {
		// 1. clean exist rules
		found := false
		ruleList, err := netlink.RuleList(NetlinkFamily(ipNet.IP))
		if err != nil {
			return fmt.Errorf("error exec ip rule list, %w", err)
		}
		for _, rule := range ruleList {
			if expected.Src != nil && rule.Src != nil {
				if expected.Src.IP.Equal(rule.Src.IP) {
					if expected.Src.IP.String() != rule.Src.IP.String() ||
						expected.Table != rule.Table ||
						expected.Priority != rule.Priority ||
						expected.IifName != rule.IifName {
						err := RuleDel(&rule)
						if err != nil {
							if os.IsNotExist(err) {
								rule.IifName = ""
								return RuleDel(&rule)
							}
						}
						changed = true
					} else {
						found = true
					}
				}
			}
			// won't have src dst both set
			if expected.Dst != nil && rule.Dst != nil {
				if expected.Dst.IP.Equal(rule.Dst.IP) {
					if expected.Dst.IP.String() != rule.Dst.IP.String() ||
						expected.Table != rule.Table ||
						expected.Priority != rule.Priority {
						err := RuleDel(&rule)
						if err != nil {
							if os.IsNotExist(err) {
								rule.IifName = ""
								return RuleDel(&rule)
							}
						}
						changed = true
					} else {
						found = true
					}
				}
			}
		}
		if found {
			return nil
		}
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
		nodeIPv4 = &net.IPNet{
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
		err = netlink.NeighSet(&netlink.Neigh{
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
		err = netlink.NeighSet(&netlink.Neigh{
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
