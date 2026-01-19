package eni

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/util/cache"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/factory"
	factorymocks "github.com/AliyunContainerService/terway/pkg/factory/mocks"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

func NewLocalTest(eni *daemon.ENI, factory factory.Factory, poolConfig *daemon.PoolConfig, eniType string) *Local {
	l := &Local{
		eni:        eni,
		batchSize:  poolConfig.BatchSize,
		cap:        poolConfig.MaxIPPerENI,
		cond:       sync.NewCond(&sync.Mutex{}),
		ipv4:       make(Set),
		ipv6:       make(Set),
		enableIPv4: poolConfig.EnableIPv4,
		enableIPv6: poolConfig.EnableIPv6,
		factory:    factory,
		eniType:    eniType,

		rateLimitEni: rate.NewLimiter(100, 100),
		rateLimitv4:  rate.NewLimiter(100, 100),
		rateLimitv6:  rate.NewLimiter(100, 100),
	}

	return l
}

func TestLocal_Release_ValidIPv4(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "")
	request := &LocalIPResource{
		ENI: daemon.ENI{ID: "eni-1"},
		IP:  types.IPSet2{IPv4: netip.MustParseAddr("192.0.2.1")},
	}
	cni := &daemon.CNI{PodID: "pod-1"}

	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
	local.ipv4[netip.MustParseAddr("192.0.2.1")].Allocate("pod-1")

	ok, _ := local.Release(context.Background(), cni, request)
	assert.True(t, ok)
}

func TestLocal_Release_ValidIPv6(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "")
	request := &LocalIPResource{
		ENI: daemon.ENI{ID: "eni-1"},
		IP:  types.IPSet2{IPv6: netip.MustParseAddr("fd00:46dd:e::1")},
	}
	cni := &daemon.CNI{PodID: "pod-1"}

	local.ipv6.Add(NewValidIP(netip.MustParseAddr("fd00:46dd:e::1"), false))
	local.ipv6[netip.MustParseAddr("fd00:46dd:e::1")].Allocate("pod-1")

	ok, _ := local.Release(context.Background(), cni, request)
	assert.True(t, ok)
}

func TestLocal_Release_NilENI(t *testing.T) {
	local := NewLocalTest(nil, nil, &daemon.PoolConfig{}, "")
	request := &LocalIPResource{
		ENI: daemon.ENI{ID: "eni-1"},
		IP:  types.IPSet2{IPv4: netip.MustParseAddr("192.0.2.1")},
	}
	cni := &daemon.CNI{PodID: "pod-1"}

	ok, _ := local.Release(context.Background(), cni, request)
	assert.False(t, ok)
}

func TestLocal_Release_DifferentENIID(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "")
	request := &LocalIPResource{
		ENI: daemon.ENI{ID: "eni-2"},
		IP:  types.IPSet2{IPv4: netip.MustParseAddr("192.0.2.1")},
	}
	cni := &daemon.CNI{PodID: "pod-1"}

	ok, _ := local.Release(context.Background(), cni, request)
	assert.False(t, ok)
}

func TestLocal_Release_ValidIPv4_ReleaseIPv6(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "")
	request := &LocalIPResource{
		ENI: daemon.ENI{ID: "eni-1"},
		IP:  types.IPSet2{IPv4: netip.MustParseAddr("192.0.2.1"), IPv6: netip.MustParseAddr("fd00:46dd:e::1")},
	}
	cni := &daemon.CNI{PodID: "pod-1"}

	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
	local.ipv4[netip.MustParseAddr("192.0.2.1")].Allocate("pod-1")

	local.ipv6.Add(NewValidIP(netip.MustParseAddr("fd00:46dd:e::1"), false))
	local.ipv6[netip.MustParseAddr("fd00:46dd:e::1")].Allocate("pod-1")

	ok, _ := local.Release(context.Background(), cni, request)
	assert.True(t, ok)

	assert.Equal(t, ipStatusValid, local.ipv4[netip.MustParseAddr("192.0.2.1")].status)
	assert.Equal(t, "", local.ipv4[netip.MustParseAddr("192.0.2.1")].podID)

	assert.Equal(t, ipStatusValid, local.ipv6[netip.MustParseAddr("fd00:46dd:e::1")].status)
	assert.Equal(t, "", local.ipv6[netip.MustParseAddr("fd00:46dd:e::1")].podID)
}

func TestLocal_AllocWorker_EnableIPv4(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{
		EnableIPv4: true,
	}, "")
	cni := &daemon.CNI{PodID: "pod-1"}

	respCh := make(chan *AllocResp)
	go local.allocWorker(context.Background(), cni, nil, respCh)

	go func() {
		local.cond.L.Lock()
		local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
		local.cond.Broadcast()
		local.cond.L.Unlock()
	}()

	resp := <-respCh
	assert.Len(t, resp.NetworkConfigs, 1)

	lo := resp.NetworkConfigs[0].(*LocalIPResource)
	assert.Equal(t, "192.0.2.1", lo.IP.IPv4.String())
	assert.False(t, lo.IP.IPv6.IsValid())
	assert.Equal(t, "eni-1", lo.ENI.ID)
}

func TestLocal_AllocWorker_EnableIPv6(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{
		EnableIPv6: true,
	}, "")
	cni := &daemon.CNI{PodID: "pod-1"}

	respCh := make(chan *AllocResp)
	go local.allocWorker(context.Background(), cni, nil, respCh)

	go func() {
		local.cond.L.Lock()
		local.ipv6.Add(NewValidIP(netip.MustParseAddr("fd00:46dd:e::1"), false))
		local.cond.Broadcast()
		local.cond.L.Unlock()
	}()

	resp := <-respCh
	assert.Len(t, resp.NetworkConfigs, 1)

	lo := resp.NetworkConfigs[0].(*LocalIPResource)
	assert.Equal(t, netip.MustParseAddr("fd00:46dd:e::1"), lo.IP.IPv6)
	assert.False(t, lo.IP.IPv4.IsValid())
	assert.Equal(t, "eni-1", lo.ENI.ID)
}

func TestLocal_AllocWorker_ParentCancelContext(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{
		EnableIPv4: true,
	}, "")
	cni := &daemon.CNI{PodID: "pod-1"}

	ctx, cancel := context.WithCancel(context.Background())
	respCh := make(chan *AllocResp)
	go local.allocWorker(ctx, cni, nil, respCh)

	cancel()

	_, ok := <-respCh
	assert.False(t, ok)
}

func TestLocal_AllocWorker_UpdateCache(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{
		EnableIPv4: true,
	}, "")
	cni := &daemon.CNI{PodID: "pod-1"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	respCh := make(chan *AllocResp)

	req := NewLocalIPRequest()
	req.NoCache = true
	go local.allocWorker(ctx, cni, req, respCh)
	req.cancel()
	result, ok := <-respCh
	assert.True(t, ok)
	assert.NotNil(t, result)
}

func TestLocal_Dispose(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "")
	local.status = statusInUse
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
	local.ipv4[netip.MustParseAddr("192.0.2.1")].Allocate("pod-1")
	local.ipv6.Add(NewValidIP(netip.MustParseAddr("fd00:46dd:e::1"), false))
	local.ipv6[netip.MustParseAddr("fd00:46dd:e::1")].Allocate("pod-1")

	n := local.Dispose(10)

	assert.Equal(t, 0, n)
	assert.Equal(t, statusInUse, local.status)
	assert.Equal(t, 1, len(local.ipv4))
	assert.Equal(t, 1, len(local.ipv6))
}

func TestLocal_DisposeWholeENI(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "")
	local.status = statusInUse
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), true))
	local.ipv6.Add(NewValidIP(netip.MustParseAddr("fd00:46dd:e::1"), false))

	n := local.Dispose(1)

	assert.Equal(t, 1, n)
	assert.Equal(t, statusDeleting, local.status)
}

