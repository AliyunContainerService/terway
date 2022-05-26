package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"time"

	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/plugin/datapath"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/rpc"
	terwayTypes "github.com/AliyunContainerService/terway/types"

	"github.com/containernetworking/cni/pkg/skel"
	cniTypes "github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/plugins/pkg/ns"
	bv "github.com/containernetworking/plugins/pkg/utils/buildversion"
	"google.golang.org/grpc"
)

const (
	defaultSocketPath   = "/var/run/eni/eni.socket"
	defaultVethPrefix   = "cali"
	defaultCniTimeout   = 120 * time.Second
	defaultEventTimeout = 10 * time.Second
	delegateIpam        = "host-local"
	defaultMTU          = 1500
	delegateConf        = `
{
	"name": "networks",
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
	skel.PluginMain(cmdAdd, cmdCheck, cmdDel, version.PluginSupports("0.3.0", "0.3.1", "0.4.0"), bv.BuildString("terway"))
}

func parseCmdArgs(args *skel.CmdArgs) (ns.NetNS, *types.CNIConf, *types.K8SArgs, error) {
	netNS, err := ns.GetNS(args.Netns)
	if err != nil {
		return nil, nil, nil, err
	}

	conf := types.CNIConf{}
	if err = json.Unmarshal(args.StdinData, &conf); err != nil {
		return nil, nil, nil, fmt.Errorf("error parse args, %w", err)
	}

	if conf.MTU == 0 {
		conf.MTU = defaultMTU
	}

	k8sConfig := types.K8SArgs{}
	if err = cniTypes.LoadArgs(args.Args, &k8sConfig); err != nil {
		return nil, nil, nil, fmt.Errorf("error parse args, %w", err)
	}

	return netNS, &conf, &k8sConfig, nil
}

func cmdAdd(args *skel.CmdArgs) error {
	utils.Hook.AddExtraInfo("cmd", "add")

	cniNetns, conf, k8sConfig, err := parseCmdArgs(args)
	if err != nil {
		return err
	}
	defer cniNetns.Close()

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
	logger.Debugf("ns %s , k8s %s, cni std %s", cniNetns.Path(), utils.JSONStr(k8sConfig), utils.JSONStr(conf))

	ctx, cancel := context.WithTimeout(context.Background(), defaultCniTimeout)
	defer cancel()

	client, conn, err := getNetworkClient(ctx)
	if err != nil {
		return fmt.Errorf("error create grpc client, %w", err)
	}
	defer conn.Close()

	var (
		containerIPNet *terwayTypes.IPNetSet
		gatewayIPSet   *terwayTypes.IPSet
	)

	defer func() {
		eventCtx, cancel := context.WithTimeout(context.Background(), defaultEventTimeout)
		defer cancel()
		if err != nil {
			_, _ = client.RecordEvent(eventCtx, &rpc.EventRequest{
				EventTarget:     rpc.EventTarget_EventTargetPod,
				K8SPodName:      string(k8sConfig.K8S_POD_NAME),
				K8SPodNamespace: string(k8sConfig.K8S_POD_NAMESPACE),
				EventType:       rpc.EventType_EventTypeWarning,
				Reason:          "AllocIPFailed",
				Message:         err.Error(),
			})
		} else {
			_, _ = client.RecordEvent(eventCtx, &rpc.EventRequest{
				EventTarget:     rpc.EventTarget_EventTargetPod,
				K8SPodName:      string(k8sConfig.K8S_POD_NAME),
				K8SPodNamespace: string(k8sConfig.K8S_POD_NAMESPACE),
				EventType:       rpc.EventType_EventTypeNormal,
				Reason:          "AllocIPSucceed",
				Message:         fmt.Sprintf("Alloc IP %s", containerIPNet.String()),
			})
		}
	}()

	allocResult, err := client.AllocIP(ctx, &rpc.AllocIPRequest{
		Netns:                  args.Netns,
		K8SPodName:             string(k8sConfig.K8S_POD_NAME),
		K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
		K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
		IfName:                 args.IfName,
	})
	if err != nil {
		return fmt.Errorf("cmdAdd: error alloc ip %w", err)
	}
	if !allocResult.Success {
		return fmt.Errorf("cmdAdd: alloc ip return not success")
	}

	defer func() {
		if err != nil {
			logger.Error(err)
			releaseCtx, cancel := context.WithTimeout(context.Background(), defaultCniTimeout)
			defer cancel()
			_, _ = client.ReleaseIP(releaseCtx, &rpc.ReleaseIPRequest{
				K8SPodName:             string(k8sConfig.K8S_POD_NAME),
				K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
				K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
				Reason:                 fmt.Sprintf("roll back ip for error: %v", err),
			})
		}
	}()

	ipv4, ipv6 := allocResult.IPv4, allocResult.IPv6

	hostIPSet, err := utils.GetHostIP(ipv4, ipv6)
	if err != nil {
		return err
	}

	err = utils.EnsureHostNsConfig(ipv4, ipv6)
	if err != nil {
		return fmt.Errorf("error setup host ns configs, %w", err)
	}

	multiNetwork := len(allocResult.NetConfs) > 1

	l, err := utils.GrabFileLock(terwayCNILock)
	if err != nil {
		return err
	}
	defer l.Close()

	for _, netConf := range allocResult.NetConfs {
		var setupCfg *types.SetupConfig
		setupCfg, err = parseSetupConf(args, netConf, conf, allocResult.IPType)
		if err != nil {
			return fmt.Errorf("error parse config, %w", err)
		}
		setupCfg.HostVETHName, _ = link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), netConf.IfName, defaultVethPrefix)
		setupCfg.HostIPSet = hostIPSet
		setupCfg.MultiNetwork = multiNetwork
		logger.Debugf("setupCfg %#v", setupCfg)

		switch setupCfg.DP {
		case types.VPCRoute:
			utils.Hook.AddExtraInfo("dp", "vpcRoute")

			var r cniTypes.Result
			r, err = ipam.ExecAdd(delegateIpam, []byte(fmt.Sprintf(delegateConf, setupCfg.ContainerIPNet.IPv4)))
			if err != nil {
				return fmt.Errorf("error allocate ip from delegate ipam %v: %v", delegateIpam, err)
			}
			var ipamResult *current.Result
			ipamResult, err = current.NewResultFromResult(r)
			if err != nil {
				return fmt.Errorf("error get result from delegate ipam result %v: %v", delegateIpam, err)
			}

			err = func() (err error) {
				defer func() {
					if err != nil {
						err = ipam.ExecDel(delegateIpam, []byte(fmt.Sprintf(delegateConf, setupCfg.ContainerIPNet.IPv4)))
					}
				}()
				if len(ipamResult.IPs) != 1 {
					return fmt.Errorf("error get result from delegate ipam result %v: ipam result is not one ip", delegateIpam)
				}
				podIPAddr := ipamResult.IPs[0].Address
				gateway := ipamResult.IPs[0].Gateway

				containerIPNet = &terwayTypes.IPNetSet{
					IPv4: &podIPAddr,
				}
				gatewayIPSet = &terwayTypes.IPSet{
					IPv4: gateway,
				}

				setupCfg.ContainerIPNet = containerIPNet
				setupCfg.GatewayIP = gatewayIPSet

				return datapath.NewVPCRoute().Setup(setupCfg, cniNetns)
			}()
			if err != nil {
				return err
			}
		case types.IPVlan:
			utils.Hook.AddExtraInfo("dp", "ipvlan")

			if conf.IPVlan() {
				available := false
				available, err = datapath.CheckIPVLanAvailable()
				if err != nil {
					return err
				}
				if available {
					if setupCfg.ContainerIfName == args.IfName {
						containerIPNet = setupCfg.ContainerIPNet
						gatewayIPSet = setupCfg.GatewayIP
					}
					err = datapath.NewIPVlanDriver().Setup(setupCfg, cniNetns)
					if err != nil {
						return err
					}
					continue
				}
				_, _ = client.RecordEvent(ctx, &rpc.EventRequest{
					EventTarget:     rpc.EventTarget_EventTargetPod,
					K8SPodName:      string(k8sConfig.K8S_POD_NAME),
					K8SPodNamespace: string(k8sConfig.K8S_POD_NAMESPACE),
					EventType:       rpc.EventType_EventTypeWarning,
					Reason:          "VirtualModeChanged",
					Message:         "IPVLan seems unavailable, use Veth instead",
				})
			}
			fallthrough
		case types.PolicyRoute:
			utils.Hook.AddExtraInfo("dp", "policyRoute")

			if setupCfg.ContainerIfName == args.IfName {
				containerIPNet = setupCfg.ContainerIPNet
				gatewayIPSet = setupCfg.GatewayIP
			}
			err = datapath.NewPolicyRoute().Setup(setupCfg, cniNetns)
			if err != nil {
				return err
			}
		case types.ExclusiveENI:
			utils.Hook.AddExtraInfo("dp", "exclusiveENI")

			if setupCfg.ContainerIfName == args.IfName {
				containerIPNet = setupCfg.ContainerIPNet
				gatewayIPSet = setupCfg.GatewayIP
			}

			err = datapath.NewExclusiveENIDriver().Setup(setupCfg, cniNetns)
			if err != nil {
				return err
			}
		case types.Vlan:
			utils.Hook.AddExtraInfo("dp", "vlan")

			if setupCfg.ContainerIfName == args.IfName {
				containerIPNet = setupCfg.ContainerIPNet
				gatewayIPSet = setupCfg.GatewayIP
			}
			err = datapath.NewVlan().Setup(setupCfg, cniNetns)
			if err != nil {
				return fmt.Errorf("setup, %w", err)
			}
		default:
			return fmt.Errorf("not support this network type")
		}
	}

	if containerIPNet == nil || gatewayIPSet == nil {
		return fmt.Errorf("eth0 config is missing")
	}

	result := &current.Result{}

	result.Interfaces = append(result.Interfaces, &current.Interface{
		Name:    args.IfName,
		Sandbox: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
	})
	if containerIPNet.IPv4 != nil && gatewayIPSet.IPv4 != nil {
		result.IPs = append(result.IPs, &current.IPConfig{
			Version:   "4",
			Address:   *containerIPNet.IPv4,
			Gateway:   gatewayIPSet.IPv4,
			Interface: current.Int(0),
		})
	}
	if containerIPNet.IPv6 != nil && gatewayIPSet.IPv6 != nil {
		result.IPs = append(result.IPs, &current.IPConfig{
			Version:   "6",
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

	cniNetns, conf, k8sConfig, err := parseCmdArgs(args)
	if err != nil {
		return err
	}
	defer cniNetns.Close()

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
	logger.Debugf("ns %s , k8s %s, cni std %s", cniNetns.Path(), utils.JSONStr(k8sConfig), utils.JSONStr(conf))

	l, err := utils.GrabFileLock(terwayCNILock)
	if err != nil {
		return err
	}

	// try cleanup all resource
	err = utils.GenericTearDown(cniNetns)
	if err != nil {
		_ = l.Close()
		logger.Errorf("error teardown %s", err.Error())

		return cniTypes.PrintResult(&current.Result{
			CNIVersion: conf.CNIVersion,
		}, conf.CNIVersion)
	}
	_ = l.Close()

	ctx, cancel := context.WithTimeout(context.Background(), defaultCniTimeout)
	defer cancel()

	client, conn, err := getNetworkClient(ctx)
	if err != nil {
		return fmt.Errorf("error create grpc client, %w", err)
	}
	defer conn.Close()

	getResult, err := client.GetIPInfo(ctx, &rpc.GetInfoRequest{
		K8SPodName:             string(k8sConfig.K8S_POD_NAME),
		K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
		K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
	})
	if err != nil {
		return fmt.Errorf("error get ip from terway, pod %s/%s, %w", string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME), err)
	}
	if getResult.Error == rpc.Error_ErrCRDNotFound {
		return cniTypes.PrintResult(&current.Result{
			CNIVersion: conf.CNIVersion,
		}, conf.CNIVersion)
	}

	err = func() error {
		l, err = utils.GrabFileLock(terwayCNILock)
		if err != nil {
			return err
		}
		defer l.Close()
		for _, netConf := range getResult.NetConfs {
			var teardownCfg *types.TeardownCfg
			teardownCfg, err = parseTearDownConf(netConf, conf, getResult.IPType)
			if err != nil {
				logger.Errorf("error parse config, %s", err.Error())
				return nil
			}

			switch teardownCfg.DP {
			case types.VPCRoute:
				utils.Hook.AddExtraInfo("dp", "vpcRoute")

				err = ipam.ExecDel(delegateIpam, []byte(fmt.Sprintf(delegateConf, teardownCfg.ContainerIPNet.IPv4)))
				if err != nil {
					return fmt.Errorf("teardown network ipam for pod: %s-%s, %w", string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME), err)
				}
			case types.IPVlan:
				utils.Hook.AddExtraInfo("dp", "ipvlan")

				if conf.IPVlan() {
					available := false
					available, err = datapath.CheckIPVLanAvailable()
					if err != nil {
						return err
					}
					if available {
						err = datapath.NewIPVlanDriver().Teardown(teardownCfg, cniNetns)
						if err != nil {
							return err
						}
						break
					}
				}
			}
		}
		return nil
	}()
	if err != nil {
		return err
	}

	reply, err := client.ReleaseIP(ctx, &rpc.ReleaseIPRequest{
		K8SPodName:             string(k8sConfig.K8S_POD_NAME),
		K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
		K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
		Reason:                 "normal release",
	})

	if err != nil || !reply.GetSuccess() {
		return fmt.Errorf("error release ip for pod, maybe cause resource leak: %v, %v", err, reply)
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
	cniNetns, conf, k8sConfig, err := parseCmdArgs(args)
	if err != nil {
		if _, ok := err.(ns.NSPathNotExistErr); ok {
			return nil
		}
		return err
	}
	defer cniNetns.Close()

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
	logger.Debugf("ns %s , k8s %s, cni std %s", cniNetns.Path(), utils.JSONStr(k8sConfig), utils.JSONStr(conf))

	ctx, cancel := context.WithTimeout(context.Background(), defaultCniTimeout)
	defer cancel()

	client, conn, err := getNetworkClient(ctx)
	if err != nil {
		return fmt.Errorf("error create grpc client, %w", err)
	}
	defer conn.Close()

	getResult, err := client.GetIPInfo(ctx, &rpc.GetInfoRequest{
		K8SPodName:             string(k8sConfig.K8S_POD_NAME),
		K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
		K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
	})
	if err != nil {
		logger.Debug(err)
		return nil
	}

	ipv4, ipv6 := getResult.IPv4, getResult.IPv6

	hostIPSet, err := utils.GetHostIP(ipv4, ipv6)
	if err != nil {
		return err
	}
	err = utils.EnsureHostNsConfig(ipv4, ipv6)
	if err != nil {
		return fmt.Errorf("error setup host ns configs, %w", err)
	}

	l, err := utils.GrabFileLock(terwayCNILock)
	if err != nil {
		return err
	}
	defer l.Close()

	for _, netConf := range getResult.NetConfs {
		var checkCfg *types.CheckConfig
		checkCfg, err = parseCheckConf(args, netConf, conf, getResult.IPType)
		if err != nil {
			return fmt.Errorf("error parse config, %w", err)
		}
		checkCfg.NetNS = cniNetns
		checkCfg.HostVETHName, _ = link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), netConf.IfName, defaultVethPrefix)
		checkCfg.HostIPSet = hostIPSet
		checkCfg.RecordPodEvent = func(msg string) {
			eventCtx, cancel := context.WithTimeout(ctx, defaultEventTimeout)
			defer cancel()
			_, _ = client.RecordEvent(eventCtx,
				&rpc.EventRequest{
					EventTarget:     rpc.EventTarget_EventTargetPod,
					K8SPodName:      string(k8sConfig.K8S_POD_NAME),
					K8SPodNamespace: string(k8sConfig.K8S_POD_NAMESPACE),
					EventType:       rpc.EventType_EventTypeWarning,
					Reason:          "ConfigCheck",
					Message:         msg,
				})
		}

		switch checkCfg.DP {
		case types.IPVlan:
			utils.Hook.AddExtraInfo("dp", "ipvlan")

			if conf.IPVlan() {
				available := false
				available, err = datapath.CheckIPVLanAvailable()
				if err != nil {
					return err
				}
				if available {
					err = datapath.NewIPVlanDriver().Check(checkCfg)
					if err != nil {
						return err
					}
					continue
				}
			}
			fallthrough
		case types.PolicyRoute:
			utils.Hook.AddExtraInfo("dp", "policyRoute")

			err = datapath.NewPolicyRoute().Check(checkCfg)
			if err != nil {
				return err
			}
		case types.ExclusiveENI:
			utils.Hook.AddExtraInfo("dp", "exclusiveENI")

			err = datapath.NewExclusiveENIDriver().Check(checkCfg)
			if err != nil {
				return err
			}
		case types.Vlan:
			utils.Hook.AddExtraInfo("dp", "vlan")

			err = datapath.NewVlan().Check(checkCfg)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("not support this network type")
		}
	}
	return nil
}

func getNetworkClient(ctx context.Context) (rpc.TerwayBackendClient, *grpc.ClientConn, error) {
	conn, err := grpc.DialContext(ctx, defaultSocketPath, grpc.WithInsecure(), grpc.WithContextDialer(
		func(ctx context.Context, s string) (net.Conn, error) {
			unixAddr, err := net.ResolveUnixAddr("unix", defaultSocketPath)
			if err != nil {
				return nil, fmt.Errorf("error resolve addr, %w", err)
			}
			d := net.Dialer{}
			return d.DialContext(ctx, "unix", unixAddr.String())
		}))
	if err != nil {
		return nil, nil, fmt.Errorf("error dial to terway %s, terway pod may staring, %w", defaultSocketPath, err)
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

		ingress uint64
		egress  uint64

		routes []cniTypes.Route

		disableCreatePeer bool
	)
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

		serviceCIDR, err = terwayTypes.ToIPNetSet(alloc.GetBasicInfo().GetServiceCIDR())
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
		DP:                dp,
		ContainerIfName:   name,
		ContainerIPNet:    containerIPNet,
		GatewayIP:         gatewayIP,
		MTU:               conf.MTU,
		ENIIndex:          int(deviceID),
		ENIGatewayIP:      eniGatewayIP,
		ServiceCIDR:       serviceCIDR,
		HostStackCIDRs:    hostStackCIDRs,
		Ingress:           ingress,
		Egress:            egress,
		StripVlan:         trunkENI,
		Vid:               int(vid),
		DefaultRoute:      alloc.GetDefaultRoute(),
		ExtraRoutes:       routes,
		DisableCreatePeer: disableCreatePeer,
	}, nil
}

func parseTearDownConf(alloc *rpc.NetConf, conf *types.CNIConf, ipType rpc.IPType) (*types.TeardownCfg, error) {
	if alloc.GetBasicInfo() == nil {
		return nil, fmt.Errorf("return empty pod alloc info: %v", alloc)
	}

	var containerIPNet *terwayTypes.IPNetSet
	var err error

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

	dp := getDatePath(ipType, conf.VlanStripType, false)
	return &types.TeardownCfg{DP: dp, ContainerIPNet: containerIPNet}, nil
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
