//go:build windows
// +build windows

package apis

import (
	"context"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/Microsoft/hcsshim"
	"github.com/Microsoft/hcsshim/hcn"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/AliyunContainerService/terway/pkg/windows/powershell"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
)

// Endpoint describes the information of an HNS v1/v2(HCN api) endpoint object.
type Endpoint struct {
	// Name specifies the name of the endpoint.
	Name string

	// Address specifies the IP address of the endpoint, included IP mask.
	Address net.IPNet

	// MAC specifies the MAC address of the endpoint,
	// it's optional.
	MAC string

	// DNS specifies the DNS configuration of the endpoint.
	DNS types.DNS

	// MTU specifies the size of MTU in network,
	// it's optional, default is `1500`.
	MTU int

	// Gateway specifies the IP address of the next hop for the endpoint.
	Gateway net.IP

	// ID specifies the id of the endpoint,
	// it's observed at creation.
	ID string
}

func (ep *Endpoint) isValid() bool {
	return ep != nil && (ep.Name != "" || ep.ID != "")
}

func (ep *Endpoint) getMTU() int {
	if ep.MTU == 0 {
		ep.MTU = 1500
	}
	return ep.MTU
}

func (ep *Endpoint) asHNSEndpoint(networkID string) *hcsshim.HNSEndpoint {
	var r = &hcsshim.HNSEndpoint{
		Name:           ep.Name,
		MacAddress:     ep.MAC,
		VirtualNetwork: networkID,
		DNSSuffix:      ToCommaList(ep.DNS.Search),
		DNSServerList:  ToCommaList(ep.DNS.Nameservers),
		IPAddress:      ep.Address.IP,
	}
	if len(ep.Gateway) != 0 {
		r.GatewayAddress = ep.Gateway.String()
	}
	return r
}

func (ep *Endpoint) asHCNEndpoint(networkID string) *hcn.HostComputeEndpoint {
	var r = &hcn.HostComputeEndpoint{
		SchemaVersion:      hcn.V2SchemaVersion(),
		Name:               ep.Name,
		MacAddress:         ep.MAC,
		HostComputeNetwork: networkID,
		Dns: hcn.Dns{
			Domain:     ep.DNS.Domain,
			Search:     ep.DNS.Search,
			ServerList: ep.DNS.Nameservers,
			Options:    ep.DNS.Options,
		},
		IpConfigurations: []hcn.IpConfig{
			{
				IpAddress: ep.Address.IP.String(),
			},
		},
	}
	if len(ep.Gateway) != 0 {
		var nextHop = ep.Gateway.String()
		var dstPrefix = "0.0.0.0/0"
		if ep.Gateway.To4() == nil {
			dstPrefix = "::/0"
		}
		r.Routes = []hcn.Route{
			{
				NextHop:           nextHop,
				DestinationPrefix: dstPrefix,
			},
		}
	}
	return r
}

func (ep *Endpoint) Format(networkID string, version APIVersion) string {
	var r interface{}
	switch version {
	case HCN:
		r = ep.asHCNEndpoint(networkID)
	default:
		r = ep.asHNSEndpoint(networkID)
	}
	return utils.JSONStr(r)
}

// HNSEndpointDecorator is a function to decorate the given HNS v1 endpoint object before creating.
type HNSEndpointDecorator func(*hcsshim.HNSEndpoint) error

// ApplyHNSEndpointOutboundNatPolicy applies the outbound NAT policy to the creating HNS v1 endpoint object.
func ApplyHNSEndpointOutboundNatPolicy(exceptionIPNets ...*net.IPNet) HNSEndpointDecorator {
	return func(ep *hcsshim.HNSEndpoint) error {
		if ep == nil {
			return nil
		}
		var exceptions []string
		for i := range exceptionIPNets {
			if exceptionIPNets[i] == nil {
				continue
			}
			exceptions = append(exceptions, exceptionIPNets[i].String())
		}

		if len(exceptionIPNets) == 0 {
			ep.Policies = append(ep.Policies,
				[]byte(`{"Type":"OutBoundNAT"}`))
		} else {
			ep.Policies = append(ep.Policies,
				bprintf(`{"Type":"OutBoundNAT","ExceptionList":["%s"]}`,
					strings.Join(exceptions, `","`)))
		}
		return nil
	}
}

