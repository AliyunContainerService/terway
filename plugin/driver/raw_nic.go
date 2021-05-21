package driver

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"time"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

// RawNicDriver put nic in net ns
type RawNicDriver struct {
	name string
	ipv4 bool
	ipv6 bool
}

func NewRawNICDriver(ipv4, ipv6 bool) *RawNicDriver {
	return &RawNicDriver{
		name: "rawNIC",
		ipv4: ipv4,
		ipv6: ipv6,
	}
}

func (r *RawNicDriver) Setup(cfg *SetupConfig, netNS ns.NetNS) error {
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

	err = LinkSetNsFd(nicLink, netNS)
	if err != nil {
		return fmt.Errorf("error set nic %s to container, %w", nicLink.Attrs().Name, err)
	}

	defer func() {
		if err != nil {
			err = netNS.Do(func(netNS ns.NetNS) error {
				nicLink, err = netlink.LinkByName(cfg.ContainerIfName)
				if err == nil {
					nicName, err1 := r.randomNicName()
					if err1 != nil {
						return err1
					}
					err = LinkSetName(nicLink, nicName)
					if err != nil {
						return err
					}
				}

				if _, ok := err.(netlink.LinkNotFoundError); ok {
					err = nil
					nicLink, err = netlink.LinkByName(nicLink.Attrs().Name)
				}
				if err == nil {
					err = LinkSetDown(nicLink)
					return LinkSetNsFd(nicLink, hostNetNS)
				}
				return err
			})
		}
	}()
	// 2. setup addr and default route
	err = netNS.Do(func(netNS ns.NetNS) error {
		if r.ipv6 {
			err := EnableIPv6()
			if err != nil {
				return err
			}
		}
		// 2.1 setup addr
		nicLink, err = netlink.LinkByName(nicLink.Attrs().Name)
		if err != nil {
			return fmt.Errorf("error find link %s, %w", nicLink.Attrs().Name, err)
		}

		_, err = EnsureLinkMTU(nicLink, cfg.MTU)
		if err != nil {
			return fmt.Errorf("error set link %s MTU %d, %w", nicLink.Attrs().Name, cfg.MTU, err)
		}
		err = SetupLink(nicLink, cfg)
		if err != nil {
			return err
		}
		_, err = EnsureDefaultRoute(nicLink, cfg.GatewayIP)
		return err
	})

	return err
}

func (r *RawNicDriver) Teardown(cfg *TeardownCfg, netNS ns.NetNS) error {
	// 1. move link out
	hostCurrentNs, err := ns.GetCurrentNS()
	defer func() {
		err = hostCurrentNs.Close()
	}()
	if err != nil {
		return fmt.Errorf("error get host net ns, %w", err)
	}
	err = netNS.Do(func(netNS ns.NetNS) error {
		var nicLink netlink.Link
		nicLink, err = netlink.LinkByName(cfg.ContainerIfName)
		if err == nil {
			nicName, err1 := r.randomNicName()
			if err1 != nil {
				return fmt.Errorf("error generate random nic name, %w", err)
			}
			err = netlink.LinkSetDown(nicLink)
			if err != nil {
				return fmt.Errorf("error set link %s down, %w", nicLink.Attrs().Name, err)
			}
			err = netlink.LinkSetName(nicLink, nicName)
			if err != nil {
				return fmt.Errorf("error set link %s name %s, %w", nicLink.Attrs().Name, nicName, err)
			}
			return netlink.LinkSetNsFd(nicLink, int(hostCurrentNs.Fd()))
		}
		return fmt.Errorf("error get link %s, %w", cfg.ContainerIfName, err)
	})
	if err != nil {
		return fmt.Errorf("error move eni to host net ns, %w", err)
	}
	return nil
}

func (r *RawNicDriver) Check(cfg *CheckConfig) error {
	_ = cfg.NetNS.Do(func(netNS ns.NetNS) error {
		link, err := netlink.LinkByName(cfg.ContainerIFName)
		if err != nil {
			return err
		}
		changed, err := EnsureLinkUp(link)
		if err != nil {
			return err
		}
		if changed {
			cfg.RecordPodEvent(fmt.Sprintf("link %s set to up", cfg.ContainerIFName))
		}
		changed, err = EnsureLinkMTU(link, cfg.MTU)
		if err != nil {
			return err
		}

		if changed {
			cfg.RecordPodEvent(fmt.Sprintf("link %s set mtu to %v", cfg.ContainerIFName, cfg.MTU))
		}
		changed, err = EnsureDefaultRoute(link, cfg.GatewayIP)
		if err != nil {
			return err
		}
		if changed {
			Log.Debugf("route is changed")
			cfg.RecordPodEvent("default route is updated")
		}

		return EnsureNetConfSet(true, false)
	})
	return nil
}

const nicPrefix = "eth"

func (*RawNicDriver) randomNicName() (string, error) {
	ethNameSuffix := make([]byte, 3)
	rand.Seed(time.Now().UnixNano())
	_, err := rand.Read(ethNameSuffix)
	return nicPrefix + hex.EncodeToString(ethNameSuffix), err
}
