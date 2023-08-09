package main

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"time"

	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/plugin/datapath"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/rpc"
	terwayTypes "github.com/AliyunContainerService/terway/types"

	"github.com/containernetworking/cni/pkg/skel"
	cniTypes "github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/cni/pkg/version"
	bv "github.com/containernetworking/plugins/pkg/utils/buildversion"
	"google.golang.org/grpc"
)

const (
	defaultSocketPath   = "/var/run/eni/eni.socket"
	defaultVethPrefix   = "cali"
	defaultDialTimeout  = 10 * time.Second
	defaultCniTimeout   = 120 * time.Second
	defaultEventTimeout = 10 * time.Second
	delegateIpam        = "host-local"
	defaultMTU          = 1500
	delegateConf        = `
{
	"name": "networks",
    "cniVersion": "0.4.0",
	"ipam": {
		"type": "host-local",
		"subnet": "%s",
		"dataDir": "/var/lib/cni/",
		"routes": [
			{ "dst": "0.0.0.0/0" }
		]
	}
}
`

	terwayCNILock = "/var/run/eni/terway_cni.lock"
)

func init() {
	runtime.LockOSThread()
}

func main() {
	skel.PluginMain(cmdAdd, cmdCheck, cmdDel, version.PluginSupports("0.3.0", "0.3.1", "0.4.0", "1.0.0"), bv.BuildString("terway"))
}