// ApplyHNSEndpointEncapsulatedRoutePolicy applies the destination encapsulated route policy to the creating HNS v1 endpoint object.
func ApplyHNSEndpointEncapsulatedRoutePolicy(destinationIPNets ...*net.IPNet) HNSEndpointDecorator {
	return func(ep *hcsshim.HNSEndpoint) error {
		if ep == nil {
			return nil
		}

		for i := range destinationIPNets {
			if destinationIPNets[i] == nil {
				continue
			}
			ep.Policies = append(ep.Policies,
				bprintf(`{"Type":"ROUTE","NeedEncap":true,"DestinationPrefix":"%s"}`,
					destinationIPNets[i].String()))
		}
		return nil
	}
}

// ApplyHNSEndpointPortMappingPolicy applies the port mapping policy to the creating HNS v1 endpoint object.
func ApplyHNSEndpointPortMappingPolicy(protocol string, containerPort int, hostPort int, hostIP string) HNSEndpointDecorator {
	return func(ep *hcsshim.HNSEndpoint) error {
		// skip the invalid protocol mapping
		var _, err = GetHCNEndpointAvailableProtocolEnum(protocol)
		if err != nil {
			return err
		}
		ep.Policies = append(ep.Policies,
			bprintf(`{"Type":"NAT","InternalPort":%d,"ExternalPort":%d,"Protocol":"%s"}`,
				containerPort, hostPort, protocol))
		return nil
	}
}

// ApplyHNSEndpointLoopbackDsrPolicy applies the loopback DSR policy to the creating HNS v1 endpoint object.
func ApplyHNSEndpointLoopbackDsrPolicy() HNSEndpointDecorator {
	return func(ep *hcsshim.HNSEndpoint) error {
		if !IsDsrSupported() {
			return nil
		}
		if ep == nil {
			return nil
		}
		var addr = ep.IPAddress.String()
		ep.Policies = append(ep.Policies,
			bprintf(`{"Type":"OutBoundNAT","Destinations":["%s"]}`,
				addr))
		return nil
	}
}

// AddHNSEndpoint parses the given Endpoint to a target HNS v1 endpoint object,
// and adds it to given netns, or returns error.
func AddHNSEndpoint(ctx context.Context, i *Endpoint, netns, networkID, containerID string, decorates ...HNSEndpointDecorator) error {
	if netns == "" || networkID == "" || containerID == "" ||
		!i.isValid() {
		return errors.Errorf("invalid HNSEndpoint configuration")
	}

	var attach = func(ep *hcsshim.HNSEndpoint, isNewlyCreatedEndpoint bool) error {
		var err = hcsshim.HotAttachEndpoint(containerID, ep.Id)
		if err != nil {
			if hcsshim.ErrComputeSystemDoesNotExist == err || // if container is not started
				syscall.Errno(0x803B0014) == err { // or already attached
				return nil
			}
			if isNewlyCreatedEndpoint {
				// avoid to leak endpoint
				if _, err := deleteHNSEndpoint(ep, containerID); err != nil {
					return errors.Wrapf(err, "failed to remove the new HNSEndpoint %s after failed attaching container %s",
						ep.Name, containerID)
				}
			}
			return errors.Wrapf(err, "failed to attach container %s to HNSEndpoint %s",
				containerID, ep.Id)
		}
		return nil
	}

	return addHNSEndpoint(ctx, i, netns, networkID, attach, decorates...)
}

// AddHNSHostEndpoint parses the given Endpoint to a target HNS v1 endpoint object,
// and adds it to given netns, or returns error.
func AddHNSHostEndpoint(ctx context.Context, i *Endpoint, netns, networkID, containerID string, decorates ...HNSEndpointDecorator) error {
	if netns == "" || networkID == "" || containerID == "" ||
		!i.isValid() {
		return errors.Errorf("invalid HNSEndpoint configuration")
	}

	var attach = func(ep *hcsshim.HNSEndpoint, isNewlyCreatedEndpoint bool) error {
		var sandboxContainerID = GetHNSEndpointSandboxContainerID(netns, containerID)
		var compartmentID, err = getNetCompartment(sandboxContainerID)
		if err != nil {
			return errors.Wrapf(err, "failed to get net compartment ID of container %s", sandboxContainerID)
		}

		var condErr error
		err = wait.PollImmediateUntil(500*time.Millisecond, func() (bool, error) {
			condErr = ep.HostAttach(compartmentID)
			if condErr == nil ||
				strings.Contains(condErr.Error(), "This endpoint is already attached to the switch.") {
				return true, nil
			}
			return false, nil
		}, ctx.Done())
		if err != nil {
			if condErr == nil {
				condErr = err
			}
			return errors.Wrapf(condErr, "failed to attach host HNSEndpoint %s", ep.Name)
		}
		return nil
	}

	return addHNSEndpoint(ctx, i, netns, networkID, attach, decorates...)
}

