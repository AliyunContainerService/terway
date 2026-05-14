package daemon

import (
	"context"
	"encoding/binary"
	"errors"
	"net"

	"github.com/samber/lo"
	"github.com/vishvananda/netlink"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/plugin/datapath"
	cnitypes "github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/types"
)

func gcPolicyRoutes(ctx context.Context, mac string, containerIPNet *types.IPNetSet, namespace, name string) error {
	index, err := link.GetDeviceNumber(mac)
	if err != nil {
		if errors.Is(err, link.ErrNotFound) {
			return nil
		}
		return err
	}
	vethName, _ := link.VethNameForPod(name, namespace, "", "cali")
	p := &datapath.PolicyRoute{}
	err = p.Teardown(ctx, &cnitypes.TeardownCfg{
		HostVETHName:   vethName,
		ContainerIPNet: containerIPNet,
		ENIIndex:       int(index),
	}, nil)
	return err
}

func gcLeakedRules(existIP sets.Set[string]) {
	links, err := netlink.LinkList()
	if err != nil {
		serviceLog.Error(err, "error list links")
		return
	}

	ipvlLinks := lo.Filter(links, func(item netlink.Link, index int) bool {
		_, ok := item.(*netlink.IPVlan)
		return ok
	})

	gcRoutes(ipvlLinks, existIP)

	normalLinks := lo.Filter(links, func(item netlink.Link, index int) bool {
		_, ok := item.(*netlink.Device)
		return ok
	})

	gcTCFilters(normalLinks, existIP)
}

func gcRoutes(links []netlink.Link, existIP sets.Set[string]) {
	for _, link := range links {
		for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
			routes, err := netlink.RouteList(link, family)
			if err != nil {
				serviceLog.Error(err, "gc list route", "link", link, "family", family)
				continue
			}
			for _, route := range routes {
				if route.Dst == nil {
					continue
				}
				if existIP.Has(route.Dst.IP.String()) {
					continue
				}

				serviceLog.Info("gc del route", "route", route)
				err = netlink.RouteDel(&route)
				if err != nil {
					serviceLog.Error(err, "gc del route", "route", route)
				}
			}
		}
	}
}

func gcTCFilters(links []netlink.Link, existIP sets.Set[string]) {
	u32IPv4Map := make(map[uint32]struct{})
	for _, item := range existIP.UnsortedList() {
		ip := net.ParseIP(item)
		if ip == nil || ip.To4() == nil {
			continue
		}
		u32IPv4Map[binary.BigEndian.Uint32(ip.To4())] = struct{}{}
	}

	for _, link := range links {
		filters, err := netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
		if err != nil {
			serviceLog.Error(err, "gc list filter", "link", link)
			continue
		}
		for _, filter := range filters {
			u32, ok := filter.(*netlink.U32)
			if !ok {
				continue
			}

			if u32.Sel == nil || len(u32.Actions) != 1 {
				continue
			}
			_, ok = u32.Actions[0].(*netlink.VlanAction)
			if !ok {
				continue
			}

			switch {
			case u32.Priority == 50001 && len(u32.Sel.Keys) == 1 && u32.Sel.Keys[0].Off == 12:
				// IPv4 VLAN push filter: single key at offset 12 (src IPv4)
				if _, alive := u32IPv4Map[u32.Sel.Keys[0].Val]; alive {
					continue
				}
			case len(u32.Sel.Keys) == 4 && (u32.Priority == 50002 || u32.Priority == 50001):
				// IPv6 VLAN push filter: 4 keys at offsets 8,12,16,20.
				// New filters use priority 50002; legacy filters used 50001.
				ip := ipv6FromU32Keys(u32.Sel.Keys)
				if ip == nil || existIP.Has(ip.String()) {
					continue
				}
			default:
				continue
			}

			serviceLog.Info("gc tc filter", "filter", filter)
			err = netlink.FilterDel(filter)
			if err != nil {
				serviceLog.Error(err, "gc list filter", "link", link)
			}
		}
	}
}

// ipv6FromU32Keys reconstructs an IPv6 src address from U32 match keys.
// The keys must cover exactly offsets 8, 12, 16, 20 (the IPv6 src field) once
// each; otherwise nil is returned so the caller will not GC unrelated filters.
func ipv6FromU32Keys(keys []netlink.TcU32Key) net.IP {
	if len(keys) != 4 {
		return nil
	}
	ip := make(net.IP, 16)
	var seen uint8
	for _, key := range keys {
		byteOff := int(key.Off) - 8
		if byteOff < 0 || byteOff%4 != 0 || byteOff >= 16 {
			return nil
		}
		bit := uint8(1) << (byteOff / 4)
		if seen&bit != 0 {
			return nil
		}
		seen |= bit
		binary.BigEndian.PutUint32(ip[byteOff:byteOff+4], key.Val)
	}
	if seen != 0x0f {
		return nil
	}
	return ip
}
