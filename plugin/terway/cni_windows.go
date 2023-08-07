package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/containernetworking/cni/pkg/skel"
	cniTypes "github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/pkg/windows/ip"
	"github.com/AliyunContainerService/terway/plugin/datapath"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/rpc"
	terwayTypes "github.com/AliyunContainerService/terway/types"
)

func getCmdArgs(args *skel.CmdArgs) (*cniCmdArgs, error) {
	var err error

	var conf types.CNIConf
	if err = json.Unmarshal(args.StdinData, &conf); err != nil {
		return nil, fmt.Errorf("error parse args, %w", err)
	}
	if conf.MTU == 0 {
		conf.MTU = defaultMTU
	}

	var k8sArgs types.K8SArgs
	if err = cniTypes.LoadArgs(args.Args, &k8sArgs); err != nil {
		return nil, fmt.Errorf("error parse args, %w", err)
	}

	return &cniCmdArgs{
		conf:      &conf,
		k8sArgs:   &k8sArgs,
		inputArgs: args,
	}, nil
}

type cniCmdArgs struct {
	conf      *types.CNIConf
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
	return ""
}

func (args *cniCmdArgs) Close() error {
	return nil
}

func isNSPathNotExist(err error) bool {
	return false
}

var errHostNetworkNotSupport = errors.New("host network is not support")