// attachHNSEndpoint attaches the given HNS v1 endpoint object.
type attachHNSEndpoint func(ep *hcsshim.HNSEndpoint, isNewlyCreatedEndpoint bool) error

// addHNSEndpoint adds the HNS v1 endpoint object without panic,
// and returns the related HNS v1 network ID.
func addHNSEndpoint(ctx context.Context, i *Endpoint, netns, networkID string, attach attachHNSEndpoint, decorates ...HNSEndpointDecorator) error {
	// get endpoint
	ep, err := hcsshim.GetHNSEndpointByName(i.Name)
	if err != nil {
		if !hcsshim.IsNotExist(err) {
			return errors.Wrapf(err, "failed to get HNSEndpoint %s", i.Name)
		}
	}
	if !IsHNSEndpointSandbox(netns) { // for shared endpoint, we expect that the endpoint already exists
		if ep == nil {
			return errors.Wrapf(err, "failed to get shared HNSEndpoint %s", i.Name)
		}
	}
	if ep != nil {
		// delete if the existing endpoint is corrupted
		if IsHNSEndpointCorrupted(ep, networkID, i.Address.IP) {
			if _, err := ep.Delete(); err != nil {
				return errors.Wrapf(err, "failed to delete corrupted HNSEnpoint %s", i.Name)
			}
			ep = nil
		}
	}

	// create endpoint if not found
	var isNewlyCreatedEndpoint = true
	if ep == nil {
		ep = i.asHNSEndpoint(networkID)
		for _, decorate := range decorates {
			if decorate == nil {
				continue
			}
			if err := decorate(ep); err != nil {
				return errors.Wrapf(err, "failed to decorate HNSEndpoint %s", i.Name)
			}
		}

		// create endpoint
		var epCreated *hcsshim.HNSEndpoint
		epCreated, err = ep.Create()
		if err != nil {
			isNewlyCreatedEndpoint = false
			if !strings.Contains(err.Error(), "already exists") {
				return errors.Wrapf(err, "failed to create a new HNSEnpoint %s: %s", i.Name, toJson(ep))
			}
			// endpoint has been created
			var condErr error
			err = wait.PollImmediateUntil(500*time.Millisecond, func() (bool, error) {
				epCreated, condErr = hcsshim.GetHNSEndpointByName(i.Name)
				if condErr != nil {
					return false, nil
				}
				return true, nil
			}, ctx.Done())
			if err != nil {
				if condErr == nil {
					condErr = err
				}
				return errors.Wrapf(condErr, "faield to get the newly created HNSEnpoint %s", i.Name)
			}
		}
		ep = epCreated
	}

	// attach container to endpoint
	if attach != nil {
		err = attach(ep, isNewlyCreatedEndpoint)
		if err != nil {
			return err
		}
	}

	// feedback
	i.ID = ep.Id
	i.MAC = ep.MacAddress
	return nil
}

// DeleteHNSEndpoint deletes the target HNS v1 endpoint object related to the given Endpoint,
// and returns the related HNS v1 network ID.
func DeleteHNSEndpoint(ctx context.Context, i *Endpoint, netns, containerID string) (string, error) {
	if netns == "" || containerID == "" ||
		!i.isValid() {
		return "", nil
	}

	// get endpoint
	var ep *hcsshim.HNSEndpoint
	if i.ID != "" {
		var err error
		ep, err = hcsshim.GetHNSEndpointByID(i.ID)
		if err != nil {
			// return directly if not found
			if hcsshim.IsNotExist(err) {
				return "", nil
			}
			return "", errors.Wrapf(err, "failed to get HNSEndpoint %s", i.ID)
		}
	} else {
		var err error
		ep, err = hcsshim.GetHNSEndpointByName(i.Name)
		if err != nil {
			// return directly if not found
			if hcsshim.IsNotExist(err) {
				return "", nil
			}
			return "", errors.Wrapf(err, "failed to get HNSEndpoint %s", i.Name)
		}
	}

	if !IsHNSEndpointSandbox(netns) { // for shared endpoint, detach it from the container
		_ = ep.ContainerDetach(containerID)
		return ep.VirtualNetwork, nil
	}

	// delete
	return deleteHNSEndpoint(ep, containerID)
}

