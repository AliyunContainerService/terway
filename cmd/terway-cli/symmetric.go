//go:build linux

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
	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"github.com/vishvananda/netlink"
	"k8s.io/klog/v2/textlogger"
)

const (
	NetfilterIptables = "iptables"
	NetfilterNftables = "nftables"
)

// FirewallBackend defines the interface for firewall rule management
type FirewallBackend interface {
	// EnsureConnmarkRules ensures the connmark rules are in place
	EnsureConnmarkRules(ifaceName string, mark, mask int, comment string) error
	// Name returns the backend name
	Name() string
}

// IPTablesBackend implements FirewallBackend using iptables
type IPTablesBackend struct {
	ipt *iptables.IPTables
}

// NewIPTablesBackend creates a new iptables backend
func NewIPTablesBackend(ipFamily iptables.Protocol) (*IPTablesBackend, error) {
	ipt, err := iptables.New(iptables.IPFamily(ipFamily), iptables.Timeout(5))
	if err != nil {
		return nil, fmt.Errorf("failed to create iptables instance: %w", err)
	}
	return &IPTablesBackend{ipt: ipt}, nil
}

func (b *IPTablesBackend) Name() string {
	return NetfilterIptables
}

func (b *IPTablesBackend) EnsureConnmarkRules(ifaceName string, mark, mask int, comment string) error {
	markHexStr := fmt.Sprintf("0x%X", mark)
	maskHexStr := fmt.Sprintf("0x%X", mask)

	rules := []ConnmarkRule{
		{
			Table: "mangle",
			Chain: "PREROUTING",
			Args: []string{
				"-i", ifaceName,
				"-j", "CONNMARK", "--set-xmark", fmt.Sprintf("%s/%s", markHexStr, maskHexStr),
				"-m", "comment", "--comment", comment,
			},
		},
		{
			Table: "mangle",
			Chain: "PREROUTING",
			Args: []string{
				"-j", "CONNMARK", "--restore-mark", "--mask", maskHexStr,
				"-m", "comment", "--comment", comment,
			},
		},
	}

	for _, rule := range rules {
		if err := ensureNFRules(b.ipt, &rule); err != nil {
			return fmt.Errorf("failed to add rule: %w", err)
		}
	}
	return nil
}

// NFTablesBackend implements FirewallBackend using nftables
type NFTablesBackend struct {
	conn     *nftables.Conn
	family   nftables.TableFamily
	ipFamily int // netlink.FAMILY_V4 or netlink.FAMILY_V6
}

// NewNFTablesBackend creates a new nftables backend
func NewNFTablesBackend(ipFamily int) (*NFTablesBackend, error) {
	conn, err := nftables.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create nftables connection: %w", err)
	}

	var family nftables.TableFamily
	if ipFamily == netlink.FAMILY_V4 {
		family = nftables.TableFamilyIPv4
	} else {
		family = nftables.TableFamilyIPv6
	}

	return &NFTablesBackend{
		conn:     conn,
		family:   family,
		ipFamily: ipFamily,
	}, nil
}

func (b *NFTablesBackend) Name() string {
	return NetfilterNftables
}

