//go:build windows
// +build windows

package apis

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/Microsoft/hcsshim"
	"github.com/Microsoft/hcsshim/hcn"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/AliyunContainerService/terway/pkg/windows/ip"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
)

// GetDefaultNetworkGateway returns the default gateway address of the given subnet.
func GetDefaultNetworkGateway(subnet *net.IPNet) net.IP {
	if subnet == nil {
		return nil
	}
	var ipv4Net = ip.FromIPNet(subnet)
	return (ipv4Net.IP + 1).ToIP()
}

type Network struct {
	// Type specifies the type of the network.
	Type string

	// Name specifies the name of the network.
	Name string

	// AdapterName specifies the physical adapter name of the network.
	AdapterName string

	// AdapterMAC specifies the physical adapter MAC of the network.
	AdapterMAC string

	// Subnet specifies the allocated IP range of the network.
	Subnet net.IPNet

	// DNS specifies the DNS configuration of the network.
	DNS types.DNS

	// MTU specifies the size of MTU in network,
	// it's optional, default is `1500`.
	MTU int

	// Gateway specifies the IP address of the next hop for the network,
	// it's optional, default is the first index IP of the subnet.
	Gateway net.IP

	// ID specifies the id of the network,
	// it's observed at creation.
	ID string

	// refAtCreation refers the network at creation.
	refAtCreation interface{}
}

func (nw *Network) isValid() bool {
	return nw != nil && (nw.Name != "" || nw.ID != "")
}

func (nw *Network) getMTU() int {
	if nw.MTU == 0 {
		nw.MTU = 1500
	}
	return nw.MTU
}

func (nw *Network) asHNSNetwork() *hcsshim.HNSNetwork {
	var r = &hcsshim.HNSNetwork{
		Name:          nw.Name,
		Type:          nw.Type,
		DNSSuffix:     ToCommaList(nw.DNS.Search),
		DNSServerList: ToCommaList(nw.DNS.Nameservers),
	}
	if nw.AdapterName != "" {
		r.NetworkAdapterName = nw.AdapterName
	}
	if len(nw.Subnet.IP) != 0 {
		r.Subnets = []hcsshim.Subnet{
			{
				AddressPrefix:  nw.Subnet.String(),
				GatewayAddress: nw.Gateway.String(),
			},
		}
	}
	return r
}

func (nw *Network) asHCNNetwork() *hcn.HostComputeNetwork {
	var r = &hcn.HostComputeNetwork{
		SchemaVersion: hcn.V2SchemaVersion(),
		Name:          nw.Name,
		Type:          hcn.NetworkType(nw.Type),
		Dns: hcn.Dns{
			Domain:     nw.DNS.Domain,
			Search:     nw.DNS.Search,
			ServerList: nw.DNS.Nameservers,
			Options:    nw.DNS.Options,
		},
	}
	if nw.AdapterName != "" {
		r.Policies = []hcn.NetworkPolicy{
			{
				Type: hcn.NetAdapterName,
				Settings: bprintf(`{"NetworkAdapterName":"%s"}`,
					nw.AdapterName),
			},
		}
	}
	if len(nw.Subnet.IP) != 0 {
		r.Ipams = []hcn.Ipam{
			{
				Subnets: []hcn.Subnet{
					{
						IpAddressPrefix: nw.Subnet.String(),
						Routes: []hcn.Route{
							{
								NextHop:           nw.Gateway.String(),
								DestinationPrefix: "0.0.0.0/0",
							},
						},
					},
				},
			},
		}
	}
	return r
}

func (nw *Network) Format(version APIVersion) string {
	var r interface{}
	switch version {
	case HCN:
		r = nw.asHCNNetwork()
	default:
		r = nw.asHNSNetwork()
	}
	return utils.JSONStr(r)
}

// GetHNSNetworkByName returns the HNS v1 network obj by the given name.
func GetHNSNetworkByName(ctx context.Context, name string) (*hcsshim.HNSNetwork, error) {
	return hcsshim.GetHNSNetworkByName(name)
}

// GetHNSNetworkIDByName returns the HNS v1 network ID with given name.
func GetHNSNetworkIDByName(ctx context.Context, name string) (string, error) {
	var nw, err = GetHNSNetworkByName(ctx, name)
	if err != nil {
		return "", err
	}
	return nw.Id, nil
}