// deleteHNSEndpoint deletes the HNS v1 endpoint object without panic,
// and returns the related HNS v1 network ID.
func deleteHNSEndpoint(ep *hcsshim.HNSEndpoint, containerID string) (string, error) {
	defer func() { _ = recover() }()
	if ep == nil {
		return "", nil
	}

	// detach container from endpoint
	if containerID != "" {
		_ = hcsshim.HotDetachEndpoint(containerID, ep.Id)
	}

	// delete endpoint
	_ = ep.HostDetach()
	if _, err := ep.Delete(); err != nil {
		if !hcsshim.IsNotExist(err) {
			return ep.VirtualNetwork, err
		}
	}
	return ep.VirtualNetwork, nil
}

// HCNEndpointDecorator is a function to decorate the given HNS v2(HCN api) endpoint object before creating.
type HCNEndpointDecorator func(*hcn.HostComputeEndpoint) error

// ApplyHCNEndpointOutboundNatPolicy applies the outbound NAT policy to the creating HNS v2(HCN api) endpoint object.
func ApplyHCNEndpointOutboundNatPolicy(exceptionIPNets ...*net.IPNet) HCNEndpointDecorator {
	return func(ep *hcn.HostComputeEndpoint) error {
		if ep == nil {
			return nil
		}
		var exceptions []string
		for i := range exceptionIPNets {
			if exceptionIPNets[i] == nil {
				continue
			}
			exceptions = append(exceptions, exceptionIPNets[i].String())
		}

		if len(exceptions) == 0 {
			ep.Policies = append(ep.Policies, hcn.EndpointPolicy{
				Type:     hcn.OutBoundNAT,
				Settings: []byte{},
			})
		} else {
			ep.Policies = append(ep.Policies, hcn.EndpointPolicy{
				Type: hcn.OutBoundNAT,
				Settings: bprintf(`{"Exceptions":["%s"]}`,
					strings.Join(exceptions, `","`)),
			})
		}
		return nil
	}
}

// ApplyHCNEndpointEncapsulatedRoutePolicy applies the destination encapsulated route policy to the creating HNS v2(HCN api) endpoint object.
func ApplyHCNEndpointEncapsulatedRoutePolicy(destinationIPNets ...*net.IPNet) HCNEndpointDecorator {
	return func(ep *hcn.HostComputeEndpoint) error {
		if ep == nil {
			return nil
		}

		for i := range destinationIPNets {
			if destinationIPNets[i] == nil {
				continue
			}
			ep.Policies = append(ep.Policies, hcn.EndpointPolicy{
				Type: hcn.SDNRoute,
				Settings: bprintf(`{"NeedEncap":true,"DestinationPrefix":"%s"}`,
					destinationIPNets[i].String()),
			})
		}
		return nil
	}
}

// ApplyHCNEndpointPortMappingPolicy applies the port mapping policy to the creating HNS v2(HCN api) endpoint object.
func ApplyHCNEndpointPortMappingPolicy(protocol string, containerPort int, hostPort int, hostIP string) HCNEndpointDecorator {
	return func(ep *hcn.HostComputeEndpoint) error {
		// skip the invalid protocol mapping
		var pe, err = GetHCNEndpointAvailableProtocolEnum(protocol)
		if err != nil {
			return err
		}
		ep.Policies = append(ep.Policies, hcn.EndpointPolicy{
			Type: hcn.PortMapping,
			Settings: bprintf(`{"InternalPort":%d,"ExternalPort":%d,"Protocol":%d,"VIP":"%s"}`,
				containerPort, hostPort, pe, hostIP),
		})
		return nil
	}
}

