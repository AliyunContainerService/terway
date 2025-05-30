package datapath

import (
	"context"
	"fmt"
	"net"

	"github.com/AliyunContainerService/terway/pkg/smc"

	"github.com/AliyunContainerService/terway/plugin/driver/nic"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/plugin/driver/veth"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type PolicyRoute struct{}

func NewPolicyRoute() *PolicyRoute {
	return &PolicyRoute{}
}

func generateContCfgForPolicy(cfg *types.SetupConfig, link netlink.Link, mac net.HardwareAddr) *nic.Conf {
	var routes []*netlink.Route
	var rules []*netlink.Rule
	var neighs []*netlink.Neigh
	var sysctl map[string][]string

	if cfg.MultiNetwork {
		table := utils.GetRouteTableID(link.Attrs().Index)

		ruleIf := netlink.NewRule()
		ruleIf.OifName = cfg.ContainerIfName
		ruleIf.Table = table
		ruleIf.Priority = toContainerPriority

		rules = append(rules, ruleIf)
	}

	if cfg.ContainerIPNet.IPv4 != nil {
		// add default route
		if cfg.DefaultRoute {
			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Scope:     netlink.SCOPE_UNIVERSE,
				Dst:       defaultRoute,
				Gw:        LinkIPNet.IP,
				Flags:     int(netlink.FLAG_ONLINK),
			})
		}

		if len(cfg.ExtraRoutes) > 0 {
			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Scope:     netlink.SCOPE_LINK,
				Dst:       LinkIPNet,
			})
		}

		neighs = append(neighs, &netlink.Neigh{
			LinkIndex:    link.Attrs().Index,
			IP:           LinkIPNet.IP,
			HardwareAddr: mac,
			State:        netlink.NUD_PERMANENT,
		})

		if cfg.MultiNetwork {
			table := utils.GetRouteTableID(link.Attrs().Index)

			v4 := utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv4)

			fromContainerRule := netlink.NewRule()
			fromContainerRule.Src = v4
			fromContainerRule.Table = table
			fromContainerRule.Priority = toContainerPriority

			rules = append(rules, fromContainerRule)

			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Scope:     netlink.SCOPE_UNIVERSE,
				Dst:       defaultRoute,
				Gw:        cfg.GatewayIP.IPv4,
				Flags:     int(netlink.FLAG_ONLINK),
				Table:     table,
			})
		}
	}

	if cfg.ContainerIPNet.IPv6 != nil {
		// add default route
		if cfg.DefaultRoute {
			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Scope:     netlink.SCOPE_UNIVERSE,
				Dst:       defaultRouteIPv6,
				Gw:        LinkIPNetv6.IP,
				Flags:     int(netlink.FLAG_ONLINK),
			})
		}

		if len(cfg.ExtraRoutes) > 0 {
			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Scope:     netlink.SCOPE_LINK,
				Dst:       LinkIPNetv6,
			})
		}

		neighs = append(neighs, &netlink.Neigh{
			LinkIndex:    link.Attrs().Index,
			IP:           LinkIPNetv6.IP,
			HardwareAddr: mac,
			State:        netlink.NUD_PERMANENT,
		})

		if cfg.MultiNetwork {
			table := utils.GetRouteTableID(link.Attrs().Index)

			v6 := utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv6)

			fromContainerRule := netlink.NewRule()
			fromContainerRule.Src = v6
			fromContainerRule.Table = table
			fromContainerRule.Priority = toContainerPriority

			rules = append(rules, fromContainerRule)

			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Scope:     netlink.SCOPE_UNIVERSE,
				Dst:       defaultRouteIPv6,
				Gw:        cfg.GatewayIP.IPv6,
				Flags:     int(netlink.FLAG_ONLINK),
				Table:     table,
			})
		}
		sysctl = utils.GenerateIPv6Sysctl(cfg.ContainerIfName, true, false)
	}

	for i := range cfg.ExtraRoutes {
		if cfg.ExtraRoutes[i].GW != nil {
			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Scope:     netlink.SCOPE_UNIVERSE,
				Flags:     int(netlink.FLAG_ONLINK),
				Dst:       &cfg.ExtraRoutes[i].Dst,
				Gw:        cfg.ExtraRoutes[i].GW,
			})
		} else {
			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Scope:     netlink.SCOPE_LINK,
				Dst:       &cfg.ExtraRoutes[i].Dst,
			})
		}
	}

	contCfg := &nic.Conf{
		IfName: cfg.ContainerIfName,
		MTU:    cfg.MTU,
		Addrs:  utils.NewIPNetToMaxMask(cfg.ContainerIPNet),
		Routes: routes,
		Rules:  rules,
		Neighs: neighs,
		SysCtl: sysctl,
	}

	return contCfg
}

