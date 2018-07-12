package daemon

import (
	"fmt"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/coreos/go-iptables/iptables"
	"github.com/j-keck/arping"
	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"gitlab.alibaba-inc.com/cos/terway/types"
	"net"
	"os"

	"crypto/sha1"
	"encoding/hex"
)

var (
	LINK_IP = &net.IPNet{
		IP:   net.IPv4(169, 254, 1, 1),
		Mask: net.CIDRMask(32, 32),
	}
)

type ENIPlugin struct {
	ENI *types.ENI
}

type VETHPlugin struct {
	ENI            *types.ENI
	ServiceAddress *net.IPNet
	Subnet         *net.IPNet
	ifName         string
	ipam           string
}

type CmdArgs struct {
	ContainerID string
	Netns       ns.NetNS
	IfName      string
	Args        string
	Path        string
	PodName     string
	PodNS       string
}

const (
	IPMASQ_CHAIN    = "eni-masq"
	vethName        = "eth0"
	DELEGATE_PLUGIN = "host-local"
	DELEGATE_CONF   = `
{
	"ipam": {
		"type": "host-local",
		"subnet": "%s",
		"routes": [
			{ "dst": "0.0.0.0/0" }
		]
	}
}
`
)

func setupIPMasq() error {
	ipt, err := iptables.NewWithProtocol(iptables.ProtocolIPv4)

	exists := false
	chains, err := ipt.ListChains("nat")
	if err != nil {
		return fmt.Errorf("failed to list chains: %v", err)
	}
	for _, ch := range chains {
		if ch == IPMASQ_CHAIN {
			exists = true
			break
		}
	}
	if !exists {
		if err = ipt.NewChain("nat", IPMASQ_CHAIN); err != nil {
			return err
		}
	}
	if err := ipt.AppendUnique("nat", "PREROUTING", "-i", "veth+", "-j", "MARK", "--set-mark", "1"); err != nil {
		return err
	}
	if err := ipt.AppendUnique("nat", IPMASQ_CHAIN, "-j", "MASQUERADE"); err != nil {
		return err
	}
	if err := ipt.AppendUnique("nat", "POSTROUTING", "-m", "mark", "--mark", "1", "-j", IPMASQ_CHAIN); err != nil {
		return err
	}
	return nil
}

func (ep *ENIPlugin) moveLinkIn(ifName string, netns ns.NetNS) error {
	dev, err := netlink.LinkByName(ep.ENI.Name)
	if err != nil {
		return err
	}

	if err := netlink.LinkSetNsFd(dev, int(netns.Fd())); err != nil {
		return err
	}
	return netns.Do(func(_ ns.NetNS) error {
		if err := netlink.LinkSetName(dev, ifName); err != nil {
			return err
		}
		return nil
	})
}

func (ep *ENIPlugin) configure(ifName string, netns ns.NetNS) error {
	err := netns.Do(func(_ ns.NetNS) error {
		link, err := netlink.LinkByName(ifName)
		if err != nil {
			return fmt.Errorf("failed to lookup %q: %v", ifName, err)
		}

		if err := netlink.LinkSetUp(link); err != nil {
			return fmt.Errorf("failed to set %q UP: %v", ifName, err)
		}

		addr := &netlink.Addr{IPNet: &ep.ENI.Address, Label: ""}
		if err = netlink.AddrAdd(link, addr); err != nil {
			return fmt.Errorf("failed to add IP addr %v to %q: %v", ep.ENI.Address, ifName, err)
		}

		if err := ip.AddDefaultRoute(ep.ENI.Gateway, link); err != nil {
			return err
		}

		return nil
	})
	return err
}

func (ep *ENIPlugin) configureIface(ifName string, res *current.Result) error {
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("failed to lookup %q: %v", ifName, err)
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("failed to set %q UP: %v", ifName, err)
	}

	var gw net.IP
	for _, ipc := range res.IPs {

		addr := &netlink.Addr{IPNet: &ipc.Address, Label: ""}
		if err = netlink.AddrAdd(link, addr); err != nil {
			return fmt.Errorf("failed to add IP addr %v to %q: %v", ipc, ifName, err)
		}

		gw = ipc.Gateway
	}

	if gw != nil {
		if err := ip.AddDefaultRoute(gw, link); err != nil {
			return err
		}
	}

	return nil
}

func (ep *ENIPlugin) Add(args *CmdArgs, result *current.Result) error {
	if err := ep.moveLinkIn(args.IfName, args.Netns); err != nil {
		return err
	}

	if err := ep.configure(args.IfName, args.Netns); err != nil {
		return err
	}

	result.IPs = []*current.IPConfig{{
		Version: "4",
		Address: ep.ENI.Address,
		Gateway: ep.ENI.Gateway,
	}}
	return nil
}

