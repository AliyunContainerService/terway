package datapath

import (
	"net"

	"github.com/vishvananda/netlink"

	"github.com/AliyunContainerService/terway/types"
)

const (
	toContainerPriority   = 512
	fromContainerPriority = 2048
)

// default addrs
var (
	_, defaultRoute, _     = net.ParseCIDR("0.0.0.0/0")
	_, defaultRouteIPv6, _ = net.ParseCIDR("::/0")
	LinkIP                 = net.IPv4(169, 254, 1, 1)
	LinkIPv6               = net.ParseIP("fe80::1")
	LinkIPNet              = &net.IPNet{
		IP:   LinkIP,
		Mask: net.CIDRMask(32, 32),
	}
	LinkIPNetv6 = &net.IPNet{
		IP:   LinkIPv6,
		Mask: net.CIDRMask(128, 128),
	}
)

var PrioMap = map[string]uint32{
	string(types.NetworkPrioGuaranteed): netlink.MakeHandle(1, 1), // band 0
	string(types.NetworkPrioBurstable):  netlink.MakeHandle(1, 2), // band 1
	string(types.NetworkPrioBestEffort): netlink.MakeHandle(1, 3), // band 2
	"":                                  netlink.MakeHandle(1, 2),
}
