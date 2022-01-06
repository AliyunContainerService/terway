//go:build windows
// +build windows

package apis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim"
	"github.com/Microsoft/hcsshim/hcn"
	"github.com/pkg/errors"
)

type APIVersion string

const (
	HNS    APIVersion = "v1"
	HNS_V2 APIVersion = "v2"
	HCN               = HNS_V2
)

const hnsPauseContainerNetNS = "none"

func toJson(elem interface{}) string {
	var bs, err = json.Marshal(elem)
	if err != nil {
		return ""
	}
	return string(bs)
}

func bprintf(format string, a ...interface{}) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(format, a...))
}

func ToCommaList(elems []string) string {
	return strings.Join(elems, ",")
}

func FromCommaList(s string) []string {
	return strings.Split(s, ",")
}

func IsHNSEndpointCorrupted(ep *hcsshim.HNSEndpoint, network string, addr net.IP) bool {
	// not in the same network
	if !strings.EqualFold(ep.VirtualNetwork, network) {
		return true
	}
	// or doesn't have same address
	return !ep.IPAddress.Equal(addr)
}

func IsHNSNetworkCorrupted(nw *hcsshim.HNSNetwork, nwAdapterName string, nwAdapterMac string, nwType string, nwSubnet net.IPNet) bool {
	// not in the same adapter name
	if nwAdapterName != "" {
		if !strings.EqualFold(nw.NetworkAdapterName, nwAdapterName) {
			return true
		}
	}

	// not in the same type
	if !strings.EqualFold(nw.Type, nwType) {
		return true
	}

	// or doesn't have same subnet
	var addressPrefix = nwSubnet.String()
	for idxSubnet := range nw.Subnets {
		var sn = &nw.Subnets[idxSubnet]
		if sn.AddressPrefix == addressPrefix {
			return false
		}
	}
	return true
}

func ParseHNSNetworkManagementIP(nw *hcsshim.HNSNetwork) net.IP {
	if nw == nil || nw.ManagementIP == "" {
		return nil
	}
	return net.ParseIP(nw.ManagementIP)
}

func IsHNSEndpointSandbox(netns string) bool {
	return netns == hnsPauseContainerNetNS
}

func GetHNSEndpointSandboxContainerID(netns string, containerID string) string {
	if len(netns) != 0 && netns != hnsPauseContainerNetNS {
		splits := strings.SplitN(netns, ":", 2)
		if len(splits) == 2 {
			return splits[1]
		}
	}
	return containerID
}

func IsHCNSupported(netns string) bool {
	var _, err = guid.FromString(netns)
	return err == nil
}

func IsHCNEndpointCorrupted(ep *hcn.HostComputeEndpoint, network string, addr net.IP) bool {
	// not in the same network
	if !strings.EqualFold(ep.HostComputeNetwork, network) {
		return true
	}
	// or doesn't have same address
	var ipAddress = addr.String()
	for idxIPConfig := range ep.IpConfigurations {
		var ipConfig = &ep.IpConfigurations[idxIPConfig]
		if ipConfig.IpAddress == ipAddress {
			return false
		}
	}
	return true
}

func IsHCNNetworkCorrupted(nw *hcn.HostComputeNetwork, nwAdapterName string, nwAdapterMac string, nwType string, nwSubnet net.IPNet) bool {
	// not in the same adapter name
	if nwAdapterName != "" {
		var policy *hcn.NetworkPolicy
		for i := range nw.Policies {
			if nw.Policies[i].Type == hcn.NetAdapterName {
				policy = &nw.Policies[i]
				break
			}
		}
		if policy == nil {
			return true
		}
		if !bytes.Equal(policy.Settings, bprintf(`{"NetworkAdapterName":"%s"}`, nwAdapterName)) {
			return true
		}
	}

	// not in the same type
	if !strings.EqualFold(string(nw.Type), nwType) {
		return true
	}

	// or doesn't have same subnet
	var addressPrefix = nwSubnet.String()
	for idxIpam := range nw.Ipams {
		var ipam = &nw.Ipams[idxIpam]
		for idxSubnet := range ipam.Subnets {
			var sn = &ipam.Subnets[idxSubnet]
			if sn.IpAddressPrefix == addressPrefix {
				return false
			}
		}
	}
	return true
}

func ParseHCNNetworkManagementIP(nw *hcn.HostComputeNetwork) net.IP {
	if nw == nil {
		return nil
	}
	for idx := range nw.Policies {
		var p = &nw.Policies[idx]
		if p.Type == hcn.ProviderAddress {
			var d hcn.ProviderAddressEndpointPolicySetting
			if err := json.Unmarshal(p.Settings, &d); err != nil {
				return nil
			}
			return net.IP(d.ProviderAddress)
		}
	}
	return nil
}

func GetHCNHostDefaultNamespace() (*hcn.HostComputeNamespace, error) {
	// list all
	var nss, err = hcn.ListNamespacesQuery(hcn.HostComputeQuery{
		SchemaVersion: hcn.V2SchemaVersion(),
		Flags:         hcn.HostComputeQueryFlagsDetailed,
		Filter:        `{"IsDefault":true}`,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to list host default HostComputeNamespace")
	}

	// return if found
	if len(nss) != 0 {
		return &nss[0], nil
	}

	// or create a new one
	var ns = hcn.NewNamespace(hcn.NamespaceTypeHostDefault)
	ns, err = ns.Create()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create host default HostComputerNamespace")
	}
	return ns, nil
}

// constants of the supported Windows Socket protocol,
// ref to https://docs.microsoft.com/en-us/dotnet/api/system.net.sockets.protocoltype.
var protocolEnums = map[string]uint32{
	"icmpv4": 1,
	"igmp":   2,
	"tcp":    6,
	"udp":    17,
	"icmpv6": 58,
}

func GetHCNEndpointAvailableProtocolEnum(p string) (uint32, error) {
	var u, err = strconv.ParseUint(p, 0, 10)
	if err != nil {
		var pe, exist = protocolEnums[strings.ToLower(p)]
		if !exist {
			return 0, errors.New("invalid protocol supplied to port mapping policy")
		}
		return pe, nil
	}
	return uint32(u), nil
}

func IsDsrSupported() bool {
	var globals, err = hcn.GetGlobals()
	if err != nil {
		return false
	}
	return isFeatureSupported(globals.Version, hcn.DSRVersion)
}

func isFeatureSupported(currentVersion hcn.Version, versionsSupported hcn.VersionRanges) bool {
	var supported bool
	for _, versionRange := range versionsSupported {
		supported = supported || isFeatureInRange(currentVersion, versionRange)
	}
	return supported
}

func isFeatureInRange(currentVersion hcn.Version, versionRange hcn.VersionRange) bool {
	if currentVersion.Major < versionRange.MinVersion.Major {
		return false
	}
	if currentVersion.Major > versionRange.MaxVersion.Major {
		return false
	}
	if currentVersion.Major == versionRange.MinVersion.Major && currentVersion.Minor < versionRange.MinVersion.Minor {
		return false
	}
	if currentVersion.Major == versionRange.MaxVersion.Major && currentVersion.Minor > versionRange.MaxVersion.Minor {
		return false
	}
	return true
}

func normalizeString(s string) string {
	if s == "" {
		return s
	}
	s = strings.Replace(s, " ", "_", -1)
	s = strings.ToLower(s)
	return s
}
