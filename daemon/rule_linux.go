package daemon

import (
	"context"
	"encoding/json"

	"github.com/samber/lo"
	"github.com/vishvananda/netlink"

	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	"github.com/AliyunContainerService/terway/plugin/datapath"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/rpc"
	terwayTypes "github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

func ruleSync(ctx context.Context, res daemon.PodResources) error {
	if res.PodInfo == nil {
		return nil
	}

	if res.PodInfo.PodNetworkType != daemon.PodNetworkTypeENIMultiIP {
		return nil
	}

	switch nodecap.GetNodeCapabilities(nodecap.NodeCapabilityDataPath) {
	case "datapathv2", "veth", "":
	default:
		return nil
	}

	netConf := make([]*rpc.NetConf, 0)
	err := json.Unmarshal([]byte(res.NetConf), &netConf)
	if err != nil {
		return nil
	}

	links, err := netlink.LinkList()
	if err != nil {
		return err
	}

	for _, conf := range netConf {
		if conf.BasicInfo == nil || conf.ENIInfo == nil ||
			conf.BasicInfo.PodIP == nil {
			continue
		}
		ifName := "eth0"
		if conf.IfName != "" {
			ifName = conf.IfName
		}

		hostVethName, _ := link.VethNameForPod(res.PodInfo.Name, res.PodInfo.Namespace, ifName, "cali")

		// check host veth ,make sure pod is present
		hostVeth, ok := lo.Find(links, func(item netlink.Link) bool {
			return hostVethName == item.Attrs().Name
		})
		if !ok {
			continue
		}

		eni, ok := lo.Find(links, func(item netlink.Link) bool {
			if _, ok := item.(*netlink.Device); !ok {
				return false
			}
			return item.Attrs().HardwareAddr.String() == conf.ENIInfo.MAC
		})
		if !ok {
			continue
		}

		setUp := &types.SetupConfig{
			ContainerIPNet: &terwayTypes.IPNetSet{},
			GatewayIP:      &terwayTypes.IPSet{},
			ENIIndex:       eni.Attrs().Index,
		}
		if conf.BasicInfo.PodIP.IPv4 != "" {
			setUp.ContainerIPNet.SetIPNet(conf.BasicInfo.PodIP.IPv4 + "/32")
		}
		if conf.BasicInfo.PodIP.IPv6 != "" {
			setUp.ContainerIPNet.SetIPNet(conf.BasicInfo.PodIP.IPv6 + "/128")
		}
		setUp.GatewayIP.SetIP(conf.BasicInfo.GatewayIP.IPv4)
		setUp.GatewayIP.SetIP(conf.BasicInfo.GatewayIP.IPv6)

		// 1. route point to hostVeth
		table := utils.GetRouteTableID(eni.Attrs().Index)

		eniConf := datapath.GenerateENICfgForPolicy(setUp, eni, table)
		hostVethConf := datapath.GenerateHostPeerCfgForPolicy(setUp, hostVeth, table)

		// default via 10.xx.xx.253 dev eth1 onlink table 1003
		for _, route := range eniConf.Routes {
			_, err = utils.EnsureRoute(ctx, route)
			if err != nil {
				return err
			}
		}
		// 10.xx.xx.xx dev calixx scope link
		for _, route := range hostVethConf.Routes {
			_, err = utils.EnsureRoute(ctx, route)
			if err != nil {
				return err
			}
		}

		for _, rule := range hostVethConf.Rules {
			_, err = utils.EnsureIPRule(ctx, rule)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
