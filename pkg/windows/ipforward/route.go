//go:build windows
// +build windows

package ipforward

import (
	"bytes"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/pkg/errors"

	"github.com/AliyunContainerService/terway/pkg/windows/converters"
	"github.com/AliyunContainerService/terway/pkg/windows/syscalls"
)

const (
	DefaultRouteMetric int = 256
)

type Route struct {
	LinkIndex         int
	DestinationSubnet *net.IPNet
	NextHop           net.IP
	RouteMetric       int
}

func (r Route) String() string {
	return fmt.Sprintf("link: %d, destnation: %s, nexthop: %s, metric: %d",
		r.LinkIndex,
		r.DestinationSubnet.String(),
		r.NextHop.String(),
		r.RouteMetric,
	)
}

func (r *Route) Equal(route Route) bool {
	if r.DestinationSubnet.IP.Equal(route.DestinationSubnet.IP) && r.NextHop.Equal(route.NextHop) && bytes.Equal(r.DestinationSubnet.Mask, route.DestinationSubnet.Mask) {
		return true
	}

	return false
}

// NewNetRouteForInterface creates a new route for given interface.
func NewNetRouteForInterface(ifaceIndex int, destinationSubnet net.IPNet, nextHop net.IP) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if err == nil {
				err = fmt.Errorf("panic while creating new routes: %v", r)
			}
		}
	}()

	var family uint32 = syscall.AF_INET
	if destinationSubnet.IP.To4() == nil {
		family = syscall.AF_INET6
	}
	ifRow := &syscalls.MibIpInterfaceRow{
		Family:         family,
		InterfaceIndex: uint32(ifaceIndex),
	}
	if err := syscalls.GetIpInterfaceEntry(ifRow); err != nil {
		return fmt.Errorf("could not get interface %d: %v", ifaceIndex, err)
	}

	frDestinationAddress := converters.Inet_aton(destinationSubnet.IP.String())
	frDestinationMaskIp := net.ParseIP("255.255.255.255").Mask(destinationSubnet.Mask)
	frDestinationMask := converters.Inet_aton(frDestinationMaskIp.String())
	frNextHop := converters.Inet_aton(nextHop.String())

	fr := &syscalls.MibIpForwardRow{
		ForwardDest:      frDestinationAddress,
		ForwardMask:      frDestinationMask,
		ForwardPolicy:    uint32(0),
		ForwardNextHop:   frNextHop,
		ForwardIfIndex:   uint32(ifaceIndex),
		ForwardType:      uint32(0), // MIB_IPROUTE_TYPE_DIRECT: A local routes where the next hop is the final destination (a local interface).
		ForwardProto:     uint32(3), // MIB_IPPROTO_NETMGMT: A static routes.
		ForwardAge:       uint32(0),
		ForwardNextHopAS: uint32(0),
		ForwardMetric1:   ifRow.Metric + uint32(DefaultRouteMetric),
		ForwardMetric2:   uint32(0),
		ForwardMetric3:   uint32(0),
		ForwardMetric4:   uint32(0),
		ForwardMetric5:   uint32(0),
	}

	if err := syscalls.CreateIpForwardEntry(fr); err != nil {
		return fmt.Errorf("failed to create nets routes: %v", err)
	}

	return nil
}

// RemoveNetRoutesForInterface removes existing routes for given interface.
func RemoveNetRoutesForInterface(ifaceIndex int, destinationSubnet *net.IPNet, nextHop *net.IP) error {
	if ifaceIndex < 0 {
		return nil
	}
	return removeNetRoutes(ifaceIndex, destinationSubnet, nextHop)
}

// RemoveNetRoutes returns given destination subnet routes.
func RemoveNetRoutes(destinationSubnet *net.IPNet) error {
	return removeNetRoutes(-1, destinationSubnet, nil)
}