func (ep *ENIPlugin) Get(args *CmdArgs) (string, error) {
	var mac string
	err := args.Netns.Do(func(_ ns.NetNS) error {
		link, err := netlink.LinkByName(args.IfName)
		if err != nil {
			return err
		}
		mac = link.Attrs().HardwareAddr.String()
		return nil
	})
	return mac, err
}

func (ep *ENIPlugin) moveLinkOut(ifName string, netns ns.NetNS) error {
	defaultNetNS, err := ns.GetCurrentNS()
	if err != nil {
		return err
	}
	err = netns.Do(func(_ ns.NetNS) error {
		link, err := netlink.LinkByName(ifName)
		if err != nil {
			return fmt.Errorf("failed to get interface by name %s: %v", ifName, err)
		}
		if err != nil {
			return fmt.Errorf("failed to find raw name of %s", ifName)
		}

		err = netlink.LinkSetDown(link)
		if err != nil {
			return fmt.Errorf("failed to down %s", ifName)
		}

		err = netlink.LinkSetName(link, ep.ENI.Name)
		if err != nil {
			return fmt.Errorf("failed to rename %s to %s", ifName, ep.ENI.Name)
		}

		if err := netlink.LinkSetNsFd(link, int(defaultNetNS.Fd())); err != nil {
			return fmt.Errorf("failed to move interface %s into default namespace: %v", ifName, err)
		}

		return nil
	})
	return err
}

func (ep *ENIPlugin) Del(args *CmdArgs, result *current.Result) error {
	return ep.moveLinkOut(args.IfName, args.Netns)
}

func (vp *VETHPlugin) Add(args *CmdArgs, result *current.Result) error {
	if args.IfName != "" {
		vp.ifName = args.IfName
	} else {
		vp.ifName = vethName
	}
	if err := vp.addVeth(args.Netns, args.PodNS, args.PodName, result); err != nil {
		return err
	}

	return nil
}
func (vp *VETHPlugin) addVeth(netns ns.NetNS, namespace, podname string, result *current.Result) error {
	defaultNetNS, err := ns.GetCurrentNS()
	if err != nil {
		return err
	}
	hostInterface := ""

	if vp.ENI != nil {
		// 有ENI的情况下，veth只用作service转发，不分配ip地址。
		// TODO 这段代码要优化
		err = netns.Do(func(_ ns.NetNS) error {
			hostVeth, _, err := SetupVethPair(vp.ifName, VethNameForWorkload(normalizePodID(namespace, podname)), 1500, defaultNetNS)
			if err != nil {
				return fmt.Errorf("error setup veth pair: %v", err)
			}
			hostInterface = hostVeth.Name
			contVeth, err := netlink.LinkByName(vp.ifName)
			if err != nil {
				return fmt.Errorf("error get veth pair in container ns: %v", err)
			}

			err = netlink.RouteAdd(&netlink.Route{
				LinkIndex: contVeth.Attrs().Index,
				Scope:     netlink.SCOPE_LINK,
				Dst:       LINK_IP,
			})

			if err != nil {
				return fmt.Errorf("error add peer ip route to container: %v", err)
			}

			err = netlink.RouteAdd(&netlink.Route{
				LinkIndex: contVeth.Attrs().Index,
				Scope:     netlink.SCOPE_UNIVERSE,
				Flags:     int(netlink.FLAG_ONLINK),
				Dst:       vp.ServiceAddress,
				Gw:        LINK_IP.IP,
			})

			if err != nil {
				return fmt.Errorf("error add service subnet route to container: %v", err)
			}
			return nil
		})

		if err != nil {
			return fmt.Errorf("error set container netns: %+v", err)
		}

		hostVeth, err := netlink.LinkByName(hostInterface)
		if err != nil {
			return err
		}

		err = netlink.AddrAdd(hostVeth, &netlink.Addr{
			IPNet: LINK_IP,
		})

		if err != nil {
			return fmt.Errorf("error add ip to host interface: %+v", err)
		}

		//add host route
		err = netlink.RouteAdd(&netlink.Route{
			LinkIndex: hostVeth.Attrs().Index,
			Scope:     netlink.SCOPE_LINK,
			Dst: &net.IPNet{
				IP:   vp.ENI.Address.IP,
				Mask: net.CIDRMask(32, 32),
			},
		})
		if err != nil {
			return fmt.Errorf("error set route to host interface: %+v", err)
		}
	} else {
		os.Setenv("CNI_COMMAND", "ADD")
		os.Setenv("CNI_PATH", "/opt/cni/bin/")
		os.Setenv("CNI_NETNS", netns.Path())
		os.Setenv("CNI_IFNAME", vethName)

		r, err := ipam.ExecAdd(DELEGATE_PLUGIN, []byte(fmt.Sprintf(DELEGATE_CONF, vp.Subnet.String())))
		log.Infof("delegate result: %+v, %v", r, err)
		if err != nil {
			return err
		}
		ipamResult, err := current.NewResultFromResult(r)
		log.Infof("delegate result: %+v, %v", ipamResult, err)
		if err != nil {
			return err
		}

		hostInterface, containerInterface, err := setupContainerVeth(netns, vethName, VethNameForWorkload(normalizePodID(namespace, podname)), 1500, ipamResult)

		if err != nil {
			return fmt.Errorf("error set up interfaces, %s, %s: err: %+v", vethName, VethNameForWorkload(normalizePodID(namespace, podname)), err)
		}

		err = setupHostVeth(hostInterface.Name, ipamResult)
		if err != nil {
			return err
		}

		result.Interfaces = []*current.Interface{hostInterface, containerInterface}
		result.IPs = ipamResult.IPs
	}
	return nil
}