func doCmdAdd(ctx context.Context, logger *logrus.Entry, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) (containerIPNet *terwayTypes.IPNetSet, gatewayIPSet *terwayTypes.IPSet, err error) {
	var conf, k8sConfig, args = cmdArgs.conf, cmdArgs.k8sArgs, cmdArgs.inputArgs

	defer func() {
		eventCtx, cancel := context.WithTimeout(ctx, defaultEventTimeout)
		defer cancel()
		if err != nil {
			if err != errHostNetworkNotSupport ||
				(containerIPNet == nil || gatewayIPSet == nil) {
				_, _ = client.RecordEvent(eventCtx, &rpc.EventRequest{
					EventTarget:     rpc.EventTarget_EventTargetPod,
					K8SPodName:      string(k8sConfig.K8S_POD_NAME),
					K8SPodNamespace: string(k8sConfig.K8S_POD_NAMESPACE),
					EventType:       rpc.EventType_EventTypeWarning,
					Reason:          "AllocIPFailed",
					Message:         err.Error(),
				})
				return
			}
		}
		var message = fmt.Sprintf("Alloc IP %s", containerIPNet.String())
		if err != nil {
			message = ""
			err = nil
		}
		_, _ = client.RecordEvent(eventCtx, &rpc.EventRequest{
			EventTarget:     rpc.EventTarget_EventTargetPod,
			K8SPodName:      string(k8sConfig.K8S_POD_NAME),
			K8SPodNamespace: string(k8sConfig.K8S_POD_NAMESPACE),
			EventType:       rpc.EventType_EventTypeNormal,
			Reason:          "AllocIPSucceed",
			Message:         message,
		})
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
			if strings.Contains(err.Error(), "failed to get shared HNSEndpoint") {
				// NB(thxCode): only happen in scheduling host network workload after cni ready,
				// just swallow this error and let it goaway.
				err = errHostNetworkNotSupport
			}
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

	multiNetwork := len(allocResult.NetConfs) > 0

	for _, netConf := range allocResult.NetConfs {
		var setupCfg *types.SetupConfig
		setupCfg, err = parseSetupConf(args, netConf, conf, allocResult.IPType)
		if err != nil {
			err = fmt.Errorf("error parse config, %w", err)
			return
		}
		setupCfg.HostVETHName, _ = link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), netConf.IfName, defaultVethPrefix)
		setupCfg.MultiNetwork = multiNetwork
		logger.Debugf("setupCfg %#v", setupCfg)

		switch setupCfg.DP {
		case types.VPCRoute:
			utils.Hook.AddExtraInfo("dp", "vpcRoute")

			var nwSubnet = ip.FromIPNet(setupCfg.ContainerIPNet.IPv4).Network().ToIPNet()
			var r cniTypes.Result
			r, err = ipam.ExecAdd(delegateIpam, []byte(fmt.Sprintf(delegateConf, nwSubnet)))
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

			err = func() (berr error) {
				defer func() {
					if berr != nil {
						_ = ipam.ExecDel(delegateIpam, []byte(fmt.Sprintf(delegateConf, nwSubnet)))
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

				return datapath.NewVPCRoute().Setup(ctx, setupCfg, args.ContainerID, args.Netns)
			}()
			if err != nil {
				return
			}
		case types.IPVlan, types.PolicyRoute:
			// NB(thxCode): there is not ipvlan mode in windows,
			// so a fallback way is to use policy route mode.
			utils.Hook.AddExtraInfo("dp", "policyRoute")

			if setupCfg.ContainerIfName == args.IfName {
				containerIPNet = setupCfg.ContainerIPNet
				gatewayIPSet = setupCfg.GatewayIP
			}

			// NB(thxCode): create a fake network to allow service connection
			var assistantNwSubnet = ip.FromIPNet(setupCfg.ServiceCIDR.IPv4).Next().ToIPNet()
			var r cniTypes.Result
			r, err = ipam.ExecAdd(delegateIpam, []byte(fmt.Sprintf(delegateConf, assistantNwSubnet)))
			if err != nil {
				err = fmt.Errorf("error allocate assistant ip from delegate ipam %v: %v", delegateIpam, err)
				return
			}
			var ipamResult *current.Result
			ipamResult, err = current.NewResultFromResult(r)
			if err != nil {
				err = fmt.Errorf("error get result from delegate ipam result %v: %v", delegateIpam, err)
				return
			}

			err = func() (berr error) {
				defer func() {
					if berr != nil {
						_ = ipam.ExecDel(delegateIpam, []byte(fmt.Sprintf(delegateConf, assistantNwSubnet)))
					}
				}()
				if len(ipamResult.IPs) != 1 {
					return fmt.Errorf("error get result from delegate ipam result %v: ipam result is not one ip", delegateIpam)
				}
				podIPAddr := ipamResult.IPs[0].Address
				gateway := ipamResult.IPs[0].Gateway

				setupCfg.AssistantContainerIPNet = &terwayTypes.IPNetSet{
					IPv4: &podIPAddr,
				}
				setupCfg.AssistantGatewayIP = &terwayTypes.IPSet{
					IPv4: gateway,
				}

				return datapath.NewPolicyRoute().Setup(ctx, setupCfg, args.ContainerID, args.Netns)
			}()
			if err != nil {
				return
			}
		case types.ExclusiveENI:
			utils.Hook.AddExtraInfo("dp", "exclusiveENI")

			if setupCfg.ContainerIfName == args.IfName {
				containerIPNet = setupCfg.ContainerIPNet
				gatewayIPSet = setupCfg.GatewayIP
			}

			// NB(thxCode): create a fake network to allow service connection
			var assistantNwSubnet = ip.FromIPNet(setupCfg.ServiceCIDR.IPv4).Next().ToIPNet()
			var r cniTypes.Result
			r, err = ipam.ExecAdd(delegateIpam, []byte(fmt.Sprintf(delegateConf, assistantNwSubnet)))
			if err != nil {
				err = fmt.Errorf("error allocate assistant ip from delegate ipam %v: %v", delegateIpam, err)
				return
			}
			var ipamResult *current.Result
			ipamResult, err = current.NewResultFromResult(r)
			if err != nil {
				err = fmt.Errorf("error get result from delegate ipam result %v: %v", delegateIpam, err)
				return
			}

			err = func() (berr error) {
				defer func() {
					if berr != nil {
						_ = ipam.ExecDel(delegateIpam, []byte(fmt.Sprintf(delegateConf, assistantNwSubnet)))
					}
				}()
				if len(ipamResult.IPs) != 1 {
					return fmt.Errorf("error get result from delegate ipam result %v: ipam result is not one ip", delegateIpam)
				}
				podIPAddr := ipamResult.IPs[0].Address
				gateway := ipamResult.IPs[0].Gateway

				setupCfg.AssistantContainerIPNet = &terwayTypes.IPNetSet{
					IPv4: &podIPAddr,
				}
				setupCfg.AssistantGatewayIP = &terwayTypes.IPSet{
					IPv4: gateway,
				}

				return datapath.NewExclusiveENIDriver().Setup(ctx, setupCfg, args.ContainerID, args.Netns)
			}()
			if err != nil {
				return
			}
		case types.Vlan:
			err = fmt.Errorf("not implement in vlan datapath")
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
	var conf, k8sConfig, args = cmdArgs.conf, cmdArgs.k8sArgs, cmdArgs.inputArgs

	infoResult, err := client.GetIPInfo(ctx, &rpc.GetInfoRequest{
		K8SPodName:             string(k8sConfig.K8S_POD_NAME),
		K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
		K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
	})
	if err != nil {
		if strings.Contains(err.Error(), "default interface is not set") {
			logger.WithError(err).Warn("ignore deleting as default interface is not set")
			return nil // swallow the error if daemon cannot find the network
		} else if strings.Contains(err.Error(), "not found") {
			logger.WithError(err).Warn("ignore deleting as not found")
			return nil // swallow the error if pod is not found
		}
		return fmt.Errorf("error get ip from terway, pod %s/%s, %w", string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME), err)
	}
	if infoResult.Error == rpc.Error_ErrCRDNotFound {
		return nil // swallow the error in case of custom resource not found
	}

	for _, netConf := range infoResult.NetConfs {
		var teardownCfg *types.TeardownCfg
		teardownCfg, err = parseTearDownConf(netConf, conf, infoResult.IPType)
		if err != nil {
			logger.Errorf("error parse config: %v", err)
			return nil
		}
		teardownCfg.ContainerIfName = netConf.IfName
		teardownCfg.HostVETHName, _ = link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), netConf.IfName, defaultVethPrefix)

		switch teardownCfg.DP {
		case types.VPCRoute:
			utils.Hook.AddExtraInfo("dp", "vpcRoute")

			err = datapath.NewVPCRoute().Teardown(ctx, teardownCfg, args.ContainerID, args.Netns)
			if err != nil {
				return fmt.Errorf("error teardown pod %s/%s, %w", string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME), err)
			}

			var nwSubnet = ip.FromIPNet(teardownCfg.ContainerIPNet.IPv4).Network().ToIPNet()
			err = ipam.ExecDel(delegateIpam, []byte(fmt.Sprintf(delegateConf, nwSubnet)))
			if err != nil {
				return fmt.Errorf("teardown network ipam for pod: %s-%s, %w", string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME), err)
			}
		case types.IPVlan, types.PolicyRoute:
			// NB(thxCode): there is not ipvlan mode in windows,
			// so a fallback way is to use policy route mode.
			utils.Hook.AddExtraInfo("dp", "policyRoute")

			err = datapath.NewPolicyRoute().Teardown(ctx, teardownCfg, args.ContainerID, args.Netns)
			if err != nil {
				return err
			}

			var assistantNwSubnet = ip.FromIPNet(teardownCfg.ServiceCIDR.IPv4).Next().ToIPNet()
			err = ipam.ExecDel(delegateIpam, []byte(fmt.Sprintf(delegateConf, assistantNwSubnet)))
			if err != nil {
				return fmt.Errorf("teardown assistant network ipam for pod: %s-%s, %w", string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME), err)
			}
		case types.ExclusiveENI:
			utils.Hook.AddExtraInfo("dp", "exclusiveENI")

			err = datapath.NewExclusiveENIDriver().Teardown(ctx, teardownCfg, args.ContainerID, args.Netns)
			if err != nil {
				return err
			}

			var assistantNwSubnet = ip.FromIPNet(teardownCfg.ServiceCIDR.IPv4).Next().ToIPNet()
			err = ipam.ExecDel(delegateIpam, []byte(fmt.Sprintf(delegateConf, assistantNwSubnet)))
			if err != nil {
				return fmt.Errorf("teardown assistant network ipam for pod: %s-%s, %w", string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME), err)
			}
		case types.Vlan:
			return fmt.Errorf("not implement in vlan datapath")
		default:
			return fmt.Errorf("not support this network type")
		}
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
	var conf, k8sConfig, args = cmdArgs.conf, cmdArgs.k8sArgs, cmdArgs.inputArgs

	getResult, err := client.GetIPInfo(ctx, &rpc.GetInfoRequest{
		K8SPodName:             string(k8sConfig.K8S_POD_NAME),
		K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
		K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
	})
	if err != nil {
		logger.WithError(err).Warn("ignore checking")
		return nil // swallow the error
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
		checkCfg.HostVETHName, _ = link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), netConf.IfName, defaultVethPrefix)
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
		case types.VPCRoute:
			utils.Hook.AddExtraInfo("dp", "vpcRoute")

			err = datapath.NewVPCRoute().Check(ctx, checkCfg, args.ContainerID, args.Netns)
			if err != nil {
				return err
			}
		case types.IPVlan, types.PolicyRoute:
			// NB(thxCode): there is not ipvlan mode in windows,
			// so a fallback way is to use policy route mode.
			utils.Hook.AddExtraInfo("dp", "policyRoute")

			err = datapath.NewPolicyRoute().Check(ctx, checkCfg, args.ContainerID, args.Netns)
			if err != nil {
				return err
			}
		case types.ExclusiveENI:
			utils.Hook.AddExtraInfo("dp", "exclusiveENI")

			err = datapath.NewExclusiveENIDriver().Check(ctx, checkCfg, args.ContainerID, args.Netns)
			if err != nil {
				return err
			}
		case types.Vlan:
			return fmt.Errorf("not implement in vlan datapath")
		default:
			return fmt.Errorf("not support this network type")
		}
	}
	return nil
}
