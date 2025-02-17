package eni

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/util/cache"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"

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