func (vp *VETHPlugin) Del(args *skel.CmdArgs, result *current.Result) error {
	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	defer netns.Close()

	os.Setenv("CNI_COMMAND", "DEL")
	os.Setenv("CNI_PATH", "/opt/cni/bin/")
	os.Setenv("CNI_NETNS", netns.Path())
	os.Setenv("CNI_IFNAME", vethName)

	err = ipam.ExecDel(DELEGATE_PLUGIN, []byte(fmt.Sprintf(DELEGATE_CONF, vp.Subnet.String())))
	if err != nil {
		return err
	}

	return netns.Do(func(_ ns.NetNS) error {
		veth, err := netlink.LinkByName(vethName)
		if err != nil {
			return err
		}

		if err = netlink.LinkDel(veth); err != nil {
			return fmt.Errorf("failed to delete veth: %v", err)
		}
		return nil
	})
}

func setupHostVeth(vethName string, result *current.Result) error {
	// hostVeth moved namespaces and may have a new ifindex
	veth, err := netlink.LinkByName(vethName)
	if err != nil {
		return fmt.Errorf("failed to lookup %q: %v", vethName, err)
	}

	for _, ipc := range result.IPs {
		maskLen := 128
		if ipc.Address.IP.To4() != nil {
			maskLen = 32
		}

		ipn := &net.IPNet{
			IP:   ipc.Gateway,
			Mask: net.CIDRMask(maskLen, maskLen),
		}
		addr := &netlink.Addr{IPNet: ipn, Label: ""}
		if err = netlink.AddrAdd(veth, addr); err != nil {
			return fmt.Errorf("failed to add IP addr (%#v) to veth: %v", ipn, err)
		}

		ipn = &net.IPNet{
			IP:   ipc.Address.IP,
			Mask: net.CIDRMask(maskLen, maskLen),
		}
		// dst happens to be the same as IP/net of host veth
		if err = ip.AddHostRoute(ipn, nil, veth); err != nil && !os.IsExist(err) {
			return fmt.Errorf("failed to add route on host: %v", err)
		}
	}

	return nil
}

