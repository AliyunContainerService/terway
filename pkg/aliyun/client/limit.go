package client

import (
	"sync"
)

// Limits specifies the IPAM relevant instance limits
type Limits struct {
	// Adapters specifies the maximum number of interfaces that can be
	// attached to the instance
	Adapters int

	// TotalAdapters maximum number of interfaces that can be
	// attached to the instance
	TotalAdapters int

	// IPv4PerAdapter is the maximum number of ipv4 addresses per adapter/interface
	IPv4PerAdapter int

	// IPv6PerAdapter is the maximum number of ipv6 addresses per adapter/interface
	IPv6PerAdapter int

	// MemberAdapterLimit is the number interfaces that type is member
	MemberAdapterLimit int

	// MaxMemberAdapterLimit is the limit to use member
	MaxMemberAdapterLimit int

	// ERdmaAdapters specifies the maximum number of erdma interfaces
	ERdmaAdapters int

	InstanceBandwidthRx int

	InstanceBandwidthTx int
}

func (l *Limits) SupportMultiIPIPv6() bool {
	return l.IPv6PerAdapter == l.IPv4PerAdapter
}

func (l *Limits) SupportIPv6() bool {
	return l.IPv6PerAdapter > 0
}

func (l *Limits) TrunkPod() int {
	return l.MemberAdapterLimit
}

func (l *Limits) MaximumTrunkPod() int {
	return l.MaxMemberAdapterLimit
}

func (l *Limits) MultiIPPod() int {
	return (l.Adapters - 1) * l.IPv4PerAdapter
}

func (l *Limits) ExclusiveERDMARes() int {
	return l.ERdmaAdapters
}

func (l *Limits) ERDMARes() int {
	return l.ERdmaAdapters * l.IPv4PerAdapter
}

func (l *Limits) ExclusiveENIPod() int {
	return l.Adapters - 1
}

var limits sync.Map

type GetLimitFunc func(a interface{}, instanceType string) (*Limits, error)

var GetLimit GetLimitFunc

func SetGetLimit(fn GetLimitFunc) {
	GetLimit = fn
}

func init() {
	GetLimit = DefaultGetLimit
}
