package eni

import (
	"net/netip"
	"time"

	"github.com/AliyunContainerService/terway/rpc"
	"github.com/AliyunContainerService/terway/types/daemon"
)

//go:generate stringer -type=ipStatus -trimprefix=ipStatus
//go:generate stringer -type=ConditionType -trimprefix=Condition

type ipStatus int

const (
	ipStatusInit ipStatus = iota
	ipStatusValid
	ipStatusInvalid
	ipStatusDeleting
)

type IP struct {
	ip      netip.Addr
	primary bool

	podID string

	status ipStatus
}

func (ip *IP) String() string {
	if ip == nil {
		return ""
	}
	return ip.ip.String()
}

func NewIP(ip netip.Addr, primary bool) *IP {
	return &IP{
		ip:      ip,
		primary: primary,
		status:  ipStatusInit,
	}
}

func NewValidIP(ip netip.Addr, primary bool) *IP {
	return &IP{
		ip:      ip,
		primary: primary,
		status:  ipStatusValid,
	}
}

func (ip *IP) Primary() bool {
	return ip.primary
}

func (ip *IP) Valid() bool {
	return ip.status == ipStatusValid
}

func (ip *IP) Deleting() bool {
	return ip.status == ipStatusDeleting
}

func (ip *IP) InUse() bool {
	return ip.podID != ""
}

func (ip *IP) Allocate(podID string) {
	ip.podID = podID
}

func (ip *IP) Release(podID string) {
	if ip.podID != podID {
		return
	}
	ip.podID = ""
}

func (ip *IP) Dispose() {
	if ip.primary {
		return
	}
	ip.status = ipStatusDeleting
}

func (ip *IP) SetInvalid() {
	ip.status = ipStatusInvalid
}

func (ip *IP) Allocatable() bool {
	return ip.Valid() && !ip.InUse()
}

type Set map[any]*IP

func (s Set) Idles() []*IP {
	var result []*IP
	for _, v := range s {
		if !v.InUse() {
			result = append(result, v)
		}
	}
	return result
}

func (s Set) InUse() []*IP {
	var result []*IP
	for _, v := range s {
		if v.InUse() {
			result = append(result, v)
		}
	}
	return result
}

func (s Set) Allocatable() []*IP {
	var result []*IP
	for _, v := range s {
		if v.Allocatable() {
			result = append(result, v)
		}
	}
	return result
}

func (s Set) PeekAvailable(podID string, prefer netip.Addr) *IP {
	if podID != "" && prefer.IsValid() {
		if v, ok := s[prefer]; ok {
			if v.podID == podID {
				return v
			}
		}
	}
	for _, v := range s {
		if v.Allocatable() {
			return v
		}
	}
	return nil
}

func (s Set) Add(ip *IP) {
	s[ip.ip] = ip
}

func (s Set) PutValid(ip ...netip.Addr) {
	for _, v := range ip {
		s[v] = &IP{ip: v, status: ipStatusValid}
	}
}

func (s Set) PutDeleting(ip ...netip.Addr) {
	for _, v := range ip {
		s[v] = &IP{ip: v, status: ipStatusDeleting}
	}
}

func (s Set) Delete(ip ...netip.Addr) {
	for _, v := range ip {
		delete(s, v)
	}
}

func (s Set) Release(podID string, ip netip.Addr) {
	i, ok := s[ip]
	if ok {
		i.Release(podID)
	}
}

func (s Set) Deleting() []netip.Addr {
	var result []netip.Addr
	for _, v := range s {
		if v.Deleting() {
			result = append(result, v.ip)
		}
	}
	return result
}

func (s Set) ByPodID(podID string) *IP {
	for _, v := range s {
		if v.podID == podID {
			return v
		}
	}
	return nil
}

type ResourceType int

const (
	ResourceTypeLocalIP = 1 << iota
	ResourceTypeVeth
	ResourceTypeRemoteIP
	ResourceTypeRDMA
)

type ResourceRequest interface {
	ResourceType() ResourceType
}

// AllocRequest represent a bunch of resource must be met.
type AllocRequest struct {
	ResourceRequests []ResourceRequest
}

type AllocResp struct {
	Err error

	NetworkConfigs NetworkResources
}

type ReleaseRequest struct {
	NetworkResources []NetworkResource
}

type NetworkResources []NetworkResource

type NetworkResource interface {
	ResourceType() ResourceType

	ToRPC() []*rpc.NetConf

	ToStore() []daemon.ResourceItem
}

type Status struct {
	NetworkInterfaceID   string
	MAC                  string
	Type                 string
	AllocInhibitExpireAt string

	Usage  [][]string
	Status string
}

type Trace struct {
	Condition ConditionType
	Reason    string
}

type Condition struct {
	ConditionType ConditionType
	Reason        string
	Last          time.Time
}

type ConditionType int

const (
	Full ConditionType = iota
	ResourceTypeMismatch
	NetworkInterfaceMismatch
	InsufficientVSwitchIP
)
