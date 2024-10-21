package veth

import (
	"context"
	"net"

	"github.com/AliyunContainerService/terway/plugin/driver/utils"

	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

type Veth struct {
	IfName   string // cont in netns
	PeerName string
	HwAddr   net.HardwareAddr
	MTU      int
}

func Setup(ctx context.Context, cfg *Veth, netNS ns.NetNS) error {
	peer, err := netlink.LinkByName(cfg.PeerName)
	if err == nil {
		// del pre link
		err = utils.LinkDel(ctx, peer)
		if err != nil {
			return err
		}
	}

	if _, ok := err.(netlink.LinkNotFoundError); !ok {
		return err
	}
	contLinkName, err := ip.RandomVethName()
	if err != nil {
		return err
	}
	v := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			MTU:       cfg.MTU,
			Name:      contLinkName,
			Namespace: netlink.NsFd(int(netNS.Fd())),
		},
		PeerName: cfg.PeerName,
	}
	if cfg.HwAddr != nil {
		v.HardwareAddr = cfg.HwAddr
	}
	err = utils.LinkAdd(ctx, v)
	if err != nil {
		return err
	}

	return netNS.Do(func(netNS ns.NetNS) error {
		contLink, innerErr := netlink.LinkByName(contLinkName)
		if innerErr != nil {
			return innerErr
		}
		_, innerErr = utils.EnsureLinkName(ctx, contLink, cfg.IfName)
		return innerErr
	})
}