// HNSNetworkDecorator is a function to decorate the given HNS v1 network object before creating.
type HNSNetworkDecorator func(*hcsshim.HNSNetwork) error

// AddHNSNetwork parses the given Network to a target HNS v1 network object,
// and brings it up.
func AddHNSNetwork(ctx context.Context, i *Network, decorates ...HNSNetworkDecorator) error {
	if !i.isValid() {
		return errors.New("invalid HNSNetwork configuration")
	}

	// get network
	var nw, err = hcsshim.GetHNSNetworkByName(i.Name)
	if err != nil {
		if !hcsshim.IsNotExist(err) {
			return errors.Wrapf(err, "failed to get HNSNetwork %s", i.Name)
		}
	}
	if nw != nil {
		// delete if the existing network is corrupted
		if IsHNSNetworkCorrupted(nw, i.AdapterName, i.AdapterMAC, i.Type, i.Subnet) {
			if err := deleteHNSNetwork(nw); err != nil {
				return errors.Wrapf(err, "failed to delete corrupted HNSNetwork %s", i.Name)
			}
			nw = nil
		}
	}

	// create network if not found
	if nw == nil {
		nw = i.asHNSNetwork()
		for _, decorate := range decorates {
			if decorate == nil {
				continue
			}
			if err := decorate(nw); err != nil {
				return errors.Wrapf(err, "failed to decorate HNSNetwork %s", i.Name)
			}
		}

		// create network
		var nwCreated *hcsshim.HNSNetwork
		nwCreated, err = nw.Create()
		if err != nil {
			if !(strings.Contains(err.Error(), "A network with this name already exists") ||
				strings.Contains(err.Error(), "The function attempted to use a name that is reserved for use by another transaction")) {
				return errors.Wrapf(err, "failed to create a new HNSNetwork %s: %s", i.Name, toJson(nw))
			}
			// network has been created
			var condErr error
			err = wait.PollImmediateUntil(500*time.Millisecond, func() (bool, error) {
				nwCreated, condErr = hcsshim.GetHNSNetworkByName(i.Name)
				if condErr != nil {
					return false, nil
				}
				return true, nil
			}, ctx.Done())
			if err != nil {
				if condErr == nil {
					condErr = err
				}
				return errors.Wrapf(condErr, "faield to get the newly created HNSNetwork %s", i.Name)
			}
		} else {
			i.refAtCreation = nwCreated
		}
		nw = nwCreated
	}

	// feedback
	i.ID = nw.Id
	return nil
}

// DeleteHNSNetwork deletes the target HNS v1 network object related to the given Network.
func DeleteHNSNetwork(ctx context.Context, i *Network) error {
	if !i.isValid() {
		return nil
	}

	// get network
	var nw *hcsshim.HNSNetwork
	if i.ID != "" {
		var err error
		nw, err = hcsshim.GetHNSNetworkByID(i.ID)
		if err != nil {
			if !hcsshim.IsNotExist(err) {
				return errors.Wrapf(err, "failed to get HNSNetwork %s", i.ID)
			}
		}
	} else {
		var err error
		nw, err = hcsshim.GetHNSNetworkByName(i.Name)
		if err != nil {
			if !hcsshim.IsNotExist(err) {
				return errors.Wrapf(err, "failed to get HNSNetwork %s", i.Name)
			}
		}
	}

	// delete
	return deleteHNSNetwork(nw)
}

// DeleteHNSNetworkIfEmpty deletes the target HNS v1 network object related to the given Network if no attaching endpoints.
func DeleteHNSNetworkIfEmpty(ctx context.Context, i *Network) error {
	if !i.isValid() {
		return nil
	}

	// get network
	var nw *hcsshim.HNSNetwork
	if i.ID != "" {
		var err error
		nw, err = hcsshim.GetHNSNetworkByID(i.ID)
		if err != nil {
			if !hcsshim.IsNotExist(err) {
				return errors.Wrapf(err, "failed to get HNSNetwork %s", i.ID)
			}
		}
	} else {
		var err error
		nw, err = hcsshim.GetHNSNetworkByName(i.Name)
		if err != nil {
			if !hcsshim.IsNotExist(err) {
				return errors.Wrapf(err, "failed to get HNSNetwork %s", i.Name)
			}
		}
	}
	if nw == nil {
		return nil
	}

	// list endpoint
	var eps, err = hcsshim.HNSListEndpointRequest()
	if err != nil {
		return errors.Wrapf(err, "failed to list HNSEndpoint of HNSNetwork %s", nw.Id)
	}

	// skip deleting if found
	for i := range eps {
		if strings.EqualFold(eps[i].VirtualNetwork, nw.Id) {
			return nil
		}
	}
	// otherwise, delete it
	return deleteHNSNetwork(nw)
}

