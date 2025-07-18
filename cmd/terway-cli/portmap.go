package main

import (
	"context"
	"fmt"
	"net"

	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/coreos/go-iptables/iptables"
	"github.com/vishvananda/netlink"
)

// Configure iptables and ip rule
func configureNetworkRules() error {
	ctx := context.Background()

	var (
		mark             = 0x10
		mask             = 0x10
		tableID          = 100
		defaultInterface = "eth0"
		comment          = "terway-portmap" // Rule comment
		rulePrio         = 600
	)

	markHexStr := fmt.Sprintf("0x%X", mark)
	maskHexStr := fmt.Sprintf("0x%X", mask)

	eth0, err := netlink.LinkByName(defaultInterface)
	if err != nil {
		return fmt.Errorf("failed to get interface %s: %v", defaultInterface, err)
	}

	routes, err := netlink.RouteList(eth0, netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("failed to get routes for interface %s: %v", defaultInterface, err)
	}

	var defaultGw net.IP
	for _, route := range routes {
		if route.Dst != nil {
			continue
		}
		defaultGw = route.Gw
		break
	}

	if defaultGw == nil {
		return fmt.Errorf("no default gateway found")
	}

	ipt, err := iptables.New()
	if err != nil {
		return err
	}

	nfRules := []ConnmarkRule{
		{
			Table: "mangle",
			Chain: "PREROUTING",
			Args: []string{
				"-i", defaultInterface,
				"-j", "CONNMARK", "--set-xmark", fmt.Sprintf("%s/%s", markHexStr, maskHexStr), "-m", "comment", "--comment", comment},
		},
		{
			Table: "mangle",
			Chain: "PREROUTING",
			Args: []string{
				"-j", "CONNMARK", "--restore-mark", "--mask", maskHexStr, "-m", "comment", "--comment", comment},
		},
	}

	for _, rule := range nfRules {
		err = ensureNFRules(ipt, &rule)
		if err != nil {
			return fmt.Errorf("failed to add nf rule: %v", err)
		}
	}
	// should only needed on ipv4 case
	prio := rulePrio
	fwMark := mark
	fwMask := mask
	family := netlink.FAMILY_V4

	rule := netlink.NewRule()
	rule.Priority = prio
	rule.Family = family
	rule.Table = tableID
	rule.Mark = fwMark
	rule.Mask = fwMask

	_, err = utils.EnsureIPRule(ctx, rule)
	if err != nil {
		return fmt.Errorf("failed to add ip rule: %v", err)
	}
	route := &netlink.Route{
		LinkIndex: eth0.Attrs().Index,
		Dst:       &net.IPNet{IP: net.ParseIP("0.0.0.0"), Mask: net.CIDRMask(0, 32)},
		Gw:        defaultGw,
		Table:     tableID,
		Scope:     netlink.SCOPE_UNIVERSE,
		Flags:     int(netlink.FLAG_ONLINK),
	}
	_, err = utils.EnsureRoute(ctx, route)
	if err != nil {
		return fmt.Errorf("failed to add route: %v", err)
	}
	return nil
}

type ConnmarkRule struct {
	Table string
	Chain string

	Args []string
}

func ensureNFRules(ipt *iptables.IPTables, rule *ConnmarkRule) error {
	exists, err := ipt.Exists(rule.Table, rule.Chain, rule.Args...)
	if err != nil {
		return fmt.Errorf("failed to check if rule exists: %w", err)
	}

	if exists {
		fmt.Printf("Rule already exists in %s table %s chain %v\n", rule.Table, rule.Chain, rule.Args)
		return nil
	}

	err = ipt.Append(rule.Table, rule.Chain, rule.Args...)
	if err != nil {
		return fmt.Errorf("failed to add rule: %w", err)
	}
	fmt.Printf("Added rule to %s table %s chain %v\n", rule.Table, rule.Chain, rule.Args)
	return nil
}
