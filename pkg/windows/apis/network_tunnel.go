//go:build windows
// +build windows

package apis

import (
	"context"
	"time"

	"github.com/Microsoft/hcsshim"
	"github.com/Microsoft/hcsshim/hcn"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/pointer"

	"github.com/AliyunContainerService/terway/pkg/windows/iface"
)

const TypeOfTunnelNetwork = "L2Tunnel"

// AddTunnelHNSNetwork parses the given Network to a target HNS v1 bridge network object,
// and brings it up.
func AddTunnelHNSNetwork(ctx context.Context, i *Network) (err error) {
	// create network
	err = AddHNSNetwork(ctx, asTunnelNetwork(i))
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
			return errors.Wrapf(err, "failed to get tunnel network %s interface %s",
				i.Name, i.AdapterMAC)
		}

		// configure adapter virtual interface
		err = iface.ConfigureInterface(mgmtIface.Index, iface.Dual, iface.ConfigurationOptions{
			Forwarding: pointer.BoolPtr(true),
			Mtu:        i.getMTU(),
		})
		if err != nil {
			return errors.Wrapf(err, "failed to configure tunnel network %s interface %s",
				i.Name, i.AdapterMAC)
		}
	}

	return
}

// AddTunnelHCNNetwork parses the given Network to a target HNS v2(HCN api) bridge network object,
// and brings it up.
func AddTunnelHCNNetwork(ctx context.Context, i *Network) (err error) {
	// create network
	err = AddHCNNetwork(ctx, asTunnelNetwork(i))
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
			return errors.Wrapf(err, "failed to get tunnel network %s interface %s",
				i.Name, i.AdapterMAC)
		}

		// configure adapter virtual interface
		err = iface.ConfigureInterface(mgmtIface.Index, iface.Dual, iface.ConfigurationOptions{
			Forwarding: pointer.BoolPtr(true),
			Mtu:        i.getMTU(),
		})
		if err != nil {
			return errors.Wrapf(err, "failed to configure tunnel network %s interface %s",
				i.Name, i.AdapterMAC)
		}
	}

	return
}

// asTunnelNetwork sets the given Network to use L2Tunnel HNS network type.
func asTunnelNetwork(i *Network) *Network {
	i.Type = TypeOfTunnelNetwork
	i.Name = normalizeString(i.Name)
	if i.Name == "" {
		i.Name = normalizeString(i.AdapterName)
	}
	return i
}