// deleteHNSNetwork deletes the HNS v1 network object without panic.
func deleteHNSNetwork(nw *hcsshim.HNSNetwork) error {
	defer func() { _ = recover() }()
	if nw == nil {
		return nil
	}

	// NB(thxCode): we don't need to cleanup the related endpoints,
	// because they can be deleted in cascade when removing the network.
	// however, the associated policies might be leak,
	// so we must remove all policies if possible.
	var eps, _ = hcsshim.HNSListEndpointRequest()
	var epRefSet = sets.NewString()
	for idx := range eps {
		var ep = &eps[idx]
		if strings.EqualFold(eps[idx].VirtualNetwork, nw.Id) {
			epRefSet.Insert("/endpoints/" + ep.Id)                  // raw
			epRefSet.Insert("/endpoints/" + strings.ToLower(ep.Id)) // lowercase
			epRefSet.Insert("/endpoints/" + strings.ToUpper(ep.Id)) // uppercase
		}
	}
	if epRefSet.Len() > 0 {
		var ps, _ = hcsshim.HNSListPolicyListRequest()
		for idx := range ps {
			var p = &ps[idx]
			if epRefSet.HasAny(p.EndpointReferences...) {
				_, _ = p.Delete()
			}
		}
	}

	// delete network
	if _, err := nw.Delete(); err != nil {
		if !hcsshim.IsNotExist(err) {
			return errors.Wrapf(err, "failed to delete HNSNetwork %s", nw.Name)
		}
	}
	return nil
}

// GetHCNNetworkByName returns the HNS v2(HCN api) network object by the given name.
func GetHCNNetworkByName(ctx context.Context, name string) (*hcn.HostComputeNetwork, error) {
	return hcn.GetNetworkByName(name)
}

// GetHCNNetworkIDByName returns the HNS v2(HCN api) network ID by the given name.
func GetHCNNetworkIDByName(ctx context.Context, name string) (string, error) {
	var nw, err = GetHCNNetworkByName(ctx, name)
	if err != nil {
		return "", err
	}
	return nw.Id, nil
}

// HCNNetworkDecorator is a function to decorate the given HNS v2(HCN api) network object before creating.
type HCNNetworkDecorator func(*hcn.HostComputeNetwork) error

// AddHCNNetwork parses the given Network to a target HNS v2(HCN api) network object,
// and brings it up.
func AddHCNNetwork(ctx context.Context, i *Network, decorates ...HCNNetworkDecorator) error {
	if !i.isValid() {
		return errors.New("invalid HostComputeNetwork configuration")
	}

	// get network
	var nw, err = hcn.GetNetworkByName(i.Name)
	if err != nil {
		if !hcn.IsNotFoundError(err) {
			return errors.Wrapf(err, "failed to get HostComputeNetwork %s", i.Name)
		}
	}
	if nw != nil {
		// delete if the existing network is corrupted
		if IsHCNNetworkCorrupted(nw, i.AdapterName, i.AdapterMAC, i.Type, i.Subnet) {
			if err := deleteHCNNetwork(nw); err != nil {
				return errors.Wrapf(err, "failed to delete corrupted HostComputeNetwork %s", i.Name)
			}
			nw = nil
		}
	}

	// create network if not found
	if nw == nil {
		nw = i.asHCNNetwork()
		for _, decorate := range decorates {
			if decorate == nil {
				continue
			}
			if err := decorate(nw); err != nil {
				return errors.Wrapf(err, "failed to decorate HostComputeNetwork %s", i.Name)
			}
		}

		// create network
		var nwCreated *hcn.HostComputeNetwork
		nwCreated, err = nw.Create()
		if err != nil {
			if !(hcn.CheckErrorWithCode(err, 0x803b0010) ||
				hcn.CheckErrorWithCode(err, 0x1a90)) {
				return errors.Wrapf(err, "failed to create a new HostComputeNetwork %s: %s", i.Name, toJson(nw))
			}
			// network has been created
			var condErr error
			err = wait.PollImmediateUntil(500*time.Millisecond, func() (bool, error) {
				nwCreated, condErr = hcn.GetNetworkByName(i.Name)
				if condErr != nil {
					return false, nil
				}
				return true, nil
			}, ctx.Done())
			if err != nil {
				if condErr == nil {
					condErr = err
				}
				return errors.Wrapf(condErr, "faield to get the newly created HostComputeNetwork %s", i.Name)
			}
		} else {
			i.refAtCreation = nwCreated
		}
		nw = nwCreated
	}

	// feedback
	i.ID = nw.Id
	return nil
}

