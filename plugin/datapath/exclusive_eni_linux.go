package datapath

import (
	"fmt"
	"net"

	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/plugin/driver/nic"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/plugin/driver/veth"
	cniTypes "github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

const defaultVethForENI = "veth1"

// ExclusiveENI put nic in net ns
type ExclusiveENI struct{}

func NewExclusiveENIDriver() *ExclusiveENI {
	return &ExclusiveENI{}
}

func generateContCfgForExclusiveENI(cfg *types.SetupConfig, link netlink.Link) *nic.Conf {
	var addrs []*netlink.Addr
	var routes []*netlink.Route
	var rules []*netlink.Rule
	var sysctl map[string][]string

	if cfg.MultiNetwork {
		addrs = utils.NewIPNet1(cfg.ContainerIPNet)
	} else {
		addrs = utils.NewIPNetToMaxMask(cfg.ContainerIPNet)
	}

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
		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst: &net.IPNet{
				IP:   cfg.GatewayIP.IPv6,
				Mask: net.CIDRMask(128, 128),
			},
		})
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
		Addrs:  addrs,
		Routes: routes,
		Rules:  rules,
		SysCtl: sysctl,
	}
	return contCfg
}

func generateVeth1Cfg(cfg *types.SetupConfig, link netlink.Link, peerMAC net.HardwareAddr) *nic.Conf {
	var routes []*netlink.Route
	var neighs []*netlink.Neigh
	var sysctl map[string][]string

	if cfg.ContainerIPNet.IPv4 != nil {
		// 169.254.1.1 dev veth1
		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst:       LinkIPNet,
		})

		if cfg.ServiceCIDR != nil && cfg.ServiceCIDR.IPv4 != nil {
			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Dst:       cfg.ServiceCIDR.IPv4,
				Gw:        LinkIP,
				Flags:     int(netlink.FLAG_ONLINK),
			})
		}
		neighs = append(neighs, &netlink.Neigh{
			LinkIndex:    link.Attrs().Index,
			IP:           LinkIPNet.IP,
			HardwareAddr: peerMAC,
			State:        netlink.NUD_PERMANENT,
		})
	}
	if cfg.ContainerIPNet.IPv6 != nil {
		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst:       LinkIPNetv6,
		})

		if cfg.ServiceCIDR != nil && cfg.ServiceCIDR.IPv6 != nil {
			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Dst:       cfg.ServiceCIDR.IPv6,
				Gw:        LinkIPv6,
				Flags:     int(netlink.FLAG_ONLINK),
			})
		}
		neighs = append(neighs, &netlink.Neigh{
			LinkIndex:    link.Attrs().Index,
			IP:           LinkIPNetv6.IP,
			HardwareAddr: peerMAC,
			State:        netlink.NUD_PERMANENT,
		})
		sysctl = utils.GenerateIPv6Sysctl(cfg.ContainerIfName, true, false)
	}

	var extraRoutes []cniTypes.Route
	for i := range cfg.HostStackCIDRs {
		r := cniTypes.Route{
			Dst: *cfg.HostStackCIDRs[i],
			GW:  LinkIP,
		}
		if terwayIP.IPv6(cfg.HostStackCIDRs[i].IP) {
			r.GW = LinkIPv6
		}
		extraRoutes = append(extraRoutes, r)
	}

	for i := range extraRoutes {
		if extraRoutes[i].GW != nil {
			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Scope:     netlink.SCOPE_UNIVERSE,
				Flags:     int(netlink.FLAG_ONLINK),
				Dst:       &extraRoutes[i].Dst,
				Gw:        extraRoutes[i].GW,
			})
		} else {
			routes = append(routes, &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Scope:     netlink.SCOPE_LINK,
				Dst:       &extraRoutes[i].Dst,
			})
		}
	}

	contCfg := &nic.Conf{
		IfName: defaultVethForENI,
		MTU:    cfg.MTU,
		Addrs:  utils.NewIPNetToMaxMask(cfg.ContainerIPNet),
		Routes: routes,
		Neighs: neighs,
		SysCtl: sysctl,
	}
	return contCfg
}

