//go:generate mockery --name Factory

package factory

import (
	"net/netip"

	"github.com/AliyunContainerService/terway/types/daemon"
)

type Factory interface {
	CreateNetworkInterface(ipv4, ipv6 int, eniType string) (*daemon.ENI, []netip.Addr, []netip.Addr, error)
	AssignNIPv4(eniID string, count int, mac string) ([]netip.Addr, error)
	AssignNIPv6(eniID string, count int, mac string) ([]netip.Addr, error)

	// UnAssignNIPv4 unassign ip from eni, the primary ip is not allowed to unassign
	UnAssignNIPv4(eniID string, ips []netip.Addr, mac string) error
	UnAssignNIPv6(eniID string, ips []netip.Addr, mac string) error

	DeleteNetworkInterface(eniID string) error

	LoadNetworkInterface(mac string) ([]netip.Addr, []netip.Addr, error)

	GetAttachedNetworkInterface(preferTrunkID string) ([]*daemon.ENI, error)
}