func (b *NFTablesBackend) EnsureConnmarkRules(ifaceName string, mark, mask int, comment string) error {
	// Create or get the terway table
	tableName := "terway_symmetric"
	if b.ipFamily == netlink.FAMILY_V6 {
		tableName = "terway_symmetric_ipv6"
	}

	table := &nftables.Table{
		Family: b.family,
		Name:   tableName,
	}

	// Get existing table or create new one
	tables, err := b.conn.ListTables()
	if err != nil {
		return fmt.Errorf("failed to list tables: %w", err)
	}

	tableExists := false
	for _, t := range tables {
		if t.Name == tableName && t.Family == b.family {
			table = t
			tableExists = true
			break
		}
	}

	if !tableExists {
		table = b.conn.AddTable(table)
	}

	// Create or get the prerouting chain
	chain := &nftables.Chain{
		Name:     "prerouting",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityMangle,
	}

	// Check if chain exists
	chains, err := b.conn.ListChainsOfTableFamily(b.family)
	if err != nil {
		return fmt.Errorf("failed to list chains: %w", err)
	}

	chainExists := false
	for _, c := range chains {
		if c.Name == "prerouting" && c.Table.Name == tableName {
			chain = c
			chainExists = true
			break
		}
	}

	if !chainExists {
		chain = b.conn.AddChain(chain)
	}

	// Validate interface exists
	if _, err := netlink.LinkByName(ifaceName); err != nil {
		return fmt.Errorf("failed to get interface %s: %w", ifaceName, err)
	}

	// Rule 1: Set connmark on incoming interface
	// Equivalent to: iptables -t mangle -A PREROUTING -i eth0 -j CONNMARK --set-xmark mark/mask
	// nft: iifname "eth0" ct mark set ct mark & ~mask ^ mark
	rule1 := &nftables.Rule{
		Table:    table,
		Chain:    chain,
		UserData: []byte(comment),
		Exprs: []expr.Any{
			// Match input interface
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     []byte(ifaceName + "\x00"),
			},
			// Load ct mark into register 1
			&expr.Ct{
				Key:      expr.CtKeyMARK,
				Register: 1,
			},
			// Compute: reg1 = (ct_mark & ~mask) ^ mark
			// This is equivalent to --set-xmark mark/mask: (ct_mark & ~mask) | mark
			// (Since mask bits are cleared first, XOR mark == OR mark for those bits)
			&expr.Bitwise{
				SourceRegister: 1,
				DestRegister:   1,
				Len:            4,
				Mask:           binaryutil.NativeEndian.PutUint32(^uint32(mask)),
				Xor:            binaryutil.NativeEndian.PutUint32(uint32(mark)),
			},
			// Write register 1 back to ct mark
			&expr.Ct{
				Key:            expr.CtKeyMARK,
				Register:       1,
				SourceRegister: true,
			},
		},
	}

	// Rule 2: Restore mark from conntrack
	// nft add rule ip terway_symmetric prerouting meta mark set ct mark and 0x10
	rule2 := &nftables.Rule{
		Table:    table,
		Chain:    chain,
		UserData: []byte(comment),
		Exprs: []expr.Any{
			// Get ct mark
			&expr.Ct{
				Key:      expr.CtKeyMARK,
				Register: 1,
			},
			// Apply mask
			&expr.Bitwise{
				SourceRegister: 1,
				DestRegister:   1,
				Len:            4,
				Mask:           binaryutil.NativeEndian.PutUint32(uint32(mask)),
				Xor:            binaryutil.NativeEndian.PutUint32(0),
			},
			// Set meta mark
			&expr.Meta{
				Key:            expr.MetaKeyMARK,
				SourceRegister: true,
				Register:       1,
			},
		},
	}

	// Check if rules already exist by listing rules and comparing
	existingRules, err := b.conn.GetRules(table, chain)
	if err != nil {
		return fmt.Errorf("failed to get rules: %w", err)
	}

	// Simple check: if we already have rules with our comment, skip adding
	// Rule1 starts with Meta IIFNAME (interface match); Rule2 starts with Ct MARK (get ct mark)
	hasRule1 := false
	hasRule2 := false
	for _, r := range existingRules {
		if string(r.UserData) != comment {
			continue
		}
		if len(r.Exprs) == 0 {
			continue
		}
		switch e := r.Exprs[0].(type) {
		case *expr.Meta:
			if e.Key == expr.MetaKeyIIFNAME {
				hasRule1 = true
			}
		case *expr.Ct:
			if e.Key == expr.CtKeyMARK {
				hasRule2 = true
			}
		}
	}

	if !hasRule1 {
		b.conn.AddRule(rule1)
		fmt.Printf("Added nftables rule: set connmark on %s\n", ifaceName)
	} else {
		fmt.Printf("NFTables rule already exists: set connmark on %s\n", ifaceName)
	}

	if !hasRule2 {
		b.conn.AddRule(rule2)
		fmt.Printf("Added nftables rule: restore mark from conntrack\n")
	} else {
		fmt.Printf("NFTables rule already exists: restore mark from conntrack\n")
	}

	// Flush the changes
	if err := b.conn.Flush(); err != nil {
		return fmt.Errorf("failed to flush nftables rules: %w", err)
	}

	return nil
}

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
		backend          = NetfilterIptables // Default backend
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
		if config.Backend != "" {
			backend = config.Backend
		}
	}

	eth0, err := netlink.LinkByName(defaultInterface)
	if err != nil {
		return fmt.Errorf("failed to get interface %s: %v", defaultInterface, err)
	}

	// Configure IPv4 rules if enabled
	if ipv4 {
		if err := configureIPv4Rules(ctx, eth0, mark, mask, tableID, defaultInterface, comment, rulePrio, backend); err != nil {
			return fmt.Errorf("failed to configure IPv4 rules: %v", err)
		}
	}

	// Configure IPv6 rules if enabled
	if ipv6 {
		if err := configureIPv6Rules(ctx, eth0, mark, mask, tableID, defaultInterface, comment, rulePrio, backend); err != nil {
			return fmt.Errorf("failed to configure IPv6 rules: %v", err)
		}
	}

	return nil
}

func configureIPv4Rules(ctx context.Context, eth0 netlink.Link, mark, mask, tableID int, defaultInterface, comment string, rulePrio int, backend string) error {
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

	// Configure firewall rules based on backend
	var fwBackend FirewallBackend
	switch backend {
	case NetfilterNftables:
		fwBackend, err = NewNFTablesBackend(netlink.FAMILY_V4)
		if err != nil {
			return fmt.Errorf("failed to create nftables backend: %w", err)
		}
	case NetfilterIptables:
		fwBackend, err = NewIPTablesBackend(iptables.ProtocolIPv4)
		if err != nil {
			return fmt.Errorf("failed to create iptables backend: %w", err)
		}
	default:
		return fmt.Errorf("unsupported backend: %s, must be 'iptables' or 'nftables'", backend)
	}

	if err := fwBackend.EnsureConnmarkRules(defaultInterface, mark, mask, comment); err != nil {
		return fmt.Errorf("failed to add IPv4 firewall rules with %s backend: %w", fwBackend.Name(), err)
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

func configureIPv6Rules(ctx context.Context, eth0 netlink.Link, mark, mask, tableID int, defaultInterface, comment string, rulePrio int, backend string) error {
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

	// Configure firewall rules based on backend
	var fwBackend FirewallBackend
	switch backend {
	case NetfilterNftables:
		fwBackend, err = NewNFTablesBackend(netlink.FAMILY_V6)
		if err != nil {
			return fmt.Errorf("failed to create nftables backend: %w", err)
		}
	case NetfilterIptables:
		fwBackend, err = NewIPTablesBackend(iptables.ProtocolIPv6)
		if err != nil {
			return fmt.Errorf("failed to create iptables backend: %w", err)
		}
	default:
		return fmt.Errorf("unsupported backend: %s, must be 'iptables' or 'nftables'", backend)
	}

	if err := fwBackend.EnsureConnmarkRules(defaultInterface, mark, mask, comment); err != nil {
		return fmt.Errorf("failed to add IPv6 firewall rules with %s backend: %w", fwBackend.Name(), err)
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
