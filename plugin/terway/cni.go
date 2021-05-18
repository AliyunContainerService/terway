package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/plugin/backend"
	"github.com/AliyunContainerService/terway/plugin/driver"
	"github.com/AliyunContainerService/terway/rpc"
	terwayTypes "github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/version"
	"github.com/sirupsen/logrus"

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

	// IPStack is the ip family CNI will config
	IPStack string `json:"ip_stack"`

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

	conf := NetConf{}
	if err = json.Unmarshal(args.StdinData, &conf); err != nil {
		return "", nil, nil, nil, fmt.Errorf("error parse args, %w", err)
	}

	if conf.MTU == 0 {
		conf.MTU = defaultMTU
	}

	ipv4 := false
	ipv6 := false
	switch conf.IPStack {
	case "":
		fallthrough
	case string(terwayTypes.IPStackIPv4):
		ipv4 = true
	case string(terwayTypes.IPStackDual):
		ipv4 = true
		ipv6 = true
	default:
		return "", nil, nil, nil, fmt.Errorf("unsupported ipStack %s", conf.IPStack)
	}
	veth = driver.NewVETHDriver(ipv4, ipv6)
	ipvlan = driver.NewIPVlanDriver(ipv4, ipv6)
	rawNIC = driver.NewRawNICDriver(ipv4, ipv6)

	k8sConfig := K8SArgs{}
	if err = types.LoadArgs(args.Args, &k8sConfig); err != nil {
		return "", nil, nil, nil, fmt.Errorf("error parse args, %w", err)
	}

	return confVersion, netNS, &conf, &k8sConfig, nil
}