// ApplyHCNEndpointLoopbackDsrPolicy applies the loopback DSR policy to the creating HNS v2(HCN api) endpoint object.
func ApplyHCNEndpointLoopbackDsrPolicy() HCNEndpointDecorator {
	return func(ep *hcn.HostComputeEndpoint) error {
		if !IsDsrSupported() {
			return nil
		}
		if ep == nil {
			return nil
		}
		var addr = ep.IpConfigurations[0].IpAddress
		ep.Policies = append(ep.Policies, hcn.EndpointPolicy{
			Type: hcn.OutBoundNAT,
			Settings: bprintf(`{"Destinations":["%s"]}`,
				addr),
		})
		return nil
	}
}

// AddHCNEndpoint parses the given Endpoint to a target HNS v2(HCN api) endpoint object,
// and adds it to given netns, or returns error.
func AddHCNEndpoint(ctx context.Context, i *Endpoint, netns, networkID string, decorates ...HCNEndpointDecorator) error {
	if netns == "" || networkID == "" ||
		!i.isValid() {
		return errors.Errorf("invalid HostComputeEndpoint configuration")
	}

	var attach = func(ep *hcn.HostComputeEndpoint, isNewlyCreatedEndpoint bool) error {
		var err = ep.NamespaceAttach(netns)
		if err != nil {
			if hcn.CheckErrorWithCode(err, 0xc037010e) || // if container is not started
				hcn.CheckErrorWithCode(err, 0x803B0014) { // or already attached
				return nil
			}
			if isNewlyCreatedEndpoint {
				// avoid to leak endpoint
				if _, err := deleteHCNEndpoint(ep, netns); err != nil {
					return errors.Wrapf(err, "failed to remove the new HostComputeEndpoint %s after adding HostComputeNamespace %s failure",
						ep.Name, netns)
				}
			}
			return errors.Wrapf(err, "failed to add HostComputeEndpoint %s to HostComputeNamespace %s",
				ep.Name, netns)
		}
		return nil
	}

	return addHCNEndpoint(ctx, i, netns, networkID, attach, decorates...)
}

// AddHCNHostEndpoint parses the given Endpoint to a target HNS v2(HCN api) endpoint object,
// and adds it to given nets, or returns error.
func AddHCNHostEndpoint(ctx context.Context, i *Endpoint, netns, networkID string, decorates ...HCNEndpointDecorator) error {
	if netns == "" || networkID == "" ||
		!i.isValid() {
		return errors.Errorf("invalid HostComputeEndpoint configuration")
	}

	var attach = func(ep *hcn.HostComputeEndpoint, isNewlyCreatedEndpoint bool) error {
		// attach gateway endpoint to host
		var condErr error
		var err = wait.PollImmediateUntil(100*time.Millisecond, func() (bool, error) {
			condErr = ep.NamespaceAttach(netns)
			if condErr == nil ||
				hcn.CheckErrorWithCode(condErr, 0x803B0014) { // if already attached
				return true, nil
			}
			return false, nil
		}, ctx.Done())
		if err != nil {
			if condErr == nil {
				condErr = err
			}
			return errors.Wrapf(condErr, "failed to attach gateway HostComputeEndpoint %s", ep.Name)
		}
		return nil
	}

	return addHCNEndpoint(ctx, i, netns, networkID, attach, decorates...)
}

// attachHCNEndpoint attaches the given HNS v2(HCN api) endpoint object.
type attachHCNEndpoint func(ep *hcn.HostComputeEndpoint, isNewlyCreatedEndpoint bool) error

