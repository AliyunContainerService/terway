package datapath

import (
	"fmt"
	"net"

	"github.com/AliyunContainerService/terway/plugin/driver/nic"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/plugin/driver/veth"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

type VPCRoute struct{}

func NewVPCRoute() *VPCRoute {
	return &VPCRoute{}
}

func generateContCfgForVPCRoute(cfg *types.SetupConfig, link netlink.Link, mac net.HardwareAddr) *nic.Conf {
	var routes []*netlink.Route
	var neighs []*netlink.Neigh

	if cfg.ContainerIPNet.IPv4 != nil {
		// add default route
		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_UNIVERSE,
			Dst:       defaultRoute,
			Gw:        LinkIPNet.IP,
			Flags:     int(netlink.FLAG_ONLINK),
		})

		neighs = append(neighs, &netlink.Neigh{
			LinkIndex:    link.Attrs().Index,
			IP:           LinkIPNet.IP,
			HardwareAddr: mac,
			State:        netlink.NUD_PERMANENT,
		})
	}

	contCfg := &nic.Conf{
		IfName: cfg.ContainerIfName,
		MTU:    cfg.MTU,
		Addrs:  utils.NewIPNetToMaxMask(cfg.ContainerIPNet),
		Routes: routes,
		Neighs: neighs,
	}

	return contCfg
}

func generateHostPeerCfgForVPCRoute(cfg *types.SetupConfig, link netlink.Link) *nic.Conf {
	var addrs []*netlink.Addr
	var routes []*netlink.Route
	var rules []*netlink.Rule
	var sysctl map[string][]string

	if cfg.ContainerIPNet.IPv4 != nil {
		// add route to container
		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst:       utils.NewIPNetWithMaxMask(cfg.ContainerIPNet.IPv4),
		})
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

func (d *VPCRoute) Setup(cfg *types.SetupConfig, netNS ns.NetNS) error {
	vethCfg := &veth.Veth{
		IfName:   cfg.ContainerIfName,
		PeerName: cfg.HostVETHName,
		MTU:      cfg.MTU,
	}
	err := veth.Setup(vethCfg, netNS)
	if err != nil {
		return err
	}

	hostVETH, err := netlink.LinkByName(cfg.HostVETHName)
	if err != nil {
		return err
	}

	// config in container netns
	err = netNS.Do(func(_ ns.NetNS) error {
		// 2. add address for container interface
		contLink, err := netlink.LinkByName(cfg.ContainerIfName)
		if err != nil {
			return fmt.Errorf("error find link %s in container, %w", cfg.ContainerIfName, err)
		}
		contCfg := generateContCfgForVPCRoute(cfg, contLink, hostVETH.Attrs().HardwareAddr)

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
		return err
	}

	hostVETHCfg := generateHostPeerCfgForVPCRoute(cfg, hostVETH)
	err = nic.Setup(hostVETH, hostVETHCfg)
	if err != nil {
		return fmt.Errorf("setup host veth config, %w", err)
	}

	if cfg.Ingress > 0 {
		return utils.SetupTC(hostVETH, cfg.Ingress)
	}
	return nil
}

func (d *VPCRoute) Check(cfg *types.CheckConfig) error { return nil }
