package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/go-logr/logr"
	"github.com/vishvananda/netlink"

	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/plugin/datapath"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/plugin/driver/vf"
	"github.com/AliyunContainerService/terway/rpc"
	terwayTypes "github.com/AliyunContainerService/terway/types"
	"github.com/containernetworking/cni/pkg/skel"
	cniTypes "github.com/containernetworking/cni/pkg/types"
)

func getCmdArgs(args *skel.CmdArgs) (*cniCmdArgs, error) {
	netNS, err := ns.GetNS(args.Netns)
	if err != nil {
		return nil, err
	}

	var conf types.CNIConf
	if err = json.Unmarshal(args.StdinData, &conf); err != nil {
		return nil, cniTypes.NewError(cniTypes.ErrDecodingFailure, "failed to parse network config", err.Error())
	}
	if conf.MTU == 0 {
		conf.MTU = defaultMTU
	}

	var k8sArgs types.K8SArgs
	if err = cniTypes.LoadArgs(args.Args, &k8sArgs); err != nil {
		return nil, cniTypes.NewError(cniTypes.ErrDecodingFailure, "failed to parse args", err.Error())
	}

	return &cniCmdArgs{
		conf:      &conf,
		netNS:     netNS,
		k8sArgs:   &k8sArgs,
		inputArgs: args,
	}, nil
}

type cniCmdArgs struct {
	conf      *types.CNIConf
	netNS     ns.NetNS
	k8sArgs   *types.K8SArgs
	inputArgs *skel.CmdArgs
}

func (args *cniCmdArgs) GetCNIConf() *types.CNIConf {
	if args == nil || args.conf == nil {
		return nil
	}
	return args.conf
}

func (args *cniCmdArgs) GetK8SConfig() *types.K8SArgs {
	if args == nil || args.k8sArgs == nil {
		return nil
	}
	return args.k8sArgs
}

func (args *cniCmdArgs) GetInputArgs() *skel.CmdArgs {
	if args == nil {
		return nil
	}
	return args.inputArgs
}

func (args *cniCmdArgs) GetNetNSPath() string {
	if args == nil || args.netNS == nil {
		return ""
	}
	return args.netNS.Path()
}

func (args *cniCmdArgs) Close() error {
	if args == nil || args.netNS == nil {
		return nil
	}
	return args.netNS.Close()
}

func isNSPathNotExist(err error) bool {
	if err == nil {
		return false
	}
	var _, ok = err.(ns.NSPathNotExistErr)
	return ok
}

