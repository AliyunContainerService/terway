package datapath

import (
	"context"
	"net"
	"time"

	cnitypes "github.com/containernetworking/cni/pkg/types"
	"github.com/pkg/errors"

	"github.com/AliyunContainerService/terway/pkg/windows/apis"
	"github.com/AliyunContainerService/terway/pkg/windows/iface"
	"github.com/AliyunContainerService/terway/pkg/windows/ip"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
)

func NewPolicyRoute() *PolicyRoute {
	return &PolicyRoute{}
}

type PolicyRoute struct{}

func (d *PolicyRoute) Setup(ctx context.Context, cfg *types.SetupConfig, containerID, netNS string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()

	// ensure network
	nwIface, err := iface.GetInterfaceByIndex(cfg.ENIIndex)
	if err != nil {
		return errors.Wrapf(err, "error getting interface by index %d", cfg.ENIIndex)
	}
	nw, err := apis.GetHNSNetworkByName(ctx, nwIface.Name)
	if err != nil {
		return errors.Wrapf(err, "error getting network by name %s", nwIface.Name)
	}

	// create endpoint
	var epAddress = net.IPNet{IP: cfg.ContainerIPNet.IPv4.IP, Mask: net.IPv4Mask(255, 255, 255, 255)}
	var ep = &apis.Endpoint{
		Name:    cfg.HostVETHName,
		Address: epAddress,
		Gateway: cfg.GatewayIP.IPv4,
		MTU:     cfg.MTU,
		DNS: cnitypes.DNS{
			Nameservers: apis.FromCommaList(nw.DNSServerList),
			Search:      apis.FromCommaList(nw.DNSSuffix),
		},
	}
	if apis.IsHCNSupported(netNS) {
		err = apis.AddHCNEndpoint(ctx, ep, netNS, nw.Id)
	} else {
		err = apis.AddHNSEndpoint(ctx, ep, netNS, nw.Id, containerID)
	}
	if err != nil {
		return errors.Wrapf(err, "error adding endpoint: %s", ep.Format(nw.Id, apis.HNS))
	}

	// get assistant network
	var assistantNwID string
	if apis.IsHCNSupported(netNS) {
		assistantNwID, err = apis.GetHCNNetworkIDByName(ctx, "cb0")
	} else {
		assistantNwID, err = apis.GetHNSNetworkIDByName(ctx, "cb0")
	}
	if err != nil {
		return errors.Wrap(err, "cannot get assistant network")
	}

	// create assistant endpoint
	var assistantSubnet = ip.FromIPNet(cfg.AssistantContainerIPNet.IPv4).Network().ToIPNet()
	var assistantEpAddress = net.IPNet{IP: cfg.AssistantContainerIPNet.IPv4.IP, Mask: net.IPv4Mask(255, 255, 255, 255)}
	var assistantEp = &apis.Endpoint{
		Name:    cfg.HostVETHName + "_",
		Address: assistantEpAddress,
		Gateway: apis.GetDefaultBridgeNetworkGateway(assistantSubnet),
		DNS:     cfg.RuntimeConfig.DNS.AsCNIDns(),
		MTU:     cfg.MTU,
	}
	var serviceCidr = ip.FromIPNet(cfg.ServiceCIDR.IPv4).Network().ToIPNet()
	if apis.IsHCNSupported(netNS) {
		var decorates = []apis.HCNEndpointDecorator{
			apis.ApplyHCNEndpointEncapsulatedRoutePolicy(serviceCidr),
			apis.ApplyHCNEndpointOutboundNatPolicy(),
		}
		err = apis.AddHCNEndpoint(ctx, assistantEp, netNS, assistantNwID, decorates...)
	} else {
		var decorates = []apis.HNSEndpointDecorator{
			apis.ApplyHNSEndpointEncapsulatedRoutePolicy(serviceCidr),
			apis.ApplyHNSEndpointOutboundNatPolicy(),
		}
		err = apis.AddHNSEndpoint(ctx, assistantEp, netNS, assistantNwID, containerID, decorates...)
	}
	if err != nil {
		return errors.Wrapf(err, "error adding assistant endpoint: %s", assistantEp.Format(assistantNwID, apis.HNS))
	}

	return
}

func (d *PolicyRoute) Teardown(ctx context.Context, cfg *types.TeardownCfg, containerID, netNS string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()

	// delete assistant endpoint
	var assistantEp = &apis.Endpoint{
		Name: cfg.HostVETHName + "_",
	}
	if apis.IsHCNSupported(netNS) {
		_, err = apis.DeleteHCNEndpoint(ctx, assistantEp, netNS)
	} else {
		_, err = apis.DeleteHNSEndpoint(ctx, assistantEp, netNS, containerID)
	}
	if err != nil {
		return errors.Wrap(err, "error deleting assistant endpoint")
	}

	// delete endpoint
	var ep = &apis.Endpoint{
		Name: cfg.HostVETHName,
	}
	if apis.IsHCNSupported(netNS) {
		_, err = apis.DeleteHCNEndpoint(ctx, ep, netNS)
	} else {
		_, err = apis.DeleteHNSEndpoint(ctx, ep, netNS, containerID)
	}
	if err != nil {
		return errors.Wrap(err, "error deleting endpoint")
	}

	return
}

func (d *PolicyRoute) Check(ctx context.Context, cfg *types.CheckConfig, containerID, netNS string) error {
	return nil
}