func generateHostSlaveCfg(cfg *types.SetupConfig, link netlink.Link) *nic.Conf {
	var addrs []*netlink.Addr
	var routes []*netlink.Route
	var sysctl map[string][]string

	if cfg.ContainerIPNet.IPv4 != nil {
		addrs = append(addrs, &netlink.Addr{
			IPNet: LinkIPNet,
		})

		// add route to container
		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst:       utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv4),
		})
	}
	if cfg.ContainerIPNet.IPv6 != nil {
		addrs = append(addrs, &netlink.Addr{
			IPNet: LinkIPNetv6,
		})

		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst:       utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv6),
		})

		sysctl = utils.GenerateIPv6Sysctl(cfg.HostVETHName, true, false)
	}
	contCfg := &nic.Conf{
		IfName: cfg.HostVETHName,
		MTU:    cfg.MTU,
		Addrs:  addrs,
		Routes: routes,
		SysCtl: sysctl,
	}

	return contCfg
}

func (r *ExclusiveENI) Setup(cfg *types.SetupConfig, netNS ns.NetNS) error {
	// 1. move link in
	nicLink, err := netlink.LinkByIndex(cfg.ENIIndex)
	if err != nil {
		return fmt.Errorf("error get eni by index %d, %w", cfg.ENIIndex, err)
	}
	hostNetNS, err := ns.GetCurrentNS()
	if err != nil {
		return fmt.Errorf("err get host net ns, %w", err)
	}
	defer hostNetNS.Close()

	err = utils.LinkSetNsFd(nicLink, netNS)
	if err != nil {
		return fmt.Errorf("error set nic %s to container, %w", nicLink.Attrs().Name, err)
	}

	defer func() {
		if err != nil {
			err = netNS.Do(func(netNS ns.NetNS) error {
				nicLink, err = netlink.LinkByName(cfg.ContainerIfName)
				if err == nil {
					nicName, err1 := ip.RandomVethName()
					if err1 != nil {
						return err1
					}
					err = utils.LinkSetName(nicLink, nicName)
					if err != nil {
						return err
					}
				}

				if _, ok := err.(netlink.LinkNotFoundError); ok {
					err = nil
					nicLink, err = netlink.LinkByName(nicLink.Attrs().Name)
				}
				if err == nil {
					err = utils.LinkSetDown(nicLink)
					return utils.LinkSetNsFd(nicLink, hostNetNS)
				}
				return err
			})
		}
	}()
	// 2. setup addr and default route
	err = netNS.Do(func(netNS ns.NetNS) error {
		// 2.1 setup addr
		contLink, err := netlink.LinkByName(nicLink.Attrs().Name)
		if err != nil {
			return fmt.Errorf("error find link %s, %w", nicLink.Attrs().Name, err)
		}

		contCfg := generateContCfgForExclusiveENI(cfg, contLink)
		err = nic.Setup(contLink, contCfg)
		if err != nil {
			return err
		}

		if cfg.Egress > 0 {
			err = utils.SetupTC(contLink, cfg.Egress)
			if err != nil {
				return err
			}
		}

		// for now we only create slave link for eth0
		if !cfg.DisableCreatePeer && cfg.ContainerIfName == "eth0" {
			err = veth.Setup(&veth.Veth{
				IfName:   cfg.HostVETHName, // name for host ns side
				PeerName: defaultVethForENI,
			}, hostNetNS)
			if err != nil {
				return fmt.Errorf("error set up slave link, %w", err)
			}

			var mac net.HardwareAddr
			err = hostNetNS.Do(func(netNS ns.NetNS) error {
				hostPeer, innerErr := netlink.LinkByName(cfg.HostVETHName)
				if innerErr != nil {
					return innerErr
				}
				mac = hostPeer.Attrs().HardwareAddr
				return innerErr
			})
			if err != nil {
				return err
			}

			veth1, err := netlink.LinkByName(defaultVethForENI)
			if err != nil {
				return err
			}
			veth1Cfg := generateVeth1Cfg(cfg, veth1, mac)
			return nic.Setup(veth1, veth1Cfg)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("error set container link/address/route, %w", err)
	}

	if cfg.DisableCreatePeer {
		return nil
	}

	hostPeer, err := netlink.LinkByName(cfg.HostVETHName)
	if err != nil {
		return fmt.Errorf("error get host veth %s, %w", cfg.HostVETHName, err)
	}
	hostPeerCfg := generateHostSlaveCfg(cfg, hostPeer)
	err = nic.Setup(hostPeer, hostPeerCfg)
	if err != nil {
		return fmt.Errorf("error set up hostpeer, %w", err)
	}

	return nil
}

func (r *ExclusiveENI) Check(cfg *types.CheckConfig) error {
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