func cmdAdd(args *skel.CmdArgs) error {
	utils.Hook.AddExtraInfo("cmd", "add")

	cmdArgs, err := getCmdArgs(args)
	if err != nil {
		return err
	}
	defer cmdArgs.Close()
	conf, k8sConfig := cmdArgs.GetCNIConf(), cmdArgs.GetK8SConfig()

	if conf.Debug {
		utils.SetLogDebug()
	}
	logger := utils.Log.WithFields(map[string]interface{}{
		"netns":        args.Netns,
		"podName":      string(k8sConfig.K8S_POD_NAME),
		"podNamespace": string(k8sConfig.K8S_POD_NAMESPACE),
		"containerID":  string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
	})
	logger.Debugf("args: %s", utils.JSONStr(args))
	logger.Debugf("ns %s , k8s %s, cni std %s", cmdArgs.GetNetNSPath(), utils.JSONStr(k8sConfig), utils.JSONStr(conf))

	ctx, cancel := context.WithTimeout(context.Background(), defaultCniTimeout)
	defer cancel()

	client, conn, err := getNetworkClient(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	var (
		containerIPNet *terwayTypes.IPNetSet
		gatewayIPSet   *terwayTypes.IPSet
	)
	containerIPNet, gatewayIPSet, err = doCmdAdd(ctx, logger, client, cmdArgs)
	if err != nil {
		logger.WithError(err).Error("error adding")
		return cniTypes.NewError(cniTypes.ErrTryAgainLater, "failed to do add", err.Error())
	}

	result := &current.Result{}

	result.Interfaces = append(result.Interfaces, &current.Interface{
		Name:    args.IfName,
		Sandbox: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
	})
	if containerIPNet.IPv4 != nil && gatewayIPSet.IPv4 != nil {
		result.IPs = append(result.IPs, &current.IPConfig{
			Address:   *containerIPNet.IPv4,
			Gateway:   gatewayIPSet.IPv4,
			Interface: current.Int(0),
		})
	}
	if containerIPNet.IPv6 != nil && gatewayIPSet.IPv6 != nil {
		result.IPs = append(result.IPs, &current.IPConfig{
			Address:   *containerIPNet.IPv6,
			Gateway:   gatewayIPSet.IPv6,
			Interface: current.Int(0),
		})
	}

	return cniTypes.PrintResult(result, conf.CNIVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	utils.Hook.AddExtraInfo("cmd", "del")
	if args.Netns == "" {
		return nil
	}

	cmdArgs, err := getCmdArgs(args)
	if err != nil {
		return err
	}
	defer cmdArgs.Close()
	conf, k8sConfig := cmdArgs.GetCNIConf(), cmdArgs.GetK8SConfig()

	if conf.Debug {
		utils.SetLogDebug()
	}
	logger := utils.Log.WithFields(map[string]interface{}{
		"netns":        args.Netns,
		"podName":      string(k8sConfig.K8S_POD_NAME),
		"podNamespace": string(k8sConfig.K8S_POD_NAMESPACE),
		"containerID":  string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
	})
	logger.Debugf("args: %s", utils.JSONStr(args))
	logger.Debugf("ns %s , k8s %s, cni std %s", cmdArgs.GetNetNSPath(), utils.JSONStr(k8sConfig), utils.JSONStr(conf))

	ctx, cancel := context.WithTimeout(context.Background(), defaultCniTimeout)
	defer cancel()

	client, conn, err := getNetworkClient(ctx)
	if err != nil {
		return fmt.Errorf("error create grpc client, %w", err)
	}
	defer conn.Close()

	err = doCmdDel(ctx, logger, client, cmdArgs)
	if err != nil {
		logger.WithError(err).Error("error deleting")
		return cniTypes.NewError(cniTypes.ErrTryAgainLater, "failed to do del", err.Error())
	}

	return cniTypes.PrintResult(&current.Result{
		CNIVersion: conf.CNIVersion,
	}, conf.CNIVersion)
}

func cmdCheck(args *skel.CmdArgs) error {
	utils.Hook.AddExtraInfo("cmd", "check")
	if args.Netns == "" {
		return nil
	}

	cmdArgs, err := getCmdArgs(args)
	if err != nil {
		if isNSPathNotExist(err) {
			return nil
		}
		return err
	}
	defer cmdArgs.Close()
	conf, k8sConfig := cmdArgs.GetCNIConf(), cmdArgs.GetK8SConfig()

	if conf.Debug {
		utils.SetLogDebug()
	}
	logger := utils.Log.WithFields(map[string]interface{}{
		"netns":        args.Netns,
		"podName":      string(k8sConfig.K8S_POD_NAME),
		"podNamespace": string(k8sConfig.K8S_POD_NAMESPACE),
		"containerID":  string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
	})
	logger.Debugf("args: %s", utils.JSONStr(args))
	logger.Debugf("ns %s , k8s %s, cni std %s", cmdArgs.GetNetNSPath(), utils.JSONStr(k8sConfig), utils.JSONStr(conf))

	ctx, cancel := context.WithTimeout(context.Background(), defaultCniTimeout)
	defer cancel()

	client, conn, err := getNetworkClient(ctx)
	if err != nil {
		return fmt.Errorf("error create grpc client, %w", err)
	}
	defer conn.Close()

	err = doCmdCheck(ctx, logger, client, cmdArgs)
	if err != nil {
		logger.WithError(err).Error("error checking")
		return err
	}
	return nil
}

func getNetworkClient(ctx context.Context) (rpc.TerwayBackendClient, *grpc.ClientConn, error) {
	ctx, parent := context.WithTimeout(ctx, defaultDialTimeout)
	defer parent()
	conn, err := grpc.DialContext(ctx, defaultSocketPath, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(
		func(ctx context.Context, s string) (net.Conn, error) {
			unixAddr, err := net.ResolveUnixAddr("unix", s)
			if err != nil {
				return nil, err
			}
			d := net.Dialer{}
			return d.DialContext(ctx, "unix", unixAddr.String())
		}),
		grpc.WithBlock(),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  time.Second,
				Multiplier: 1,
				MaxDelay:   time.Second,
			},
		}),
	)

	if err != nil {
		return nil, nil, cniTypes.NewError(cniTypes.ErrTryAgainLater, "failed connect to daemon", err.Error())
	}

	client := rpc.NewTerwayBackendClient(conn)
	return client, conn, nil
}

