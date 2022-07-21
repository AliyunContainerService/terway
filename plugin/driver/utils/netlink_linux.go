/*
Copyright 2021 The Terway Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"fmt"
	"net"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

func NetlinkFamily(ip net.IP) int {
	if ip.To4() == nil {
		return netlink.FAMILY_V6
	}
	return netlink.FAMILY_V4
}

func LinkSetName(link netlink.Link, name string) error {
	cmd := fmt.Sprintf("ip link set %s name %s", link.Attrs().Name, name)
	Log.Infof(cmd)
	err := netlink.LinkSetName(link, name)
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}

func LinkAdd(link netlink.Link) error {
	cmd := fmt.Sprintf("ip link add %s type %s", link.Attrs().Name, link.Type())
	Log.Infof(cmd)
	err := netlink.LinkAdd(link)
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}

func LinkSetUp(link netlink.Link) error {
	cmd := fmt.Sprintf("ip link set %s up", link.Attrs().Name)
	Log.Infof(cmd)
	err := netlink.LinkSetUp(link)
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}

func LinkSetDown(link netlink.Link) error {
	cmd := fmt.Sprintf("ip link set %s down", link.Attrs().Name)
	Log.Infof(cmd)
	err := netlink.LinkSetDown(link)
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}

func LinkDel(link netlink.Link) error {
	cmd := fmt.Sprintf("ip link del %s", link.Attrs().Name)
	Log.Infof(cmd)
	err := netlink.LinkDel(link)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); ok {
			return nil
		}
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}

func LinkSetMTU(link netlink.Link, mtu int) error {
	cmd := fmt.Sprintf("ip link set %s mtu %d", link.Attrs().Name, mtu)
	Log.Infof(cmd)
	err := netlink.LinkSetMTU(link, mtu)
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}

func AddrDel(link netlink.Link, addr *netlink.Addr) error {
	cmd := fmt.Sprintf("ip addr del %s dev %s", addr.IPNet.String(), link.Attrs().Name)
	Log.Infof(cmd)
	err := netlink.AddrDel(link, addr)
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}

func AddrReplace(link netlink.Link, addr *netlink.Addr) error {
	cmd := fmt.Sprintf("ip addr replace %s dev %s", addr.IPNet.String(), link.Attrs().Name)
	Log.Infof(cmd)
	err := netlink.AddrReplace(link, addr)
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}

func RouteReplace(route *netlink.Route) error {
	cmd := fmt.Sprintf("ip route replace %s", route.String())
	Log.Infof(cmd)
	err := netlink.RouteReplace(route)
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}

func RouteDel(route *netlink.Route) error {
	cmd := fmt.Sprintf("ip route del %s", route.String())
	Log.Infof(cmd)
	err := netlink.RouteDel(route)
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}

func NeighSet(neigh *netlink.Neigh) error {
	cmd := fmt.Sprintf("ip neigh replace %s", neigh.String())
	Log.Infof(cmd)
	err := netlink.NeighSet(neigh)
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}

func RuleAdd(rule *netlink.Rule) error {
	cmd := fmt.Sprintf("ip rule add %s", rule.String())
	Log.Infof(cmd)
	err := netlink.RuleAdd(rule)
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}

func RuleDel(rule *netlink.Rule) error {
	cmd := fmt.Sprintf("ip rule del %s", rule.String())
	Log.Infof(cmd)
	err := netlink.RuleDel(rule)
	if err != nil {
		rule.IifName = ""
		rule.OifName = ""

		err = netlink.RuleDel(rule)
		if err != nil {
			return fmt.Errorf("error %s, %w", cmd, err)
		}
	}
	return nil
}

func LinkSetNsFd(link netlink.Link, netNS ns.NetNS) error {
	cmd := fmt.Sprintf("ip link set %s netns %s", link.Attrs().Name, netNS.Path())
	Log.Infof(cmd)
	err := netlink.LinkSetNsFd(link, int(netNS.Fd()))
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}

func QdiscReplace(qdisc netlink.Qdisc) error {
	cmd := fmt.Sprintf("tc qdisc replace %s", qdisc.Attrs().String())
	Log.Infof(cmd)
	err := netlink.QdiscReplace(qdisc)
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}
func QdiscDel(qdisc netlink.Qdisc) error {
	cmd := fmt.Sprintf("tc qdisc del %s", qdisc.Attrs().String())
	Log.Infof(cmd)
	err := netlink.QdiscDel(qdisc)
	if err != nil {
		return fmt.Errorf("error %s, %w", cmd, err)
	}
	return nil
}
