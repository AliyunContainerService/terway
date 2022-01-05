package ipvlan

import (
	"github.com/AliyunContainerService/terway/plugin/driver/utils"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

type IPVlan struct {
	Parent  string
	PreName string
	IfName  string
	MTU     int
}

func Setup(cfg *IPVlan, netNS ns.NetNS) error {
	parentLink, err := netlink.LinkByName(cfg.Parent)
	if err != nil {
		return err
	}

	pre, err := netlink.LinkByName(cfg.PreName)
	if err == nil {
		// del pre link
		err = utils.LinkDel(pre)
		if err != nil {
			return err
		}
	}

	if _, ok := err.(netlink.LinkNotFoundError); !ok {
		return err
	}

	v := &netlink.IPVlan{
		LinkAttrs: netlink.LinkAttrs{
			MTU:         cfg.MTU,
			Name:        cfg.PreName,
			Namespace:   netlink.NsFd(int(netNS.Fd())),
			ParentIndex: parentLink.Attrs().Index,
		},
		Mode: netlink.IPVLAN_MODE_L2,
	}
	err = utils.LinkAdd(v)
	if err != nil {
		return err
	}

	return netNS.Do(func(netNS ns.NetNS) error {
		contLink, innerErr := netlink.LinkByName(cfg.PreName)
		if innerErr != nil {
			return innerErr
		}
		_, innerErr = utils.EnsureLinkName(contLink, cfg.IfName)
		return innerErr
	})
}