func doCmdAdd(ctx context.Context, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) (containerIPNet *terwayTypes.IPNetSet, gatewayIPSet *terwayTypes.IPSet, err error) {
	var conf, cniNetns, k8sConfig, args = cmdArgs.conf, cmdArgs.netNS, cmdArgs.k8sArgs, cmdArgs.inputArgs

	log := logr.FromContextOrDiscard(ctx)

	start := time.Now()
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
				Message:         fmt.Sprintf("Alloc IP %s took %s", containerIPNet.String(), time.Since(start).String()),
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
		err = fmt.Errorf("cmdAdd: error alloc ip %w", err)
		return
	}
	if !allocResult.Success {
		err = fmt.Errorf("cmdAdd: alloc ip return not success")
		return
	}

	defer func() {
		if err != nil {
			log.Error(err, "failed to allocate IP")
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

	if !ipv4 && !ipv6 {
		err = fmt.Errorf("cmdAdd: alloc ip no valid ip type")
		return
	}

	hostIPSet, err := utils.GetHostIP(ipv4, ipv6)
	if err != nil {
		return
	}

	err = utils.EnsureHostNsConfig(ipv4, ipv6)
	if err != nil {
		err = fmt.Errorf("error setup host ns configs, %w", err)
		return
	}

	multiNetwork := len(allocResult.NetConfs) > 1

	l, err := utils.GrabFileLock(terwayCNILock)
	if err != nil {
		return
	}
	defer l.Close()

	for _, netConf := range allocResult.NetConfs {
		var setupCfg *types.SetupConfig
		setupCfg, err = parseSetupConf(ctx, args, netConf, conf, allocResult.IPType)
		if err != nil {
			err = fmt.Errorf("error parse config, %w", err)
			return
		}
		setupCfg.HostVETHName, _ = link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), netConf.IfName, defaultVethPrefix)
		setupCfg.HostIPSet = hostIPSet
		setupCfg.MultiNetwork = multiNetwork
		log.V(4).Info("setupCfg", "cfg", setupCfg)

		switch setupCfg.DP {
		case types.IPVlan:
			if conf.IPVlan() {
				available := false
				available, err = datapath.CheckIPVLanAvailable()
				if err != nil {
					return
				}
				if available {
					if setupCfg.ContainerIfName == args.IfName {
						containerIPNet = setupCfg.ContainerIPNet
						gatewayIPSet = setupCfg.GatewayIP
					}
					ctx = logr.NewContext(ctx, log.WithValues("dp", "ipvlan"))
					err = datapath.NewIPVlanDriver().Setup(ctx, setupCfg, cniNetns)
					if err != nil {
						return
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
			ctx = logr.NewContext(ctx, log.WithValues("dp", "policyRoute"))

			if setupCfg.ContainerIfName == args.IfName {
				containerIPNet = setupCfg.ContainerIPNet
				gatewayIPSet = setupCfg.GatewayIP
			}
			err = datapath.NewPolicyRoute().Setup(ctx, setupCfg, cniNetns)
			if err != nil {
				return
			}
		case types.ExclusiveENI:
			ctx = logr.NewContext(ctx, log.WithValues("dp", "exclusiveENI"))
			if setupCfg.ContainerIfName == args.IfName {
				containerIPNet = setupCfg.ContainerIPNet
				gatewayIPSet = setupCfg.GatewayIP
			}

			err = datapath.NewExclusiveENIDriver().Setup(ctx, setupCfg, cniNetns)
			if err != nil {
				return
			}
		case types.Vlan:
			ctx = logr.NewContext(ctx, log.WithValues("dp", "vlan"))

			if setupCfg.ContainerIfName == args.IfName {
				containerIPNet = setupCfg.ContainerIPNet
				gatewayIPSet = setupCfg.GatewayIP
			}
			err = datapath.NewVlan().Setup(ctx, setupCfg, cniNetns)
			if err != nil {
				err = fmt.Errorf("setup, %w", err)
				return
			}
		default:
			err = fmt.Errorf("not support this network type")
			return
		}
	}

	if containerIPNet == nil || gatewayIPSet == nil {
		err = fmt.Errorf("eth0 config is missing")
		return
	}
	return
}

func doCmdDel(ctx context.Context, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) error {
	var conf, cniNetns, k8sConfig = cmdArgs.conf, cmdArgs.netNS, cmdArgs.k8sArgs

	log := logr.FromContextOrDiscard(ctx)

	l, err := utils.GrabFileLock(terwayCNILock)
	if err != nil {
		return err
	}

	// try cleanup all resource
	err = utils.GenericTearDown(ctx, cniNetns)
	if err != nil {
		_ = l.Close()
		log.Error(err, "error teardown")
		return nil // swallow the error in case of containerd using
	}
	_ = l.Close()

	getResult, err := client.GetIPInfo(ctx, &rpc.GetInfoRequest{
		K8SPodName:             string(k8sConfig.K8S_POD_NAME),
		K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
		K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
	})
	if err != nil {
		return fmt.Errorf("error get ip from terway, pod %s/%s, %w", string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME), err)
	}
	if getResult.Error == rpc.Error_ErrCRDNotFound {
		return nil // swallow the error in case of custom resource not found
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
				log.Error(err, "error parse config")
				return nil
			}

			switch teardownCfg.DP {
			case types.IPVlan:
				if conf.IPVlan() {
					available := false
					available, err = datapath.CheckIPVLanAvailable()
					if err != nil {
						return err
					}
					if available {
						ctx = logr.NewContext(ctx, log.WithValues("dp", "ipvlan"))
						err = datapath.NewIPVlanDriver().Teardown(ctx, teardownCfg, cniNetns)
						if err != nil {
							return err
						}
						continue
					}
				}
				fallthrough
			case types.PolicyRoute:
				ctx = logr.NewContext(ctx, log.WithValues("dp", "policyRoute"))

				err = datapath.NewPolicyRoute().Teardown(ctx, teardownCfg, cniNetns)
				if err != nil {
					return err
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
	return nil
}

func doCmdCheck(ctx context.Context, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) error {
	var conf, cniNetns, k8sConfig, args = cmdArgs.conf, cmdArgs.netNS, cmdArgs.k8sArgs, cmdArgs.inputArgs
	log := logr.FromContextOrDiscard(ctx)

	getResult, err := client.GetIPInfo(ctx, &rpc.GetInfoRequest{
		K8SPodName:             string(k8sConfig.K8S_POD_NAME),
		K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
		K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
	})
	if err != nil {
		log.V(4).Error(err, "cni check filed")
		return nil
	}

	ipv4, ipv6 := getResult.IPv4, getResult.IPv6
	if !ipv4 && !ipv6 {
		return fmt.Errorf("cmdCheck: no valid ip type")
	}

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
			log = log.WithValues("dp", "ipvlan")

			if conf.IPVlan() {
				available := false
				available, err = datapath.CheckIPVLanAvailable()
				if err != nil {
					return err
				}
				if available {
					err = datapath.NewIPVlanDriver().Check(ctx, checkCfg)
					if err != nil {
						return err
					}
					continue
				}
			}
			fallthrough
		case types.PolicyRoute:
			ctx = logr.NewContext(ctx, log.WithValues("dp", "policyRoute"))
			err = datapath.NewPolicyRoute().Check(ctx, checkCfg)
			if err != nil {
				return err
			}
		case types.ExclusiveENI:
			ctx = logr.NewContext(ctx, log.WithValues("dp", "exclusiveENI"))
			err = datapath.NewExclusiveENIDriver().Check(ctx, checkCfg)
			if err != nil {
				return err
			}
		case types.Vlan:
			ctx = logr.NewContext(ctx, log.WithValues("dp", "vlan"))

			err = datapath.NewVlan().Check(ctx, checkCfg)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("not support this network type")
		}
	}
	return nil
}

func prepareVF(ctx context.Context, id int, mac string) (int32, error) {
	// vf-topo-vpc
	configPath := ""
	v := nodecap.GetNodeCapabilities(nodecap.NodeCapabilityLinJunNetwork)
	if v == string(terwayTypes.LinjunNetworkWorkENI) {
		configPath = "/var/run/hc-eni-host/vf-topo-vpc"
	}

	deviceID, err := vf.SetupDriverAndGetNetInterface(ctx, id, configPath)
	if err != nil {
		return 0, err
	}
	link, err := netlink.LinkByIndex(deviceID)
	if err != nil {
		return 0, err
	}
	logr.FromContextOrDiscard(ctx).Info("config vf", "index", deviceID, "link", link.Attrs().Name, "link_mac", link.Attrs().HardwareAddr, "expect_mac", mac)

	return int32(link.Attrs().Index), utils.EnsureLinkMAC(ctx, link, mac)
}
