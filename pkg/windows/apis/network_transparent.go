//go:build windows
// +build windows

package apis

import (
	"context"

	"github.com/Microsoft/hcsshim"
	"github.com/Microsoft/hcsshim/hcn"
	"github.com/pkg/errors"
	"k8s.io/utils/pointer"

	"github.com/AliyunContainerService/terway/pkg/windows/iface"
)

const TypeOfTransparentNetwork = "Transparent"

// AddTransparentHNSNetwork parses the given Network to a target HNS v1 transparent network object,
// and brings it up.
func AddTransparentHNSNetwork(ctx context.Context, i *Network) (err error) {
	// create network
	err = AddHNSNetwork(ctx, asTransparentNetwork(i))
	if err != nil {
		return err
	}

	if ref := i.refAtCreation; ref != nil {
		var nw = ref.(*hcsshim.HNSNetwork)
		defer func() {
			if err == nil {
				return
			}
			_, _ = nw.Delete()
		}()

		// configure adapter virtual network
		var mgmtIface *iface.Interface
		mgmtIface, err = iface.GetInterfaceByMAC(i.AdapterMAC, false)
		if err != nil {
			return errors.Wrapf(err, "failed to get transparent network %s interface %s",
				i.Name, i.AdapterMAC)
		}
		err = iface.ConfigureInterface(mgmtIface.Index, iface.Dual, iface.ConfigurationOptions{
			Dhcp: pointer.BoolPtr(false),
			Mtu:  i.getMTU(),
		})
		if err != nil {
			return errors.Wrapf(err, "failed to configure transparent network %s interface %s",
				i.Name, i.AdapterMAC)
		}
	}

	return
}

// AddTransparentHCNNetwork parses the given Network to a target HNS v2(HCN api) transparent network object,
// and brings it up.
func AddTransparentHCNNetwork(ctx context.Context, i *Network) (err error) {
	// create network
	err = AddHCNNetwork(ctx, asTransparentNetwork(i))
	if err != nil {
		return err
	}

	if ref := i.refAtCreation; ref != nil {
		var nw = ref.(*hcn.HostComputeNetwork)
		defer func() {
			if err == nil {
				return
			}
			_ = nw.Delete()
		}()

		// configure adapter virtual network
		var mgmtIface *iface.Interface
		mgmtIface, err = iface.GetInterfaceByMAC(i.AdapterMAC, false)
		if err != nil {
			return errors.Wrapf(err, "failed to get transparent network %s interface %s",
				i.Name, i.AdapterMAC)
		}
		err = iface.ConfigureInterface(mgmtIface.Index, iface.Dual, iface.ConfigurationOptions{
			Dhcp: pointer.BoolPtr(false),
			Mtu:  i.getMTU(),
		})
		if err != nil {
			return errors.Wrapf(err, "failed to configure transparent network %s interface %s",
				i.Name, i.AdapterMAC)
		}
	}

	return
}

// asTransparentNetwork sets the given Network to use Transparent HNS network type.
func asTransparentNetwork(i *Network) *Network {
	i.Type = TypeOfTransparentNetwork
	i.Name = normalizeString(i.Name)
	if i.Name == "" {
		i.Name = normalizeString(i.AdapterName)
	}
	return i
}