func GenerateHostPeerCfgForPolicy(cfg *types.SetupConfig, link netlink.Link, table int) *nic.Conf {
	var addrs []*netlink.Addr
	var routes []*netlink.Route
	var rules []*netlink.Rule
	var sysctl map[string][]string

	if cfg.ContainerIPNet.IPv4 != nil {
		if len(cfg.ExtraRoutes) > 0 {
			addrs = append(addrs, &netlink.Addr{
				IPNet: LinkIPNet,
			})
		}

		// add route to container
		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst:       utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv4),
		})

		v4 := utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv4)
		// 2. add host to container rule
		toContainerRule := netlink.NewRule()
		toContainerRule.Dst = v4
		toContainerRule.Table = unix.RT_TABLE_MAIN
		toContainerRule.Priority = toContainerPriority

		fromContainerRule := netlink.NewRule()
		fromContainerRule.Src = v4
		fromContainerRule.Table = table
		fromContainerRule.Priority = fromContainerPriority

		rules = append(rules, toContainerRule, fromContainerRule)
	}

	if cfg.ContainerIPNet.IPv6 != nil {
		if len(cfg.ExtraRoutes) > 0 {
			addrs = append(addrs, &netlink.Addr{
				IPNet: LinkIPNetv6,
			})
		}

		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst:       utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv6),
		})

		v6 := utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv6)
		// 2. add host to container rule
		toContainerRule := netlink.NewRule()
		toContainerRule.Dst = v6
		toContainerRule.Table = unix.RT_TABLE_MAIN
		toContainerRule.Priority = toContainerPriority

		fromContainerRule := netlink.NewRule()
		fromContainerRule.Src = v6
		fromContainerRule.Table = table
		fromContainerRule.Priority = fromContainerPriority

		rules = append(rules, toContainerRule, fromContainerRule)

		sysctl = utils.GenerateIPv6Sysctl(link.Attrs().Name, true, true)
	}

	return &nic.Conf{
		MTU:       cfg.MTU,
		Addrs:     addrs,
		Routes:    routes,
		Rules:     rules,
		SysCtl:    sysctl,
		StripVlan: false,
	}
}

func GenerateENICfgForPolicy(cfg *types.SetupConfig, link netlink.Link, table int) *nic.Conf {
	var routes []*netlink.Route
	var rules []*netlink.Rule
	var neighs []*netlink.Neigh
	var sysctl map[string][]string

	if cfg.ContainerIPNet.IPv4 != nil {
		// add default route
		gw := cfg.GatewayIP.IPv4
		if cfg.StripVlan {
			gw = cfg.ENIGatewayIP.IPv4
		}
		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_UNIVERSE,
			Table:     table,
			Dst:       defaultRoute,
			Gw:        gw,
			Flags:     int(netlink.FLAG_ONLINK),
		})
	}
	if cfg.ContainerIPNet.IPv6 != nil {
		gw := cfg.GatewayIP.IPv6
		if cfg.StripVlan {
			gw = cfg.ENIGatewayIP.IPv6
		}
		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst: &net.IPNet{
				IP:   gw,
				Mask: net.CIDRMask(128, 128),
			},
		}, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_UNIVERSE,
			Table:     table,
			Dst:       defaultRouteIPv6,
			Gw:        gw,
			Flags:     int(netlink.FLAG_ONLINK),
		})

		sysctl = utils.GenerateIPv6Sysctl(link.Attrs().Name, true, true)
	}

	contCfg := &nic.Conf{
		MTU:       cfg.MTU,
		Addrs:     utils.NewIPNetToMaxMask(cfg.HostIPSet),
		Routes:    routes,
		Rules:     rules,
		Neighs:    neighs,
		SysCtl:    sysctl,
		StripVlan: cfg.StripVlan, // if trunk enabled, will remote vlan tag
	}

	return contCfg
}

