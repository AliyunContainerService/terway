package datapath

import (
	"net"
)

const (
	// mainRouteTable the system "main" route table id
	mainRouteTable        = 254
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
