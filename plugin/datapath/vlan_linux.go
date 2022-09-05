package datapath

import (
	"fmt"

	"github.com/AliyunContainerService/terway/plugin/driver/nic"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/plugin/driver/vlan"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

type Vlan struct{}

func NewVlan() *Vlan {
	return &Vlan{}
}

func generateContCfgForVlan(cfg *types.SetupConfig, link netlink.Link) *nic.Conf {
	var routes []*netlink.Route
	var rules []*netlink.Rule
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
				Gw:        cfg.GatewayIP.IPv4,
				Flags:     int(netlink.FLAG_ONLINK),
			})
		}

		// 512: from 192.168.1.1 lookup 200
		// 512: from oif eth1 lookup 200
		// default via 192.168.1.253 dev eth1 table 200
		if cfg.MultiNetwork {
			table := utils.GetRouteTableID(link.Attrs().Index)

			v4 := utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv4)

			ruleSrc := netlink.NewRule()
			ruleSrc.Src = v4
			ruleSrc.Table = table
			ruleSrc.Priority = toContainerPriority

			rules = append(rules, ruleSrc)

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
				Gw:        cfg.GatewayIP.IPv6,
				Flags:     int(netlink.FLAG_ONLINK),
			})
		}

		if cfg.MultiNetwork {
			table := utils.GetRouteTableID(link.Attrs().Index)

			v6 := utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv6)

			ruleSrc := netlink.NewRule()
			ruleSrc.Src = v6
			ruleSrc.Table = table
			ruleSrc.Priority = toContainerPriority

			rules = append(rules, ruleSrc)

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
		Addrs:  utils.NewIPNet1(cfg.ContainerIPNet),
		Routes: routes,
		Rules:  rules,
		SysCtl: sysctl,
	}

	return contCfg
}

// generateENICfgForVlan
// set trunk eni mtu and up
func generateENICfgForVlan(cfg *types.SetupConfig) *nic.Conf {
	contCfg := &nic.Conf{
		MTU: cfg.MTU,
	}
	return contCfg
}

func (d *Vlan) Setup(cfg *types.SetupConfig, netNS ns.NetNS) error {
	master, err := netlink.LinkByIndex(cfg.ENIIndex)
	if err != nil {
		return fmt.Errorf("error get link by index %d, %w", cfg.ENIIndex, err)
	}

	if cfg.EnableNetworkPriority {
		err = utils.SetEgressPriority(master, cfg.NetworkPriority, cfg.ContainerIPNet)
		if err != nil {
			return err
		}
	}

	eniCfg := generateENICfgForVlan(cfg)
	err = nic.Setup(master, eniCfg)
	if err != nil {
		return fmt.Errorf("setup eni config, %w", err)
	}

	vlanCfg := &vlan.Vlan{
		IfName: cfg.ContainerIfName,
		Master: master.Attrs().Name,
		Vid:    cfg.Vid,
		MTU:    cfg.MTU,
	}
	err = vlan.Setup(vlanCfg, netNS)
	if err != nil {
		return fmt.Errorf("error setup vlan, %w", err)
	}

	err = netNS.Do(func(_ ns.NetNS) error {
		// 2. add address for container interface
		contLink, err := netlink.LinkByName(cfg.ContainerIfName)
		if err != nil {
			return fmt.Errorf("error find link %s in container, %w", cfg.ContainerIfName, err)
		}

		contCfg := generateContCfgForVlan(cfg, contLink)
		err = nic.Setup(contLink, contCfg)
		if err != nil {
			return err
		}
		if cfg.Egress > 0 {
			return utils.SetupTC(contLink, cfg.Egress)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("setup container, %w", err)
	}
	return nil
}

func (d *Vlan) Check(cfg *types.CheckConfig) error {
	err := cfg.NetNS.Do(func(netNS ns.NetNS) error {
		link, err := netlink.LinkByName(cfg.ContainerIfName)
		if err != nil {
			return err
		}
		changed, err := utils.EnsureLinkUp(link)
		if err != nil {
			return err
		}
		if changed {
			cfg.RecordPodEvent(fmt.Sprintf("link %s set up", cfg.ContainerIfName))
		}
		changed, err = utils.EnsureLinkMTU(link, cfg.MTU)
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
