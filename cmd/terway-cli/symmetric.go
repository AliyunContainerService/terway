package main

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/coreos/go-iptables/iptables"
	"github.com/go-logr/logr"
	"github.com/vishvananda/netlink"
	"k8s.io/klog/v2/textlogger"
)

// Configure iptables and ip rule with custom configuration
func configureNetworkRulesWithConfig(ipv4, ipv6 bool, config *types.SymmetricRoutingConfig) error {
	ctx := context.Background()
	logConfig := textlogger.NewConfig(
		textlogger.Verbosity(4),
		textlogger.Output(os.Stdout),
	)

	ctx = logr.NewContext(ctx, textlogger.NewLogger(logConfig))

	// Default values
	var (
		mark             = 0x10
		mask             = 0x10
		tableID          = 100
		defaultInterface = "eth0"
		comment          = "terway-symmetric" // Rule comment
		rulePrio         = 600
	)

	// Use user configuration if provided
	if config != nil {
		if config.Interface != "" {
			defaultInterface = config.Interface
		}
		if config.Mark > 0 {
			mark = config.Mark
		}
		if config.Mask > 0 {
			mask = config.Mask
		}
		if config.TableID > 0 {
			tableID = config.TableID
		}
		if config.RulePriority > 0 {
			rulePrio = config.RulePriority
		}
		if config.Comment != "" {
			comment = config.Comment
		}
	}

	markHexStr := fmt.Sprintf("0x%X", mark)
	maskHexStr := fmt.Sprintf("0x%X", mask)

	eth0, err := netlink.LinkByName(defaultInterface)
	if err != nil {
		return fmt.Errorf("failed to get interface %s: %v", defaultInterface, err)
	}

	// Configure IPv4 rules if enabled
	if ipv4 {
		if err := configureIPv4Rules(ctx, eth0, mark, mask, tableID, defaultInterface, comment, rulePrio, markHexStr, maskHexStr); err != nil {
			return fmt.Errorf("failed to configure IPv4 rules: %v", err)
		}
	}

	// Configure IPv6 rules if enabled
	if ipv6 {
		if err := configureIPv6Rules(ctx, eth0, mark, mask, tableID, defaultInterface, comment, rulePrio, markHexStr, maskHexStr); err != nil {
			return fmt.Errorf("failed to configure IPv6 rules: %v", err)
		}
	}

	return nil
}

func configureIPv4Rules(ctx context.Context, eth0 netlink.Link, mark, mask, tableID int, defaultInterface, comment string, rulePrio int, markHexStr, maskHexStr string) error {
	// Get IPv4 routes
	routes, err := netlink.RouteList(eth0, netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("failed to get IPv4 routes for interface %s: %v", defaultInterface, err)
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
		return fmt.Errorf("no IPv4 default gateway found")
	}

	// Configure IPv4 iptables
	ipt, err := iptables.New(iptables.IPFamily(iptables.ProtocolIPv4), iptables.Timeout(5))
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
			return fmt.Errorf("failed to add IPv4 nf rule: %v", err)
		}
	}

	// Configure IPv4 ip rule
	rule := netlink.NewRule()
	rule.Priority = rulePrio
	rule.Family = netlink.FAMILY_V4
	rule.Table = tableID
	rule.Mark = mark
	rule.Mask = mask

	_, err = utils.EnsureIPRule(ctx, rule)
	if err != nil {
		return fmt.Errorf("failed to add IPv4 ip rule: %v", err)
	}

	// Configure IPv4 route
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
		return fmt.Errorf("failed to add IPv4 route: %v", err)
	}

	return nil
}

func configureIPv6Rules(ctx context.Context, eth0 netlink.Link, mark, mask, tableID int, defaultInterface, comment string, rulePrio int, markHexStr, maskHexStr string) error {
	// Get IPv6 routes
	routes, err := netlink.RouteList(eth0, netlink.FAMILY_V6)
	if err != nil {
		return fmt.Errorf("failed to get IPv6 routes for interface %s: %v", defaultInterface, err)
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
		return fmt.Errorf("no IPv6 default gateway found")
	}

	// Configure IPv6 iptables
	ipt, err := iptables.New(iptables.IPFamily(iptables.ProtocolIPv6), iptables.Timeout(5))
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
			return fmt.Errorf("failed to add IPv6 nf rule: %v", err)
		}
	}

	// Configure IPv6 ip rule
	rule := netlink.NewRule()
	rule.Priority = rulePrio
	rule.Family = netlink.FAMILY_V6
	rule.Table = tableID
	rule.Mark = mark
	rule.Mask = mask

	_, err = utils.EnsureIPRule(ctx, rule)
	if err != nil {
		return fmt.Errorf("failed to add IPv6 ip rule: %v", err)
	}

	// Configure IPv6 route
	route := &netlink.Route{
		LinkIndex: eth0.Attrs().Index,
		Dst:       &net.IPNet{IP: net.ParseIP("::"), Mask: net.CIDRMask(0, 128)},
		Gw:        defaultGw,
		Table:     tableID,
		Scope:     netlink.SCOPE_UNIVERSE,
		Flags:     int(netlink.FLAG_ONLINK),
	}
	_, err = utils.EnsureRoute(ctx, route)
	if err != nil {
		return fmt.Errorf("failed to add IPv6 route: %v", err)
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
