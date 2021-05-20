package driver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/utils/sysctl"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	fileLockTimeOut = 11 * time.Second
)

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

// EnsureLinkUp set link up,return changed and err
func EnsureLinkUp(link netlink.Link) (bool, error) {
	if link.Attrs().Flags&net.FlagUp != 0 {
		return false, nil
	}
	return true, netlink.LinkSetUp(link)
}

// EnsureLinkMTU set link mtu,return changed and err
func EnsureLinkMTU(link netlink.Link, mtu int) (bool, error) {
	if link.Attrs().MTU == mtu {
		return false, nil
	}
	return true, netlink.LinkSetMTU(link, mtu)
}

// EnsureDefaultRoute set default route
func EnsureDefaultRoute(link netlink.Link, gw net.IP) (bool, error) {
	err := ip.ValidateExpectedRoute([]*types.Route{
		{
			Dst: *defaultRoute,
			GW:  gw,
		},
	})
	if err == nil {
		return false, nil
	}

	if !strings.Contains(err.Error(), "not found") {
		return false, err
	}

	err = netlink.RouteReplace(&netlink.Route{
		LinkIndex: link.Attrs().Index,
		Scope:     netlink.SCOPE_UNIVERSE,
		Flags:     int(netlink.FLAG_ONLINK),
		Dst:       defaultRoute,
		Gw:        gw,
	})
	return true, err
}

// EnsureHostToContainerRoute create policy route
func EnsureHostToContainerRoute(addr *net.IPNet, linkIndex int) error {
	family := netlink.FAMILY_V4
	if addr.IP.To4() == nil {
		family = netlink.FAMILY_V6
	}
	routes, err := netlink.RouteListFiltered(family, nil, 0)
	if err != nil {
		Log.Debugf("error list route: %v", err)
		return fmt.Errorf("error list route: %v", err)
	}
	// 1. create host to veth route
	found := false
	for _, r := range routes {
		if r.Table != mainRouteTable {
			continue
		}
		if r.Scope != netlink.SCOPE_LINK {
			continue
		}
		if r.Dst == nil {
			continue
		}
		if !r.Dst.IP.Equal(addr.IP) {
			continue
		}
		if r.LinkIndex != linkIndex || !bytes.Equal(r.Dst.Mask, addr.Mask) {
			// del link
			Log.Debugf("delete route %#v ,linkIndex %d", r, linkIndex)
			err := netlink.RouteDel(&r)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
			}
		}
		found = true
	}
	if !found {
		newRoute := &netlink.Route{
			LinkIndex: linkIndex,
			Scope:     netlink.SCOPE_LINK,
			Dst:       addr,
		}
		Log.Debugf("add route %s", newRoute.String())
		err := netlink.RouteAdd(newRoute)
		if err != nil {
			if os.IsExist(err) {
				return nil
			}
			return err
		}
	}
	return nil
}

// Log Log
var Log = MyLog{
	l: log.New(ioutil.Discard, "", log.LstdFlags),
}

type MyLog struct {
	l     *log.Logger
	debug bool
}

// Debugf Debugf
func (m *MyLog) Debugf(format string, v ...interface{}) {
	if !m.debug {
		return
	}
	m.l.Printf(format, v...)
}

// Debug Debug
func (m *MyLog) Debug(v ...interface{}) {
	if !m.debug {
		return
	}
	m.l.Print(v...)
}

// SetDebug SetDebug
func (m *MyLog) SetDebug(d bool, fd *os.File) {
	if !d {
		m.l.SetOutput(ioutil.Discard)
		return
	}
	m.debug = true
	m.l.SetOutput(fd)
}

// JSONStr json to string
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
			l.Close()
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
