package daemon

import (
	"context"
	"encoding/json"
	"fmt"

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

	if res.PodInfo.PodNetworkType == daemon.PodNetworkTypeVPCENI {
		return syncExclusiveENIRoutes(ctx, res)
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
			ENIGatewayIP:   &terwayTypes.IPSet{},
			ENIIndex:       eni.Attrs().Index,
			StripVlan:      conf.ENIInfo.Trunk,
		}
		if conf.BasicInfo.PodIP.IPv4 != "" {
			setUp.ContainerIPNet.SetIPNet(conf.BasicInfo.PodIP.IPv4 + "/32")
		}
		if conf.BasicInfo.PodIP.IPv6 != "" {
			setUp.ContainerIPNet.SetIPNet(conf.BasicInfo.PodIP.IPv6 + "/128")
		}

		if setUp.StripVlan {
			if conf.ENIInfo.GatewayIP == nil {
				continue
			}
			setUp.ENIGatewayIP.SetIP(conf.ENIInfo.GatewayIP.IPv4)
			setUp.ENIGatewayIP.SetIP(conf.ENIInfo.GatewayIP.IPv6)
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

func syncExclusiveENIRoutes(ctx context.Context, res daemon.PodResources) error {
	if res.NetConf == "" {
		return nil
	}

	netConf := make([]*rpc.NetConf, 0)
	err := json.Unmarshal([]byte(res.NetConf), &netConf)
	if err != nil {
		return fmt.Errorf("parse network config for pod %s/%s: %w", res.PodInfo.Namespace, res.PodInfo.Name, err)
	}

	links, err := netlink.LinkList()
	if err != nil {
		return err
	}

	for _, conf := range netConf {
		if conf.BasicInfo == nil || conf.BasicInfo.PodIP == nil {
			continue
		}

		ifName := "eth0"
		if conf.IfName != "" {
			ifName = conf.IfName
		}
		hostVethName, _ := link.VethNameForPod(res.PodInfo.Name, res.PodInfo.Namespace, ifName, "cali")
		hostVeth, ok := lo.Find(links, func(item netlink.Link) bool {
			return hostVethName == item.Attrs().Name
		})
		if !ok {
			continue
		}

		setup := &types.SetupConfig{
			HostVETHName:   hostVethName,
			ContainerIPNet: &terwayTypes.IPNetSet{},
		}
		if conf.BasicInfo.PodIP.IPv4 != "" {
			setup.ContainerIPNet.SetIPNet(conf.BasicInfo.PodIP.IPv4 + "/32")
		}
		if conf.BasicInfo.PodIP.IPv6 != "" {
			setup.ContainerIPNet.SetIPNet(conf.BasicInfo.PodIP.IPv6 + "/128")
		}

		setup.HostIPSet, err = utils.GetHostIP(
			setup.ContainerIPNet.IPv4 != nil,
			setup.ContainerIPNet.IPv6 != nil,
		)
		if err != nil {
			return err
		}

		var routes []*netlink.Route
		if setup.ContainerIPNet.IPv4 != nil {
			routes = append(routes, &netlink.Route{
				LinkIndex: hostVeth.Attrs().Index,
				Scope:     netlink.SCOPE_LINK,
				Dst:       utils.NewIPNetWithMaxMask(setup.ContainerIPNet.IPv4),
				Src:       setup.HostIPSet.IPv4.IP,
			})
		}
		if setup.ContainerIPNet.IPv6 != nil {
			routes = append(routes, &netlink.Route{
				LinkIndex: hostVeth.Attrs().Index,
				Dst:       utils.NewIPNetWithMaxMask(setup.ContainerIPNet.IPv6),
				Src:       setup.HostIPSet.IPv6.IP,
			})
		}
		for _, route := range routes {
			_, err = utils.EnsureRoute(ctx, route)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
