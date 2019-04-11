package driver

import (
	"encoding/hex"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
	"math/rand"
	"net"
	"time"
)

type rawNicDriver struct {
}

func (r *rawNicDriver) Setup(hostVeth string,
	containerVeth string,
	ipv4Addr *net.IPNet,
	primaryIpv4Addr *net.IPNet,
	gateway net.IP,
	extraRoutes []*types.Route,
	deviceID int,
	ingress uint64,
	egress uint64,
	netNS ns.NetNS) error {
	// 1. move link in
	nicLink, err := netlink.LinkByIndex(deviceID)
	if err != nil {
		return errors.Wrapf(err, "NicDriver, cannot found spec nic link")
	}
	hostCurrentNs, err := ns.GetCurrentNS()
	defer hostCurrentNs.Close()
	if err != nil {
		return errors.Wrapf(err, "NicDriver, cannot get host netns")
	}

	err = netlink.LinkSetNsFd(nicLink, int(netNS.Fd()))
	if err != nil {
		return errors.Wrapf(err, "NicDriver, cannot set nic link to container netns")
	}

	defer func() {
		if err != nil {
			netNS.Do(func(netNS ns.NetNS) error {
				nicLink, err = netlink.LinkByName(containerVeth)
				if err == nil {
					nicName, err1 := r.randomNicName()
					if err1 != nil {
						return err1
					}
					err = netlink.LinkSetName(nicLink, nicName)
					if err != nil {
						return err
					}
				}

				if _, ok := err.(netlink.LinkNotFoundError); ok {
					err = nil
					nicLink, err = netlink.LinkByName(nicLink.Attrs().Name)
				}
				if err == nil {
					netlink.LinkSetDown(nicLink)
					return netlink.LinkSetNsFd(nicLink, int(hostCurrentNs.Fd()))
				}
				return err
			})
		}
	}()
	// 2. setup addr and default route
	err = netNS.Do(func(netNS ns.NetNS) error {
		// remove equal ip net
		var linkList []netlink.Link
		linkList, err = netlink.LinkList()
		if err != nil {
			return errors.Wrapf(err, "error list netns links")
		}

		for _, link := range linkList {
			var addrList []netlink.Addr
			addrList, err = netlink.AddrList(link, netlink.FAMILY_ALL)
			if err != nil {
				return errors.Wrapf(err, "error list addrs in netns")
			}
			for _, addr := range addrList {
				if ipNetEqual(addr.IPNet, ipv4Addr) {
					err = netlink.AddrDel(link, &netlink.Addr{IPNet: ipv4Addr})
					if err != nil {
						return errors.Wrapf(err, "error delete conflict addr in netns")
					}
				}
			}
		}

		// 2.1 setup addr
		nicLink, err = netlink.LinkByName(nicLink.Attrs().Name)
		if err != nil {
			return errors.Wrapf(err, "error get link by name: %s", nicLink.Attrs().Name)
		}

		err = netlink.LinkSetName(nicLink, containerVeth)
		if err != nil {
			return errors.Wrapf(err, "setup nic link name failed")
		}

		err = netlink.LinkSetUp(nicLink)
		if err != nil {
			return errors.Wrapf(err, "setup set nic link up")
		}

		err = netlink.AddrAdd(nicLink, &netlink.Addr{
			IPNet: ipv4Addr,
		})
		if err != nil {
			return errors.Wrapf(err, "setup add addr to link")
		}

		var defaultRoutes []netlink.Route
		defaultRoutes, err = netlink.RouteListFiltered(netlink.FAMILY_ALL,
			&netlink.Route{
				Dst:   nil,
				Scope: netlink.SCOPE_UNIVERSE,
			}, netlink.RT_FILTER_DST|netlink.RT_FILTER_SCOPE)
		if err != nil {
			return errors.Wrapf(err, "error found conflict route for nic")
		}
		for _, route := range defaultRoutes {
			err = netlink.RouteDel(&route)
			if err != nil {
				return errors.Wrapf(err, "error delete conflict route for nic")
			}
		}

		// 2.2 setup default route
		err = netlink.RouteAdd(&netlink.Route{
			LinkIndex: nicLink.Attrs().Index,
			Scope:     netlink.SCOPE_UNIVERSE,
			Flags:     int(netlink.FLAG_ONLINK),
			Dst:       defaultRoute,
			Gw:        gateway,
		})
		if err != nil {
			return errors.Wrap(err, "error add route for nic")
		}
		return nil
	})

	if err != nil {
		return errors.Wrapf(err, "NicDriver, cannot set nic addr and default route")
	}
	return nil
}

func (r *rawNicDriver) Teardown(hostVeth string, containerVeth string, netNS ns.NetNS) error {
	// 1. move link out
	hostCurrentNs, err := ns.GetCurrentNS()
	defer hostCurrentNs.Close()
	if err != nil {
		return errors.Wrapf(err, "NicDriver, cannot get host netns")
	}
	err = netNS.Do(func(netNS ns.NetNS) error {
		var nicLink netlink.Link
		nicLink, err = netlink.LinkByName(containerVeth)
		if err == nil {
			nicName, err1 := r.randomNicName()
			if err1 != nil {
				return errors.Wrapf(err1, "error get random nic name")
			}
			err = netlink.LinkSetDown(nicLink)
			if err != nil {
				return errors.Wrapf(err, "error set link down")
			}
			err = netlink.LinkSetName(nicLink, nicName)
			if err != nil {
				return errors.Wrapf(err, "error set link name: %v", nicName)
			}
			return netlink.LinkSetNsFd(nicLink, int(hostCurrentNs.Fd()))
		}
		return errors.Wrapf(err, "error get link from namespace")
	})
	if err != nil {
		return errors.Wrapf(err, "NicDriver, error move nic out")
	}
	return nil
}

const nicPrefix = "eth"

func (*rawNicDriver) randomNicName() (string, error) {
	ethNameSuffix := make([]byte, 3)
	rand.Seed(time.Now().UnixNano())
	_, err := rand.Read(ethNameSuffix)
	return nicPrefix + hex.EncodeToString(ethNameSuffix), err
}