func parseSetupConf(args *skel.CmdArgs, alloc *rpc.NetConf, conf *types.CNIConf, ipType rpc.IPType) (*types.SetupConfig, error) {
	var (
		err            error
		containerIPNet *terwayTypes.IPNetSet
		gatewayIP      *terwayTypes.IPSet
		serviceCIDR    *terwayTypes.IPNetSet
		eniGatewayIP   *terwayTypes.IPSet
		deviceID       int32
		trunkENI       bool
		vid            uint32

		ingress         uint64
		egress          uint64
		networkPriority uint32

		routes []cniTypes.Route

		disableCreatePeer bool
	)

	serviceCIDR, err = terwayTypes.ToIPNetSet(alloc.GetBasicInfo().GetServiceCIDR())
	if err != nil {
		return nil, err
	}

	if ipType == rpc.IPType_TypeVPCIP {
		subnetStr := alloc.GetBasicInfo().GetPodCIDR().GetIPv4()
		_, subnet, err := net.ParseCIDR(subnetStr)
		if err != nil {
			return nil, fmt.Errorf("parse cidr %s, %w", subnetStr, err)
		}
		containerIPNet = &terwayTypes.IPNetSet{
			IPv4: subnet,
			IPv6: nil,
		}
	} else if alloc.GetBasicInfo() != nil {
		podIP := alloc.GetBasicInfo().GetPodIP()
		subNet := alloc.GetBasicInfo().GetPodCIDR()
		gw := alloc.GetBasicInfo().GetGatewayIP()

		containerIPNet, err = terwayTypes.BuildIPNet(podIP, subNet)
		if err != nil {
			return nil, err
		}
		gatewayIP, err = terwayTypes.ToIPSet(gw)
		if err != nil {
			return nil, err
		}
		disableCreatePeer = conf.DisableHostPeer
	}

	if alloc.GetENIInfo() != nil {
		mac := alloc.GetENIInfo().GetMAC()
		if mac != "" {
			deviceID, err = link.GetDeviceNumber(mac)
			if err != nil {
				return nil, err
			}
		}
		trunkENI = alloc.GetENIInfo().GetTrunk()
		vid = alloc.GetENIInfo().GetVid()
		if alloc.GetENIInfo().GetGatewayIP() != nil {
			eniGatewayIP, err = terwayTypes.ToIPSet(alloc.GetENIInfo().GetGatewayIP())
			if err != nil {
				return nil, err
			}
		}
	}
	if alloc.GetPod() != nil {
		ingress = alloc.GetPod().GetIngress()
		egress = alloc.GetPod().GetEgress()
		networkPriority = datapath.PrioMap[alloc.GetPod().GetNetworkPriority()]
	}
	if conf.RuntimeConfig.Bandwidth.EgressRate > 0 {
		egress = uint64(conf.RuntimeConfig.Bandwidth.EgressRate / 8)
	}
	if conf.RuntimeConfig.Bandwidth.IngressRate > 0 {
		ingress = uint64(conf.RuntimeConfig.Bandwidth.IngressRate / 8)
	}

	hostStackCIDRs := make([]*net.IPNet, 0)
	for _, v := range conf.HostStackCIDRs {
		_, cidr, err := net.ParseCIDR(v)
		if err != nil {
			return nil, fmt.Errorf("host_stack_cidrs(%s) is invaild: %v", v, err)

		}
		hostStackCIDRs = append(hostStackCIDRs, cidr)
	}

	name := alloc.IfName
	if name == "" {
		name = args.IfName
	}
	for _, r := range alloc.GetExtraRoutes() {
		ip, n, err := net.ParseCIDR(r.Dst)
		if err != nil {
			return nil, fmt.Errorf("error parse extra routes, %w", err)
		}
		route := cniTypes.Route{Dst: *n}
		if ip.To4() != nil {
			route.GW = gatewayIP.IPv4
		} else {
			route.GW = gatewayIP.IPv6
		}
		routes = append(routes, route)
	}

	dp := getDatePath(ipType, conf.VlanStripType, trunkENI)
	return &types.SetupConfig{
		DP:                    dp,
		ContainerIfName:       name,
		ContainerIPNet:        containerIPNet,
		GatewayIP:             gatewayIP,
		MTU:                   conf.MTU,
		ENIIndex:              int(deviceID),
		ENIGatewayIP:          eniGatewayIP,
		ServiceCIDR:           serviceCIDR,
		HostStackCIDRs:        hostStackCIDRs,
		BandwidthMode:         conf.BandwidthMode,
		EnableNetworkPriority: conf.EnableNetworkPriority,
		Ingress:               ingress,
		Egress:                egress,
		StripVlan:             trunkENI,
		Vid:                   int(vid),
		DefaultRoute:          alloc.GetDefaultRoute(),
		ExtraRoutes:           routes,
		DisableCreatePeer:     disableCreatePeer,
		RuntimeConfig:         conf.RuntimeConfig,
		NetworkPriority:       networkPriority,
	}, nil
}

