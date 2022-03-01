//go:build windows
// +build windows

package iface

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/containernetworking/cni/pkg/types"
	"github.com/pkg/errors"

	"github.com/AliyunContainerService/terway/pkg/windows/powershell"
)

type AddressFamily string

const (
	IPv4 AddressFamily = "IPv4"
	IPv6 AddressFamily = "IPv6"
	Dual AddressFamily = ""
)

type Interface struct {
	Index      int    `json:"InterfaceIndex"`
	Alias      string `json:"InterfaceAlias"`
	Name       string `json:"InterfaceName"`
	Desc       string `json:"InterfaceDescription"`
	MTU        int    `json:"MtuSize"`
	Virtual    bool   `json:"Virtual"`
	MacAddress string `json:"MacAddress"`
}

func (i *Interface) ToNetInterface() *net.Interface {
	var hardwareAddr, _ = net.ParseMAC(i.MacAddress)
	return &net.Interface{
		Index:        i.Index,
		Name:         i.Alias,
		MTU:          i.MTU,
		HardwareAddr: hardwareAddr,
		Flags:        net.FlagUp,
	}
}

// GetDefaultGatewayInterface returns the first network interface found with a default gateway set.
func GetDefaultGatewayInterface() (*Interface, error) {
	var s = "Get-NetAdapter | Where-Object { (-not $_.Virtual) -and ($_.Status -eq 'Up') } | Sort-Object -Property InterfaceName,InterfaceAlias -Descending | Select-Object -Last 1 | Select-Object -Property InterfaceName,InterfaceAlias,InterfaceIndex,InterfaceDescription,MtuSize,Virtual,MacAddress"
	var d = Interface{Index: -1}

	if err := powershell.RunCommandWithJsonResult(s, &d); err != nil {
		return nil, err
	}
	if d.Index < 0 {
		return nil, errors.Errorf("no interface found")
	}
	return &d, nil
}

// GetInterfaceByIndex tries to get the network interface with the given index.
func GetInterfaceByIndex(search int) (*Interface, error) {
	var s = "Get-NetAdapter -InterfaceIndex %d | Where-Object { $_.Status -eq 'Up' } | Select-Object -Property InterfaceName,InterfaceAlias,InterfaceIndex,InterfaceDescription,MtuSize,Virtual,MacAddress"
	var d = Interface{Index: -1}

	if err := powershell.RunCommandWithJsonResult(fmt.Sprintf(s, search), &d); err != nil {
		return nil, err
	}
	if d.Index < 0 {
		return nil, errors.Errorf("no interface with given index %d found", search)
	}
	return &d, nil
}

// GetInterfaceByMAC tries to get the network interface with the given mac address.
func GetInterfaceByMAC(search string, isPhysical bool) (*Interface, error) {
	var s = "Get-NetAdapter | Where-Object { $_.Status -eq 'Up' } | Select-Object -Property InterfaceName,InterfaceAlias,InterfaceIndex,InterfaceDescription,MtuSize,Virtual,MacAddress"
	var ds []Interface

	if err := powershell.RunCommandWithJsonResult(s, &ds); err != nil {
		return nil, err
	}

	var m = strings.ToLower(strings.Replace(search, ":", "-", -1))
	var d *Interface
	for i := range ds {
		if strings.ToLower(ds[i].MacAddress) == m {
			var isPhysicalActual = !ds[i].Virtual
			if (!isPhysical && isPhysicalActual) || // expect virtual but get physical
				(isPhysical && !isPhysicalActual) { // expect physical but get virtual
				continue
			}
			d = &ds[i]
			break
		}
	}
	if d == nil {
		return nil, errors.Errorf("no interface with given MAC %s found", search)
	}
	return d, nil
}