func cmdAdd(args *skel.CmdArgs) error {
	logger := driver.Log.WithField("cmd", "add")
	confVersion, cniNetns, conf, k8sConfig, err := parseCmdArgs(args)
	if err != nil {
		return err
	}
	defer cniNetns.Close()

	if conf.Debug {
		driver.DefaultLogger.SetLevel(logrus.DebugLevel)
	}
	logger = logger.WithFields(map[string]interface{}{
		"netns":        args.Netns,
		"podName":      string(k8sConfig.K8S_POD_NAME),
		"podNamespace": string(k8sConfig.K8S_POD_NAMESPACE),
		"containerID":  string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
	})
	logger.Debugf("args: %s", driver.JSONStr(args))
	logger.Debugf("k8s %s, cni std %s", driver.JSONStr(k8sConfig), driver.JSONStr(conf))

	err = driver.EnsureHostNsConfig()
	if err != nil {
		return fmt.Errorf("error setup host ns configs, %w", err)
	}

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

	hostVETHName, _ := link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), defaultVethPrefix)
	var (
		allocatedIPAddr net.IPNet
	)

	var containerIPNet *terwayTypes.IPNetSet
	var gatewayIPSet *terwayTypes.IPSet

	switch allocResult.IPType {
	case rpc.IPType_TypeENIMultiIP:
		if allocResult.GetENIMultiIP() == nil || allocResult.GetENIMultiIP().GetENIConfig() == nil {
			return fmt.Errorf("eni multi ip return result is empty: %v", allocResult)
		}
		podIP := allocResult.GetENIMultiIP().GetENIConfig().GetPodIP()
		gatewayIP := allocResult.GetENIMultiIP().GetENIConfig().GetGatewayIP()
		eniMAC := allocResult.GetENIMultiIP().GetENIConfig().GetMAC()
		ingress := allocResult.GetENIMultiIP().GetPodConfig().GetIngress()
		egress := allocResult.GetENIMultiIP().GetPodConfig().GetEgress()
		serviceCIDR := allocResult.GetENIMultiIP().GetServiceCIDR()

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
		if allocResult.GetVPCIP() == nil || allocResult.GetVPCIP().GetPodConfig() == nil ||
			allocResult.GetVPCIP().NodeCidr == "" {
			return fmt.Errorf("vpc ip result is empty: %v", allocResult)
		}
		var subnet *net.IPNet
		_, subnet, err = net.ParseCIDR(allocResult.GetVPCIP().GetNodeCidr())
		if err != nil {
			return fmt.Errorf("vpc veth return subnet is not vaild: %v", allocResult.GetVPCIP().GetNodeCidr())
		}

		var r types.Result
		r, err = ipam.ExecAdd(delegateIpam, []byte(fmt.Sprintf(delegateConf, subnet.String())))
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
				err = ipam.ExecDel(delegateIpam, []byte(fmt.Sprintf(delegateConf, subnet.String())))
			}
		}()

		if len(ipamResult.IPs) != 1 {
			return fmt.Errorf("error get result from delegate ipam result %v: ipam result is not one ip", delegateIpam)
		}
		podIPAddr := ipamResult.IPs[0].Address
		gateway := ipamResult.IPs[0].Gateway

		ingress := allocResult.GetVPCIP().GetPodConfig().GetIngress()
		egress := allocResult.GetVPCIP().GetPodConfig().GetEgress()
		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			logger.Debug(err)
			return nil
		}
		defer l.Close()

		setupCfg := &driver.SetupConfig{
			HostVETHName:    hostVETHName,
			ContainerIfName: args.IfName,
			ContainerIPNet: &terwayTypes.IPNetSet{
				IPv4: &podIPAddr,
			},
			GatewayIP: &terwayTypes.IPSet{
				IPv4: gateway,
			},
			MTU:     conf.MTU,
			Ingress: ingress,
			Egress:  egress,
		}

		err = veth.Setup(setupCfg, cniNetns)
		if err != nil {
			return fmt.Errorf("setup network failed: %v", err)
		}
	case rpc.IPType_TypeVPCENI:
		if allocResult.GetVPCENI() == nil || allocResult.GetVPCENI().GetServiceCIDR() == nil ||
			allocResult.GetVPCENI().GetENIConfig() == nil {
			return fmt.Errorf("vpcEni ip result is empty: %v", allocResult)
		}

		serviceCIDR := allocResult.GetVPCENI().GetServiceCIDR()

		var svc *terwayTypes.IPNetSet
		svc, err = terwayTypes.ToIPNetSet(serviceCIDR)
		if err != nil {
			return err
		}

		podIP := allocResult.GetVPCENI().GetENIConfig().GetPodIP()
		gatewayIP := allocResult.GetVPCENI().GetENIConfig().GetGatewayIP()
		eniMAC := allocResult.GetVPCENI().GetENIConfig().GetMAC()

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

		// fixme
		extraRoutes := []types.Route{
			{
				Dst: *svc.IPv4,
				GW:  net.ParseIP("169.254.1.1"),
			},
		}

		for _, v := range conf.HostStackCIDRs {
			_, cidr, err := net.ParseCIDR(v)
			if err != nil {
				return fmt.Errorf("host_stack_cidrs(%s) is invaild: %v", v, err)

			}
			extraRoutes = append(extraRoutes, types.Route{
				Dst: *cidr,
				GW:  net.ParseIP("169.254.1.1"),
			})
		}

		ingress := allocResult.GetVPCENI().GetPodConfig().GetIngress()
		egress := allocResult.GetVPCENI().GetPodConfig().GetEgress()
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
			ENIIndex:        int(deviceID),
			Ingress:         ingress,
			Egress:          egress,
			ExtraRoutes:     extraRoutes,
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

		err = rawNIC.Setup(setupCfg, cniNetns)
		if err != nil {
			return fmt.Errorf("setup network for vpc eni failed: %v", err)
		}

	default:
		return fmt.Errorf("not support this network type")
	}

	result := &current.Result{}

	if containerIPNet.IPv4 != nil && gatewayIPSet.IPv4 != nil {
		result.IPs = append(result.IPs, &current.IPConfig{
			Version: "4",
			Address: *containerIPNet.IPv4,
			Gateway: gatewayIPSet.IPv4,
		})
	}
	if containerIPNet.IPv6 != nil && gatewayIPSet.IPv6 != nil {
		result.IPs = append(result.IPs, &current.IPConfig{
			Version: "6",
			Address: *containerIPNet.IPv6,
			Gateway: gatewayIPSet.IPv6,
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
			Message:         fmt.Sprintf("Alloc IP %s for Pod", allocatedIPAddr.String()),
		})

	return types.PrintResult(result, confVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	logger := driver.Log.WithField("cmd", "del")
	confVersion, cniNetns, conf, k8sConfig, err := parseCmdArgs(args)
	if err != nil {
		return err
	}
	defer cniNetns.Close()

	if conf.Debug {
		driver.DefaultLogger.SetLevel(logrus.DebugLevel)
	}
	logger = logger.WithFields(map[string]interface{}{
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

	hostVETHName, _ := link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), defaultVethPrefix)

	switch infoResult.IPType {
	case rpc.IPType_TypeENIMultiIP:
		eniMultiIPDriver := veth
		if strings.ToLower(conf.ENIIPVirtualType) == eniIPVirtualTypeIPVlan {
			available, err := driver.CheckIPVLanAvailable()
			if err != nil {
				return err
			}
			if available {
				eniMultiIPDriver = ipvlan
			}
		}
		containerIPNet, err := terwayTypes.BuildIPNet(infoResult.GetPodIP(), &rpc.IPSet{IPv4: "0.0.0.0/32", IPv6: "::/128"})
		if err != nil {
			return err
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
		var subnet *net.IPNet
		_, subnet, err = net.ParseCIDR(infoResult.GetNodeCidr())
		if err != nil {
			return fmt.Errorf("get info return subnet is not vaild: %v", infoResult.GetNodeCidr())
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

		err = ipam.ExecDel(delegateIpam, []byte(fmt.Sprintf(delegateConf, subnet.String())))
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
	logger := driver.Log.WithField("cmd", "check")
	_, cniNetns, conf, k8sConfig, err := parseCmdArgs(args)
	if err != nil {
		if _, ok := err.(ns.NSPathNotExistErr); ok {
			return nil
		}
		return err
	}
	defer cniNetns.Close()

	if conf.Debug {
		driver.DefaultLogger.SetLevel(logrus.DebugLevel)
	}
	logger = logger.WithFields(map[string]interface{}{
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
	hostVethName, _ := link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), defaultVethPrefix)

	var containerIPNet *terwayTypes.IPNetSet

	switch getResult.IPType {
	case rpc.IPType_TypeENIMultiIP:
		if getResult.GetENIMultiIP() == nil ||
			getResult.GetENIMultiIP().GetENIConfig() == nil {
			return nil
		}

		podIP := getResult.GetENIMultiIP().GetENIConfig().GetPodIP()

		containerIPNet, err = terwayTypes.BuildIPNet(podIP, &rpc.IPSet{IPv4: "0.0.0.0/32", IPv6: "::/128"})
		if err != nil {
			return err
		}

		gw, err := ip.ToIP(getResult.GetENIMultiIP().GetENIConfig().GetGatewayIP().IPv4)
		if err != nil {
			logger.Debug(err)
			return nil
		}
		eniMAC := getResult.GetENIMultiIP().GetENIConfig().GetMAC()
		deviceID, err := link.GetDeviceNumber(eniMAC)
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
			GatewayIP: &terwayTypes.IPSet{
				IPv4: gw,
			},
			ENIIndex: deviceID,
			MTU:      conf.MTU,
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
		if getResult.GetVPCENI() == nil ||
			getResult.GetVPCENI().GetServiceCIDR() != nil ||
			getResult.GetVPCENI().GetENIConfig() == nil {
			return nil
		}

		podIP := getResult.GetVPCENI().GetENIConfig().GetPodIP()
		containerIPNet, err = terwayTypes.BuildIPNet(podIP, &rpc.IPSet{IPv4: "0.0.0.0/32", IPv6: "::/128"})
		if err != nil {
			return err
		}

		eniMAC := getResult.GetVPCENI().GetENIConfig().GetMAC()
		var deviceID int32
		deviceID, err = link.GetDeviceNumber(eniMAC)
		if err != nil {
			return nil
		}

		gw, err := ip.ToIP(getResult.GetVPCENI().GetENIConfig().GetGatewayIP().IPv4)
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
			GatewayIP: &terwayTypes.IPSet{
				IPv4: gw,
			},
			ENIIndex: deviceID,
			MTU:      conf.MTU,
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
	// if test
	if os.Getenv("TERWAY_E2E") == "true" {
		return backend.NewMock(), func() {}, nil
	}
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
