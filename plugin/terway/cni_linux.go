package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/containernetworking/cni/pkg/skel"
	cniTypes "github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/sirupsen/logrus"

	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/plugin/datapath"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/rpc"
	terwayTypes "github.com/AliyunContainerService/terway/types"
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

func doCmdAdd(ctx context.Context, logger *logrus.Entry, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) (containerIPNet *terwayTypes.IPNetSet, gatewayIPSet *terwayTypes.IPSet, err error) {
	var conf, cniNetns, k8sConfig, args = cmdArgs.conf, cmdArgs.netNS, cmdArgs.k8sArgs, cmdArgs.inputArgs

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
		setupCfg, err = parseSetupConf(args, netConf, conf, allocResult.IPType)
		if err != nil {
			err = fmt.Errorf("error parse config, %w", err)
			return
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
				err = fmt.Errorf("error allocate ip from delegate ipam %v: %v", delegateIpam, err)
				return
			}
			var ipamResult *current.Result
			ipamResult, err = current.NewResultFromResult(r)
			if err != nil {
				err = fmt.Errorf("error get result from delegate ipam result %v: %v", delegateIpam, err)
				return
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
				return
			}
		case types.IPVlan:
			utils.Hook.AddExtraInfo("dp", "ipvlan")

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
					err = datapath.NewIPVlanDriver().Setup(setupCfg, cniNetns)
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
			utils.Hook.AddExtraInfo("dp", "policyRoute")

			if setupCfg.ContainerIfName == args.IfName {
				containerIPNet = setupCfg.ContainerIPNet
				gatewayIPSet = setupCfg.GatewayIP
			}
			err = datapath.NewPolicyRoute().Setup(setupCfg, cniNetns)
			if err != nil {
				return
			}
		case types.ExclusiveENI:
			utils.Hook.AddExtraInfo("dp", "exclusiveENI")

			if setupCfg.ContainerIfName == args.IfName {
				containerIPNet = setupCfg.ContainerIPNet
				gatewayIPSet = setupCfg.GatewayIP
			}

			err = datapath.NewExclusiveENIDriver().Setup(setupCfg, cniNetns)
			if err != nil {
				return
			}
		case types.Vlan:
			utils.Hook.AddExtraInfo("dp", "vlan")

			if setupCfg.ContainerIfName == args.IfName {
				containerIPNet = setupCfg.ContainerIPNet
				gatewayIPSet = setupCfg.GatewayIP
			}
			err = datapath.NewVlan().Setup(setupCfg, cniNetns)
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

func doCmdDel(ctx context.Context, logger *logrus.Entry, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) error {
	var conf, cniNetns, k8sConfig = cmdArgs.conf, cmdArgs.netNS, cmdArgs.k8sArgs

	l, err := utils.GrabFileLock(terwayCNILock)
	if err != nil {
		return err
	}

	// try cleanup all resource
	err = utils.GenericTearDown(cniNetns)
	if err != nil {
		_ = l.Close()
		logger.Errorf("error teardown %s", err.Error())
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
						continue
					}
				}
				fallthrough
			case types.PolicyRoute:
				utils.Hook.AddExtraInfo("dp", "policyRoute")
				err = datapath.NewPolicyRoute().Teardown(teardownCfg, cniNetns)
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

func doCmdCheck(ctx context.Context, logger *logrus.Entry, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) error {
	var conf, cniNetns, k8sConfig, args = cmdArgs.conf, cmdArgs.netNS, cmdArgs.k8sArgs, cmdArgs.inputArgs

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
