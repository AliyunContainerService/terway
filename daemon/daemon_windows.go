package daemon

import (
	"context"
	"time"

	"github.com/pkg/errors"

	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	"github.com/AliyunContainerService/terway/pkg/windows/apis"
	"github.com/AliyunContainerService/terway/pkg/windows/iface"
	"github.com/AliyunContainerService/terway/pkg/windows/ip"
)

func preStartResourceManager(daemonMode string, k8s Kubernetes) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	primaryMac, err := metadata.GetPrimaryENIMAC()
	if err != nil {
		return errors.Wrap(err, "error getting primary mac")
	}

	switch daemonMode {
	case daemonModeVPC:
		var nwIface, err = iface.GetInterfaceByMAC(primaryMac, true)
		if err != nil {
			return errors.Wrap(err, "error getting interface")
		}
		var nwSubnet = ip.FromIPNet(k8s.GetNodeCidr().IPv4).Network().ToIPNet()
		var nw = &apis.Network{
			Name:        "cb0",
			AdapterName: nwIface.Alias,
			AdapterMAC:  nwIface.MacAddress,
			Subnet:      *nwSubnet,
			Gateway:     apis.GetDefaultNetworkGateway(nwSubnet),
		}
		err = apis.AddBridgeHNSNetwork(ctx, nw)
		if err != nil {
			return errors.Wrapf(err, "error adding network: %s", nw.Format(apis.HNS))
		}
	case daemonModeENIOnly, daemonModeENIMultiIP:
		// NB(thxCode): create a fake network to allow service connection
		var assistantIface, err = iface.GetInterfaceByMAC(primaryMac, true)
		if err != nil {
			return errors.Wrap(err, "error getting assistant interface")
		}
		var assistantNwSubnet = ip.FromIPNet(k8s.GetServiceCIDR().IPv4).Next().ToIPNet()
		var assistantNw = &apis.Network{
			Name:        "cb0",
			AdapterName: assistantIface.Alias,
			AdapterMAC:  assistantIface.MacAddress,
			Subnet:      *assistantNwSubnet,
			Gateway:     apis.GetDefaultNetworkGateway(assistantNwSubnet),
		}
		err = apis.AddBridgeHNSNetwork(ctx, assistantNw)
		if err != nil {
			return errors.Wrapf(err, "error adding assistant network: %s", assistantNw.Format(apis.HNS))
		}
	}

	return nil
}
