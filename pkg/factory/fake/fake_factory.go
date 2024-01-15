package fake

import (
	"fmt"
	"net/netip"
	"sync"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"github.com/AliyunContainerService/terway/pkg/factory"
	"github.com/AliyunContainerService/terway/types/daemon"
)

type ENI struct {
	ID string

	IPv4 sets.Set[netip.Addr]
	IPv6 sets.Set[netip.Addr]
}

var _ factory.Factory = &FakeFactory{}

type FakeFactory struct {
	sync.Mutex
	ENIS map[string]*ENI

	ipv4Next netip.Addr
	ipv6Next netip.Addr
}

func NewFakeFactory() *FakeFactory {
	return &FakeFactory{
		ENIS:     map[string]*ENI{},
		ipv4Next: netip.MustParseAddr("10.0.0.0"),
		ipv6Next: netip.MustParseAddr("fd00::0"),
	}
}

func (f *FakeFactory) CreateNetworkInterface(ipv4, ipv6 int, eniType string) (*daemon.ENI, []netip.Addr, []netip.Addr, error) {
	if ipv4 <= 0 {
		return nil, nil, nil, fmt.Errorf("ipv4 must be greater than 0")
	}

	f.Lock()
	defer f.Unlock()

	n := &ENI{
		ID:   uuid.NewString(),
		IPv4: make(sets.Set[netip.Addr]),
		IPv6: make(sets.Set[netip.Addr]),
	}

	f.ENIS[n.ID] = n

	var v4, v6 []netip.Addr
	for i := 0; i < ipv4; i++ {
		ip := f.ipv4Next.Next()
		n.IPv4.Insert(ip)
		f.ipv4Next = ip
		v4 = append(v4, ip)
		klog.Infof("fake factory create eni %s ipv4 %s", n.ID, ip.String())
	}

	for i := 0; i < ipv6; i++ {
		ip := f.ipv6Next.Next()
		n.IPv6.Insert(ip)
		f.ipv6Next = ip
		v6 = append(v6, ip)
		klog.Infof("fake factory create eni %s ipv6 %s", n.ID, ip.String())
	}

	return &daemon.ENI{ID: n.ID}, v4, v6, nil
}

func (f *FakeFactory) AssignNIPv4(eniID string, count int, mac string) ([]netip.Addr, error) {
	f.Lock()
	defer f.Unlock()

	n, ok := f.ENIS[eniID]
	if !ok {
		return nil, fmt.Errorf("eni %s not found", eniID)
	}

	var v4 []netip.Addr
	for i := 0; i < count; i++ {
		ip := f.ipv4Next.Next()
		n.IPv4.Insert(ip)
		f.ipv4Next = ip
		v4 = append(v4, ip)
		klog.Infof("fake factory assign eni %s ipv4 %s", n.ID, ip.String())
	}
	return v4, nil
}

func (f *FakeFactory) AssignNIPv6(eniID string, count int, mac string) ([]netip.Addr, error) {
	f.Lock()
	defer f.Unlock()

	n, ok := f.ENIS[eniID]
	if !ok {
		return nil, fmt.Errorf("eni %s not found", eniID)
	}

	var v6 []netip.Addr
	for i := 0; i < count; i++ {
		ip := f.ipv6Next.Next()
		n.IPv6.Insert(ip)
		f.ipv6Next = ip
		v6 = append(v6, ip)
		klog.Infof("fake factory assign eni %s ipv6 %s", n.ID, ip.String())
	}
	return v6, nil
}

func (f *FakeFactory) DeleteNetworkInterface(eniID string) error {
	f.Lock()
	defer f.Unlock()
	delete(f.ENIS, eniID)
	return nil
}

func (f *FakeFactory) UnAssignNIPv4(eniID string, ips []netip.Addr, mac string) error {
	f.Lock()
	defer f.Unlock()

	e, ok := f.ENIS[eniID]
	if !ok {
		return fmt.Errorf("eni not found")
	}
	e.IPv4.Delete(ips...)
	return nil
}

func (f *FakeFactory) UnAssignNIPv6(eniID string, ips []netip.Addr, mac string) error {
	f.Lock()
	defer f.Unlock()

	e, ok := f.ENIS[eniID]
	if !ok {
		return fmt.Errorf("eni not found")
	}
	e.IPv6.Delete(ips...)
	return nil
}

func (f *FakeFactory) LoadNetworkInterface(mac string) ([]netip.Addr, []netip.Addr, error) {
	return nil, nil, nil
}

func (f *FakeFactory) GetAttachedNetworkInterface(preferTrunkID string) ([]*daemon.ENI, error) {
	return nil, nil
}