// DeleteHCNNetwork deletes the target HNS v2(HCN api) network object related to the given Network.
func DeleteHCNNetwork(ctx context.Context, i *Network) error {
	if !i.isValid() {
		return nil
	}

	// get network
	var nw *hcn.HostComputeNetwork
	if i.ID != "" {
		var err error
		nw, err = hcn.GetNetworkByID(i.ID)
		if err != nil {
			if !hcn.IsNotFoundError(err) {
				return errors.Wrapf(err, "failed to get HostComputeNetwork %s", i.ID)
			}
		}
	} else {
		var err error
		nw, err = hcn.GetNetworkByName(i.Name)
		if err != nil {
			if !hcn.IsNotFoundError(err) {
				return errors.Wrapf(err, "failed to get HostComputeNetwork %s", i.Name)
			}
		}
	}

	// delete
	return deleteHCNNetwork(nw)
}

// DeleteHCNNetworkIfEmpty deletes the target HNS v2(HCN api) network object related to the given Network if no attaching endpoints.
func DeleteHCNNetworkIfEmpty(ctx context.Context, i *Network) error {
	if !i.isValid() {
		return nil
	}

	// get network
	var nw *hcn.HostComputeNetwork
	if i.ID != "" {
		var err error
		nw, err = hcn.GetNetworkByID(i.ID)
		if err != nil {
			if !hcn.IsNotFoundError(err) {
				return errors.Wrapf(err, "failed to get HostComputeNetwork %s", i.ID)
			}
		}
	} else {
		var err error
		nw, err = hcn.GetNetworkByName(i.Name)
		if err != nil {
			if !hcn.IsNotFoundError(err) {
				return errors.Wrapf(err, "failed to get HostComputeNetwork %s", i.Name)
			}
		}
	}
	if nw == nil {
		return nil
	}

	// list endpoint
	var eps, err = hcn.ListEndpointsOfNetwork(nw.Id)
	if err != nil {
		return errors.Wrapf(err, "failed to list HostComputeEndpoint of HostComputeNetwork %s", nw.Id)
	}

	// skip deleting if found
	if len(eps) != 0 {
		return nil
	}
	// otherwise, delete it
	return deleteHCNNetwork(nw)
}

// deleteHCNNetwork deletes the HNS v2(HCN api) network object without panic.
func deleteHCNNetwork(nw *hcn.HostComputeNetwork) error {
	defer func() { _ = recover() }()
	if nw == nil {
		return nil
	}

	// NB(thxCode): we don't need to cleanup the related endpoints,
	// because they can be deleted in cascade when removing the network.
	// however, the associated policies might be leak,
	// so we must remove all policies if possible.
	var eps, _ = hcn.ListEndpointsOfNetwork(nw.Id)
	var epRefSet = sets.NewString()
	for idx := range eps {
		var ep = &eps[idx]
		epRefSet.Insert(ep.Id)                  // raw
		epRefSet.Insert(strings.ToLower(ep.Id)) // lowercase
		epRefSet.Insert(strings.ToUpper(ep.Id)) // uppercase
	}
	if epRefSet.Len() > 0 {
		var ps, _ = hcn.ListLoadBalancers()
		for idx := range ps {
			var p = &ps[idx]
			if epRefSet.HasAny(p.HostComputeEndpoints...) {
				_ = p.Delete()
			}
		}
	}

	// delete network
	if err := nw.Delete(); err != nil {
		if !hcn.IsNotFoundError(err) {
			return errors.Wrapf(err, "failed to delete HostComputeNetwork %s", nw.Name)
		}
	}
	return nil
}
