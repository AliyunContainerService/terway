package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"strings"
	"time"

	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/plugin/driver"
	"github.com/AliyunContainerService/terway/plugin/version"
	"github.com/AliyunContainerService/terway/rpc"
	terwayTypes "github.com/AliyunContainerService/terway/types"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	cniversion "github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

const (
	defaultSocketPath      = "/var/run/eni/eni.socket"
	defaultVethPrefix      = "cali"
	defaultCniTimeout      = 120 * time.Second
	defaultEventTimeout    = 10 * time.Second
	defaultVethForENI      = "veth1"
	delegateIpam           = "host-local"
	eniIPVirtualTypeIPVlan = "ipvlan"
	defaultMTU             = 1500
	delegateConf           = `
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
	skel.PluginMain(cmdAdd, cmdCheck, cmdDel, version.GetSpecVersionSupported(), "")
}

// NetConf is the cni network config
type NetConf struct {
	// CNIVersion is the plugin version
	CNIVersion string `json:"cniVersion,omitempty"`

	// Name is the plugin name
	Name string `json:"name"`

	// Type is the plugin type
	Type string `json:"type"`

	// HostVethPrefix is the veth for container prefix on host
	HostVethPrefix string `json:"veth_prefix"`

	// eniIPVirtualType is the ipvlan for container
	ENIIPVirtualType string `json:"eniip_virtual_type"`

	// HostStackCIDRs is a list of CIDRs, all traffic targeting these CIDRs will be redirected to host network stack
	HostStackCIDRs []string `json:"host_stack_cidrs"`

	// MTU is container and ENI network interface MTU
	MTU int `json:"mtu"`

	// Debug
	Debug bool `json:"debug"`
}

// K8SArgs is cni args of kubernetes
type K8SArgs struct {
	types.CommonArgs
	IP                         net.IP
	K8S_POD_NAME               types.UnmarshallableString // nolint
	K8S_POD_NAMESPACE          types.UnmarshallableString // nolint
	K8S_POD_INFRA_CONTAINER_ID types.UnmarshallableString // nolint
}

var veth, rawNIC, ipvlan driver.NetnsDriver

func parseCmdArgs(args *skel.CmdArgs) (string, ns.NetNS, *NetConf, *K8SArgs, error) {
	versionDecoder := &cniversion.ConfigDecoder{}
	confVersion, err := versionDecoder.Decode(args.StdinData)
	if err != nil {
		return "", nil, nil, nil, err
	}
	netNS, err := ns.GetNS(args.Netns)
	if err != nil {
		return "", nil, nil, nil, err
	}

	driver.Hook.AddExtraInfo("ns", args.Netns)

	conf := NetConf{}
	if err = json.Unmarshal(args.StdinData, &conf); err != nil {
		return "", nil, nil, nil, fmt.Errorf("error parse args, %w", err)
	}

	if conf.MTU == 0 {
		conf.MTU = defaultMTU
	}

	k8sConfig := K8SArgs{}
	if err = types.LoadArgs(args.Args, &k8sConfig); err != nil {
		return "", nil, nil, nil, fmt.Errorf("error parse args, %w", err)
	}

	return confVersion, netNS, &conf, &k8sConfig, nil
}

func initDrivers(ipv4, ipv6 bool) {
	veth = driver.NewVETHDriver(ipv4, ipv6)
	ipvlan = driver.NewIPVlanDriver(ipv4, ipv6)
	rawNIC = driver.NewRawNICDriver(ipv4, ipv6)
}

func cmdAdd(args *skel.CmdArgs) error {
	driver.Hook.AddExtraInfo("cmd", "add")
	confVersion, cniNetns, conf, k8sConfig, err := parseCmdArgs(args)
	if err != nil {
		return err
	}
	defer cniNetns.Close()

	if conf.Debug {
		driver.SetLogDebug()
	}
	logger := driver.Log.WithFields(map[string]interface{}{
		"netns":        args.Netns,
		"podName":      string(k8sConfig.K8S_POD_NAME),
		"podNamespace": string(k8sConfig.K8S_POD_NAMESPACE),
		"containerID":  string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
	})
	logger.Debugf("args: %s", driver.JSONStr(args))
	logger.Debugf("k8s %s, cni std %s", driver.JSONStr(k8sConfig), driver.JSONStr(conf))

	terwayBackendClient, closeConn, err := getNetworkClient()
	if err != nil {
		return fmt.Errorf("error create grpc client,pod %s/%s, %w", string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME), err)
	}
	defer closeConn()

	timeoutContext, cancel := context.WithTimeout(context.Background(), defaultCniTimeout)
	defer cancel()

	defer func() {
		if err != nil {
			ctx, cancel := context.WithTimeout(context.Background(), defaultEventTimeout)
			defer cancel()
			_, _ = terwayBackendClient.RecordEvent(ctx,
				&rpc.EventRequest{
					EventTarget:     rpc.EventTarget_EventTargetPod,
					K8SPodName:      string(k8sConfig.K8S_POD_NAME),
					K8SPodNamespace: string(k8sConfig.K8S_POD_NAMESPACE),
					EventType:       rpc.EventType_EventTypeWarning,
					Reason:          "AllocIPFailed",
					Message:         err.Error(),
				})
		}
	}()

	allocResult, err := terwayBackendClient.AllocIP(
		timeoutContext,
		&rpc.AllocIPRequest{
			Netns:                  args.Netns,
			K8SPodName:             string(k8sConfig.K8S_POD_NAME),
			K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
			K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
			IfName:                 args.IfName,
		})
	if err != nil {
		return fmt.Errorf("cmdAdd: error alloc ip for pod %s, %w", driver.PodInfoKey(string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME)), err)
	}
	if !allocResult.Success {
		return fmt.Errorf("cmdAdd: error alloc ip for pod %s", driver.PodInfoKey(string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME)))
	}
	logger.Debugf("%#v", allocResult)
	defer func() {
		if err != nil {
			ctx, cancel := context.WithTimeout(context.Background(), defaultCniTimeout)
			defer cancel()
			_, _ = terwayBackendClient.ReleaseIP(ctx,
				&rpc.ReleaseIPRequest{
					K8SPodName:             string(k8sConfig.K8S_POD_NAME),
					K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
					K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
					IPType:                 allocResult.IPType,
					Reason:                 fmt.Sprintf("roll back ip for error: %v", err),
				})
		}
	}()

	ipv4, ipv6 := allocResult.IPv4, allocResult.IPv6
	initDrivers(ipv4, ipv6)
	hostIPSet, err := driver.GetHostIP(ipv4, ipv6)
	if err != nil {
		return err
	}

	err = driver.EnsureHostNsConfig(ipv4, ipv6)
	if err != nil {
		return fmt.Errorf("error setup host ns configs, %w", err)
	}

	hostVETHName, _ := link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), defaultVethPrefix)

	var containerIPNet *terwayTypes.IPNetSet
	var gatewayIPSet *terwayTypes.IPSet
	switch allocResult.IPType {
	case rpc.IPType_TypeENIMultiIP:
		if allocResult.GetBasicInfo() == nil || allocResult.GetENIInfo() == nil || allocResult.GetPod() == nil {
			return fmt.Errorf("eni multi ip return result is empty: %v", allocResult)
		}
		podIP := allocResult.GetBasicInfo().GetPodIP()
		subNet := allocResult.GetBasicInfo().GetPodCIDR()
		gatewayIP := allocResult.GetBasicInfo().GetGatewayIP()
		eniMAC := allocResult.GetENIInfo().GetMAC()
		ingress := allocResult.GetPod().GetIngress()
		egress := allocResult.GetPod().GetEgress()
		serviceCIDR := allocResult.GetBasicInfo().GetServiceCIDR()
		trunkENI := allocResult.GetENIInfo().GetTrunk()

		containerIPNet, err = terwayTypes.BuildIPNet(podIP, subNet)
		if err != nil {
			return err
		}
		gatewayIPSet, err = terwayTypes.ToIPSet(gatewayIP)
		if err != nil {
			return err
		}

		var deviceID int32
		deviceID, err = link.GetDeviceNumber(eniMAC)
		if err != nil {
			return err
		}

		var svc *terwayTypes.IPNetSet
		svc, err = terwayTypes.ToIPNetSet(serviceCIDR)
		if err != nil {
			return err
		}

		hostStackCIDRs := make([]*net.IPNet, 0)
		for _, v := range conf.HostStackCIDRs {
			_, cidr, err := net.ParseCIDR(v)
			if err != nil {
				return fmt.Errorf("host_stack_cidrs(%s) is invaild: %v", v, err)

			}
			hostStackCIDRs = append(hostStackCIDRs, cidr)
		}

		setupCfg := &driver.SetupConfig{
			HostVETHName:    hostVETHName,
			ContainerIfName: args.IfName,
			ContainerIPNet:  containerIPNet,
			GatewayIP:       gatewayIPSet,
			MTU:             conf.MTU,
			ENIIndex:        int(deviceID),
			ServiceCIDR:     svc,
			HostStackCIDRs:  hostStackCIDRs,
			Ingress:         ingress,
			Egress:          egress,
			TrunkENI:        trunkENI,
			HostIPSet:       hostIPSet,
		}
		eniMultiIPDriver := veth
		if strings.ToLower(conf.ENIIPVirtualType) == eniIPVirtualTypeIPVlan {
			available, err := driver.CheckIPVLanAvailable()
			if err != nil {
				return err
			}
			if !available {
				_, _ = terwayBackendClient.RecordEvent(timeoutContext, &rpc.EventRequest{
					EventTarget:     rpc.EventTarget_EventTargetPod,
					K8SPodName:      string(k8sConfig.K8S_POD_NAME),
					K8SPodNamespace: string(k8sConfig.K8S_POD_NAMESPACE),
					EventType:       rpc.EventType_EventTypeWarning,
					Reason:          "VirtualModeChanged",
					Message:         "IPVLan seems unavailable, use Veth instead",
				})
			} else {
				eniMultiIPDriver = ipvlan
			}
		}
		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			logger.Debug(err)
			return nil
		}
		defer l.Close()
		err = eniMultiIPDriver.Setup(setupCfg, cniNetns)
		if err != nil {
			return fmt.Errorf("setup network failed: %v", err)
		}
	case rpc.IPType_TypeVPCIP:
		if allocResult.GetBasicInfo() == nil || allocResult.GetPod() == nil {
			return fmt.Errorf("vpc ip result is empty: %v", allocResult)
		}
		ingress := allocResult.GetPod().GetIngress()
		egress := allocResult.GetPod().GetEgress()

		subnet := allocResult.GetBasicInfo().GetPodCIDR().GetIPv4()
		if subnet == "" {
			return fmt.Errorf("pod cidr not set")
		}
		var r types.Result
		r, err = ipam.ExecAdd(delegateIpam, []byte(fmt.Sprintf(delegateConf, subnet)))
		if err != nil {
			return fmt.Errorf("error allocate ip from delegate ipam %v: %v", delegateIpam, err)
		}
		var ipamResult *current.Result
		ipamResult, err = current.NewResultFromResult(r)
		if err != nil {
			return fmt.Errorf("error get result from delegate ipam result %v: %v", delegateIpam, err)
		}

		defer func() {
			if err != nil {
				err = ipam.ExecDel(delegateIpam, []byte(fmt.Sprintf(delegateConf, subnet)))
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

		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			logger.Debug(err)
			return nil
		}
		defer l.Close()

		setupCfg := &driver.SetupConfig{
			HostVETHName:    hostVETHName,
			ContainerIfName: args.IfName,
			ContainerIPNet:  containerIPNet,
			GatewayIP:       gatewayIPSet,
			MTU:             conf.MTU,
			Ingress:         ingress,
			Egress:          egress,
			HostIPSet:       hostIPSet,
		}

		err = veth.Setup(setupCfg, cniNetns)
		if err != nil {
			return fmt.Errorf("setup network failed: %v", err)
		}
	case rpc.IPType_TypeVPCENI:
		if allocResult.GetENIInfo() == nil || allocResult.GetBasicInfo() == nil ||
			allocResult.GetPod() == nil {
			return fmt.Errorf("vpcEni ip result is empty: %v", allocResult)
		}

		serviceCIDR := allocResult.GetBasicInfo().GetServiceCIDR()

		var svc *terwayTypes.IPNetSet
		svc, err = terwayTypes.ToIPNetSet(serviceCIDR)
		if err != nil {
			return err
		}

		podIP := allocResult.GetBasicInfo().GetPodIP()
		gatewayIP := allocResult.GetBasicInfo().GetGatewayIP()
		eniMAC := allocResult.GetENIInfo().GetMAC()

		containerIPNet, err = terwayTypes.BuildIPNet(podIP, &rpc.IPSet{IPv4: "0.0.0.0/32", IPv6: "::/128"})
		if err != nil {
			return err
		}
		gatewayIPSet, err = terwayTypes.ToIPSet(gatewayIP)
		if err != nil {
			return err
		}

		var deviceID int32
		deviceID, err = link.GetDeviceNumber(eniMAC)
		if err != nil {
			return err
		}

		var extraRoutes []types.Route
		if ipv4 {
			extraRoutes = append(extraRoutes, types.Route{Dst: *svc.IPv4, GW: driver.LinkIP})
		}
		if ipv6 {
			extraRoutes = append(extraRoutes, types.Route{Dst: *svc.IPv6, GW: driver.LinkIP})
		}

		for _, v := range conf.HostStackCIDRs {
			_, cidr, err := net.ParseCIDR(v)
			if err != nil {
				return fmt.Errorf("host_stack_cidrs(%s) is invaild: %v", v, err)

			}
			r := types.Route{
				Dst: *cidr,
			}

			if terwayIP.IPv6(cidr.IP) {
				r.GW = driver.LinkIPv6
			} else {
				r.GW = driver.LinkIP
			}
			extraRoutes = append(extraRoutes, r)
		}

		ingress := allocResult.GetPod().GetIngress()
		egress := allocResult.GetPod().GetEgress()
		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			logger.Debug(err)
			return nil
		}
		defer l.Close()

		setupCfg := &driver.SetupConfig{
			HostVETHName:    hostVETHName,
			ContainerIfName: defaultVethForENI,
			ContainerIPNet:  containerIPNet,
			GatewayIP:       gatewayIPSet,
			MTU:             conf.MTU,
			ENIIndex:        int(deviceID),
			Ingress:         ingress,
			Egress:          egress,
			ExtraRoutes:     extraRoutes,
			HostIPSet:       hostIPSet,
		}

		err = veth.Setup(setupCfg, cniNetns)
		if err != nil {
			return fmt.Errorf("setup veth network for eni failed: %v", err)
		}

		defer func() {
			if err != nil {
				if e := veth.Teardown(&driver.TeardownCfg{
					HostVETHName:    hostVETHName,
					ContainerIfName: args.IfName,
				}, cniNetns); e != nil {
					err = errors.Wrapf(err, "tear down veth network for eni failed: %v", e)
				}
			}
		}()
		setupCfg.ContainerIfName = args.IfName
		err = rawNIC.Setup(setupCfg, cniNetns)
		if err != nil {
			return fmt.Errorf("setup network for vpc eni failed: %v", err)
		}

	default:
		return fmt.Errorf("not support this network type")
	}

	index0 := 0
	result := &current.Result{}
	result.Interfaces = append(result.Interfaces, &current.Interface{
		Name: args.IfName,
	})
	if containerIPNet.IPv4 != nil && gatewayIPSet.IPv4 != nil {
		result.IPs = append(result.IPs, &current.IPConfig{
			Version:   "4",
			Address:   *containerIPNet.IPv4,
			Gateway:   gatewayIPSet.IPv4,
			Interface: &index0,
		})
	}
	if containerIPNet.IPv6 != nil && gatewayIPSet.IPv6 != nil {
		result.IPs = append(result.IPs, &current.IPConfig{
			Version:   "6",
			Address:   *containerIPNet.IPv6,
			Gateway:   gatewayIPSet.IPv6,
			Interface: &index0,
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultEventTimeout)
	defer cancel()
	_, _ = terwayBackendClient.RecordEvent(ctx,
		&rpc.EventRequest{
			EventTarget:     rpc.EventTarget_EventTargetPod,
			K8SPodName:      string(k8sConfig.K8S_POD_NAME),
			K8SPodNamespace: string(k8sConfig.K8S_POD_NAMESPACE),
			EventType:       rpc.EventType_EventTypeNormal,
			Reason:          "AllocIPSucceed",
			Message:         fmt.Sprintf("Alloc IP %s for Pod", containerIPNet.String()),
		})

	return types.PrintResult(result, confVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	driver.Hook.AddExtraInfo("cmd", "del")
	confVersion, cniNetns, conf, k8sConfig, err := parseCmdArgs(args)
	if err != nil {
		return err
	}
	defer cniNetns.Close()

	if conf.Debug {
		driver.SetLogDebug()
	}
	logger := driver.Log.WithFields(map[string]interface{}{
		"netns":        args.Netns,
		"podName":      string(k8sConfig.K8S_POD_NAME),
		"podNamespace": string(k8sConfig.K8S_POD_NAMESPACE),
		"containerID":  string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
	})
	logger.Debugf("args: %s", driver.JSONStr(args))
	logger.Debugf("ns %s , k8s %s, cni std %s", cniNetns.Path(), driver.JSONStr(k8sConfig), driver.JSONStr(conf))

	terwayBackendClient, closeConn, err := getNetworkClient()
	if err != nil {
		return fmt.Errorf("error create grpc client, pod %s/%s, %w", string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME), err)
	}
	defer closeConn()

	timeoutContext, cancel := context.WithTimeout(context.Background(), defaultCniTimeout)
	defer cancel()

	infoResult, err := terwayBackendClient.GetIPInfo(
		timeoutContext,
		&rpc.GetInfoRequest{
			K8SPodName:             string(k8sConfig.K8S_POD_NAME),
			K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
			K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
		})

	if err != nil {
		return fmt.Errorf("error get ip from terway, pod %s/%s, %w", string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME), err)
	}

	ipv4, ipv6 := infoResult.IPv4, infoResult.IPv6
	initDrivers(ipv4, ipv6)

	hostVETHName, _ := link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), defaultVethPrefix)

	switch infoResult.IPType {
	case rpc.IPType_TypeENIMultiIP:
		eniMultiIPDriver := veth
		containerIPNet, err := terwayTypes.BuildIPNet(infoResult.GetBasicInfo().GetPodIP(), &rpc.IPSet{IPv4: "0.0.0.0/32", IPv6: "::/128"})
		if err != nil {
			return err
		}
		if strings.ToLower(conf.ENIIPVirtualType) == eniIPVirtualTypeIPVlan {
			available, err := driver.CheckIPVLanAvailable()
			if err != nil {
				return err
			}
			if available {
				eniMultiIPDriver = ipvlan
				if containerIPNet == nil {
					return fmt.Errorf("pod ip is required for ipvlan %#v", infoResult)
				}
			}
		}

		var l *driver.Locker
		l, err = driver.GrabFileLock(terwayCNILock)
		if err != nil {
			logger.Debug(err)
			return nil
		}
		defer l.Close()
		err = eniMultiIPDriver.Teardown(&driver.TeardownCfg{
			HostVETHName:    hostVETHName,
			ContainerIfName: args.IfName,
			ContainerIPNet:  containerIPNet,
		}, cniNetns)
		if err != nil {
			return fmt.Errorf("error teardown pod %s/%s, %w", string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME), err)
		}

	case rpc.IPType_TypeVPCIP:
		subnet := infoResult.GetBasicInfo().GetPodCIDR().GetIPv4()
		if subnet == "" {
			return fmt.Errorf("error get pod cidr")
		}
		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			logger.Debug(err)
			return nil
		}
		defer l.Close()
		err = veth.Teardown(&driver.TeardownCfg{
			HostVETHName:    hostVETHName,
			ContainerIfName: args.IfName,
		}, cniNetns)
		if err != nil {
			return fmt.Errorf("error teardown pod %s/%s, %w", string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME), err)
		}

		err = ipam.ExecDel(delegateIpam, []byte(fmt.Sprintf(delegateConf, subnet)))
		if err != nil {
			return errors.Wrapf(err, "error teardown network ipam for pod: %s-%s",
				string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME))
		}

	case rpc.IPType_TypeVPCENI:
		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			logger.Debug(err)
			return nil
		}
		defer l.Close()
		_ = veth.Teardown(&driver.TeardownCfg{
			HostVETHName:    hostVETHName,
			ContainerIfName: defaultVethForENI,
		}, cniNetns)
		// ignore ENI veth release error
		//if err != nil {
		//	// ignore ENI veth release error
		//}
		err = rawNIC.Teardown(&driver.TeardownCfg{
			HostVETHName:    hostVETHName,
			ContainerIfName: args.IfName,
		}, cniNetns)
		if err != nil {
			return fmt.Errorf("error teardown pod %s/%s, %w", string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME), err)
		}

	default:
		return fmt.Errorf("not support this network type")
	}

	reply, err := terwayBackendClient.ReleaseIP(
		context.Background(),
		&rpc.ReleaseIPRequest{
			K8SPodName:             string(k8sConfig.K8S_POD_NAME),
			K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
			K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
			IPType:                 infoResult.GetIPType(),
			Reason:                 "normal release",
		})

	if err != nil || !reply.GetSuccess() {
		return fmt.Errorf("error release ip for pod, maybe cause resource leak: %v, %v", err, reply)
	}

	result := &current.Result{
		CNIVersion: confVersion,
	}

	return types.PrintResult(result, confVersion)
}

func cmdCheck(args *skel.CmdArgs) error {
	driver.Hook.AddExtraInfo("cmd", "check")
	_, cniNetns, conf, k8sConfig, err := parseCmdArgs(args)
	if err != nil {
		if _, ok := err.(ns.NSPathNotExistErr); ok {
			return nil
		}
		return err
	}
	defer cniNetns.Close()

	if conf.Debug {
		driver.SetLogDebug()
	}
	logger := driver.Log.WithFields(map[string]interface{}{
		"netns":        args.Netns,
		"podName":      string(k8sConfig.K8S_POD_NAME),
		"podNamespace": string(k8sConfig.K8S_POD_NAMESPACE),
		"containerID":  string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
	})
	logger.Debugf("args: %s", driver.JSONStr(args))
	logger.Debugf("ns %s , k8s %s, cni std %s", cniNetns.Path(), driver.JSONStr(k8sConfig), driver.JSONStr(conf))

	terwayBackendClient, closeConn, err := getNetworkClient()
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("add cmd: create grpc client, pod: %s-%s",
			string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME),
		))
	}
	defer closeConn()

	timeoutContext, cancel := context.WithTimeout(context.Background(), defaultCniTimeout)
	defer cancel()
	getResult, err := terwayBackendClient.GetIPInfo(
		timeoutContext,
		&rpc.GetInfoRequest{
			K8SPodName:             string(k8sConfig.K8S_POD_NAME),
			K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
			K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
		})
	if err != nil {
		logger.Debug(err)
		return nil
	}

	ipv4, ipv6 := getResult.IPv4, getResult.IPv6
	initDrivers(ipv4, ipv6)
	hostIPSet, err := driver.GetHostIP(ipv4, ipv6)
	if err != nil {
		return err
	}
	err = driver.EnsureHostNsConfig(ipv4, ipv6)
	if err != nil {
		return fmt.Errorf("error setup host ns configs, %w", err)
	}

	hostVethName, _ := link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), defaultVethPrefix)

	var containerIPNet *terwayTypes.IPNetSet

	switch getResult.IPType {
	case rpc.IPType_TypeENIMultiIP:
		if getResult.GetBasicInfo() == nil ||
			getResult.GetENIInfo() == nil {
			return nil
		}

		podIP := getResult.GetBasicInfo().GetPodIP()
		subNet := getResult.GetBasicInfo().GetPodCIDR()
		gw := getResult.GetBasicInfo().GetGatewayIP()
		mac := getResult.GetENIInfo().GetMAC()

		containerIPNet, err = terwayTypes.BuildIPNet(podIP, subNet)
		if err != nil {
			return err
		}

		gwIPSet, err := terwayTypes.ToIPSet(gw)
		if err != nil {
			return err
		}

		deviceID, err := link.GetDeviceNumber(mac)
		if err != nil {
			return err
		}

		cfg := driver.CheckConfig{
			RecordPodEvent: func(msg string) {
				ctx, cancel := context.WithTimeout(context.Background(), defaultEventTimeout)
				defer cancel()
				_, _ = terwayBackendClient.RecordEvent(ctx,
					&rpc.EventRequest{
						EventTarget:     rpc.EventTarget_EventTargetPod,
						K8SPodName:      string(k8sConfig.K8S_POD_NAME),
						K8SPodNamespace: string(k8sConfig.K8S_POD_NAMESPACE),
						EventType:       rpc.EventType_EventTypeWarning,
						Reason:          "ConfigCheck",
						Message:         msg,
					})
			},
			NetNS:           cniNetns,
			ContainerIFName: args.IfName,
			HostVETHName:    hostVethName,
			ContainerIPNet:  containerIPNet,
			GatewayIP:       gwIPSet,
			ENIIndex:        deviceID,
			MTU:             conf.MTU,
			HostIPSet:       hostIPSet,
		}
		eniMultiIPDriver := veth

		if strings.ToLower(conf.ENIIPVirtualType) == eniIPVirtualTypeIPVlan {
			ok, err := driver.CheckIPVLanAvailable()
			if err != nil {
				logger.Debug(err)
				return nil
			}
			if ok {
				eniMultiIPDriver = ipvlan
			}
		}
		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			logger.Debug(err)
			return nil
		}
		defer l.Close()
		err = eniMultiIPDriver.Check(&cfg)
		if err != nil {
			logger.Debug(err)
			return nil
		}
	case rpc.IPType_TypeVPCIP:
		return nil
	case rpc.IPType_TypeVPCENI:
		if getResult.GetPod() == nil ||
			getResult.GetENIInfo() != nil ||
			getResult.GetBasicInfo() == nil {
			return nil
		}

		podIP := getResult.GetBasicInfo().GetPodIP()
		containerIPNet, err = terwayTypes.BuildIPNet(podIP, &rpc.IPSet{IPv4: "0.0.0.0/32", IPv6: "::/128"})
		if err != nil {
			return err
		}
		gw := getResult.GetBasicInfo().GetGatewayIP()
		gwIPSet, err := terwayTypes.ToIPSet(gw)
		if err != nil {
			return err
		}
		eniMAC := getResult.GetENIInfo().GetMAC()
		var deviceID int32
		deviceID, err = link.GetDeviceNumber(eniMAC)
		if err != nil {
			return nil
		}

		cfg := &driver.CheckConfig{
			RecordPodEvent: func(msg string) {
				ctx, cancel := context.WithTimeout(context.Background(), defaultEventTimeout)
				defer cancel()
				_, _ = terwayBackendClient.RecordEvent(ctx,
					&rpc.EventRequest{
						EventTarget:     rpc.EventTarget_EventTargetPod,
						K8SPodName:      string(k8sConfig.K8S_POD_NAME),
						K8SPodNamespace: string(k8sConfig.K8S_POD_NAMESPACE),
						EventType:       rpc.EventType_EventTypeWarning,
						Reason:          "ConfigCheck",
						Message:         msg,
					})
			},
			NetNS:           cniNetns,
			ContainerIFName: args.IfName,
			ContainerIPNet:  containerIPNet,
			GatewayIP:       gwIPSet,
			ENIIndex:        deviceID,
			MTU:             conf.MTU,
			HostIPSet:       hostIPSet,
		}
		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			logger.Debug(err)
			return nil
		}
		defer l.Close()
		err = rawNIC.Check(cfg)
		if err != nil {
			logger.Debug(err)
			return nil
		}
	default:
		return nil
	}
	return nil
}

func getNetworkClient() (rpc.TerwayBackendClient, func(), error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), defaultCniTimeout)
	grpcConn, err := grpc.DialContext(timeoutCtx, defaultSocketPath, grpc.WithInsecure(), grpc.WithContextDialer(
		func(ctx context.Context, s string) (net.Conn, error) {
			unixAddr, err := net.ResolveUnixAddr("unix", defaultSocketPath)
			if err != nil {
				return nil, fmt.Errorf("error while resolve unix addr:%w", err)
			}
			d := net.Dialer{}
			return d.DialContext(timeoutCtx, "unix", unixAddr.String())
		}))
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("error dial to terway %s, %w", defaultSocketPath, err)
	}

	terwayBackendClient := rpc.NewTerwayBackendClient(grpcConn)
	return terwayBackendClient, func() {
		grpcConn.Close()
		cancel()
	}, nil
}