// GetInterfaceIndexByIP tries to get the network interface index with the given ip address.
func GetInterfaceIndexByIP(search net.IP) (int, error) {
	var s = "Get-NetIPAddress -IPAddress %s | Select-Object -Property InterfaceIndex"
	var d = Interface{Index: -1}

	if err := powershell.RunCommandWithJsonResult(fmt.Sprintf(s, search), &d); err != nil {
		return -1, err
	}
	if d.Index < 0 {
		return -1, errors.Errorf("not interface with given %s IP found", search)
	}
	return d.Index, nil
}

// EnableForwardingForInterface enables forwarding for given network interface.
func EnableForwardingForInterface(ifaceIndex int, family AddressFamily) error {
	return setForwardingForInterface(ifaceIndex, family, true)
}

// DisableForwardingForInterface disables forwarding for given network interface.
func DisableForwardingForInterface(ifaceIndex int, family AddressFamily) error {
	return setForwardingForInterface(ifaceIndex, family, false)
}

// setForwardingForInterface configures the given network interface with the given forwarding configuration.
func setForwardingForInterface(ifaceIndex int, family AddressFamily, forwarding bool) error {
	var s = "Set-NetIPInterface -ifIndex %d %s"
	var e = "-Forwarding Enabled"
	if !forwarding {
		e = "-Forwarding Disabled"
	}
	if family == IPv4 || family == IPv6 {
		e = e + " -AddressFamily " + string(family)
	}

	if _, err := powershell.RunCommand(fmt.Sprintf(s, ifaceIndex, e)); err != nil {
		return err
	}
	return nil
}

// IsForwardingEnabledForInterface returns true if the given network interface is enabling forwarding.
func IsForwardingEnabledForInterface(ifaceIndex int, family AddressFamily) (bool, error) {
	if family == Dual {
		return false, errors.New("must specify the address family")
	}
	var s = "Get-NetIPInterface -ifIndex %d -AddressFamily %s | Select-Object -Property Forwarding"
	var d = struct {
		Forwarding int `json:"Forwarding"`
	}{Forwarding: 0}

	if err := powershell.RunCommandWithJsonResult(fmt.Sprintf(s, ifaceIndex, family), &d); err != nil {
		return false, err
	}
	return d.Forwarding == 1, nil
}

// SetMTUForInterface configures the MTU for given network interface.
func SetMTUForInterface(ifaceIndex int, family AddressFamily, mtu int) error {
	var s = "Set-NetIPInterface -ifIndex %d -NlMtuBytes %d %s"
	var e string
	if family == IPv4 || family == IPv6 {
		e = "-AddressFamily " + string(family)
	}

	if _, err := powershell.RunCommand(fmt.Sprintf(s, ifaceIndex, mtu, e)); err != nil {
		return err
	}
	return nil
}

// IsDhcpEnabledForInterface returns true if the given network interface is enabling dhcp.
func IsDhcpEnabledForInterface(ifaceIndex int, family AddressFamily) (bool, error) {
	if family != IPv4 && family != IPv6 {
		return false, errors.New("must specify picking IPv4 or IPv6")
	}
	var s = "Get-NetIPInterface -ifIndex %d -AddressFamily %s | Select-Object -Property Dhcp"
	var d = struct {
		Dhcp int `json:"Dhcp"`
	}{Dhcp: 0}

	if err := powershell.RunCommandWithJsonResult(fmt.Sprintf(s, ifaceIndex, family), &d); err != nil {
		return false, err
	}
	return d.Dhcp == 1, nil
}

// EnableDhcpForInterface enables dhcp for given network interface.
func EnableDhcpForInterface(ifaceIndex int, family AddressFamily) error {
	return setDhcpForInterface(ifaceIndex, family, true)
}

// DisableDhcpForInterface disables hcp for given network interface.
func DisableDhcpForInterface(ifaceIndex int, family AddressFamily) error {
	return setDhcpForInterface(ifaceIndex, family, false)
}

