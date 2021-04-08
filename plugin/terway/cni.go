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

	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/plugin/driver"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/AliyunContainerService/terway/version"

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

	terwayCNILock         = "/var/run/eni/terway_cni.lock"
	terwayCNIDebugLogPath = "/tmp/terway_debug.log"
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

var vethDriver = driver.VethDriver
var eniMultiIPDriver = driver.VethDriver
var nicDriver = driver.NicDriver

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
		return "", nil, nil, nil, errors.Wrap(err, "error loading config from args")
	}

	if conf.MTU == 0 {
		conf.MTU = defaultMTU
	}

	k8sConfig := K8SArgs{}
	if err = types.LoadArgs(args.Args, &k8sConfig); err != nil {
		return "", nil, nil, nil, errors.Wrap(err, "error loading config from args")
	}

	return confVersion, netNS, &conf, &k8sConfig, nil
}

func cmdAdd(args *skel.CmdArgs) error {
	confVersion, cniNetns, conf, k8sConfig, err := parseCmdArgs(args)
	if err != nil {
		return err
	}
	defer cniNetns.Close()

	if conf.Debug {
		fd, err := os.OpenFile(terwayCNIDebugLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err == nil {
			defer fd.Close()
			driver.Log.SetDebug(true, fd)
		}
	}
	driver.Log.Debugf("args: %s", driver.JSONStr(args))
	driver.Log.Debugf("cmdAdd: ns %s , k8s %s, cni std %s", cniNetns.Path(), driver.JSONStr(k8sConfig), driver.JSONStr(conf))

	err = driver.EnsureHostNsConfig()
	if err != nil {
		return errors.Wrapf(err, "add cmd: failed setup host namespace configs")
	}

	terwayBackendClient, closeConn, err := getNetworkClient()
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("add cmd: create grpc client, pod: %s-%s",
			string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME),
		))
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
		return errors.Wrap(err, fmt.Sprintf("add cmd: error alloc ip from grpc call, pod: %s-%s",
			string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME),
		))
	}

	if !allocResult.Success {
		return fmt.Errorf("error on alloc eip from terway backend")
	}

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

	hostVethName, _ := link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), defaultVethPrefix)
	var (
		allocatedIPAddr      net.IPNet
		allocatedGatewayAddr net.IP
	)

	switch allocResult.IPType {
	case rpc.IPType_TypeENIMultiIP:
		if allocResult.GetENIMultiIP() == nil || allocResult.GetENIMultiIP().GetEniConfig() == nil {
			return fmt.Errorf("eni multi ip return result is empty: %v", allocResult)
		}
		ipAddrStr := allocResult.GetENIMultiIP().GetEniConfig().GetIPv4Addr()
		subnetStr := allocResult.GetENIMultiIP().GetEniConfig().GetIPv4Subnet()
		gatewayStr := allocResult.GetENIMultiIP().GetEniConfig().GetGateway()
		primaryIPStr := allocResult.GetENIMultiIP().GetEniConfig().GetPrimaryIPv4Addr()
		deviceID := allocResult.GetENIMultiIP().GetEniConfig().GetDeviceNumber()
		ingress := allocResult.GetENIMultiIP().GetPodConfig().GetIngress()
		egress := allocResult.GetENIMultiIP().GetPodConfig().GetEgress()
		serviceCIDRStr := allocResult.GetENIMultiIP().GetServiceCidr()

		var subnet *net.IPNet
		_, subnet, err = net.ParseCIDR(subnetStr)
		if err != nil {
			return fmt.Errorf("eni multi ip return subnet is not vaild: %v", subnetStr)
		}
		ip := net.ParseIP(ipAddrStr)
		if ip == nil {
			return fmt.Errorf("eni multi ip return ip is not vaild: %v", ipAddrStr)
		}
		subnet.IP = ip

		gw := net.ParseIP(gatewayStr)
		if gw == nil {
			return fmt.Errorf("eni multi ip return gateway is not vaild: %v", gatewayStr)
		}

		primaryIP := net.ParseIP(primaryIPStr)
		if primaryIP == nil {
			return fmt.Errorf("eni multi ip return primary ip is invaild: %s", primaryIPStr)
		}

		_, serviceCIDR, err := net.ParseCIDR(serviceCIDRStr)
		if err != nil {
			return fmt.Errorf("eni multi ip return servicecidr(%s) is invaild: %v", serviceCIDRStr, err)
		}

		hostStackCIDRs := make([]*net.IPNet, 0)
		for _, v := range conf.HostStackCIDRs {
			_, cidr, err := net.ParseCIDR(v)
			if err != nil {
				return fmt.Errorf("host_stack_cidrs(%s) is invaild: %v", v, err)

			}
			hostStackCIDRs = append(hostStackCIDRs, cidr)
		}

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
				eniMultiIPDriver = driver.IPVlanDriver
			}
		}
		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			driver.Log.Debug(err)
			return nil
		}
		defer l.Close()
		err = eniMultiIPDriver.Setup(hostVethName, args.IfName, subnet, primaryIP, serviceCIDR, hostStackCIDRs, gw, nil, int(deviceID), ingress, egress, conf.MTU, cniNetns)
		if err != nil {
			return fmt.Errorf("setup network failed: %v", err)
		}
		allocatedIPAddr = *subnet
		allocatedGatewayAddr = gw
	case rpc.IPType_TypeVPCIP:
		if allocResult.GetVpcIp() == nil || allocResult.GetVpcIp().GetPodConfig() == nil ||
			allocResult.GetVpcIp().NodeCidr == "" {
			return fmt.Errorf("vpc ip result is empty: %v", allocResult)
		}
		var subnet *net.IPNet
		_, subnet, err = net.ParseCIDR(allocResult.GetVpcIp().GetNodeCidr())
		if err != nil {
			return fmt.Errorf("vpc veth return subnet is not vaild: %v", allocResult.GetVpcIp().GetNodeCidr())
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

		ingress := allocResult.GetVpcIp().GetPodConfig().GetIngress()
		egress := allocResult.GetVpcIp().GetPodConfig().GetEgress()
		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			driver.Log.Debug(err)
			return nil
		}
		defer l.Close()
		err = vethDriver.Setup(hostVethName, args.IfName, &podIPAddr, nil, nil, nil, gateway, nil, 0, ingress, egress, conf.MTU, cniNetns)
		if err != nil {
			return fmt.Errorf("setup network failed: %v", err)
		}
		allocatedIPAddr = podIPAddr
		allocatedGatewayAddr = gateway
	case rpc.IPType_TypeVPCENI:
		if allocResult.GetVpcEni() == nil || allocResult.GetVpcEni().GetServiceCidr() == "" ||
			allocResult.GetVpcEni().GetEniConfig() == nil {
			return fmt.Errorf("vpcEni ip result is empty: %v", allocResult)
		}
		var srvSubnet *net.IPNet
		_, srvSubnet, err = net.ParseCIDR(allocResult.GetVpcEni().GetServiceCidr())
		if err != nil {
			return fmt.Errorf("vpc eni return srv subnet is not vaild: %v", allocResult.GetVpcEni().GetServiceCidr())
		}

		var eniAddrIP net.IP
		eniAddrIP = net.ParseIP(allocResult.GetVpcEni().GetEniConfig().GetIPv4Addr())
		if eniAddrIP == nil {
			return fmt.Errorf("error get ip from alloc result: %s", allocResult.GetVpcEni().GetEniConfig().GetIPv4Addr())
		}

		var eniAddrSubnet *net.IPNet
		_, eniAddrSubnet, err = net.ParseCIDR(allocResult.GetVpcEni().GetEniConfig().GetIPv4Subnet())
		if err != nil {
			return fmt.Errorf("error get subnet from alloc result: %s", allocResult.GetVpcEni().GetEniConfig().GetIPv4Subnet())
		}
		eniAddrSubnet.IP = eniAddrIP

		var gw net.IP
		gw = net.ParseIP(allocResult.GetVpcEni().GetEniConfig().GetGateway())
		if gw == nil {
			return fmt.Errorf("error get gw from alloc result: %s", allocResult.GetVpcEni().GetEniConfig().GetGateway())
		}

		deviceNumber, err := link.GetDeviceNumber(allocResult.GetVpcEni().GetEniConfig().GetMacAddr())
		if err != nil {
			return err
		}

		extraRoutes := []*types.Route{
			{
				Dst: *srvSubnet,
				GW:  net.ParseIP("169.254.1.1"),
			},
		}

		for _, v := range conf.HostStackCIDRs {
			_, cidr, err := net.ParseCIDR(v)
			if err != nil {
				return fmt.Errorf("host_stack_cidrs(%s) is invaild: %v", v, err)

			}
			extraRoutes = append(extraRoutes, &types.Route{
				Dst: *cidr,
				GW:  net.ParseIP("169.254.1.1"),
			})
		}

		ingress := allocResult.GetVpcEni().GetPodConfig().GetIngress()
		egress := allocResult.GetVpcEni().GetPodConfig().GetEgress()
		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			driver.Log.Debug(err)
			return nil
		}
		defer l.Close()
		err = vethDriver.Setup(hostVethName, defaultVethForENI, eniAddrSubnet, nil, nil, nil, gw, extraRoutes, 0, ingress, egress, conf.MTU, cniNetns)
		if err != nil {
			return fmt.Errorf("setup veth network for eni failed: %v", err)
		}

		defer func() {
			if err != nil {
				if e := vethDriver.Teardown(hostVethName, args.IfName, cniNetns, nil); e != nil {
					err = errors.Wrapf(err, "tear down veth network for eni failed: %v", e)
				}
			}
		}()

		err = nicDriver.Setup(hostVethName, args.IfName, eniAddrSubnet, nil, nil, nil, gw, nil, int(deviceNumber), 0, 0, conf.MTU, cniNetns)
		if err != nil {
			return fmt.Errorf("setup network for vpc eni failed: %v", err)
		}
		allocatedIPAddr = *eniAddrSubnet
		allocatedGatewayAddr = gw
	default:
		return fmt.Errorf("not support this network type")
	}

	result := &current.Result{
		IPs: []*current.IPConfig{{
			Version: "4",
			Address: allocatedIPAddr,
			Gateway: allocatedGatewayAddr,
		}},
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
	confVersion, cniNetns, conf, k8sConfig, err := parseCmdArgs(args)
	if err != nil {
		return err
	}
	defer cniNetns.Close()

	if conf.Debug {
		fd, err := os.OpenFile(terwayCNIDebugLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err == nil {
			defer fd.Close()
			driver.Log.SetDebug(true, fd)
		}
	}
	driver.Log.Debugf("args: %s", driver.JSONStr(args))
	driver.Log.Debugf("cmdDel: ns %s , k8s %s, cni std %s", cniNetns.Path(), driver.JSONStr(k8sConfig), driver.JSONStr(conf))

	terwayBackendClient, closeConn, err := getNetworkClient()
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("del cmd: create grpc client, pod: %s-%s",
			string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME),
		))
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
		return errors.Wrap(err, fmt.Sprintf("del cmd: error get ip info from grpc call, pod: %s-%s",
			string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME),
		))
	}

	hostVethName, _ := link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), defaultVethPrefix)

	switch infoResult.IPType {
	case rpc.IPType_TypeENIMultiIP:

		if strings.ToLower(conf.ENIIPVirtualType) == eniIPVirtualTypeIPVlan {
			available, err := driver.CheckIPVLanAvailable()
			if err != nil {
				return err
			}
			if available {
				eniMultiIPDriver = driver.IPVlanDriver
			}
		}

		podIP := net.ParseIP(infoResult.GetPodIP())
		if podIP == nil {
			return errors.Wrapf(err, "invalid pod ip %s", infoResult.GetPodIP())
		}
		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			driver.Log.Debug(err)
			return nil
		}
		defer l.Close()
		err = eniMultiIPDriver.Teardown(hostVethName, args.IfName, cniNetns, podIP)
		if err != nil {
			return errors.Wrapf(err, "error teardown network for pod: %s-%s",
				string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME))
		}
	case rpc.IPType_TypeVPCIP:
		var subnet *net.IPNet
		_, subnet, err = net.ParseCIDR(infoResult.GetNodeCidr())
		if err != nil {
			return fmt.Errorf("get info return subnet is not vaild: %v", infoResult.GetNodeCidr())
		}
		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			driver.Log.Debug(err)
			return nil
		}
		defer l.Close()
		err = vethDriver.Teardown(hostVethName, args.IfName, cniNetns, nil)
		if err != nil {
			return errors.Wrapf(err, "error teardown network for pod: %s-%s",
				string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME))
		}

		err = ipam.ExecDel(delegateIpam, []byte(fmt.Sprintf(delegateConf, subnet.String())))
		if err != nil {
			return errors.Wrapf(err, "error teardown network ipam for pod: %s-%s",
				string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME))
		}

	case rpc.IPType_TypeVPCENI:
		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			driver.Log.Debug(err)
			return nil
		}
		defer l.Close()
		_ = vethDriver.Teardown(hostVethName, defaultVethForENI, cniNetns, nil)
		// ignore ENI veth release error
		//if err != nil {
		//	// ignore ENI veth release error
		//}
		err = nicDriver.Teardown(hostVethName, args.IfName, cniNetns, nil)
		if err != nil {
			return errors.Wrapf(err, "error teardown nic network for pod: %s-%s",
				string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME))
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
	_, cniNetns, conf, k8sConfig, err := parseCmdArgs(args)
	if err != nil {
		if _, ok := err.(ns.NSPathNotExistErr); ok {
			return nil
		}
		return err
	}
	defer cniNetns.Close()

	if conf.Debug {
		fd, err := os.OpenFile(terwayCNIDebugLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err == nil {
			defer fd.Close()
			driver.Log.SetDebug(true, fd)
		}
	}
	driver.Log.Debugf("args: %s", driver.JSONStr(args))
	driver.Log.Debugf("cmdCheck: ns %s , k8s %s, cni std %s", cniNetns.Path(), driver.JSONStr(k8sConfig), driver.JSONStr(conf))

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
		driver.Log.Debug(err)
		return nil
	}
	hostVethName, _ := link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), defaultVethPrefix)

	switch getResult.IPType {
	case rpc.IPType_TypeENIMultiIP:
		if getResult.GetENIMultiIP() == nil ||
			getResult.GetENIMultiIP().GetEniConfig() == nil {
			return nil
		}
		containerIPv4Addr, err := ParseAddr(getResult.GetENIMultiIP().GetEniConfig().GetIPv4Addr(), getResult.GetENIMultiIP().GetEniConfig().GetIPv4Subnet())
		if err != nil {
			driver.Log.Debug(err)
			return nil
		}

		gw, err := ParesIP(getResult.GetENIMultiIP().GetEniConfig().GetGateway())
		if err != nil {
			driver.Log.Debug(err)
			return nil
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
			HostVethName:    hostVethName,
			IPv4Addr:        containerIPv4Addr,
			Gateway:         gw,
			DeviceID:        getResult.GetENIMultiIP().EniConfig.DeviceNumber,
			MTU:             conf.MTU,
		}
		if strings.ToLower(conf.ENIIPVirtualType) == eniIPVirtualTypeIPVlan {
			ok, err := driver.CheckIPVLanAvailable()
			if err != nil {
				driver.Log.Debug(err)
				return nil
			}
			if ok {
				eniMultiIPDriver = driver.IPVlanDriver
			}
		}
		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			driver.Log.Debug(err)
			return nil
		}
		defer l.Close()
		err = eniMultiIPDriver.Check(&cfg)
		if err != nil {
			driver.Log.Debug(err)
			return nil
		}
	case rpc.IPType_TypeVPCIP:
		return nil
	case rpc.IPType_TypeVPCENI:
		if getResult.GetVpcEni() == nil ||
			getResult.GetVpcEni().GetServiceCidr() == "" ||
			getResult.GetVpcEni().GetEniConfig() == nil {
			return nil
		}

		containerIPv4Addr, err := ParseAddr(getResult.PodIP, getResult.GetVpcEni().GetEniConfig().GetIPv4Subnet())
		if err != nil {
			return nil
		}

		gw, err := ParesIP(getResult.GetVpcEni().GetEniConfig().GetGateway())
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
			IPv4Addr:        containerIPv4Addr,
			Gateway:         gw,
			DeviceID:        getResult.GetVpcEni().EniConfig.DeviceNumber,
			MTU:             conf.MTU,
		}
		l, err := driver.GrabFileLock(terwayCNILock)
		if err != nil {
			driver.Log.Debug(err)
			return nil
		}
		defer l.Close()
		err = nicDriver.Check(cfg)
		if err != nil {
			driver.Log.Debug(err)
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
		return nil, nil, errors.Wrap(err, "error dial terway daemon")
	}

	terwayBackendClient := rpc.NewTerwayBackendClient(grpcConn)
	return terwayBackendClient, func() {
		grpcConn.Close()
		cancel()
	}, nil
}

// ParseAddr build with ip and subnet
func ParseAddr(ip string, subnet string) (*net.IPNet, error) {
	i, err := ParesIP(ip)
	if err != nil {
		return nil, err
	}
	_, s, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil, fmt.Errorf("parseCIDR failed [%s]", subnet)
	}
	s.IP = i
	return s, nil
}

// ParesIP return ip or err
func ParesIP(ip string) (net.IP, error) {
	i := net.ParseIP(ip)
	if i == nil {
		return nil, fmt.Errorf("parseIP failed [%s]", ip)
	}
	return i, nil
}
