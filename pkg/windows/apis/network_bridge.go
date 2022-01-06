//go:build windows
// +build windows

package apis

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Microsoft/hcsshim"
	"github.com/Microsoft/hcsshim/hcn"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/pointer"

	"github.com/AliyunContainerService/terway/pkg/windows/iface"
	"github.com/AliyunContainerService/terway/pkg/windows/ip"
)

const TypeOfBridgeNetwork = "L2Bridge"

// GetDefaultBridgeNetworkGateway returns the default gateway address of the given bridge subnet.
func GetDefaultBridgeNetworkGateway(subnet *net.IPNet) net.IP {
	if subnet == nil {
		return nil
	}
	var ipv4Net = ip.FromIPNet(subnet)
	return (ipv4Net.IP + 2).ToIP()
}

// AddBridgeHNSNetwork parses the given Network to a target HNS v1 bridge network object,
// and brings it up.
func AddBridgeHNSNetwork(ctx context.Context, i *Network) (err error) {
	// create network
	err = AddHNSNetwork(ctx, asBridgeNetwork(i))
	if err != nil {
		return err
	}

	// check newly created network whether to be ready
	if ref := i.refAtCreation; ref != nil {
		var nw = ref.(*hcsshim.HNSNetwork)
		defer func() {
			if err == nil {
				return
			}
			_, _ = nw.Delete()
		}()

		// find adapter virtual interface
		var mgmtIface *iface.Interface
		_ = wait.PollImmediateUntil(200*time.Millisecond, func() (bool, error) {
			mgmtIface, err = iface.GetInterfaceByMAC(i.AdapterMAC, false)
			if err != nil {
				return false, nil
			}
			return true, nil
		}, ctx.Done())
		if err != nil {
			return errors.Wrapf(err, "failed to get bridge network %s interface %s",
				i.Name, i.AdapterMAC)
		}

		// configure adapter virtual interface
		err = iface.ConfigureInterface(mgmtIface.Index, iface.Dual, iface.ConfigurationOptions{
			Forwarding: pointer.BoolPtr(true),
			Mtu:        i.getMTU(),
		})
		if err != nil {
			return errors.Wrapf(err, "failed to configure bridge network %s interface %s",
				i.Name, i.AdapterMAC)
		}
	}

	// get gateway endpoint
	var epName = fmt.Sprintf("%s_gw", i.Name)
	var epAddr = GetDefaultBridgeNetworkGateway(&i.Subnet)
	ep, err := hcsshim.GetHNSEndpointByName(epName)
	if err != nil {
		if !hcsshim.IsNotExist(err) {
			return errors.Wrapf(err, "failed to get gateway HNSEndpoint %s", epName)
		}
	}
	if ep != nil {
		// delete if the existing gateway endpoint is corrupted
		if IsHNSEndpointCorrupted(ep, i.ID, epAddr) {
			if _, err := deleteHNSEndpoint(ep, ""); err != nil {
				return errors.Wrapf(err, "failed to remove the corrupted gateway HNSEndpoint %s",
					ep.Name)
			}
			ep = nil
		}
	}

	// create gateway endpoint if not found
	if ep == nil {
		ep = &hcsshim.HNSEndpoint{
			Name:           epName,
			VirtualNetwork: i.ID,
			IPAddress:      epAddr,
		}

		// create endpoint
		var epCreated *hcsshim.HNSEndpoint
		epCreated, err = ep.Create()
		if err != nil {
			return errors.Wrapf(err, "failed to create gateway HNSEndpoint %s(%s)", epName, epAddr)
		}
		ep = epCreated
		if ep == nil {
			return errors.Errorf("failed to create gateway HNSEndpoint %s(%s)", epName, epAddr)
		}

		defer func() {
			if err == nil {
				return
			}
			_, _ = ep.Delete()
		}()

		// attach gateway endpoint to host
		_ = wait.PollImmediateUntil(200*time.Millisecond, func() (bool, error) {
			err = ep.HostAttach(1)
			if err == nil ||
				strings.Contains(err.Error(), "This endpoint is already attached to the switch.") {
				err = nil
				return true, nil
			}
			return false, nil
		}, ctx.Done())
		if err != nil {
			return errors.Wrapf(err, "failed to attach gateway HNSEndpoint %s", epName)
		}

		// configure gateway interface
		var gatewayIfaceIndex int
		gatewayIfaceIndex, err = iface.GetInterfaceIndexByIP(epAddr)
		if err != nil {
			return errors.Wrapf(err, "failed to get bridge gateway network interface %s", epAddr)
		}
		err = iface.ConfigureInterface(gatewayIfaceIndex, iface.Dual, iface.ConfigurationOptions{
			Forwarding: pointer.BoolPtr(true),
			Mtu:        i.getMTU(),
		})
		if err != nil {
			return errors.Wrapf(err, "failed to configure bridge gateway network interface %s", i.AdapterMAC)
		}
	}

	// feedback
	i.Gateway = epAddr
	return
}