func (d *PolicyRoute) Setup(ctx context.Context, cfg *types.SetupConfig, netNS ns.NetNS) error {
	eni, err := netlink.LinkByIndex(cfg.ENIIndex)
	if err != nil {
		return err
	}

	if cfg.BandwidthMode == types.BandwidthModeEDT && cfg.Egress > 0 {
		err = ensureMQFQ(ctx, eni)
		if err != nil {
			return err
		}
	}
	vethCfg := &veth.Veth{
		IfName:   cfg.ContainerIfName,
		PeerName: cfg.HostVETHName,
		MTU:      cfg.MTU,
	}
	if cfg.ERDMA {
		vethCfg.HwAddr = eni.Attrs().HardwareAddr
	}
	err = veth.Setup(ctx, vethCfg, netNS)
	if err != nil {
		return err
	}

	hostVETH, err := netlink.LinkByName(cfg.HostVETHName)
	if err != nil {
		return err
	}

	err = netNS.Do(func(_ ns.NetNS) error {

		// 2. add address for container interface
		contLink, err := netlink.LinkByName(cfg.ContainerIfName)
		if err != nil {
			return fmt.Errorf("error find link %s in container, %w", cfg.ContainerIfName, err)
		}

		contCfg := generateContCfgForPolicy(cfg, contLink, hostVETH.Attrs().HardwareAddr)
		err = nic.Setup(ctx, contLink, contCfg)
		if err != nil {
			return err
		}
		if cfg.BandwidthMode != types.BandwidthModeEDT && cfg.Egress > 0 {
			return utils.SetupTC(contLink, cfg.Egress)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("setup container, %w", err)
	}

	if cfg.ERDMA {
		rdmaDev, err := utils.GetERdmaFromLink(eni)
		if err != nil {
			return fmt.Errorf("error get erdma device: %w", err)
		}
		err = smc.ConfigSMCForDevice(rdmaDev.Attrs.Name, cfg.ContainerIfName, netNS)
		if err != nil {
			return fmt.Errorf("error setup pnet config for pod: %w", err)
		}
	}

	if cfg.EnableNetworkPriority {
		err = utils.SetEgressPriority(ctx, eni, cfg.NetworkPriority, cfg.ContainerIPNet)
		if err != nil {
			return err
		}
	}

	table := utils.GetRouteTableID(eni.Attrs().Index)

	eniCfg := GenerateENICfgForPolicy(cfg, eni, table)
	err = nic.Setup(ctx, eni, eniCfg)
	if err != nil {
		return fmt.Errorf("setup eni config, %w", err)
	}

	if cfg.StripVlan {
		err = utils.EnsureVlanTag(ctx, eni, cfg.ContainerIPNet, uint16(cfg.Vid))
		if err != nil {
			return err
		}
	}

	hostVETHCfg := GenerateHostPeerCfgForPolicy(cfg, hostVETH, table)
	err = nic.Setup(ctx, hostVETH, hostVETHCfg)
	if err != nil {
		return fmt.Errorf("setup host veth config, %w", err)
	}

	if cfg.BandwidthMode != types.BandwidthModeEDT && cfg.Ingress > 0 {
		return utils.SetupTC(hostVETH, cfg.Ingress)
	}
	return nil
}

func (d *PolicyRoute) Check(ctx context.Context, cfg *types.CheckConfig) error {
	err := cfg.NetNS.Do(func(netNS ns.NetNS) error {
		link, err := netlink.LinkByName(cfg.ContainerIfName)
		if err != nil {
			return err
		}
		changed, err := utils.EnsureLinkUp(ctx, link)
		if err != nil {
			return err
		}
		if changed {
			cfg.RecordPodEvent(fmt.Sprintf("link %s set up", cfg.ContainerIfName))
		}
		changed, err = utils.EnsureLinkMTU(ctx, link, cfg.MTU)
		if err != nil {
			return err
		}

		if changed {
			cfg.RecordPodEvent(fmt.Sprintf("link %s set mtu to %v", cfg.ContainerIfName, cfg.MTU))
		}
		return nil
	})
	return err
}

func (d *PolicyRoute) Teardown(ctx context.Context, cfg *types.TeardownCfg, netNS ns.NetNS) error {
	if cfg.ContainerIPNet == nil {
		return nil
	}

	if cfg.HostVETHName != "" {
		err := utils.DelLinkByName(ctx, cfg.HostVETHName)
		if err != nil {
			return err
		}
	}

	extender := utils.NewIPNet(cfg.ContainerIPNet)
	// delete ip rule by ip
	exec := func(rule *netlink.Rule) error {
		rules, err := utils.FindIPRule(rule)
		if err != nil {
			return err
		}
		for _, r := range rules {
			err = utils.RuleDel(ctx, &r)
			if err != nil {
				return err
			}
		}
		return nil
	}
	if extender.IPv4 != nil {
		err := exec(&netlink.Rule{Priority: fromContainerPriority, Src: extender.IPv4})
		if err != nil {
			return err
		}
		err = exec(&netlink.Rule{Priority: toContainerPriority, Dst: extender.IPv4})
		if err != nil {
			return err
		}
	}
	if extender.IPv6 != nil {
		err := exec(&netlink.Rule{Priority: fromContainerPriority, Src: extender.IPv6})
		if err != nil {
			return err
		}
		err = exec(&netlink.Rule{Priority: toContainerPriority, Dst: extender.IPv6})
		if err != nil {
			return err
		}
	}

	if cfg.ENIIndex <= 0 {
		return nil
	}
	link, err := netlink.LinkByIndex(cfg.ENIIndex)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); !ok {
			return err
		}
		return nil
	}

	err = utils.DelFilter(ctx, link, netlink.HANDLE_MIN_EGRESS, cfg.ContainerIPNet)
	if err != nil {
		return err
	}

	if !cfg.EnableNetworkPriority {
		return nil
	}

	return utils.DelEgressPriority(ctx, link, cfg.ContainerIPNet)
}

func ensureMQFQ(ctx context.Context, link netlink.Link) error {
	// create mq at 1: root
	err := utils.EnsureMQQdisc(ctx, link)
	if err != nil {
		return err
	}

	qds, err := netlink.QdiscList(link)
	if err != nil {
		return err
	}
	for _, qd := range qds {
		if qd.Attrs().LinkIndex != link.Attrs().Index {
			continue
		}
		// find parent is mq
		major, minor := netlink.MajorMinor(qd.Attrs().Parent)
		if major != 1 || minor == 0 {
			continue
		}

		if qd.Type() == "fq" {
			continue
		}
		err = utils.QdiscReplace(ctx, &netlink.GenericQdisc{
			QdiscAttrs: netlink.QdiscAttrs{
				LinkIndex: link.Attrs().Index,
				Parent:    qd.Attrs().Parent,
			},
			QdiscType: "fq",
		})
		if err != nil {
			return err
		}
	}
	return nil
}
