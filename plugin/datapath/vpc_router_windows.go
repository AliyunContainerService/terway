package datapath

import (
	"context"
	"net"
	"time"

	"github.com/pkg/errors"

	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	"github.com/AliyunContainerService/terway/pkg/windows/apis"
	"github.com/AliyunContainerService/terway/pkg/windows/ip"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
)

func NewVPCRoute() *VPCRoute {
	return &VPCRoute{}
}

type VPCRoute struct{}

func (d *VPCRoute) Setup(ctx context.Context, cfg *types.SetupConfig, containerID, netNS string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()

	// requirement
	var vpcCidr *net.IPNet
	vpcCidr, err = getVPCCidr()
	if err != nil {
		return err
	}

	// get network
	var nwID string
	if apis.IsHCNSupported(netNS) {
		nwID, err = apis.GetHCNNetworkIDByName(ctx, "cb0")
	} else {
		nwID, err = apis.GetHNSNetworkIDByName(ctx, "cb0")
	}
	if err != nil {
		return errors.Wrap(err, "cannot get network")
	}

	// create endpoint
	var nwSubnet = ip.FromIPNet(cfg.ContainerIPNet.IPv4).Next().ToIPNet()
	var epAddress = net.IPNet{IP: cfg.ContainerIPNet.IPv4.IP, Mask: net.IPv4Mask(255, 255, 255, 255)}
	var ep = apis.Endpoint{
		Name:    cfg.HostVETHName,
		Address: epAddress,
		Gateway: apis.GetDefaultBridgeNetworkGateway(nwSubnet),
		DNS:     cfg.RuntimeConfig.DNS.AsCNIDns(),
		MTU:     cfg.MTU,
	}
	var serviceCidr = ip.FromIPNet(cfg.ServiceCIDR.IPv4).Network().ToIPNet()
	var portMappings = cfg.RuntimeConfig.PortMaps
	if apis.IsHCNSupported(netNS) {
		var decorates = []apis.HCNEndpointDecorator{
			apis.ApplyHCNEndpointEncapsulatedRoutePolicy(vpcCidr, serviceCidr),
			apis.ApplyHCNEndpointOutboundNatPolicy(vpcCidr, serviceCidr),
			apis.ApplyHCNEndpointLoopbackDsrPolicy(),
		}
		for idx := range portMappings {
			var p = &portMappings[idx]
			decorates = append(decorates,
				apis.ApplyHCNEndpointPortMappingPolicy(p.Protocol, p.ContainerPort, p.HostPort, p.HostIP),
			)
		}
		err = apis.AddHCNEndpoint(ctx, &ep, netNS, nwID, decorates...)
	} else {
		var decorates = []apis.HNSEndpointDecorator{
			apis.ApplyHNSEndpointEncapsulatedRoutePolicy(vpcCidr, serviceCidr),
			apis.ApplyHNSEndpointOutboundNatPolicy(vpcCidr, serviceCidr),
			apis.ApplyHNSEndpointLoopbackDsrPolicy(),
		}
		for idx := range portMappings {
			var p = &portMappings[idx]
			decorates = append(decorates,
				apis.ApplyHNSEndpointPortMappingPolicy(p.Protocol, p.ContainerPort, p.HostPort, p.HostIP),
			)
		}
		err = apis.AddHNSEndpoint(ctx, &ep, netNS, nwID, containerID, decorates...)
	}
	if err != nil {
		return errors.Wrapf(err, "error adding endpoint: %s", ep.Format(nwID, apis.HNS))
	}

	return
}

func (d *VPCRoute) Teardown(ctx context.Context, cfg *types.TeardownCfg, containerID, netNS string) (err error) {
	ctx, cancel := context.WithTimeout(context.TODO(), 100*time.Second)
	defer cancel()

	// delete endpoint
	var ep = apis.Endpoint{
		Name: cfg.HostVETHName,
	}
	if apis.IsHCNSupported(netNS) {
		_, err = apis.DeleteHCNEndpoint(ctx, &ep, netNS)
	} else {
		_, err = apis.DeleteHNSEndpoint(ctx, &ep, netNS, containerID)
	}
	if err != nil {
		return errors.Wrap(err, "error deleting endpoint")
	}

	return
}

func (d *VPCRoute) Check(ctx context.Context, cfg *types.CheckConfig, containerID, netNS string) error {
	return nil
}

func getVPCCidr() (*net.IPNet, error) {
	vpcCidrString, err := metadata.GetLocalVPCCIDR() // TODO: shall we promote this to daemon server?
	if err != nil {
		return nil, errors.Wrap(err, "failed to detect local vpc CIDR")
	}
	_, vpcCidr, err := net.ParseCIDR(vpcCidrString)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse local vpc CIDR string")
	}
	return vpcCidr, nil
}