// AddBridgeHCNNetwork parses the given Network to a target HNS v2(HCN api) bridge network object,
// and brings it up.
func AddBridgeHCNNetwork(ctx context.Context, i *Network) (err error) {
	// create network
	err = AddHCNNetwork(ctx, asBridgeNetwork(i))
	if err != nil {
		return err
	}

	// check newly created network whether to be ready
	if ref := i.refAtCreation; ref != nil {
		var nw = ref.(*hcn.HostComputeNetwork)
		defer func() {
			if err == nil {
				return
			}
			_ = nw.Delete()
		}()

		// find adapter virtual interface
		var mgmtIface *iface.Interface
		_ = wait.PollImmediateUntil(200*time.Millisecond, func() (bool, error) {
			mgmtIface, err = iface.GetInterfaceByMAC(i.AdapterMAC, false)
			if err != nil {
				return false, nil
			}
			return true, nil
		}, ctx.Done())
		if err != nil {
			return errors.Wrapf(err, "failed to get bridge network %s interface %s",
				i.Name, i.AdapterMAC)
		}

		// configure adapter virtual interface
		err = iface.ConfigureInterface(mgmtIface.Index, iface.Dual, iface.ConfigurationOptions{
			Forwarding: pointer.BoolPtr(true),
			Mtu:        i.getMTU(),
		})
		if err != nil {
			return errors.Wrapf(err, "failed to configure bridge network %s interface %s",
				i.Name, i.AdapterMAC)
		}
	}

	// get gateway endpoint
	var epName = fmt.Sprintf("%s_gw", i.Name)
	var epAddr = GetDefaultBridgeNetworkGateway(&i.Subnet)
	ep, err := hcn.GetEndpointByName(epName)
	if err != nil {
		if !hcn.IsNotFoundError(err) {
			return errors.Wrapf(err, "failed to get gateway HostComputeEndpoint %s", epName)
		}
	}
	if ep != nil {
		// delete if the existing gateway endpoint is corrupted
		if IsHCNEndpointCorrupted(ep, i.ID, epAddr) {
			if _, err := deleteHCNEndpoint(ep, ""); err != nil {
				return errors.Wrapf(err, "failed to remove the corrupted gateway HostComputeEndpoint %s",
					ep.Name)
			}
			ep = nil
		}
	}

	// create gateway endpoint if not found
	if ep == nil {
		var ns *hcn.HostComputeNamespace
		ns, err = GetHCNHostDefaultNamespace()
		if err != nil {
			return err
		}
		ep = &hcn.HostComputeEndpoint{
			SchemaVersion:      hcn.V2SchemaVersion(),
			Name:               epName,
			HostComputeNetwork: i.ID,
			IpConfigurations: []hcn.IpConfig{
				{
					IpAddress:    epAddr.String(),
					PrefixLength: 32,
				},
			},
		}

		// create endpoint
		var epCreated *hcn.HostComputeEndpoint
		epCreated, err = ep.Create()
		if err != nil {
			return errors.Wrapf(err, "failed to create gateway HostComputeEndpoint %s(%s)", epName, epAddr)
		}
		ep = epCreated
		if ep == nil {
			return errors.Errorf("failed to create gateway HostComputeEndpoint %s(%s)", epName, epAddr)
		}

		defer func() {
			if err == nil {
				return
			}
			_ = ep.Delete()
		}()

		// attach gateway endpoint to host
		_ = wait.PollImmediateUntil(200*time.Millisecond, func() (bool, error) {
			err = ep.NamespaceAttach(ns.Id)
			if err == nil ||
				strings.Contains(err.Error(), "This requested operation is invalid as endpoint is already part of a network namespace.") {
				err = nil
				return true, nil
			}
			return false, nil
		}, ctx.Done())
		if err != nil {
			return errors.Wrapf(err, "failed to attach gateway HostComputeEndpoint %s", epName)
		}

		// configure gateway interface
		var gatewayIfaceIndex int
		gatewayIfaceIndex, err = iface.GetInterfaceIndexByIP(epAddr)
		if err != nil {
			return errors.Wrapf(err, "failed to get bridge gateway network interface %s", epAddr)
		}
		err = iface.ConfigureInterface(gatewayIfaceIndex, iface.Dual, iface.ConfigurationOptions{
			Forwarding: pointer.BoolPtr(true),
			Mtu:        i.getMTU(),
		})
		if err != nil {
			return errors.Wrapf(err, "failed to configure bridge gateway network interface %s", i.AdapterMAC)
		}
	}

	// feedback
	i.Gateway = epAddr
	return
}

// asBridgeNetwork sets the given Network to use L2Bridge HNS network type.
func asBridgeNetwork(i *Network) *Network {
	i.Type = TypeOfBridgeNetwork
	i.Name = normalizeString(i.Name)
	if i.Name == "" {
		i.Name = normalizeString(i.AdapterName)
	}
	return i
}
