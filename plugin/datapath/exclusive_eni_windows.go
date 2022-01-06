package datapath

import (
	"context"
	"net"
	"time"

	"github.com/pkg/errors"

	"github.com/AliyunContainerService/terway/pkg/windows/apis"
	"github.com/AliyunContainerService/terway/pkg/windows/iface"
	"github.com/AliyunContainerService/terway/pkg/windows/ip"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
)

func NewExclusiveENIDriver() *ExclusiveENI {
	return &ExclusiveENI{}
}

// ExclusiveENI put nic in net ns
type ExclusiveENI struct{}

func (d *ExclusiveENI) Setup(ctx context.Context, cfg *types.SetupConfig, containerID, netNS string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()

	// ensure network
	nwIface, err := iface.GetInterfaceByIndex(cfg.ENIIndex)
	if err != nil {
		return errors.Wrapf(err, "error getting interface by index %d", cfg.ENIIndex)
	}
	var nwSubnet = ip.FromIPNet(cfg.ContainerIPNet.IPv4).Network().ToIPNet()
	var nw = &apis.Network{
		Name:        nwIface.Name,
		AdapterName: nwIface.Alias,
		AdapterMAC:  nwIface.MacAddress,
		Subnet:      *nwSubnet,
		Gateway:     cfg.GatewayIP.IPv4,
	}
	if apis.IsHCNSupported(netNS) {
		err = apis.AddTransparentHCNNetwork(ctx, nw)
	} else {
		err = apis.AddTransparentHNSNetwork(ctx, nw)
	}
	if err != nil {
		return errors.Wrapf(err, "error adding network: %s", nw.Format(apis.HNS))
	}

	defer func() {
		if err == nil {
			return
		}
		// clean up network if error creating endpoint
		if apis.IsHCNSupported(netNS) {
			_ = apis.DeleteHCNNetwork(ctx, nw)
		} else {
			_ = apis.DeleteHNSNetwork(ctx, nw)
		}
	}()

	// create endpoint
	var epAddress = net.IPNet{IP: cfg.ContainerIPNet.IPv4.IP, Mask: net.IPv4Mask(255, 255, 255, 255)}
	var ep = &apis.Endpoint{
		Name:    cfg.HostVETHName,
		Address: epAddress,
		MAC:     nwIface.MacAddress, // use the mac of eni
		Gateway: nw.Gateway,
		MTU:     cfg.MTU,
	}
	if apis.IsHCNSupported(netNS) {
		err = apis.AddHCNEndpoint(ctx, ep, netNS, nw.ID)
	} else {
		err = apis.AddHNSEndpoint(ctx, ep, netNS, nw.ID, containerID)
	}
	if err != nil {
		return errors.Wrapf(err, "error adding endpoint: %s", ep.Format(nw.ID, apis.HNS))
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

func (d *ExclusiveENI) Teardown(ctx context.Context, cfg *types.TeardownCfg, containerID, netNS string) (err error) {
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

	var nwID string

	// delete endpoint
	var ep = &apis.Endpoint{
		Name: cfg.HostVETHName,
	}
	if apis.IsHCNSupported(netNS) {
		nwID, err = apis.DeleteHCNEndpoint(ctx, ep, netNS)
	} else {
		nwID, err = apis.DeleteHNSEndpoint(ctx, ep, netNS, containerID)
	}
	if err != nil {
		return errors.Wrap(err, "error deleting endpoint")
	}

	// delete network
	var nw = &apis.Network{
		ID: nwID,
	}
	if apis.IsHCNSupported(netNS) {
		err = apis.DeleteHCNNetwork(ctx, nw)
	} else {
		err = apis.DeleteHNSNetwork(ctx, nw)
	}
	if err != nil {
		return errors.Wrap(err, "error deleting network")
	}

	return
}

func (d *ExclusiveENI) Check(ctx context.Context, cfg *types.CheckConfig, containerID, netNS string) error {
	return nil
}
