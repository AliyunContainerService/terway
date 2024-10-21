package eni

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/util/cache"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/AliyunContainerService/terway/pkg/factory"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

func NewLocalTest(eni *daemon.ENI, factory factory.Factory, poolConfig *types.PoolConfig, eniType string) *Local {
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
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{}, "")
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
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{}, "")
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
	local := NewLocalTest(nil, nil, &types.PoolConfig{}, "")
	request := &LocalIPResource{
		ENI: daemon.ENI{ID: "eni-1"},
		IP:  types.IPSet2{IPv4: netip.MustParseAddr("192.0.2.1")},
	}
	cni := &daemon.CNI{PodID: "pod-1"}

	ok, _ := local.Release(context.Background(), cni, request)
	assert.False(t, ok)
}

func TestLocal_Release_DifferentENIID(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{}, "")
	request := &LocalIPResource{
		ENI: daemon.ENI{ID: "eni-2"},
		IP:  types.IPSet2{IPv4: netip.MustParseAddr("192.0.2.1")},
	}
	cni := &daemon.CNI{PodID: "pod-1"}

	ok, _ := local.Release(context.Background(), cni, request)
	assert.False(t, ok)
}

func TestLocal_Release_ValidIPv4_ReleaseIPv6(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{}, "")
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
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{
		EnableIPv4: true,
	}, "")
	cni := &daemon.CNI{PodID: "pod-1"}

	respCh := make(chan *AllocResp)
	go local.allocWorker(context.Background(), cni, nil, respCh, func() {})

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
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{
		EnableIPv6: true,
	}, "")
	cni := &daemon.CNI{PodID: "pod-1"}

	respCh := make(chan *AllocResp)
	go local.allocWorker(context.Background(), cni, nil, respCh, func() {})

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
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{
		EnableIPv4: true,
	}, "")
	cni := &daemon.CNI{PodID: "pod-1"}

	ctx, cancel := context.WithCancel(context.Background())
	respCh := make(chan *AllocResp)
	go local.allocWorker(ctx, cni, nil, respCh, func() {})

	cancel()

	_, ok := <-respCh
	assert.False(t, ok)
}

func TestLocal_Dispose(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{}, "")
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
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{}, "")
	local.status = statusInUse
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), true))
	local.ipv6.Add(NewValidIP(netip.MustParseAddr("fd00:46dd:e::1"), false))

	n := local.Dispose(1)

	assert.Equal(t, 1, n)
	assert.Equal(t, statusDeleting, local.status)
}

func TestLocal_Allocate_NoCache(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{MaxIPPerENI: 2, EnableIPv4: true}, "")

	request := &LocalIPRequest{NoCache: true}
	cni := &daemon.CNI{PodID: "pod-1"}

	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.2"), false))

	_, resp := local.Allocate(context.Background(), cni, request)

	assert.Equal(t, 1, len(resp))
}

func TestLocal_DisposeWholeERDMA(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{}, "erdma")
	local.status = statusInUse
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))

	n := local.Dispose(1)

	assert.Equal(t, 1, n)
	assert.NotEqual(t, statusDeleting, local.status)
}

func TestLocal_Allocate_ERDMA(t *testing.T) {
	localErdma := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{MaxIPPerENI: 2, EnableIPv4: true}, "erdma")

	request := &LocalIPRequest{NoCache: true}
	cni := &daemon.CNI{PodID: "pod-1"}

	localErdma.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
	localErdma.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.2"), false))

	_, resp := localErdma.Allocate(context.Background(), cni, request)

	assert.Equal(t, 1, len(resp))
	assert.Equal(t, ResourceTypeMismatch, resp[0].Condition)

	request = &LocalIPRequest{NoCache: true, LocalIPType: LocalIPTypeERDMA}

	_, resp = localErdma.Allocate(context.Background(), cni, request)
	assert.Equal(t, 1, len(resp))
	assert.NotEqual(t, ResourceTypeMismatch, resp[0].Condition)

	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{MaxIPPerENI: 2, EnableIPv4: true}, "")
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.2"), false))

	request = &LocalIPRequest{NoCache: true}
	_, resp = local.Allocate(context.Background(), cni, request)

	assert.Equal(t, 1, len(resp))
	assert.NotEqual(t, ResourceTypeMismatch, resp[0].Condition)

	request = &LocalIPRequest{NoCache: true, LocalIPType: LocalIPTypeERDMA}
	_, resp = local.Allocate(context.Background(), cni, request)

	assert.Equal(t, 1, len(resp))
	assert.Equal(t, ResourceTypeMismatch, resp[0].Condition)
}

func TestLocal_Allocate_Inhibit(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{MaxIPPerENI: 2, EnableIPv4: true}, "")

	request := &LocalIPRequest{}
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