// addHCNEndpoint adds the HNS v2(HCN api) endpoint object without panic,
// and returns the related HNS v2(HCN api) network ID.
func addHCNEndpoint(ctx context.Context, i *Endpoint, netns, networkID string, attach attachHCNEndpoint, decorates ...HCNEndpointDecorator) error {
	ep, err := hcn.GetEndpointByName(i.Name)
	if err != nil {
		if !hcn.IsNotFoundError(err) {
			return errors.Wrapf(err, "failed to get HostComputeEndpoint %s", i.Name)
		}
	}
	if ep != nil {
		// delete if the existing endpoint is corrupted
		if IsHCNEndpointCorrupted(ep, networkID, i.Address.IP) {
			if err := ep.Delete(); err != nil {
				return errors.Wrapf(err, "failed to delete corrupted HostComputeEndpoint %s", i.Name)
			}
			ep = nil
		}
	}

	// create endpoint if not found
	var isNewlyCreatedEndpoint = true
	if ep == nil {
		ep = i.asHCNEndpoint(networkID)
		for _, decorate := range decorates {
			if decorate == nil {
				continue
			}
			if err := decorate(ep); err != nil {
				return errors.Wrapf(err, "failed to decorate HostComputeEndpoint %s", i.Name)
			}
		}

		// create endpoint
		var epCreated *hcn.HostComputeEndpoint
		epCreated, err = ep.Create()
		if err != nil {
			isNewlyCreatedEndpoint = false
			if !strings.Contains(err.Error(), "already exists") {
				return errors.Wrapf(err, "failed to create a new HostComputeEndpoint %s: %s", i.Name, toJson(ep))
			}
			// endpoint has been created
			var condErr error
			err = wait.PollImmediateUntil(100*time.Millisecond, func() (bool, error) {
				epCreated, condErr = hcn.GetEndpointByName(i.Name)
				if condErr != nil {
					return false, nil
				}
				return true, nil
			}, ctx.Done())
			if err != nil {
				if condErr == nil {
					condErr = err
				}
				return errors.Wrapf(condErr, "faield to get the newly created HostComputeEndpoint %s", i.Name)
			}
		}
		ep = epCreated
	}

	// add to namespace
	if attach != nil {
		err = attach(ep, isNewlyCreatedEndpoint)
		if err != nil {
			return err
		}
	}

	// feedback
	i.ID = ep.Id
	i.MAC = ep.MacAddress
	return nil
}

// DeleteHCNEndpoint deletes the target HNS v2(HCN api) endpoint object related to the given Endpoint,
// and returns the related HNS v2(HCN api) network ID.
func DeleteHCNEndpoint(ctx context.Context, i *Endpoint, netns string) (string, error) {
	if netns == "" ||
		!i.isValid() {
		return "", nil
	}

	// get endpoint
	var ep *hcn.HostComputeEndpoint
	if i.ID != "" {
		var err error
		ep, err = hcn.GetEndpointByID(i.ID)
		if err != nil {
			if hcn.IsNotFoundError(err) {
				return "", nil
			}
			return "", errors.Wrapf(err, "failed to get HostComputeEndpoint %s", i.ID)
		}
	} else {
		var err error
		ep, err = hcn.GetEndpointByName(i.Name)
		if err != nil {
			if hcn.IsNotFoundError(err) {
				return "", nil
			}
			return "", errors.Wrapf(err, "failed to get HostComputeEndpoint %s", i.Name)
		}
	}

	// delete
	return deleteHCNEndpoint(ep, netns)
}

// deleteHCNEndpoint deletes the HNS v2(HCN api) endpoint object without panic,
// and returns the related HNS v2(HCN api) network ID.
func deleteHCNEndpoint(ep *hcn.HostComputeEndpoint, netns string) (string, error) {
	defer func() { _ = recover() }()
	if ep == nil {
		return "", nil
	}

	// remove endpoint from namespace
	if netns == "" {
		netns = ep.HostComputeNamespace
	}
	_ = hcn.RemoveNamespaceEndpoint(netns, ep.Id)

	// delete endpoint
	if err := ep.Delete(); err != nil {
		if !hcn.IsNotFoundError(err) {
			return ep.HostComputeNetwork, err
		}
	}
	return ep.HostComputeNetwork, nil
}

func getNetCompartment(sandboxContainerID string) (uint16, error) {
	var s = fmt.Sprintf("Get-NetCompartment | Where-Object { $_.CompartmentDescription -eq '\\Container_%s' } | Select-Object -Property CompartmentId", sandboxContainerID)
	var d = struct {
		CompartmentId int `json:"CompartmentId"`
	}{CompartmentId: -1}

	if err := powershell.RunCommandWithJsonResult(s, &d); err != nil {
		return 0, err
	}
	if d.CompartmentId < 0 {
		return 0, errors.Errorf("no net compartment with given container ID %s found", sandboxContainerID)
	}
	return uint16(d.CompartmentId), nil
}
