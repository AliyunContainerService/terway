package daemon

import (
	"context"
	"strings"
	"time"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/AliyunContainerService/terway/pkg/windows/apis"
	"github.com/AliyunContainerService/terway/pkg/windows/iface"
	"github.com/AliyunContainerService/terway/types"
)

func (f *eniIPFactory) setupENICompartment(eni *types.ENI) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var nwIface *iface.Interface
	_ = wait.PollUntil(2*time.Second, func() (bool, error) {
		nwIface, err = iface.GetInterfaceByMAC(eni.MAC, true)
		if err != nil {
			return false, nil
		}
		return true, nil
	}, ctx.Done())
	if err != nil {
		return errors.Wrapf(err, "error getting interface by mac %s", eni.MAC)
	}

	dnsConfig, _ := iface.GetDNSConfigurationByMAC(eni.MAC, iface.IPv4)
	var nwSubnet = eni.VSwitchCIDR.IPv4
	var nw = &apis.Network{
		Name:        nwIface.Name,
		AdapterName: nwIface.Alias,
		AdapterMAC:  nwIface.MacAddress,
		Subnet:      *nwSubnet,
		Gateway:     eni.GatewayIP.IPv4,
		DNS:         dnsConfig,
	}
	err = apis.AddTunnelHNSNetwork(ctx, nw)
	if err != nil {
		return errors.Wrapf(err, "error adding network: %s", nw.Format(apis.HNS))
	}

	return
}

func (f *eniIPFactory) destroyENICompartment(eni *types.ENI) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var nwIface *iface.Interface
	_ = wait.PollImmediateUntil(2*time.Second, func() (bool, error) {
		nwIface, err = iface.GetInterfaceByMAC(eni.MAC, true)
		if err != nil && !strings.Contains(err.Error(), "no interface with given MAC") {
			err = nil
			return false, nil
		}
		return true, nil
	}, ctx.Done())
	if err != nil {
		return errors.Wrapf(err, "error getting interface by mac %s", eni.MAC)
	}
	// go away if not found
	if nwIface == nil {
		return
	}

	var nw = &apis.Network{
		Name: nwIface.Name,
	}
	err = apis.DeleteHNSNetwork(ctx, nw)
	if err != nil {
		return errors.Wrap(err, "error deleting network")
	}

	return
}