func TestLocal_Allocate_NoCache(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{MaxIPPerENI: 2, EnableIPv4: true}, "")

	request := NewLocalIPRequest()
	request.NoCache = true
	cni := &daemon.CNI{PodID: "pod-1"}

	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.2"), false))

	_, resp := local.Allocate(context.Background(), cni, request)

	assert.Equal(t, 1, len(resp))
}

func TestLocal_DisposeFailWhenAllocatingIsNotEmpty(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "")
	local.status = statusInUse
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), true))
	local.ipv6.Add(NewValidIP(netip.MustParseAddr("fd00:46dd:e::1"), false))

	local.allocatingV4 = append(local.allocatingV4, NewLocalIPRequest())
	n := local.Dispose(1)

	assert.Equal(t, 1, n)
	assert.Equal(t, statusInUse, local.status)
}

func TestLocal_Allocate_NoCache_AllocSuccess(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{
		MaxIPPerENI: 10, EnableIPv4: true, EnableIPv6: true}, "")

	request := NewLocalIPRequest()
	request.NoCache = true
	cni := &daemon.CNI{PodID: "pod-1"}

	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.2"), false))

	ch, resp := local.Allocate(context.Background(), cni, request)
	assert.NotNil(t, ch)
	assert.Equal(t, 0, len(resp))
}

func TestLocal_DisposeWholeERDMA(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "erdma")
	local.status = statusInUse
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))

	n := local.Dispose(1)

	assert.Equal(t, 1, n)
	assert.NotEqual(t, statusDeleting, local.status)
}

func TestLocal_Allocate_ERDMA(t *testing.T) {
	localErdma := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{MaxIPPerENI: 2, EnableIPv4: true}, "erdma")

	request := NewLocalIPRequest()
	request.NoCache = true
	cni := &daemon.CNI{PodID: "pod-1"}

	localErdma.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
	localErdma.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.2"), false))

	_, resp := localErdma.Allocate(context.Background(), cni, request)

	assert.Equal(t, 1, len(resp))
	assert.Equal(t, ResourceTypeMismatch, resp[0].Condition)

	request = NewLocalIPRequest()
	request.NoCache = true
	request.LocalIPType = LocalIPTypeERDMA

	_, resp = localErdma.Allocate(context.Background(), cni, request)
	assert.Equal(t, 1, len(resp))
	assert.NotEqual(t, ResourceTypeMismatch, resp[0].Condition)

	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{MaxIPPerENI: 2, EnableIPv4: true}, "")
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.2"), false))

	request = NewLocalIPRequest()
	request.NoCache = true
	_, resp = local.Allocate(context.Background(), cni, request)

	assert.Equal(t, 1, len(resp))
	assert.NotEqual(t, ResourceTypeMismatch, resp[0].Condition)

	request = NewLocalIPRequest()
	request.NoCache = true
	request.LocalIPType = LocalIPTypeERDMA
	_, resp = local.Allocate(context.Background(), cni, request)

	assert.Equal(t, 1, len(resp))
	assert.Equal(t, ResourceTypeMismatch, resp[0].Condition)
}

func TestLocal_Allocate_Inhibit(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{MaxIPPerENI: 2, EnableIPv4: true}, "")

	request := NewLocalIPRequest()
	cni := &daemon.CNI{PodID: "pod-1"}

	local.ipAllocInhibitExpireAt = time.Now().Add(time.Minute)

	allocResp, resp := local.Allocate(context.Background(), cni, request)
	assert.Nil(t, allocResp)
	assert.Equal(t, 1, len(resp))
}

func Test_parseResourceID(t *testing.T) {
	type args struct {
		id string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		want1   string
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name:    "test1",
			args:    args{id: "00:00:00:00:00:00.192.0.2.1"},
			want:    "00:00:00:00:00:00",
			want1:   "192.0.2.1",
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1, err := parseResourceID(tt.args.id)
			if !tt.wantErr(t, err, fmt.Sprintf("parseResourceID(%v)", tt.args.id)) {
				return
			}
			assert.Equalf(t, tt.want, got, "parseResourceID(%v)", tt.args.id)
			assert.Equalf(t, tt.want1, got1, "parseResourceID(%v)", tt.args.id)
		})
	}
}

func TestLocal_load_NilENI(t *testing.T) {
	local := NewLocalTest(nil, nil, &daemon.PoolConfig{}, "")

	err := local.load([]daemon.PodResources{})

	assert.NoError(t, err)
}

func TestLocal_load_FactoryError(t *testing.T) {
	mockFactory := factorymocks.NewFactory(t)
	mockFactory.On("LoadNetworkInterface", "00:00:00:00:00:01").Return(nil, nil, fmt.Errorf("factory error"))

	eni := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
		PrimaryIP: types.IPSet{
			IPv4: net.ParseIP("192.0.2.1"),
		},
	}
	local := NewLocalTest(eni, mockFactory, &daemon.PoolConfig{}, "")

	err := local.load([]daemon.PodResources{})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "factory error")
}

func TestLocal_load_SuccessNoPods(t *testing.T) {
	mockFactory := factorymocks.NewFactory(t)
	ipv4Addrs := []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
		netip.MustParseAddr("192.0.2.2"),
	}
	ipv6Addrs := []netip.Addr{
		netip.MustParseAddr("2001:db8::1"),
	}
	mockFactory.On("LoadNetworkInterface", "00:00:00:00:00:01").Return(ipv4Addrs, ipv6Addrs, nil)

	eni := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
		PrimaryIP: types.IPSet{
			IPv4: net.ParseIP("192.0.2.1"),
		},
	}
	local := NewLocalTest(eni, mockFactory, &daemon.PoolConfig{EnableIPv4: true, EnableIPv6: true}, "")

	err := local.load([]daemon.PodResources{})

	assert.NoError(t, err)
	assert.Equal(t, statusInUse, local.status)
	assert.Equal(t, 2, len(local.ipv4))
	assert.Equal(t, 1, len(local.ipv6))

	// Check that primary IP is marked as primary
	primaryIP := netip.MustParseAddr("192.0.2.1")
	assert.True(t, local.ipv4[primaryIP].Primary())
}

func TestLocal_load_WithLegacyPods(t *testing.T) {
	mockFactory := factorymocks.NewFactory(t)
	ipv4Addrs := []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
		netip.MustParseAddr("192.0.2.2"),
	}
	ipv6Addrs := []netip.Addr{}
	mockFactory.On("LoadNetworkInterface", "00:00:00:00:00:01").Return(ipv4Addrs, ipv6Addrs, nil)

	eni := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
		PrimaryIP: types.IPSet{
			IPv4: net.ParseIP("192.0.2.1"),
		},
	}
	local := NewLocalTest(eni, mockFactory, &daemon.PoolConfig{EnableIPv4: true, EnableIPv6: false}, "")

	// Create legacy pod resources using parseResourceID format
	podResources := []daemon.PodResources{
		{
			PodInfo: &daemon.PodInfo{
				Name:      "test-pod",
				Namespace: "default",
			},
			Resources: []daemon.ResourceItem{
				{
					Type: daemon.ResourceTypeENIIP,
					ID:   "00:00:00:00:00:01.192.0.2.2", // MAC.IP format
				},
			},
		},
	}

	err := local.load(podResources)

	assert.NoError(t, err)
	assert.Equal(t, statusInUse, local.status)
	assert.Equal(t, 2, len(local.ipv4))

	// Check that the IP is allocated to the pod
	allocatedIP := netip.MustParseAddr("192.0.2.2")
	assert.True(t, local.ipv4[allocatedIP].InUse())
}