func removeNetRoutes(ifaceIndex int, destinationSubnet *net.IPNet, nextHop *net.IP) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if err == nil {
				err = errors.Errorf("panic while deleting nets routes: %v", r)
			}
		}
	}()

	// find out how big our buffer needs to be
	b := make([]byte, 1)
	ft := (*syscalls.MibIpForwardTable)(unsafe.Pointer(&b[0]))
	ol := uint32(0)
	_ = syscalls.GetIpForwardTable(ft, &ol, false)

	// start to get table
	b = make([]byte, ol)
	ft = (*syscalls.MibIpForwardTable)(unsafe.Pointer(&b[0]))
	if err := syscalls.GetIpForwardTable(ft, &ol, false); err != nil {
		return errors.Wrapf(err, "failed to remove nets routes: could not call system GetAdaptersInfo")
	}

	// iterate to find
	for i := 0; i < int(ft.NumEntries); i++ {
		fr := *(*syscalls.MibIpForwardRow)(unsafe.Pointer(
			uintptr(unsafe.Pointer(&ft.Table[0])) + uintptr(i)*uintptr(unsafe.Sizeof(ft.Table[0])), // head idx + offset
		))

		frIfIndex := int(fr.ForwardIfIndex)
		if ifaceIndex != -1 &&
			frIfIndex != ifaceIndex {
			continue
		}

		frDestinationAddress := converters.Inet_ntoa(fr.ForwardDest)
		frDestinationMask := converters.Inet_ntoa(fr.ForwardMask)
		frDestinationIpNet := parseStringToIpNet(frDestinationAddress, frDestinationMask)
		if destinationSubnet != nil &&
			!isIpNetsEqual(frDestinationIpNet, destinationSubnet) {
			continue
		}

		if nextHop != nil &&
			len(*nextHop) != 0 {
			frNextHop := converters.Inet_ntoa(fr.ForwardNextHop)
			if frNextHop != nextHop.String() {
				continue
			}
		}

		if err := syscalls.DeleteIpForwardEntry(&fr); err != nil {
			return errors.Wrapf(err, "failed to delete nets routes")
		}
		return nil
	}
	return nil
}

// GetNetRoutesForInterface returns nets routes for given interface index and destination subnet.
func GetNetRoutesForInterface(ifaceIndex int, destinationSubnet *net.IPNet) ([]Route, error) {
	if ifaceIndex < 0 {
		return nil, nil
	}
	return getNetRoutes(ifaceIndex, destinationSubnet)
}

// GetNetRoutes returns all nets routes.
func GetNetRoutes() ([]Route, error) {
	return getNetRoutes(-1, nil)
}

func getNetRoutes(ifaceIndex int, destinationSubnet *net.IPNet) (routes []Route, err error) {
	defer func() {
		if r := recover(); r != nil {
			if err == nil {
				err = errors.Errorf("panic while getting nets routes: %v", r)
			}
		}
	}()

	// find out how big our buffer needs to be
	b := make([]byte, 1)
	ft := (*syscalls.MibIpForwardTable)(unsafe.Pointer(&b[0]))
	ol := uint32(0)
	_ = syscalls.GetIpForwardTable(ft, &ol, false)

	// start to get table
	b = make([]byte, ol)
	ft = (*syscalls.MibIpForwardTable)(unsafe.Pointer(&b[0]))
	if err := syscalls.GetIpForwardTable(ft, &ol, false); err != nil {
		return nil, errors.Wrap(err, "failed to get nets routes: could not call system GetAdaptersInfo")
	}

	// iterate to find
	for i := 0; i < int(ft.NumEntries); i++ {
		fr := *(*syscalls.MibIpForwardRow)(unsafe.Pointer(
			uintptr(unsafe.Pointer(&ft.Table[0])) + uintptr(i)*uintptr(unsafe.Sizeof(ft.Table[0])), // head idx + offset
		))

		frIfIndex := int(fr.ForwardIfIndex)
		if ifaceIndex != -1 &&
			frIfIndex != ifaceIndex {
			continue
		}

		frDestinationAddress := converters.Inet_ntoa(fr.ForwardDest)
		frDestinationMask := converters.Inet_ntoa(fr.ForwardMask)
		frDestinationIpNet := parseStringToIpNet(frDestinationAddress, frDestinationMask)
		if destinationSubnet != nil &&
			!isIpNetsEqual(frDestinationIpNet, destinationSubnet) {
			continue
		}

		frNextHop := converters.Inet_ntoa(fr.ForwardNextHop)
		frMetric := int(fr.ForwardMetric1)

		routes = append(routes, Route{
			LinkIndex:         frIfIndex,
			DestinationSubnet: frDestinationIpNet,
			NextHop:           net.ParseIP(frNextHop),
			RouteMetric:       frMetric,
		})
	}

	return routes, nil
}

func parseStringToIpNet(addr, mask string) *net.IPNet {
	ipNet := &net.IPNet{
		IP:   net.ParseIP(addr),
		Mask: make(net.IPMask, net.IPv4len),
	}
	for i, mask := range strings.SplitN(mask, ".", 4) {
		aInt, _ := strconv.ParseInt(mask, 10, 8)
		aIntByte := byte(aInt)
		ipNet.Mask[i] = aIntByte
	}

	ipNet.IP = ipNet.IP.Mask(ipNet.Mask)
	return ipNet
}

func isIpNetsEqual(left, right *net.IPNet) bool {
	return left.IP.Equal(right.IP) && bytes.Equal(left.Mask, right.Mask)
}