func setupContainerVeth(netns ns.NetNS, ifName, pairName string, mtu int, pr *current.Result) (*current.Interface, *current.Interface, error) {
	// The IPAM result will be something like IP=192.168.3.5/24, GW=192.168.3.1.
	// What we want is really a point-to-point link but veth does not support IFF_POINTTOPOINT.
	// Next best thing would be to let it ARP but set interface to 192.168.3.5/32 and
	// add a route like "192.168.3.0/24 via 192.168.3.1 dev $ifName".
	// Unfortunately that won't work as the GW will be outside the interface's subnet.

	// Our solution is to configure the interface with 192.168.3.5/24, then delete the
	// "192.168.3.0/24 dev $ifName" route that was automatically added. Then we add
	// "192.168.3.1/32 dev $ifName" and "192.168.3.0/24 via 192.168.3.1 dev $ifName".
	// In other words we force all traffic to ARP via the gateway except for GW itself.

	hostInterface := &current.Interface{}
	containerInterface := &current.Interface{}

	err := netns.Do(func(hostNS ns.NetNS) error {
		hostVeth, contVeth0, err := SetupVethPair(ifName, pairName, mtu, hostNS)
		if err != nil {
			return err
		}
		hostInterface.Name = hostVeth.Name
		hostInterface.Mac = hostVeth.HardwareAddr.String()
		containerInterface.Name = contVeth0.Name
		containerInterface.Mac = contVeth0.HardwareAddr.String()
		containerInterface.Sandbox = netns.Path()

		for _, ipc := range pr.IPs {
			// All addresses apply to the container veth interface
			ipc.Interface = current.Int(1)
		}

		pr.Interfaces = []*current.Interface{hostInterface, containerInterface}

		if err = ipam.ConfigureIface(ifName, pr); err != nil {
			return err
		}

		contVeth, err := net.InterfaceByName(ifName)
		if err != nil {
			return fmt.Errorf("failed to look up %q: %v", ifName, err)
		}

		for _, ipc := range pr.IPs {
			// Delete the route that was automatically added
			route := netlink.Route{
				LinkIndex: contVeth.Index,
				Dst: &net.IPNet{
					IP:   ipc.Address.IP.Mask(ipc.Address.Mask),
					Mask: ipc.Address.Mask,
				},
				Scope: netlink.SCOPE_NOWHERE,
			}

			if err := netlink.RouteDel(&route); err != nil {
				return fmt.Errorf("failed to delete route %v: %v", route, err)
			}

			addrBits := 32
			if ipc.Version == "6" {
				addrBits = 128
			}

			for _, r := range []netlink.Route{
				netlink.Route{
					LinkIndex: contVeth.Index,
					Dst: &net.IPNet{
						IP:   ipc.Gateway,
						Mask: net.CIDRMask(addrBits, addrBits),
					},
					Scope: netlink.SCOPE_LINK,
					Src:   ipc.Address.IP,
				},
				netlink.Route{
					LinkIndex: contVeth.Index,
					Dst: &net.IPNet{
						IP:   ipc.Address.IP.Mask(ipc.Address.Mask),
						Mask: ipc.Address.Mask,
					},
					Scope: netlink.SCOPE_UNIVERSE,
					Gw:    ipc.Gateway,
					Src:   ipc.Address.IP,
				},
			} {
				if err := netlink.RouteAdd(&r); err != nil {
					return fmt.Errorf("failed to add route %v: %v", r, err)
				}
			}
		}

		// Send a gratuitous arp for all v4 addresses
		for _, ipc := range pr.IPs {
			if ipc.Version == "4" {
				_ = arping.GratuitousArpOverIface(ipc.Address.IP, *contVeth)
			}
		}

		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return hostInterface, containerInterface, nil
}

// VethNameForWorkload returns a deterministic veth name
// for the given Kubernetes workload.
func VethNameForWorkload(workload string) string {
	// A SHA1 is always 20 bytes long, and so is sufficient for generating the
	// veth name and mac addr.
	h := sha1.New()
	h.Write([]byte(workload))
	return fmt.Sprintf("cali%s", hex.EncodeToString(h.Sum(nil))[:11])
}

func SetupVethPair(contVethName, pairName string, mtu int, hostNS ns.NetNS) (net.Interface, net.Interface, error) {
	contVeth, err := makeVethPair(contVethName, pairName, mtu)
	if err != nil {
		return net.Interface{}, net.Interface{}, err
	}

	if err = netlink.LinkSetUp(contVeth); err != nil {
		return net.Interface{}, net.Interface{}, fmt.Errorf("failed to set %q up: %v", contVethName, err)
	}

	hostVeth, err := netlink.LinkByName(pairName)
	if err != nil {
		return net.Interface{}, net.Interface{}, fmt.Errorf("failed to lookup %q: %v", pairName, err)
	}

	if err = netlink.LinkSetNsFd(hostVeth, int(hostNS.Fd())); err != nil {
		return net.Interface{}, net.Interface{}, fmt.Errorf("failed to move veth to host netns: %v", err)
	}

	err = hostNS.Do(func(_ ns.NetNS) error {
		hostVeth, err = netlink.LinkByName(pairName)
		if err != nil {
			return fmt.Errorf("failed to lookup %q in %q: %v", pairName, hostNS.Path(), err)
		}

		if err = netlink.LinkSetUp(hostVeth); err != nil {
			return fmt.Errorf("failed to set %q up: %v", pairName, err)
		}
		return nil
	})
	if err != nil {
		return net.Interface{}, net.Interface{}, err
	}
	return ifaceFromNetlinkLink(hostVeth), ifaceFromNetlinkLink(contVeth), nil
}

func makeVethPair(name, peer string, mtu int) (netlink.Link, error) {
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name:  name,
			Flags: net.FlagUp,
			MTU:   mtu,
		},
		PeerName: peer,
	}
	if err := netlink.LinkAdd(veth); err != nil {
		return nil, err
	}

	return veth, nil
}

func ifaceFromNetlinkLink(l netlink.Link) net.Interface {
	a := l.Attrs()
	return net.Interface{
		Index:        a.Index,
		MTU:          a.MTU,
		Name:         a.Name,
		HardwareAddr: a.HardwareAddr,
		Flags:        a.Flags,
	}
}

func normalizePodID(namespace, podName string) string {
	return fmt.Sprintf("%s.%s", namespace, podName)
}