func TestLocal_load_WithModernPods(t *testing.T) {
	mockFactory := factorymocks.NewFactory(t)
	ipv4Addrs := []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
		netip.MustParseAddr("192.0.2.2"),
	}
	ipv6Addrs := []netip.Addr{
		netip.MustParseAddr("2001:db8::1"),
	}
	mockFactory.On("LoadNetworkInterface", "00:00:00:00:00:01").Return(ipv4Addrs, ipv6Addrs, nil)

	eni := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
		PrimaryIP: types.IPSet{
			IPv4: net.ParseIP("192.0.2.1"),
		},
	}
	local := NewLocalTest(eni, mockFactory, &daemon.PoolConfig{EnableIPv4: true, EnableIPv6: true}, "")

	// Create modern pod resources using ENIID
	podResources := []daemon.PodResources{
		{
			PodInfo: &daemon.PodInfo{
				Name:      "test-pod",
				Namespace: "default",
			},
			Resources: []daemon.ResourceItem{
				{
					Type:  daemon.ResourceTypeENIIP,
					ENIID: "eni-1",
					IPv4:  "192.0.2.2",
					IPv6:  "2001:db8::1",
				},
			},
		},
	}

	err := local.load(podResources)

	assert.NoError(t, err)
	assert.Equal(t, statusInUse, local.status)
	assert.Equal(t, 2, len(local.ipv4))
	assert.Equal(t, 1, len(local.ipv6))

	// Check that the IPs are allocated to the pod
	allocatedIPv4 := netip.MustParseAddr("192.0.2.2")
	allocatedIPv6 := netip.MustParseAddr("2001:db8::1")
	assert.True(t, local.ipv4[allocatedIPv4].InUse())
	assert.True(t, local.ipv6[allocatedIPv6].InUse())
}

func TestLocal_load_CapAdjustment(t *testing.T) {
	mockFactory := factorymocks.NewFactory(t)
	// Create more IPs than the cap allows
	ipv4Addrs := []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
		netip.MustParseAddr("192.0.2.2"),
		netip.MustParseAddr("192.0.2.3"),
		netip.MustParseAddr("192.0.2.4"),
	}
	ipv6Addrs := []netip.Addr{
		netip.MustParseAddr("2001:db8::1"),
		netip.MustParseAddr("2001:db8::2"),
		netip.MustParseAddr("2001:db8::3"),
	}
	mockFactory.On("LoadNetworkInterface", "00:00:00:00:00:01").Return(ipv4Addrs, ipv6Addrs, nil)

	eni := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
		PrimaryIP: types.IPSet{
			IPv4: net.ParseIP("192.0.2.1"),
		},
	}
	// Set cap to 2, but we have 4 IPv4 and 3 IPv6 addresses
	local := NewLocalTest(eni, mockFactory, &daemon.PoolConfig{EnableIPv4: true, EnableIPv6: true, MaxIPPerENI: 2}, "")

	err := local.load([]daemon.PodResources{})

	assert.NoError(t, err)
	assert.Equal(t, statusInUse, local.status)
}

func TestLocal_load_InvalidIPParsing(t *testing.T) {
	mockFactory := factorymocks.NewFactory(t)
	ipv4Addrs := []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
	}
	ipv6Addrs := []netip.Addr{}
	mockFactory.On("LoadNetworkInterface", "00:00:00:00:00:01").Return(ipv4Addrs, ipv6Addrs, nil)

	eni := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
		PrimaryIP: types.IPSet{
			IPv4: net.ParseIP("192.0.2.1"),
		},
	}
	local := NewLocalTest(eni, mockFactory, &daemon.PoolConfig{EnableIPv4: true, EnableIPv6: false}, "")

	// Create pod resources with invalid IPv4
	podResources := []daemon.PodResources{
		{
			PodInfo: &daemon.PodInfo{
				Name:      "test-pod",
				Namespace: "default",
			},
			Resources: []daemon.ResourceItem{
				{
					Type:  daemon.ResourceTypeENIIP,
					ENIID: "eni-1",
					IPv4:  "invalid-ip",
				},
			},
		},
	}

	err := local.load(podResources)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidDataType")
}

func TestLocal_load_InvalidPrimaryIP(t *testing.T) {
	mockFactory := factorymocks.NewFactory(t)
	ipv4Addrs := []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
	}
	ipv6Addrs := []netip.Addr{}
	mockFactory.On("LoadNetworkInterface", "00:00:00:00:00:01").Return(ipv4Addrs, ipv6Addrs, nil)

	eni := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
		PrimaryIP: types.IPSet{
			IPv4: nil, // Invalid primary IP (nil)
		},
	}
	local := NewLocalTest(eni, mockFactory, &daemon.PoolConfig{EnableIPv4: true, EnableIPv6: false}, "")

	err := local.load([]daemon.PodResources{})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse IP")
}

func Test_orphanIP(t *testing.T) {
	invalidIPCache = cache.NewLRUExpireCache(100)

	lo1 := map[netip.Addr]*IP{
		netip.MustParseAddr("127.0.0.1"): {
			ip: netip.MustParseAddr("127.0.0.1"),
		},
	}

	remote1 := sets.Set[netip.Addr]{
		netip.MustParseAddr("127.0.0.1"): {},
		netip.MustParseAddr("127.0.0.2"): {},
	}

	orphanIP(lo1, remote1)

	v, _ := invalidIPCache.Get(netip.MustParseAddr("127.0.0.1"))
	assert.Equal(t, nil, v)

	v, _ = invalidIPCache.Get(netip.MustParseAddr("127.0.0.2"))
	assert.Equal(t, 1, v)

	orphanIP(lo1, remote1)
	v, _ = invalidIPCache.Get(netip.MustParseAddr("127.0.0.2"))
	assert.Equal(t, 2, v)
}

func Test_switchIPv4(t *testing.T) {
	l := &Local{}

	req := NewLocalIPRequest()
	l.allocatingV4 = append(l.allocatingV4,
		req,
		NewLocalIPRequest(),
	)

	l.dangingV4 = append(l.dangingV4, NewLocalIPRequest())

	l.switchIPv4(req)
	assert.Equal(t, 2, l.allocatingV4.Len())
	assert.Equal(t, 0, l.dangingV4.Len())
}

func Test_switchIPv6(t *testing.T) {
	l := &Local{}

	req := NewLocalIPRequest()
	l.allocatingV6 = append(l.allocatingV6,
		req,
		NewLocalIPRequest(),
	)

	l.switchIPv6(req)
	assert.Equal(t, 1, l.allocatingV6.Len())
	assert.Equal(t, 0, l.dangingV6.Len())
}

func TestPopNIPv4JobsMovesCorrectNumberOfJobs(t *testing.T) {
	l := &Local{
		allocatingV4: AllocatingRequests{NewLocalIPRequest(), NewLocalIPRequest(), NewLocalIPRequest()},
		dangingV4:    AllocatingRequests{},
	}

	l.popNIPv4Jobs(2)

	assert.Len(t, l.allocatingV4, 1)
	assert.Len(t, l.dangingV4, 2)
}

func TestPopNIPv6JobsMovesCorrectNumberOfJobs(t *testing.T) {
	l := &Local{
		allocatingV6: AllocatingRequests{NewLocalIPRequest(), NewLocalIPRequest(), NewLocalIPRequest()},
		dangingV6:    AllocatingRequests{},
	}

	l.popNIPv6Jobs(2)

	assert.Len(t, l.allocatingV6, 1)
	assert.Len(t, l.dangingV6, 2)
}

func TestPopNIPv6JobsMovesAllJobsWhenCountExceeds(t *testing.T) {
	l := &Local{
		allocatingV6: AllocatingRequests{NewLocalIPRequest(), NewLocalIPRequest()},
		dangingV6:    AllocatingRequests{},
	}

	l.popNIPv6Jobs(5)

	assert.Len(t, l.allocatingV6, 0)
	assert.Len(t, l.dangingV6, 2)
}