func parseTearDownConf(alloc *rpc.NetConf, conf *types.CNIConf, ipType rpc.IPType) (*types.TeardownCfg, error) {
	if alloc.GetBasicInfo() == nil {
		return nil, fmt.Errorf("return empty pod alloc info: %v", alloc)
	}

	var (
		err            error
		containerIPNet *terwayTypes.IPNetSet
		serviceCIDR    *terwayTypes.IPNetSet
		eniIndex       int32
	)

	serviceCIDR, err = terwayTypes.ToIPNetSet(alloc.GetBasicInfo().GetServiceCIDR())
	if err != nil {
		return nil, err
	}

	if ipType == rpc.IPType_TypeVPCIP {
		subnetStr := alloc.GetBasicInfo().GetPodCIDR().GetIPv4()
		_, subnet, err := net.ParseCIDR(subnetStr)
		if err != nil {
			return nil, fmt.Errorf("parse cidr %s, %w", subnetStr, err)
		}
		containerIPNet = &terwayTypes.IPNetSet{
			IPv4: subnet,
			IPv6: nil,
		}
	} else if alloc.GetBasicInfo() != nil {
		podIP := alloc.GetBasicInfo().GetPodIP()
		subNet := alloc.GetBasicInfo().GetPodCIDR()

		containerIPNet, err = terwayTypes.BuildIPNet(podIP, subNet)
		if err != nil {
			return nil, err
		}
	}
	if alloc.GetENIInfo() != nil {
		mac := alloc.GetENIInfo().GetMAC()
		if mac != "" {
			eniIndex, _ = link.GetDeviceNumber(mac)
		}
	}

	dp := getDatePath(ipType, conf.VlanStripType, false)
	return &types.TeardownCfg{
		DP:                    dp,
		ContainerIPNet:        containerIPNet,
		ServiceCIDR:           serviceCIDR,
		ENIIndex:              int(eniIndex),
		EnableNetworkPriority: conf.EnableNetworkPriority,
	}, nil
}

func parseCheckConf(args *skel.CmdArgs, alloc *rpc.NetConf, conf *types.CNIConf, ipType rpc.IPType) (*types.CheckConfig, error) {
	var (
		err            error
		containerIPNet *terwayTypes.IPNetSet
		gatewayIP      *terwayTypes.IPSet
		deviceID       int32
		trunkENI       bool
	)

	if alloc.GetBasicInfo() != nil {
		podIP := alloc.GetBasicInfo().GetPodIP()
		subNet := alloc.GetBasicInfo().GetPodCIDR()
		gw := alloc.GetBasicInfo().GetGatewayIP()

		containerIPNet, err = terwayTypes.BuildIPNet(podIP, subNet)
		if err != nil {
			return nil, err
		}
		gatewayIP, err = terwayTypes.ToIPSet(gw)
		if err != nil {
			return nil, err
		}
	}
	if alloc.GetENIInfo() != nil {
		mac := alloc.GetENIInfo().GetMAC()
		if mac != "" {
			deviceID, err = link.GetDeviceNumber(mac)
			if err != nil {
				return nil, err
			}
		}
		trunkENI = alloc.GetENIInfo().GetTrunk()
	}

	name := alloc.IfName
	if name == "" {
		name = args.IfName
	}

	dp := getDatePath(ipType, conf.VlanStripType, trunkENI)
	return &types.CheckConfig{
		DP:              dp,
		ContainerIfName: name,
		ContainerIPNet:  containerIPNet,
		GatewayIP:       gatewayIP,
		MTU:             conf.MTU,
		ENIIndex:        deviceID,
		TrunkENI:        trunkENI,
		DefaultRoute:    alloc.GetDefaultRoute(),
	}, nil
}

func getDatePath(ipType rpc.IPType, vlanStripType types.VlanStripType, trunk bool) types.DataPath {
	switch ipType {
	case rpc.IPType_TypeVPCIP:
		return types.VPCRoute
	case rpc.IPType_TypeVPCENI:
		if trunk {
			return types.Vlan
		}
		return types.ExclusiveENI
	case rpc.IPType_TypeENIMultiIP:
		if trunk && vlanStripType == types.VlanStripTypeVlan {
			return types.Vlan
		}
		return types.IPVlan
	default:
		panic(fmt.Sprintf("unsupported ipType %s", ipType))
	}
}
