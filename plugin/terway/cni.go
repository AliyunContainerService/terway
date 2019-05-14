package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/vishvananda/netlink"
	"google.golang.org/grpc"
	"net"
	"runtime"
	"time"
)

const (
	defaultSocketPath      = "/var/run/eni/eni.socket"
	defaultVethPrefix      = "cali"
	defaultCniTimeout      = 120
	defaultVethForENI      = "veth1"
	delegateIpam           = "host-local"
	eniIPVirtualTypeIPVlan = "IPVlan"
	delegateConf           = `
{
	"ipam": {
		"type": "host-local",
		"subnet": "%s",
		"routes": [
			{ "dst": "0.0.0.0/0" }
		]
	}
}
`
)

func init() {
	runtime.LockOSThread()
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.GetSpecVersionSupported())
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
}

// K8SArgs is cni args of kubernetes
type K8SArgs struct {
	types.CommonArgs
	IP                         net.IP
	K8S_POD_NAME               types.UnmarshallableString
	K8S_POD_NAMESPACE          types.UnmarshallableString
	K8S_POD_INFRA_CONTAINER_ID types.UnmarshallableString
}

var networkDriver = driver.VethDriver
var eniMultiIPDriver = driver.VethDriver
var nicDriver = driver.NicDriver

func cmdAdd(args *skel.CmdArgs) (err error) {
	versionDecoder := &cniversion.ConfigDecoder{}
	var (
		confVersion string
		cniNetns    ns.NetNS
	)
	confVersion, err = versionDecoder.Decode(args.StdinData)
	if err != nil {
		return err
	}

	cniNetns, err = ns.GetNS(args.Netns)
	if err != nil {
		return err
	}

	conf := NetConf{}
	if err = json.Unmarshal(args.StdinData, &conf); err != nil {
		return errors.Wrap(err, "add cmd: error loading config from args")
	}

	k8sConfig := K8SArgs{}
	if err = types.LoadArgs(args.Args, &k8sConfig); err != nil {
		return errors.Wrap(err, "add cmd: failed to load k8s config from args")
	}

	if err = driver.EnsureHostNsConfig(); err != nil {
		return errors.Wrapf(err, "add cmd: failed setup host namespace configs")
	}

	terwayBackendClient, closeConn, err := getNetworkClient()
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("add cmd: create grpc client, pod: %s-%s",
			string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME),
		))
	}
	defer closeConn()

	timeoutContext, cancel := context.WithTimeout(context.Background(), defaultCniTimeout*time.Second)
	defer cancel()

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
			terwayBackendClient.ReleaseIP(context.Background(),
				&rpc.ReleaseIPRequest{
					K8SPodName:             string(k8sConfig.K8S_POD_NAME),
					K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
					K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
					IPType:                 allocResult.IPType,
					Reason:                 fmt.Sprintf("roll back ip for error: %v", err),
				})
		}
	}()

	hostVethName := link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), defaultVethPrefix)
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
			return fmt.Errorf("eni multi ip return primary ip is not vaild: %v", primaryIPStr)
		}
		primaryIpv4Addr := &net.IPNet{
			IP:   primaryIP,
			Mask: net.CIDRMask(32, 32),
		}
		copy(primaryIpv4Addr.Mask, subnet.Mask)

		if conf.ENIIPVirtualType == eniIPVirtualTypeIPVlan {
			eniMultiIPDriver = driver.IPVlanDriver
		}
		err = eniMultiIPDriver.Setup(hostVethName, args.IfName, subnet, primaryIpv4Addr, gw, nil, int(deviceID), ingress, egress, cniNetns)
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
				ipam.ExecDel(delegateIpam, []byte(fmt.Sprintf(delegateConf, subnet.String())))
			}
		}()

		if len(ipamResult.IPs) != 1 {
			return fmt.Errorf("error get result from delegate ipam result %v: ipam result is not one ip", delegateIpam)
		}
		podIPAddr := ipamResult.IPs[0].Address
		gateway := ipamResult.IPs[0].Gateway

		ingress := allocResult.GetVpcIp().GetPodConfig().GetIngress()
		egress := allocResult.GetVpcIp().GetPodConfig().GetEgress()

		err = networkDriver.Setup(hostVethName, args.IfName, &podIPAddr, nil, gateway, nil, 0, ingress, egress, cniNetns)
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

		deviceNumber := 0
		if allocResult.GetVpcEni().GetEniConfig().GetMacAddr() == "" {
			return fmt.Errorf("error get devicenumber from alloc result: %v", allocResult.GetVpcEni().GetEniConfig().GetMacAddr())
		}
		var linkList []netlink.Link
		linkList, err = netlink.LinkList()
		found := false
		for _, enilink := range linkList {
			if enilink.Attrs().HardwareAddr.String() == allocResult.GetVpcEni().GetEniConfig().GetMacAddr() {
				deviceNumber = enilink.Attrs().Index
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("error get allocated mac address for eni: %s", allocResult.GetVpcEni().GetEniConfig().GetMacAddr())
		}

		if deviceNumber == 0 {
			return fmt.Errorf("invaild device number: %v", deviceNumber)
		}

		extraRoutes := []*types.Route{
			{
				Dst: *srvSubnet,
				GW:  net.ParseIP("169.254.1.1"),
			},
		}

		ingress := allocResult.GetVpcEni().GetPodConfig().GetIngress()
		egress := allocResult.GetVpcEni().GetPodConfig().GetEgress()
		err = networkDriver.Setup(hostVethName, defaultVethForENI, eniAddrSubnet, nil, gw, extraRoutes, 0, ingress, egress, cniNetns)
		if err != nil {
			return fmt.Errorf("setup veth network for eni failed: %v", err)
		}

		defer func() {
			if err != nil {
				networkDriver.Teardown(hostVethName, args.IfName, cniNetns)
			}
		}()

		err = nicDriver.Setup(hostVethName, args.IfName, eniAddrSubnet, nil, gw, nil, int(deviceNumber), 0, 0, cniNetns)
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

	return types.PrintResult(result, confVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	versionDecoder := &cniversion.ConfigDecoder{}
	confVersion, err := versionDecoder.Decode(args.StdinData)
	if err != nil {
		return err
	}

	cniNetns, err := ns.GetNS(args.Netns)
	if err != nil {
		return err
	}

	conf := NetConf{}
	if err := json.Unmarshal(args.StdinData, &conf); err != nil {
		return errors.Wrap(err, "add cmd: error loading config from args")
	}

	k8sConfig := K8SArgs{}
	if err := types.LoadArgs(args.Args, &k8sConfig); err != nil {
		return errors.Wrap(err, "add cmd: failed to load k8s config from args")
	}

	terwayBackendClient, closeConn, err := getNetworkClient()
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("add cmd: create grpc client, pod: %s-%s",
			string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME),
		))
	}
	defer closeConn()

	timeoutContext, cancel := context.WithTimeout(context.Background(), defaultCniTimeout*time.Second)
	defer cancel()

	infoResult, err := terwayBackendClient.GetIPInfo(
		timeoutContext,
		&rpc.GetInfoRequest{
			K8SPodName:             string(k8sConfig.K8S_POD_NAME),
			K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
			K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
		})

	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("add cmd: error get ip info from grpc call, pod: %s-%s",
			string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME),
		))
	}

	hostVethName := link.VethNameForPod(string(k8sConfig.K8S_POD_NAME), string(k8sConfig.K8S_POD_NAMESPACE), defaultVethPrefix)

	switch infoResult.IPType {
	case rpc.IPType_TypeENIMultiIP:
		if conf.ENIIPVirtualType == eniIPVirtualTypeIPVlan {
			eniMultiIPDriver = driver.IPVlanDriver
		}
		err = eniMultiIPDriver.Teardown(hostVethName, args.IfName, cniNetns)
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
		err = networkDriver.Teardown(hostVethName, args.IfName, cniNetns)
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
		err = networkDriver.Teardown(hostVethName, defaultVethForENI, cniNetns)
		if err != nil {
			return errors.Wrapf(err, "error teardown veth network for pod: %s-%s",
				string(k8sConfig.K8S_POD_NAMESPACE), string(k8sConfig.K8S_POD_NAME))
		}
		err = nicDriver.Teardown(hostVethName, args.IfName, cniNetns)
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

func getNetworkClient() (rpc.TerwayBackendClient, func(), error) {
	grpcConn, err := grpc.Dial(defaultSocketPath, grpc.WithInsecure(), grpc.WithDialer(
		func(s string, duration time.Duration) (net.Conn, error) {
			unixAddr, err := net.ResolveUnixAddr("unix", defaultSocketPath)
			if err != nil {
				return nil, nil
			}
			return net.DialUnix("unix", nil, unixAddr)
		}))
	if err != nil {
		return nil, nil, errors.Wrap(err, "error dial terway daemon")
	}

	terwayBackendClient := rpc.NewTerwayBackendClient(grpcConn)
	return terwayBackendClient, func() {
		grpcConn.Close()
	}, nil
}