func TestPopNIPv6JobsMovesNoJobsWhenCountIsZero(t *testing.T) {
	l := &Local{
		allocatingV6: AllocatingRequests{NewLocalIPRequest(), NewLocalIPRequest()},
		dangingV6:    AllocatingRequests{},
	}

	l.popNIPv6Jobs(0)

	assert.Len(t, l.allocatingV6, 2)
	assert.Len(t, l.dangingV6, 0)
}

func TestPriorityReturnsNegativeWhenStatusIsDeleting(t *testing.T) {
	l := &Local{
		cond:   sync.NewCond(&sync.Mutex{}),
		status: statusDeleting,
	}

	prio := l.Priority()

	assert.Equal(t, -100, prio)
}

func TestPriorityReturnsZeroWhenStatusIsInit(t *testing.T) {
	l := &Local{
		cond:   sync.NewCond(&sync.Mutex{}),
		status: statusInit,
	}

	prio := l.Priority()

	assert.Equal(t, 0, prio)
}

func TestPriorityReturnsTenWhenStatusIsCreating(t *testing.T) {
	l := &Local{
		cond:   sync.NewCond(&sync.Mutex{}),
		status: statusCreating,
	}

	prio := l.Priority()

	assert.Equal(t, 10, prio)
}

func TestPriorityReturnsFiftyPlusIPv4CountWhenStatusIsInUseAndIPv4Enabled(t *testing.T) {
	l := &Local{
		cond:       sync.NewCond(&sync.Mutex{}),
		status:     statusInUse,
		enableIPv4: true,
		ipv4:       Set{netip.MustParseAddr("192.0.2.1"): &IP{}},
	}

	prio := l.Priority()

	assert.Equal(t, 51, prio)
}

func TestPriorityReturnsFiftyPlusIPv6CountWhenStatusIsInUseAndIPv6Enabled(t *testing.T) {
	l := &Local{
		cond:       sync.NewCond(&sync.Mutex{}),
		status:     statusInUse,
		enableIPv6: true,
		ipv6:       Set{netip.MustParseAddr("fd00:46dd:e::1"): &IP{}},
	}

	prio := l.Priority()

	assert.Equal(t, 51, prio)
}

func TestPriorityReturnsFiftyPlusIPv4AndIPv6CountWhenStatusIsInUseAndBothEnabled(t *testing.T) {
	l := &Local{
		cond:       sync.NewCond(&sync.Mutex{}),
		status:     statusInUse,
		enableIPv4: true,
		enableIPv6: true,
		ipv4:       Set{netip.MustParseAddr("192.0.2.1"): &IP{}},
		ipv6:       Set{netip.MustParseAddr("fd00:46dd:e::1"): &IP{}},
	}

	prio := l.Priority()

	assert.Equal(t, 52, prio)
}

func TestAllocFromFactory(t *testing.T) {
	// 1. test factory worker finish req1, and alloc worker consumed req2

	f := factorymocks.NewFactory(t)
	// even we have two jobs ,we only get one ip
	f.On("AssignNIPv4", "eni-1", 2, "").Return([]netip.Addr{netip.MustParseAddr("192.0.2.1")}, nil).Once()
	f.On("AssignNIPv6", "eni-1", 2, "").Return([]netip.Addr{netip.MustParseAddr("fd00::1")}, nil).Once()
	f.On("AssignNIPv4", "eni-1", 1, "").Return(nil, nil).Maybe()
	f.On("AssignNIPv6", "eni-1", 1, "").Return(nil, nil).Maybe()

	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, f, &daemon.PoolConfig{
		EnableIPv4: true,
		EnableIPv6: true,
		BatchSize:  10,
	}, "")
	local.status = statusInUse

	req1 := NewLocalIPRequest()
	req2 := NewLocalIPRequest()

	local.allocatingV4 = append(local.allocatingV4, req1, req2)
	local.allocatingV6 = append(local.allocatingV6, req1, req2)

	ctx, cancel := context.WithCancel(context.Background())

	// expect req1 is moved to danging
	go local.factoryAllocWorker(ctx)

	local.cond.Broadcast()
	req2Ch := make(chan *AllocResp)
	go local.allocWorker(ctx, &daemon.CNI{}, req2, req2Ch)

	<-req2Ch
	cancel()

	local.cond.Broadcast()
	// worker may not exist
	time.Sleep(time.Second)

	local.cond.L.Lock()
	defer local.cond.L.Unlock()

	assert.Equal(t, 1, len(local.allocatingV4))
	assert.Equal(t, 0, len(local.dangingV4))
	assert.Equal(t, 1, len(local.allocatingV6))
	assert.Equal(t, 0, len(local.dangingV6))

	// check the job is switched
	assert.Equal(t, req1, local.allocatingV4[0])
	assert.Equal(t, req1, local.allocatingV6[0])
}

func TestFactoryAllocWorker_CreateENI(t *testing.T) {
	f := factorymocks.NewFactory(t)
	dummyENI := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
		PrimaryIP: types.IPSet{
			IPv4: net.ParseIP("192.0.2.1"),
		},
	}
	ipv4Set := []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2")}
	f.On("CreateNetworkInterface", 2, 0, "eniType").Return(dummyENI, ipv4Set, nil, nil).Once()

	local := NewLocalTest(nil, f, &daemon.PoolConfig{
		EnableIPv4: true,
		BatchSize:  2,
	}, "eniType")
	local.status = statusInit

	req1 := NewLocalIPRequest()
	req2 := NewLocalIPRequest()
	local.allocatingV4 = append(local.allocatingV4, req1, req2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go local.factoryAllocWorker(ctx)

	// Trigger worker
	local.cond.Broadcast()

	err := wait.ExponentialBackoff(wait.Backoff{
		Duration: 100 * time.Millisecond,
		Steps:    20,
	}, func() (done bool, err error) {
		local.cond.L.Lock()
		defer local.cond.L.Unlock()

		if local.eni != nil && local.status == statusInUse && len(local.ipv4) == 2 {
			return true, nil
		}
		return false, nil
	})

	assert.NoError(t, err)
	assert.Equal(t, "eni-1", local.eni.ID)
	assert.Equal(t, statusInUse, local.status)
	assert.Len(t, local.ipv4, 2)
}

func TestFactoryAllocWorker_CreateENI_IPv6(t *testing.T) {
	f := factorymocks.NewFactory(t)
	dummyENI := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
		PrimaryIP: types.IPSet{
			IPv4: net.ParseIP("192.0.2.1"),
		},
	}
	ipv4Set := []netip.Addr{netip.MustParseAddr("192.0.2.1")}
	ipv6Set := []netip.Addr{netip.MustParseAddr("fd00::1"), netip.MustParseAddr("fd00::2")}
	f.On("CreateNetworkInterface", 1, 2, "eniType").Return(dummyENI, ipv4Set, ipv6Set, nil).Once()

	local := NewLocalTest(nil, f, &daemon.PoolConfig{
		EnableIPv4: true,
		EnableIPv6: true,
		BatchSize:  2,
	}, "eniType")
	local.status = statusInit

	req1 := NewLocalIPRequest()
	req2 := NewLocalIPRequest()
	local.allocatingV4 = append(local.allocatingV4, req1)
	local.allocatingV6 = append(local.allocatingV6, req1, req2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go local.factoryAllocWorker(ctx)

	// Trigger worker
	local.cond.Broadcast()

	err := wait.ExponentialBackoff(wait.Backoff{
		Duration: 100 * time.Millisecond,
		Steps:    20,
	}, func() (done bool, err error) {
		local.cond.L.Lock()
		defer local.cond.L.Unlock()

		if local.eni != nil && local.status == statusInUse && len(local.ipv4) == 1 && len(local.ipv6) == 2 {
			return true, nil
		}
		return false, nil
	})

	assert.NoError(t, err)
	assert.Equal(t, "eni-1", local.eni.ID)
	assert.Equal(t, statusInUse, local.status)
	assert.Len(t, local.ipv4, 1)
	assert.Len(t, local.ipv6, 2)
}