// setDhcpForInterface configures the given network interface with the given dhcp configuration.
func setDhcpForInterface(ifaceIndex int, family AddressFamily, dhcp bool) error {
	var convert = func(i int, af AddressFamily) string {
		var s = "Set-NetIPInterface -ifIndex %d %s"
		var e string
		switch af {
		case IPv6:
			e = "-Dhcp Enabled -DadTransmits 3 -DadRetransmitTimeMs 1000"
			if !dhcp {
				e = "-Dhcp Disabled -DadTransmits 0 -DadRetransmitTimeMs 0"
			}
		default:
			e = "-Dhcp Enabled -DadTransmits 1 -DadRetransmitTimeMs 1000"
			if !dhcp {
				e = "-Dhcp Disabled -DadTransmits 0 -DadRetransmitTimeMs 0"
			}
		}
		e = e + " -AddressFamily " + string(af)
		return fmt.Sprintf(s, i, e)
	}

	var ss []string
	if family == Dual {
		ss = []string{convert(ifaceIndex, IPv4), convert(ifaceIndex, IPv6)}
	} else {
		ss = []string{convert(ifaceIndex, family)}
	}
	for _, s := range ss {
		if _, err := powershell.RunCommand(s); err != nil {
			return err
		}
	}
	return nil
}

// GetDNSConfigurationByMAC returns dns configuration for given network interface.
func GetDNSConfigurationByMAC(search string, family AddressFamily) (types.DNS, error) {
	if family != IPv4 && family != IPv6 {
		return types.DNS{}, errors.New("must specify picking IPv4 or IPv6")
	}
	var i, err = GetInterfaceByMAC(search, false)
	if err != nil {
		i, err = GetInterfaceByMAC(search, true)
	}
	if err != nil {
		return types.DNS{}, err
	}
	var s = "Get-DnsClientServerAddress -InterfaceIndex %d -AddressFamily %s | Select-Object -Property ServerAddresses"
	var d = struct {
		ServerAddresses []string `json:"ServerAddresses"`
	}{}

	if err := powershell.RunCommandWithJsonResult(fmt.Sprintf(s, i.Index, family), &d); err != nil {
		return types.DNS{}, err
	}
	return types.DNS{
		Nameservers: d.ServerAddresses,
	}, nil
}

// ConfigurationOptions specifies the options to configure.
type ConfigurationOptions struct {
	Forwarding *bool
	Dhcp       *bool
	Mtu        int
}

func (o *ConfigurationOptions) isZero() bool {
	return o.Forwarding == nil &&
		o.Dhcp == nil &&
		o.Mtu <= 0
}

// ConfigureInterface configures  the given network interface with the given configuration options.
func ConfigureInterface(ifaceIndex int, family AddressFamily, options ConfigurationOptions) error {
	if options.isZero() {
		return nil
	}

	var convert = func(i int, af AddressFamily) string {
		const separator = " "
		var o = "Set-NetIPInterface -ifIndex " + strconv.Itoa(i) + separator
		if options.Mtu > 0 {
			var mtu = strconv.Itoa(options.Mtu)
			o = o + "-NlMtuBytes " + mtu + separator
		}
		if options.Forwarding != nil {
			var forwarding = "Disabled"
			if *options.Forwarding {
				forwarding = "Enabled"
			}
			o = o + "-Forwarding " + forwarding + separator
		}
		if options.Dhcp != nil {
			var dhcp = "Disabled -DadTransmits 0 -DadRetransmitTimeMs 0"
			if *options.Dhcp {
				switch af {
				case IPv6:
					dhcp = "Enabled -DadTransmits 3 -DadRetransmitTimeMs 1000"
				default:
					dhcp = "Enabled -DadTransmits 1 -DadRetransmitTimeMs 1000"
				}
			}
			o = o + "-Dhcp " + dhcp + separator
		}
		o = o + "-AddressFamily " + string(af)
		return o
	}

	var ss []string
	if family == Dual {
		ss = []string{convert(ifaceIndex, IPv4), convert(ifaceIndex, IPv6)}
	} else {
		ss = []string{convert(ifaceIndex, family)}
	}
	for _, s := range ss {
		if _, err := powershell.RunCommand(s); err != nil {
			return err
		}
	}
	return nil
}
