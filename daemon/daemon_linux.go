package daemon

import (
	"context"
	"encoding/binary"
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
		if _, ok := err.(netlink.LinkNotFoundError); ok {
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
		routes, err := netlink.RouteList(link, netlink.FAMILY_V4)
		if err != nil {
			serviceLog.Error(err, "gc list route", "link", link)
			return
		}
		for _, route := range routes {
			if route.Dst == nil {
				continue
			}
			// if not found
			if existIP.Has(route.Dst.IP.String()) {
				continue
			}

			serviceLog.Info("gc del route", "route", route)
			err = netlink.RouteDel(&route)
			if err != nil {
				serviceLog.Error(err, "gc del route", "route", route)
				return
			}
		}
	}
}

func gcTCFilters(links []netlink.Link, existIP sets.Set[string]) {
	// map ip to u32
	toU32List := lo.Map(existIP.UnsortedList(), func(item string, index int) uint32 {
		ip := net.ParseIP(item)
		if ip == nil {
			return 0
		}
		return binary.BigEndian.Uint32(ip.To4())
	})
	u32IPMap := lo.SliceToMap(toU32List, func(item uint32) (uint32, struct{}) {
		return item, struct{}{}
	})

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
			if u32.Priority != 50001 {
				continue
			}

			if u32.Sel == nil || len(u32.Sel.Keys) != 1 {
				continue
			}
			if len(u32.Actions) != 1 {
				continue
			}
			_, ok = u32.Actions[0].(*netlink.VlanAction)
			if !ok {
				continue
			}
			if u32.Sel.Keys[0].Off != 12 {
				continue
			}
			_, ok = u32IPMap[u32.Sel.Keys[0].Val]
			if ok {
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