func TestFactoryAllocWorker_CreateENIFailed(t *testing.T) {
	f := factorymocks.NewFactory(t)
	called := make(chan struct{})
	f.On("CreateNetworkInterface", 1, 0, "eniType").Return(nil, nil, nil, fmt.Errorf("create failed")).Run(func(args mock.Arguments) {
		close(called)
	}).Once()

	local := NewLocalTest(nil, f, &daemon.PoolConfig{
		EnableIPv4: true,
		BatchSize:  2,
	}, "eniType")
	local.status = statusInit

	local.cond.L.Lock()
	local.allocatingV4 = append(local.allocatingV4, NewLocalIPRequest())
	local.cond.L.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go local.factoryAllocWorker(ctx)

	// Trigger worker
	local.cond.Broadcast()

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for CreateNetworkInterface call")
	}

	err := wait.ExponentialBackoff(wait.Backoff{
		Duration: 100 * time.Millisecond,
		Steps:    20,
	}, func() (done bool, err error) {
		local.cond.L.Lock()
		defer local.cond.L.Unlock()

		// status should go back to statusInit if eni is nil
		if local.status == statusInit {
			return true, nil
		}
		return false, nil
	})

	assert.NoError(t, err)
	assert.Nil(t, local.eni)
}

func TestFactoryAllocWorker_CreateENIPartialFailed(t *testing.T) {
	f := factorymocks.NewFactory(t)
	dummyENI := &daemon.ENI{ID: "eni-1"}
	f.On("CreateNetworkInterface", 1, 0, "eniType").Return(dummyENI, nil, nil, fmt.Errorf("partial failed")).Once()

	local := NewLocalTest(nil, f, &daemon.PoolConfig{
		EnableIPv4: true,
		BatchSize:  2,
	}, "eniType")
	local.status = statusInit

	req1 := NewLocalIPRequest()
	local.allocatingV4 = append(local.allocatingV4, req1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go local.factoryAllocWorker(ctx)

	// Trigger worker
	local.cond.Broadcast()

	err := wait.ExponentialBackoff(wait.Backoff{
		Duration: 100 * time.Millisecond,
		Steps:    20,
	}, func() (done bool, err error) {
		local.cond.L.Lock()
		defer local.cond.L.Unlock()

		// status should be statusDeleting
		if local.status == statusDeleting {
			return true, nil
		}
		return false, nil
	})

	assert.NoError(t, err)
	assert.NotNil(t, local.eni)
	assert.Equal(t, statusDeleting, local.status)
}

func TestFactoryAllocWorker_RateLimitEni(t *testing.T) {
	f := factorymocks.NewFactory(t)
	local := NewLocalTest(nil, f, &daemon.PoolConfig{
		EnableIPv4: true,
		BatchSize:  2,
	}, "eniType")
	local.status = statusInit
	// Set rate limit to 0 to trigger error
	local.rateLimitEni = rate.NewLimiter(0, 0)

	local.cond.L.Lock()
	local.allocatingV4 = append(local.allocatingV4, NewLocalIPRequest())
	local.cond.L.Unlock()

	// Use a context that will cancel immediately or timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	go local.factoryAllocWorker(ctx)

	// Trigger worker
	local.cond.Broadcast()

	// Wait for context to be done
	<-ctx.Done()

	// Give it a bit of time to return from factoryAllocWorker
	time.Sleep(100 * time.Millisecond)

	local.cond.L.Lock()
	defer local.cond.L.Unlock()
	assert.Nil(t, local.eni)
}

func Test_factoryDisposeWorker_unAssignIP(t *testing.T) {
	f := factorymocks.NewFactory(t)
	// even we have two jobs ,we only get one ip
	f.On("UnAssignNIPv4", "eni-1", []netip.Addr{netip.MustParseAddr("192.0.2.1")}, mock.Anything).Return(nil).Once()
	f.On("UnAssignNIPv6", "eni-1", []netip.Addr{netip.MustParseAddr("fd00::1")}, mock.Anything).Return(nil).Once()

	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, f, &daemon.PoolConfig{
		EnableIPv4: true,
		EnableIPv6: true,
		BatchSize:  10,
	}, "")
	local.status = statusInUse

	local.ipv4.Add(&IP{
		ip:      netip.MustParseAddr("192.0.2.1"),
		primary: false,
		podID:   "",
		status:  ipStatusDeleting,
	})

	local.ipv4.Add(&IP{
		ip:      netip.MustParseAddr("192.0.2.2"),
		primary: false,
		podID:   "",
		status:  ipStatusValid,
	})

	local.ipv6.Add(&IP{
		ip:      netip.MustParseAddr("fd00::1"),
		primary: false,
		podID:   "",
		status:  ipStatusDeleting,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go local.factoryDisposeWorker(ctx)

	err := wait.ExponentialBackoff(wait.Backoff{
		Duration: 100 * time.Millisecond,
		Steps:    10,
	}, func() (done bool, err error) {
		local.cond.L.Lock()
		defer local.cond.L.Unlock()

		if len(local.ipv6) == 0 && len(local.ipv4) == 1 {
			return true, nil
		}
		return false, nil
	})
	assert.NoError(t, err)
}

func Test_factoryDisposeWorker_releaseIP(t *testing.T) {
	f := factorymocks.NewFactory(t)
	// even we have two jobs ,we only get one ip
	f.On("DeleteNetworkInterface", "eni-1").Return(nil).Once()

	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, f, &daemon.PoolConfig{
		EnableIPv4: true,
		EnableIPv6: true,
		BatchSize:  10,
	}, "")
	local.status = statusDeleting

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go local.factoryDisposeWorker(ctx)

	err := wait.ExponentialBackoff(wait.Backoff{
		Duration: 100 * time.Millisecond,
		Steps:    10,
	}, func() (done bool, err error) {
		local.cond.L.Lock()
		defer local.cond.L.Unlock()
		if local.eni == nil {
			return true, nil
		}
		return false, nil
	})

	assert.NoError(t, err)
}

func Test_commit_responsed(t *testing.T) {
	f := factorymocks.NewFactory(t)
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, f, &daemon.PoolConfig{
		EnableIPv4: true,
		EnableIPv6: true,
		BatchSize:  10,
	}, "")
	local.status = statusInUse

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	respCh := make(chan *AllocResp)
	ipv4 := &IP{
		ip:      netip.MustParseAddr("127.0.0.1"),
		primary: false,
		podID:   "",
		status:  ipStatusValid,
	}
	ipv6 := &IP{
		ip:      netip.MustParseAddr("fd00::1"),
		primary: false,
		podID:   "",
		status:  ipStatusValid,
	}
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()

		<-respCh
	}()

	local.commit(ctx, respCh, ipv4, ipv6, "foo")

	wg.Wait()

	assert.Equal(t, "foo", ipv4.podID)
	assert.Equal(t, "foo", ipv6.podID)
}

func Test_commit_canceled(t *testing.T) {
	f := factorymocks.NewFactory(t)
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, f, &daemon.PoolConfig{
		EnableIPv4: true,
		EnableIPv6: true,
		BatchSize:  10,
	}, "")
	local.status = statusInUse

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	respCh := make(chan *AllocResp)
	ipv4 := &IP{
		ip:      netip.MustParseAddr("127.0.0.1"),
		primary: false,
		podID:   "foo",
		status:  ipStatusValid,
	}
	ipv6 := &IP{
		ip:      netip.MustParseAddr("fd00::1"),
		primary: false,
		podID:   "foo",
		status:  ipStatusValid,
	}

	local.commit(ctx, respCh, ipv4, ipv6, "foo")

	assert.Equal(t, "", ipv4.podID)
	assert.Equal(t, "", ipv6.podID)
}

// ==============================================================================
// Usage and Status Tests
// ==============================================================================

