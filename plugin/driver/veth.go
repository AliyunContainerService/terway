package driver

import (
	"fmt"
	"net"
	"os"

	"github.com/AliyunContainerService/terway/pkg/ip"
	terwaySysctl "github.com/AliyunContainerService/terway/pkg/sysctl"
	"github.com/AliyunContainerService/terway/pkg/tc"
	terwayTypes "github.com/AliyunContainerService/terway/types"
	"golang.org/x/sys/unix"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
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
	linkIPNet              = &net.IPNet{
		IP:   LinkIP,
		Mask: net.CIDRMask(32, 32),
	}
	linkIPNetv6 = &net.IPNet{
		IP:   LinkIPv6,
		Mask: net.CIDRMask(128, 128),
	}
)

type VETHDriver struct {
	name string
	ipv4 bool
	ipv6 bool
}

func NewVETHDriver(ipv4, ipv6 bool) *VETHDriver {
	return &VETHDriver{
		name: "veth",
		ipv4: ipv4,
		ipv6: ipv6,
	}
}

func (d *VETHDriver) Setup(cfg *SetupConfig, netNS ns.NetNS) error {
	preHostLink, err := netlink.LinkByName(cfg.HostVETHName)
	if err == nil {
		if err = LinkDel(preHostLink); err != nil {
			return fmt.Errorf("error del pre host link, %w", err)
		}
	}

	// disable ra first
	if cfg.ENIIndex != 0 {
		parentLink, err := netlink.LinkByIndex(cfg.ENIIndex)
		if err != nil {
			return fmt.Errorf("error get eni by index %d, %w", cfg.ENIIndex, err)
		}

		if d.ipv6 {
			_ = terwaySysctl.EnsureConf(fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/accept_ra", parentLink.Attrs().Name), "0")
			_ = terwaySysctl.EnsureConf(fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/forwarding", parentLink.Attrs().Name), "1")
		}
	}

	hostNetNS, err := ns.GetCurrentNS()
	if err != nil {
		return fmt.Errorf("err get host net ns, %w", err)
	}
	defer hostNetNS.Close()

	var hostVETH, contVETH netlink.Link

	// config in container netns
	err = netNS.Do(func(_ ns.NetNS) error {
		if d.ipv6 {
			err := EnableIPv6()
			if err != nil {
				return err
			}
		}

		// 1. create veth pair
		hostVETH, contVETH, err = setupVETHPair(cfg.ContainerIfName, cfg.HostVETHName, cfg.MTU, hostNetNS)
		if err != nil {
			return fmt.Errorf("error create veth pair, %w", err)
		}

		// 2. add address for container interface
		contLink, err := netlink.LinkByName(contVETH.Attrs().Name)
		if err != nil {
			return fmt.Errorf("error find link %s in container, %w", contVETH.Attrs().Name, err)
		}
		IPNetToMaxMask(cfg.ContainerIPNet)
		err = SetupLink(contLink, cfg)
		if err != nil {
			return err
		}

		if d.ipv6 {
			_ = terwaySysctl.EnsureConf(fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/accept_ra", cfg.ContainerIfName), "0")
		}

		defaultGW := &terwayTypes.IPSet{}
		if cfg.ContainerIPNet.IPv4 != nil {
			defaultGW.IPv4 = linkIPNet.IP
		}
		if cfg.ContainerIPNet.IPv6 != nil {
			defaultGW.IPv6 = linkIPNetv6.IP
		}
		_, err = EnsureDefaultRoute(contLink, defaultGW, unix.RT_TABLE_MAIN)
		if err != nil {
			return err
		}

		// 3. add route and neigh for container
		err = AddNeigh(contLink, hostVETH.Attrs().HardwareAddr, defaultGW)
		if err != nil {
			return err
		}

		if len(cfg.ExtraRoutes) != 0 {
			if d.ipv4 {
				_, err = EnsureRoute(&netlink.Route{
					LinkIndex: contLink.Attrs().Index,
					Scope:     netlink.SCOPE_LINK,
					Dst:       linkIPNet,
				})
				if err != nil {
					return fmt.Errorf("error add route for container veth, %w", err)
				}
			}
			if d.ipv6 {
				_, err = EnsureRoute(&netlink.Route{
					LinkIndex: contLink.Attrs().Index,
					Scope:     netlink.SCOPE_LINK,
					Dst:       linkIPNetv6,
				})
				if err != nil {
					return fmt.Errorf("error add route for container veth, %w", err)
				}
			}

			for _, extraRoute := range cfg.ExtraRoutes {
				err = RouteAdd(&netlink.Route{
					LinkIndex: contLink.Attrs().Index,
					Scope:     netlink.SCOPE_UNIVERSE,
					Flags:     int(netlink.FLAG_ONLINK),
					Dst:       &extraRoute.Dst,
					Gw:        extraRoute.GW,
				})
				if err != nil {
					return fmt.Errorf("error add extra route for container veth, %w", err)
				}
			}
		}

		if cfg.Egress > 0 {
			return d.setupTC(contLink, cfg.Egress)
		}
		return nil
	})

	if err != nil {
		return err
	}

	// config in host netns
	hostVETHLink, err := netlink.LinkByName(hostVETH.Attrs().Name)
	if err != nil {
		return fmt.Errorf("error found link %s, %w", hostVETH.Attrs().Name, err)
	}

	_, err = EnsureLinkUp(hostVETHLink)
	if err != nil {
		return fmt.Errorf("error set link %s to up, %w", hostVETHLink.Attrs().Name, err)
	}

	// 1. config to container routes
	_, err = EnsureHostToContainerRoute(hostVETHLink, cfg.ContainerIPNet)
	if err != nil {
		return err
	}

	if len(cfg.ExtraRoutes) != 0 {
		if d.ipv4 {
			err = AddrReplace(hostVETHLink, &netlink.Addr{
				IPNet: linkIPNet,
			})
			if err != nil {
				return fmt.Errorf("error add extra addr %s, %w", linkIPNet.String(), err)
			}
		}
		if d.ipv6 {
			err = AddrReplace(hostVETHLink, &netlink.Addr{
				IPNet: linkIPNetv6,
			})
			if err != nil {
				return fmt.Errorf("error add extra addr %s, %w", linkIPNetv6.String(), err)
			}
		}
	}

	// 2. config from container routes
	if cfg.ENIIndex != 0 {
		parentLink, err := netlink.LinkByIndex(cfg.ENIIndex)
		if err != nil {
			return fmt.Errorf("error get eni by index %d, %w", cfg.ENIIndex, err)
		}

		tableID := getRouteTableID(parentLink.Attrs().Index)

		// ensure eni config
		err = d.ensureENIConfig(parentLink, cfg.TrunkENI, cfg.MTU, tableID, cfg.GatewayIP, cfg.HostIPSet)
		if err != nil {
			return fmt.Errorf("error setup eni config, %w", err)
		}

		_, err = EnsureIPRule(hostVETHLink, cfg.ContainerIPNet, tableID)
		if err != nil {
			return err
		}
	}

	if cfg.Ingress > 0 {
		return d.setupTC(hostVETHLink, cfg.Ingress)
	}

	return nil
}

func (d *VETHDriver) Teardown(cfg *TeardownCfg, netNS ns.NetNS) error {
	// 1. get container ip
	hostVeth, err := netlink.LinkByName(cfg.HostVETHName)
	if err != nil {
		return fmt.Errorf("error get link %s, %w", cfg.HostVETHName, err)
	}

	containerIP, err := getIPsByNS(cfg.ContainerIfName, netNS)
	if err != nil {
		return fmt.Errorf("error get container ip %s, %w", cfg.HostVETHName, err)
	}

	// 2. fixme remove ingress/egress rule for pod ip

	// 3. clean ip rules
	if containerIP.IPv4 != nil {
		innerErr := DelIPRulesByIP(containerIP.IPv4)
		if innerErr != nil {
			err = fmt.Errorf("%w", innerErr)
		}
	}
	if containerIP.IPv6 != nil {
		innerErr := DelIPRulesByIP(containerIP.IPv6)
		if innerErr != nil {
			err = fmt.Errorf("%w", innerErr)
		}
	}
	if err != nil {
		return err
	}

	// 4. remove container veth
	Log.Infof("ip link del %s", hostVeth.Attrs().Name)
	return netlink.LinkDel(hostVeth)
}

func (d *VETHDriver) Check(cfg *CheckConfig) error {
	err := cfg.NetNS.Do(func(netNS ns.NetNS) error {
		link, err := netlink.LinkByName(cfg.ContainerIFName)
		if err != nil {
			return err
		}
		if link.Type() != "veth" {
			return fmt.Errorf("link type mismatch want veth, got %s", link.Type())
		}
		return nil
	})
	if err != nil {
		cfg.RecordPodEvent(fmt.Sprintf("veth driver failed to check nic %#v", err))
		return nil
	}

	hostVETHLink, err := netlink.LinkByName(cfg.HostVETHName)
	if err != nil {
		Log.Debugf("can't found veth %s on host", cfg.HostVETHName)
		if os.IsNotExist(err) {
			cfg.RecordPodEvent(fmt.Sprintf("can't found veth %s on host", cfg.HostVETHName))
		}
		return nil
	}

	_, err = EnsureHostToContainerRoute(hostVETHLink, cfg.ContainerIPNet)
	if err != nil {
		return err
	}

	if cfg.ENIIndex == 0 {
		return nil
	}

	// sync policy route
	parentLink, err := netlink.LinkByIndex(int(cfg.ENIIndex))
	if err != nil {
		cfg.RecordPodEvent(fmt.Sprintf("failed to get nic by id %d %#v", cfg.ENIIndex, err))
		Log.Debugf("failed to get nic by id %d %#v", cfg.ENIIndex, err)
		return nil
	}
	tableID := getRouteTableID(parentLink.Attrs().Index)
	// ensure eni config
	err = d.ensureENIConfig(parentLink, cfg.TrunkENI, cfg.MTU, tableID, cfg.GatewayIP, cfg.HostIPSet)
	if err != nil {
		Log.Debug(errors.Wrapf(err, "vethDriver, fail ensure eni config"))
		return nil
	}

	// cfg.HostVETHName
	_, err = EnsureIPRule(hostVETHLink, cfg.ContainerIPNet, tableID)
	if err != nil {
		return err
	}

	return nil
}

func (d *VETHDriver) setupTC(link netlink.Link, bandwidthInBytes uint64) error {
	rule := &tc.TrafficShapingRule{
		Rate: bandwidthInBytes,
	}
	return tc.SetRule(link, rule)
}

func (d *VETHDriver) ensureENIConfig(link netlink.Link, trunk bool, mtu, tableID int, gw *terwayTypes.IPSet, hostIPSet *terwayTypes.IPNetSet) error {
	// set link up
	_, err := EnsureLinkUp(link)
	if err != nil {
		return err
	}

	// ensure mtu setting
	_, err = EnsureLinkMTU(link, mtu)
	if err != nil {
		return err
	}

	_, err = EnsureAddrWithPrefix(link, hostIPSet, true)
	if err != nil {
		return err
	}
	if trunk {
		err = EnsureVlanUntagger(link)
		if err != nil {
			return err
		}
	}

	if gw.IPv6 != nil {
		_, err = EnsureRoute(&netlink.Route{
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst: &net.IPNet{
				IP:   gw.IPv6,
				Mask: net.CIDRMask(128, 128),
			},
		})
		if err != nil {
			return err
		}
	}

	// ensure default route
	_, err = EnsureDefaultRoute(link, gw, tableID)
	return err
}

func setupVETHPair(contVethName, pairName string, mtu int, hostNetNS ns.NetNS) (netlink.Link, netlink.Link, error) {
	contVETH := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name: contVethName,
			MTU:  mtu,
		},
		PeerName: pairName,
	}

	err := LinkAdd(contVETH)
	if err != nil {
		return nil, nil, err
	}

	hostVETH, err := netlink.LinkByName(pairName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to lookup %q: %v", pairName, err)
	}

	if err = LinkSetNsFd(hostVETH, hostNetNS); err != nil {
		return nil, nil, fmt.Errorf("failed to move veth to host netns: %v", err)
	}

	err = hostNetNS.Do(func(_ ns.NetNS) error {
		hostVETH, err = netlink.LinkByName(pairName)
		if err != nil {
			return fmt.Errorf("failed to lookup %q in %q: %v", pairName, hostNetNS.Path(), err)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return hostVETH, contVETH, nil
}

func getIPsByNS(ifName string, nsHandler ns.NetNS) (*terwayTypes.IPNetSet, error) {
	ipSet := &terwayTypes.IPNetSet{}
	err := nsHandler.Do(func(netNS ns.NetNS) error {
		link, err := netlink.LinkByName(ifName)
		if err != nil {
			return err
		}
		addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
		if err != nil {
			return err
		}
		for _, addr := range addrs {
			if !addr.IP.IsGlobalUnicast() {
				continue
			}
			if ip.IPv6(addr.IP) {
				ipSet.IPv6 = NewIPNetWithMaxMask(&net.IPNet{
					IP: addr.IP,
				})
			} else {
				ipSet.IPv4 = NewIPNetWithMaxMask(&net.IPNet{
					IP: addr.IP,
				})
			}
		}
		return nil
	})
	return ipSet, err
}
