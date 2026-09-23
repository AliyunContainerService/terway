//go:build linux

package main

import (
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
)

// resolveExclusiveNodeIP returns the node address(es) used as the SNAT source
// for the eniOnly same-node Service fix. It mirrors the helper the daemon uses
// when programming the exclusive-ENI host routes (utils.GetHostIP).
//
// IPv4 is always requested; IPv6 is only resolved when requested so a
// dual-stack policy container does not fail on an IPv4-only node.
func resolveExclusiveNodeIP(ipv4, ipv6 bool) (string, string, error) {
	ips, err := utils.GetHostIP(ipv4, ipv6)
	if err != nil {
		return "", "", err
	}
	var v4, v6 string
	if ips != nil && ips.IPv4 != nil {
		v4 = ips.IPv4.IP.String()
	}
	if ips != nil && ips.IPv6 != nil {
		v6 = ips.IPv6.IP.String()
	}
	return v4, v6, nil
}