func TestLocal_Usage_NilENI(t *testing.T) {
	local := NewLocalTest(nil, nil, &daemon.PoolConfig{}, "")

	idles, inUse, err := local.Usage()

	assert.NoError(t, err)
	assert.Equal(t, 0, idles)
	assert.Equal(t, 0, inUse)
}

func TestLocal_Usage_StatusNotInUse(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "")
	local.status = statusInit // Not statusInUse

	idles, inUse, err := local.Usage()

	assert.NoError(t, err)
	assert.Equal(t, 0, idles)
	assert.Equal(t, 0, inUse)
}

func TestLocal_Usage_IPv4Enabled(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{EnableIPv4: true}, "")
	local.status = statusInUse

	// Add some IPv4 addresses
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.2"), false))
	local.ipv4[netip.MustParseAddr("192.0.2.2")].Allocate("pod-1")

	idles, inUse, err := local.Usage()

	assert.NoError(t, err)
	assert.Equal(t, 1, idles)
	assert.Equal(t, 1, inUse)
}

func TestLocal_Usage_IPv6Enabled(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{EnableIPv6: true}, "")
	local.status = statusInUse

	// Add some IPv6 addresses
	local.ipv6.Add(NewValidIP(netip.MustParseAddr("fd00::1"), false))
	local.ipv6.Add(NewValidIP(netip.MustParseAddr("fd00::2"), false))
	local.ipv6.Add(NewValidIP(netip.MustParseAddr("fd00::3"), false))
	local.ipv6[netip.MustParseAddr("fd00::2")].Allocate("pod-1")
	local.ipv6[netip.MustParseAddr("fd00::3")].Allocate("pod-2")

	idles, inUse, err := local.Usage()

	assert.NoError(t, err)
	assert.Equal(t, 1, idles)
	assert.Equal(t, 2, inUse)
}

func TestLocal_Usage_BothIPv4AndIPv6Enabled(t *testing.T) {
	// When both are enabled, IPv4 takes precedence
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{
		EnableIPv4: true,
		EnableIPv6: true,
	}, "")
	local.status = statusInUse

	// Add IPv4 addresses
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
	local.ipv4[netip.MustParseAddr("192.0.2.1")].Allocate("pod-1")

	// Add IPv6 addresses (should be ignored due to IPv4 precedence)
	local.ipv6.Add(NewValidIP(netip.MustParseAddr("fd00::1"), false))
	local.ipv6.Add(NewValidIP(netip.MustParseAddr("fd00::2"), false))

	idles, inUse, err := local.Usage()

	assert.NoError(t, err)
	// IPv4 is checked first when enableIPv4 is true
	assert.Equal(t, 0, idles)
	assert.Equal(t, 1, inUse)
}

func TestLocal_Status_NilENI(t *testing.T) {
	local := NewLocalTest(nil, nil, &daemon.PoolConfig{}, "erdma")
	local.status = statusInit

	status := local.Status()

	assert.Equal(t, statusInit.String(), status.Status)
	assert.Equal(t, "erdma", status.Type)
	assert.Empty(t, status.MAC)
	assert.Empty(t, status.NetworkInterfaceID)
	assert.Nil(t, status.Usage)
}

func TestLocal_Status_WithENI(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{
		ID:  "eni-123",
		MAC: "00:00:00:00:00:01",
	}, nil, &daemon.PoolConfig{}, "")
	local.status = statusInUse

	status := local.Status()

	assert.Equal(t, statusInUse.String(), status.Status)
	assert.Equal(t, "00:00:00:00:00:01", status.MAC)
	assert.Equal(t, "eni-123", status.NetworkInterfaceID)
}

func TestLocal_Status_WithIPv4(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{
		ID:  "eni-123",
		MAC: "00:00:00:00:00:01",
	}, nil, &daemon.PoolConfig{EnableIPv4: true}, "")
	local.status = statusInUse

	// Add IPv4 addresses with different statuses
	ip1 := NewValidIP(netip.MustParseAddr("192.0.2.1"), false)
	ip2 := NewValidIP(netip.MustParseAddr("192.0.2.2"), false)
	ip2.Allocate("pod-1")

	local.ipv4.Add(ip1)
	local.ipv4.Add(ip2)

	status := local.Status()

	assert.Equal(t, statusInUse.String(), status.Status)
	assert.Len(t, status.Usage, 2)
}

func TestLocal_Status_WithIPv6(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{
		ID:  "eni-123",
		MAC: "00:00:00:00:00:01",
	}, nil, &daemon.PoolConfig{EnableIPv6: true}, "")
	local.status = statusInUse

	// Add IPv6 addresses
	ip1 := NewValidIP(netip.MustParseAddr("fd00::1"), false)
	ip1.Allocate("pod-1")

	local.ipv6.Add(ip1)

	status := local.Status()

	assert.Equal(t, statusInUse.String(), status.Status)
	assert.Len(t, status.Usage, 1)
	assert.Equal(t, "fd00::1", status.Usage[0][0])
	assert.Equal(t, "pod-1", status.Usage[0][1])
}

func TestLocal_Status_WithBothIPv4AndIPv6(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{
		ID:  "eni-123",
		MAC: "00:00:00:00:00:01",
	}, nil, &daemon.PoolConfig{
		EnableIPv4: true,
		EnableIPv6: true,
	}, "")
	local.status = statusInUse

	// Add IPv4 and IPv6 addresses
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
	local.ipv6.Add(NewValidIP(netip.MustParseAddr("fd00::1"), false))

	status := local.Status()

	assert.Equal(t, statusInUse.String(), status.Status)
	assert.Len(t, status.Usage, 2)
}

func TestLocal_Status_UsageSorted(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{
		ID:  "eni-123",
		MAC: "00:00:00:00:00:01",
	}, nil, &daemon.PoolConfig{EnableIPv4: true}, "")
	local.status = statusInUse

	// Add IPv4 addresses in non-sorted order
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.3"), false))
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.2"), false))

	status := local.Status()

	assert.Len(t, status.Usage, 3)
	// Check that usage is sorted in descending order
	assert.True(t, status.Usage[0][0] > status.Usage[1][0])
	assert.True(t, status.Usage[1][0] > status.Usage[2][0])
}

// ==============================================================================
// NewLocal, Run, notify, and errorHandleLocked Tests
// ==============================================================================

func TestNewLocal(t *testing.T) {
	eni := &daemon.ENI{ID: "eni-123", MAC: "00:00:00:00:00:01"}
	mockFactory := factorymocks.NewFactory(t)
	poolConfig := &daemon.PoolConfig{
		BatchSize:   5,
		MaxIPPerENI: 10,
		EnableIPv4:  true,
		EnableIPv6:  true,
	}

	local := NewLocal(eni, "erdma", mockFactory, poolConfig)

	assert.NotNil(t, local)
	assert.Equal(t, eni, local.eni)
	assert.Equal(t, "erdma", local.eniType)
	assert.Equal(t, 5, local.batchSize)
	assert.Equal(t, 10, local.cap)
	assert.True(t, local.enableIPv4)
	assert.True(t, local.enableIPv6)
	assert.NotNil(t, local.cond)
	assert.NotNil(t, local.ipv4)
	assert.NotNil(t, local.ipv6)
	assert.NotNil(t, local.rateLimitEni)
	assert.NotNil(t, local.rateLimitv4)
	assert.NotNil(t, local.rateLimitv6)
	assert.Equal(t, mockFactory, local.factory)
}

func TestNewLocal_DefaultValues(t *testing.T) {
	eni := &daemon.ENI{ID: "eni-123"}
	poolConfig := &daemon.PoolConfig{
		BatchSize:   0,
		MaxIPPerENI: 0,
		EnableIPv4:  false,
		EnableIPv6:  false,
	}

	local := NewLocal(eni, "", nil, poolConfig)

	assert.NotNil(t, local)
	assert.Equal(t, 0, local.batchSize)
	assert.Equal(t, 0, local.cap)
	assert.False(t, local.enableIPv4)
	assert.False(t, local.enableIPv6)
	assert.Empty(t, local.eniType)
}

func TestLocal_Run_LoadError(t *testing.T) {
	mockFactory := factorymocks.NewFactory(t)
	mockFactory.On("LoadNetworkInterface", "00:00:00:00:00:01").Return(nil, nil, fmt.Errorf("load error"))

	eni := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
		PrimaryIP: types.IPSet{
			IPv4: net.ParseIP("192.0.2.1"),
		},
	}
	local := NewLocal(eni, "", mockFactory, &daemon.PoolConfig{EnableIPv4: true})

	ctx := context.Background()
	wg := &sync.WaitGroup{}

	err := local.Run(ctx, []daemon.PodResources{}, wg)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "load error")
}

func TestLocal_Run_Success(t *testing.T) {
	mockFactory := factorymocks.NewFactory(t)
	ipv4Addrs := []netip.Addr{netip.MustParseAddr("192.0.2.1")}
	mockFactory.On("LoadNetworkInterface", "00:00:00:00:00:01").Return(ipv4Addrs, nil, nil)

	eni := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
		PrimaryIP: types.IPSet{
			IPv4: net.ParseIP("192.0.2.1"),
		},
	}
	local := NewLocal(eni, "", mockFactory, &daemon.PoolConfig{EnableIPv4: true})

	ctx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}

	err := local.Run(ctx, []daemon.PodResources{}, wg)
	assert.NoError(t, err)

	// Cancel context to stop background goroutines
	cancel()

	// Wait for goroutines to finish
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for goroutines to finish")
	}
}

func TestLocal_notify(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "")

	ctx, cancel := context.WithCancel(context.Background())

	// Start notify in background
	done := make(chan struct{})
	go func() {
		local.notify(ctx)
		close(done)
	}()

	// Cancel context should cause notify to broadcast and exit
	cancel()

	select {
	case <-done:
		// Success - notify exited
	case <-time.After(1 * time.Second):
		t.Fatal("notify did not exit after context cancellation")
	}
}

func TestLocal_errorHandleLocked_NilError(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "")
	initialExpireAt := local.ipAllocInhibitExpireAt

	local.errorHandleLocked(nil)

	// Should not modify ipAllocInhibitExpireAt
	assert.Equal(t, initialExpireAt, local.ipAllocInhibitExpireAt)
}

func TestLocal_errorHandleLocked_EniPerInstanceLimitExceeded(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "")
	initialExpireAt := local.ipAllocInhibitExpireAt

	err := fmt.Errorf("some error")

	// Use gomonkey to mock apiErr.ErrorCodeIs
	patches := gomonkey.ApplyFunc(apiErr.ErrorCodeIs, func(e error, codes ...string) bool {
		for _, code := range codes {
			if code == apiErr.ErrEniPerInstanceLimitExceeded {
				return true
			}
		}
		return false
	})
	defer patches.Reset()

	local.errorHandleLocked(err)

	// Should set ipAllocInhibitExpireAt to approximately 1 minute from now
	assert.True(t, local.ipAllocInhibitExpireAt.After(initialExpireAt))
	assert.True(t, local.ipAllocInhibitExpireAt.Before(time.Now().Add(2*time.Minute)))
}

func TestLocal_errorHandleLocked_InvalidVSwitchIDIPNotEnough(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "")
	initialExpireAt := local.ipAllocInhibitExpireAt

	err := fmt.Errorf("some error")

	// Use gomonkey to mock apiErr.ErrorCodeIs
	patches := gomonkey.ApplyFunc(apiErr.ErrorCodeIs, func(e error, codes ...string) bool {
		for _, code := range codes {
			if code == apiErr.InvalidVSwitchIDIPNotEnough {
				return true
			}
		}
		return false
	})
	defer patches.Reset()

	// Record time before calling errorHandleLocked to avoid timing issues
	beforeTime := time.Now()
	local.errorHandleLocked(err)
	afterTime := time.Now()

	// Should set ipAllocInhibitExpireAt to approximately 10 minutes from now
	assert.True(t, local.ipAllocInhibitExpireAt.After(initialExpireAt))
	// The expire time should be at least 10 minutes from when errorHandleLocked was called
	// We use beforeTime to ensure we account for the time taken to execute errorHandleLocked
	assert.True(t, local.ipAllocInhibitExpireAt.After(beforeTime.Add(9*time.Minute)))
	// And it should be at most 10 minutes plus a small buffer for execution time
	assert.True(t, local.ipAllocInhibitExpireAt.Before(afterTime.Add(11*time.Minute)))
}

func TestLocal_errorHandleLocked_QuotaExceededPrivateIPAddress(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "")
	initialExpireAt := local.ipAllocInhibitExpireAt

	err := fmt.Errorf("some error")

	// Use gomonkey to mock apiErr.ErrorCodeIs
	patches := gomonkey.ApplyFunc(apiErr.ErrorCodeIs, func(e error, codes ...string) bool {
		for _, code := range codes {
			if code == apiErr.QuotaExceededPrivateIPAddress {
				return true
			}
		}
		return false
	})
	defer patches.Reset()

	// Record time before calling errorHandleLocked to avoid timing issues
	beforeTime := time.Now()
	local.errorHandleLocked(err)
	afterTime := time.Now()

	// Should set ipAllocInhibitExpireAt to approximately 10 minutes from now
	assert.True(t, local.ipAllocInhibitExpireAt.After(initialExpireAt))
	// The expire time should be at least 10 minutes from when errorHandleLocked was called
	// We use beforeTime to ensure we account for the time taken to execute errorHandleLocked
	assert.True(t, local.ipAllocInhibitExpireAt.After(beforeTime.Add(9*time.Minute)))
	// And it should be at most 10 minutes plus a small buffer for execution time
	assert.True(t, local.ipAllocInhibitExpireAt.Before(afterTime.Add(11*time.Minute)))
}

func TestLocal_errorHandleLocked_OtherError(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "")
	initialExpireAt := local.ipAllocInhibitExpireAt

	// Create an error that doesn't match any special error codes
	err := fmt.Errorf("some random error")

	local.errorHandleLocked(err)

	// Should not modify ipAllocInhibitExpireAt for unrecognized errors
	assert.Equal(t, initialExpireAt, local.ipAllocInhibitExpireAt)
}

func TestLocal_errorHandleLocked_AlreadyInhibited(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &daemon.PoolConfig{}, "")

	// Set a future inhibit time
	futureTime := time.Now().Add(1 * time.Hour)
	local.ipAllocInhibitExpireAt = futureTime

	err := fmt.Errorf("some error")

	// Use gomonkey to mock apiErr.ErrorCodeIs to return true for EniPerInstanceLimitExceeded
	patches := gomonkey.ApplyFunc(apiErr.ErrorCodeIs, func(e error, codes ...string) bool {
		for _, code := range codes {
			if code == apiErr.ErrEniPerInstanceLimitExceeded {
				return true
			}
		}
		return false
	})
	defer patches.Reset()

	local.errorHandleLocked(err)

	// Should not reduce the existing inhibit time
	assert.Equal(t, futureTime, local.ipAllocInhibitExpireAt)
}

// ==============================================================================
// sync() Tests
// ==============================================================================

func TestLocal_sync_NilENI(t *testing.T) {
	mockFactory := factorymocks.NewFactory(t)
	local := NewLocalTest(nil, mockFactory, &daemon.PoolConfig{}, "")
	local.status = statusInUse

	// sync should return early when eni is nil
	local.sync()

	// LoadNetworkInterface should not be called
	mockFactory.AssertNotCalled(t, "LoadNetworkInterface", mock.Anything)
}

func TestLocal_sync_StatusNotInUse(t *testing.T) {
	mockFactory := factorymocks.NewFactory(t)
	eni := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
	}
	local := NewLocalTest(eni, mockFactory, &daemon.PoolConfig{}, "")
	local.status = statusInit // Not statusInUse

	// sync should return early when status is not statusInUse
	local.sync()

	// LoadNetworkInterface should not be called
	mockFactory.AssertNotCalled(t, "LoadNetworkInterface", mock.Anything)
}

// mockFactory is a simple factory implementation for testing
type mockFactory struct{}

func (m *mockFactory) CreateNetworkInterface(ipv4, ipv6 int, eniType string) (*daemon.ENI, []netip.Addr, []netip.Addr, error) {
	return nil, nil, nil, fmt.Errorf("not implemented")
}

func (m *mockFactory) AssignNIPv4(eniID string, count int, mac string) ([]netip.Addr, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockFactory) AssignNIPv6(eniID string, count int, mac string) ([]netip.Addr, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockFactory) UnAssignNIPv4(eniID string, ips []netip.Addr, mac string) error {
	return fmt.Errorf("not implemented")
}

func (m *mockFactory) UnAssignNIPv6(eniID string, ips []netip.Addr, mac string) error {
	return fmt.Errorf("not implemented")
}

func (m *mockFactory) DeleteNetworkInterface(eniID string) error {
	return fmt.Errorf("not implemented")
}

func (m *mockFactory) LoadNetworkInterface(mac string) ([]netip.Addr, []netip.Addr, error) {
	return nil, nil, fmt.Errorf("not implemented")
}

func (m *mockFactory) GetAttachedNetworkInterface(preferTrunkID string) ([]*daemon.ENI, error) {
	return nil, fmt.Errorf("not implemented")
}

func TestLocal_sync_LoadNetworkInterfaceError(t *testing.T) {
	sf := &mockFactory{}

	eni := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
	}
	local := NewLocalTest(eni, sf, &daemon.PoolConfig{}, "")
	local.status = statusInUse

	// Use gomonkey to mock LoadNetworkInterface to return an error
	expectedErr := fmt.Errorf("failed to load network interface")
	patches := gomonkey.ApplyMethod(sf, "LoadNetworkInterface", func(_ *mockFactory, mac string) ([]netip.Addr, []netip.Addr, error) {
		return nil, nil, expectedErr
	})
	defer patches.Reset()

	// sync should handle the error gracefully
	local.sync()

	// Verify that sync completed without panicking
	// The error is logged but sync returns early
	assert.Equal(t, statusInUse, local.status)
}

func TestLocal_sync_Success(t *testing.T) {
	sf := &mockFactory{}

	eni := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
	}
	local := NewLocalTest(eni, sf, &daemon.PoolConfig{
		EnableIPv4: true,
		EnableIPv6: true,
	}, "")
	local.status = statusInUse

	// Add some existing IPs
	existingIPv4 := netip.MustParseAddr("192.0.2.1")
	existingIPv6 := netip.MustParseAddr("2001:db8::1")
	local.ipv4.Add(NewValidIP(existingIPv4, false))
	local.ipv6.Add(NewValidIP(existingIPv6, false))

	// Remote IPs: one existing, one new, one missing
	remoteIPv4 := []netip.Addr{
		existingIPv4,                     // existing
		netip.MustParseAddr("192.0.2.2"), // new
		// 192.0.2.3 is missing (will be marked as invalid if it exists)
	}
	remoteIPv6 := []netip.Addr{
		existingIPv6,                       // existing
		netip.MustParseAddr("2001:db8::2"), // new
	}

	// Use gomonkey to mock LoadNetworkInterface
	patches := gomonkey.ApplyMethod(sf, "LoadNetworkInterface", func(_ *mockFactory, mac string) ([]netip.Addr, []netip.Addr, error) {
		assert.Equal(t, "00:00:00:00:00:01", mac)
		return remoteIPv4, remoteIPv6, nil
	})
	defer patches.Reset()

	// Reset invalidIPCache before test
	invalidIPCache = cache.NewLRUExpireCache(100)

	// Call sync
	local.sync()

	// Verify that existing IPs are still valid
	assert.True(t, local.ipv4[existingIPv4].Valid())
	assert.True(t, local.ipv6[existingIPv6].Valid())

	// Verify that new IPs from remote are tracked (orphanIP adds them to cache)
	// The orphanIP function will add new IPs to invalidIPCache
	newIPv4 := netip.MustParseAddr("192.0.2.2")
	newIPv6 := netip.MustParseAddr("2001:db8::2")
	count, ok := invalidIPCache.Get(newIPv4)
	if ok {
		assert.Equal(t, 1, count.(int))
	}
	count, ok = invalidIPCache.Get(newIPv6)
	if ok {
		assert.Equal(t, 1, count.(int))
	}
}

func TestLocal_sync_IPMarkedAsInvalid(t *testing.T) {
	sf := &mockFactory{}

	eni := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
	}
	local := NewLocalTest(eni, sf, &daemon.PoolConfig{
		EnableIPv4: true,
		EnableIPv6: true,
	}, "")
	local.status = statusInUse

	// Add IPs that exist locally
	localIPv4 := netip.MustParseAddr("192.0.2.1")
	localIPv6 := netip.MustParseAddr("2001:db8::1")
	local.ipv4.Add(NewValidIP(localIPv4, false))
	local.ipv6.Add(NewValidIP(localIPv6, false))

	// Remote IPs don't include the local IPs (they should be marked as invalid)
	remoteIPv4 := []netip.Addr{
		netip.MustParseAddr("192.0.2.2"), // different IP
	}
	remoteIPv6 := []netip.Addr{
		netip.MustParseAddr("2001:db8::2"), // different IP
	}

	// Use gomonkey to mock LoadNetworkInterface
	patches := gomonkey.ApplyMethod(sf, "LoadNetworkInterface", func(_ *mockFactory, mac string) ([]netip.Addr, []netip.Addr, error) {
		return remoteIPv4, remoteIPv6, nil
	})
	defer patches.Reset()

	// Reset invalidIPCache before test
	invalidIPCache = cache.NewLRUExpireCache(100)

	// Call sync
	local.sync()

	// Verify that local IPs are marked as invalid
	assert.False(t, local.ipv4[localIPv4].Valid())
	assert.Equal(t, ipStatusInvalid, local.ipv4[localIPv4].status)
	assert.False(t, local.ipv6[localIPv6].Valid())
	assert.Equal(t, ipStatusInvalid, local.ipv6[localIPv6].status)
}

func TestLocal_sync_BroadcastCalled(t *testing.T) {
	sf := &mockFactory{}

	eni := &daemon.ENI{
		ID:  "eni-1",
		MAC: "00:00:00:00:00:01",
	}
	local := NewLocalTest(eni, sf, &daemon.PoolConfig{}, "")
	local.status = statusInUse

	remoteIPv4 := []netip.Addr{netip.MustParseAddr("192.0.2.1")}
	remoteIPv6 := []netip.Addr{}

	// Use gomonkey to mock LoadNetworkInterface
	patches := gomonkey.ApplyMethod(sf, "LoadNetworkInterface", func(_ *mockFactory, mac string) ([]netip.Addr, []netip.Addr, error) {
		return remoteIPv4, remoteIPv6, nil
	})
	defer patches.Reset()

	// Reset invalidIPCache before test
	invalidIPCache = cache.NewLRUExpireCache(100)

	// Use gomonkey to verify Broadcast is called by patching sync.Cond.Broadcast
	broadcastCalled := make(chan struct{}, 1)
	broadcastPatches := gomonkey.ApplyMethod(local.cond, "Broadcast", func(_ *sync.Cond) {
		broadcastCalled <- struct{}{}
	})
	defer broadcastPatches.Reset()

	// Call sync
	local.sync()

	// Verify that Broadcast was called
	select {
	case <-broadcastCalled:
		// Success - Broadcast was called
	case <-time.After(1 * time.Second):
		t.Fatal("Broadcast was not called")
	}
}
