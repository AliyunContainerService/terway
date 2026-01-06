package eni

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	k8smocks "github.com/AliyunContainerService/terway/pkg/k8s/mocks"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
	"github.com/stretchr/testify/mock"
)

var _ NetworkInterface = &timeOut{}

type timeOut struct {
}

func (o *timeOut) Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	return make(chan *AllocResp), nil
}

func (o *timeOut) Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) (bool, error) {
	return true, nil
}

func (o *timeOut) Priority() int {
	return 0
}

func (o *timeOut) Dispose(n int) int {
	return 0
}

func (o *timeOut) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	return nil
}

var _ NetworkInterface = &success{}

type success struct {
	priority int
	IPv4     netip.Addr
}

func (s *success) Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	ch := make(chan *AllocResp)

	go func() {
		ch <- &AllocResp{
			NetworkConfigs: []NetworkResource{
				&LocalIPResource{
					PodID: "",
					ENI:   daemon.ENI{},
					IP: types.IPSet2{
						IPv4: s.IPv4,
					},
				}},
		}
	}()
	return ch, nil
}

func (s *success) Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) (bool, error) {
	return true, nil
}

func (s *success) Priority() int {
	return s.priority
}

func (s *success) Dispose(n int) int {
	return 0
}

func (s *success) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	return nil
}

func TestManagerAllocateReturnsResourcesWhenSuccessful(t *testing.T) {
	mockNI := &success{}
	manager := NewManager(nil, 0, []NetworkInterface{mockNI}, daemon.EniSelectionPolicyMostIPs, k8smocks.NewKubernetes(t))

	request := NewLocalIPRequest()
	resources, err := manager.Allocate(context.Background(), &daemon.CNI{}, &AllocRequest{
		ResourceRequests: []ResourceRequest{request},
	})

	assert.Nil(t, err)
	assert.NotNil(t, resources)
}

func TestManagerAllocateSelectionPolicy(t *testing.T) {
	ip, _ := netip.AddrFromSlice(net.ParseIP("192.168.0.1"))
	mockNI := &success{
		priority: 1,
		IPv4:     ip,
	}
	ip, _ = netip.AddrFromSlice(net.ParseIP("192.168.0.2"))
	mockNI2 := &success{
		priority: 2,
		IPv4:     ip,
	}

	{
		manager := NewManager(nil, 0, []NetworkInterface{mockNI, mockNI2}, daemon.EniSelectionPolicyMostIPs, k8smocks.NewKubernetes(t))

		request := NewLocalIPRequest()
		resources, err := manager.Allocate(context.Background(), &daemon.CNI{}, &AllocRequest{
			ResourceRequests: []ResourceRequest{request},
		})

		assert.Nil(t, err)
		assert.NotNil(t, resources)
		assert.Equal(t, mockNI2.IPv4.String(), resources[0].ToStore()[0].IPv4)
	}

	{
		manager := NewManager(nil, 0, []NetworkInterface{mockNI, mockNI2}, daemon.EniSelectionPolicyLeastIPs, k8smocks.NewKubernetes(t))

		request := NewLocalIPRequest()
		resources, err := manager.Allocate(context.Background(), &daemon.CNI{}, &AllocRequest{
			ResourceRequests: []ResourceRequest{request},
		})

		assert.Nil(t, err)
		assert.NotNil(t, resources)
		assert.Equal(t, mockNI.IPv4.String(), resources[0].ToStore()[0].IPv4)
	}
}

func TestManagerAllocateReturnsErrorWhenNoBackendCanHandleAllocation(t *testing.T) {
	manager := NewManager(nil, 0, []NetworkInterface{}, daemon.EniSelectionPolicyMostIPs, k8smocks.NewKubernetes(t))

	request := NewLocalIPRequest()
	_, err := manager.Allocate(context.Background(), &daemon.CNI{}, &AllocRequest{
		ResourceRequests: []ResourceRequest{request},
	})

	assert.NotNil(t, err)
}

func TestManagerAllocateWithTimeoutWhenAllocationFails(t *testing.T) {
	mockNI := &timeOut{}
	manager := NewManager(nil, 0, []NetworkInterface{mockNI}, daemon.EniSelectionPolicyMostIPs, k8smocks.NewKubernetes(t))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	request := NewLocalIPRequest()
	_, err := manager.Allocate(ctx, &daemon.CNI{}, &AllocRequest{
		ResourceRequests: []ResourceRequest{request},
	})
	assert.NotNil(t, err)
}

func TestManager_calculateToDel(t *testing.T) {
	tests := []struct {
		name             string
		minIdles         int
		maxIdles         int
		idles            int
		reclaimBatchSize int
		reclaimInterval  time.Duration
		reclaimAfter     time.Duration
		reclaimFactor    float64
		lastModified     time.Time
		nextReclaimTime  time.Time
		wantToDel        int
		wantNextReclaim  bool // whether nextReclaimTime should be set
	}{
		{
			name:             "basic case - idles exceed maxIdles",
			minIdles:         5,
			maxIdles:         10,
			idles:            15,
			reclaimBatchSize: 0,
			reclaimInterval:  0,
			reclaimAfter:     0,
			wantToDel:        5, // 15 - 10 = 5
			wantNextReclaim:  false,
		},
		{
			name:             "idles below maxIdles",
			minIdles:         5,
			maxIdles:         10,
			idles:            8,
			reclaimBatchSize: 0,
			reclaimInterval:  0,
			reclaimAfter:     0,
			wantToDel:        -2, // 8 - 10 = -2
			wantNextReclaim:  false,
		},
		{
			name:             "reclaimBatchSize is zero - disable reclaim feature",
			minIdles:         5,
			maxIdles:         10,
			idles:            15,
			reclaimBatchSize: 0,
			reclaimInterval:  time.Minute,
			reclaimAfter:     time.Minute,
			lastModified:     time.Now().Add(-2 * time.Minute),
			wantToDel:        5,
			wantNextReclaim:  false,
		},
		{
			name:             "reclaimAfter is zero - disable reclaim feature",
			minIdles:         5,
			maxIdles:         10,
			idles:            15,
			reclaimBatchSize: 3,
			reclaimInterval:  time.Minute,
			reclaimAfter:     0,
			lastModified:     time.Now().Add(-2 * time.Minute),
			wantToDel:        5,
			wantNextReclaim:  false,
		},
		{
			name:             "pool recently modified - reset nextReclaimTime",
			minIdles:         5,
			maxIdles:         10,
			idles:            15,
			reclaimBatchSize: 3,
			reclaimInterval:  time.Minute,
			reclaimAfter:     time.Minute,
			reclaimFactor:    0.1,
			lastModified:     time.Now().Add(-30 * time.Second), // modified 30s ago, within reclaimAfter
			nextReclaimTime:  time.Now().Add(time.Minute),       // should be reset
			wantToDel:        5,
			wantNextReclaim:  false, // should be reset to zero
		},
		{
			name:             "nextReclaimTime not set - initialize it",
			minIdles:         5,
			maxIdles:         10,
			idles:            15,
			reclaimBatchSize: 3,
			reclaimInterval:  time.Minute,
			reclaimAfter:     time.Minute,
			reclaimFactor:    0.1,
			lastModified:     time.Now().Add(-2 * time.Minute),
			nextReclaimTime:  time.Time{}, // zero time
			wantToDel:        5,
			wantNextReclaim:  true, // should be set
		},
		{
			name:             "nextReclaimTime not reached yet",
			minIdles:         5,
			maxIdles:         10,
			idles:            15,
			reclaimBatchSize: 3,
			reclaimInterval:  time.Minute,
			reclaimAfter:     time.Minute,
			reclaimFactor:    0.1,
			lastModified:     time.Now().Add(-2 * time.Minute),
			nextReclaimTime:  time.Now().Add(30 * time.Second), // in the future
			wantToDel:        5,
			wantNextReclaim:  true, // should remain set
		},
		{
			name:             "nextReclaimTime reached - reclaim extra IPs",
			minIdles:         5,
			maxIdles:         10,
			idles:            20,
			reclaimBatchSize: 3,
			reclaimInterval:  time.Minute,
			reclaimAfter:     time.Minute,
			reclaimFactor:    0.1,
			lastModified:     time.Now().Add(-2 * time.Minute),
			nextReclaimTime:  time.Now().Add(-10 * time.Second), // in the past
			wantToDel:        13,                                // base: 20-10=10, extra: min(3, 20-10-5)=3, total: 10+3=13
			wantNextReclaim:  true,
		},
		{
			name:             "nextReclaimTime reached - limited by reclaimBatchSize",
			minIdles:         5,
			maxIdles:         10,
			idles:            25,
			reclaimBatchSize: 3,
			reclaimInterval:  time.Minute,
			reclaimAfter:     time.Minute,
			reclaimFactor:    0.1,
			lastModified:     time.Now().Add(-2 * time.Minute),
			nextReclaimTime:  time.Now().Add(-10 * time.Second),
			wantToDel:        18, // base: 25-10=15, extra: min(3, 25-15-5)=3, total: 15+3=18
			wantNextReclaim:  true,
		},
		{
			name:             "nextReclaimTime reached - limited by minIdles",
			minIdles:         18,
			maxIdles:         20,
			idles:            22,
			reclaimBatchSize: 5,
			reclaimInterval:  time.Minute,
			reclaimAfter:     time.Minute,
			reclaimFactor:    0.1,
			lastModified:     time.Now().Add(-2 * time.Minute),
			nextReclaimTime:  time.Now().Add(-10 * time.Second),
			wantToDel:        4, // base: 22-20=2, extraDel = min(5, max(0, 22-2-18)) = min(5, 2) = 2, total: 2+2=4
			wantNextReclaim:  true,
		},
		{
			name:             "nextReclaimTime reached - some extra reclaim",
			minIdles:         15,
			maxIdles:         20,
			idles:            21,
			reclaimBatchSize: 5,
			reclaimInterval:  time.Minute,
			reclaimAfter:     time.Minute,
			reclaimFactor:    0.1,
			lastModified:     time.Now().Add(-2 * time.Minute),
			nextReclaimTime:  time.Now().Add(-10 * time.Second),
			wantToDel:        6, // base: 21-20=1, extraDel = min(5, max(0, 21-1-15)) = min(5, 5) = 5, total: 1+5=6
			wantNextReclaim:  true,
		},
		{
			name:             "toDel negative but nextReclaimTime reached - reclaim only extra",
			minIdles:         5,
			maxIdles:         20,
			idles:            15,
			reclaimBatchSize: 3,
			reclaimInterval:  time.Minute,
			reclaimAfter:     time.Minute,
			reclaimFactor:    0.1,
			lastModified:     time.Now().Add(-2 * time.Minute),
			nextReclaimTime:  time.Now().Add(-10 * time.Second),
			wantToDel:        3, // base: 15-20=-5->0, extra: min(3, 15-0-5)=min(3,10)=3, total: 0+3=3
			wantNextReclaim:  true,
		},
		{
			name:             "custom reclaimFactor",
			minIdles:         5,
			maxIdles:         10,
			idles:            15,
			reclaimBatchSize: 3,
			reclaimInterval:  time.Minute,
			reclaimAfter:     time.Minute,
			reclaimFactor:    0.5, // custom factor
			lastModified:     time.Now().Add(-2 * time.Minute),
			nextReclaimTime:  time.Time{}, // will be initialized
			wantToDel:        5,
			wantNextReclaim:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				minIdles:         tt.minIdles,
				maxIdles:         tt.maxIdles,
				reclaimBatchSize: tt.reclaimBatchSize,
				reclaimInterval:  tt.reclaimInterval,
				reclaimAfter:     tt.reclaimAfter,
				reclaimFactor:    tt.reclaimFactor,
				lastModified:     tt.lastModified,
				nextReclaimTime:  tt.nextReclaimTime,
			}

			got := m.calculateToDel(tt.idles)

			// For negative expected values, just check the result
			if tt.wantToDel < 0 {
				assert.Equal(t, tt.wantToDel, got, "calculateToDel() result mismatch")
			} else {
				// For positive values, allow some tolerance due to timing
				// When nextReclaimTime is involved, the exact value might vary slightly
				if tt.nextReclaimTime.IsZero() && tt.reclaimBatchSize > 0 && tt.reclaimAfter > 0 {
					// First call to set nextReclaimTime, should return base toDel
					assert.Equal(t, tt.wantToDel, got, "calculateToDel() result mismatch")
				} else if !tt.nextReclaimTime.IsZero() && tt.nextReclaimTime.Before(time.Now()) && tt.reclaimBatchSize > 0 {
					// nextReclaimTime reached, should include extra
					assert.Equal(t, tt.wantToDel, got, "calculateToDel() result mismatch")
				} else {
					assert.Equal(t, tt.wantToDel, got, "calculateToDel() result mismatch")
				}
			}

			// Check if nextReclaimTime was set/reset appropriately
			if tt.wantNextReclaim {
				assert.False(t, m.nextReclaimTime.IsZero(), "nextReclaimTime should be set")
			} else if tt.reclaimBatchSize > 0 && tt.reclaimAfter > 0 {
				// If recently modified, nextReclaimTime should be reset to zero
				if tt.lastModified.Add(tt.reclaimAfter).After(time.Now()) {
					assert.True(t, m.nextReclaimTime.IsZero(), "nextReclaimTime should be reset to zero")
				}
			}
		})
	}
}

func TestManager_calculateToDel_EdgeCases(t *testing.T) {
	t.Run("zero idles", func(t *testing.T) {
		m := &Manager{
			minIdles:         5,
			maxIdles:         10,
			reclaimBatchSize: 0,
		}
		got := m.calculateToDel(0)
		assert.Equal(t, -10, got)
	})

	t.Run("very large idles", func(t *testing.T) {
		m := &Manager{
			minIdles:         5,
			maxIdles:         10,
			reclaimBatchSize: 0,
		}
		got := m.calculateToDel(1000)
		assert.Equal(t, 990, got)
	})

	t.Run("idles equal to maxIdles", func(t *testing.T) {
		m := &Manager{
			minIdles:         5,
			maxIdles:         10,
			reclaimBatchSize: 0,
		}
		got := m.calculateToDel(10)
		assert.Equal(t, 0, got)
	})

	t.Run("nextReclaimTime reached but no room for extra reclaim", func(t *testing.T) {
		m := &Manager{
			minIdles:         10,
			maxIdles:         15,
			reclaimBatchSize: 5,
			reclaimInterval:  time.Minute,
			reclaimAfter:     time.Minute,
			lastModified:     time.Now().Add(-2 * time.Minute),
			nextReclaimTime:  time.Now().Add(-10 * time.Second),
		}
		// idles=16, base toDel=16-15=1
		// after base: 16-1=15
		// extraDel = min(5, 16-1-10) = min(5, 5) = 5
		// total = 1 + 5 = 6
		got := m.calculateToDel(16)
		assert.Equal(t, 6, got)
	})

	t.Run("default reclaimFactor when zero", func(t *testing.T) {
		m := &Manager{
			minIdles:         5,
			maxIdles:         10,
			reclaimBatchSize: 3,
			reclaimInterval:  time.Minute,
			reclaimAfter:     time.Minute,
			reclaimFactor:    0, // should default to 0.1
			lastModified:     time.Now().Add(-2 * time.Minute),
			nextReclaimTime:  time.Time{},
		}
		m.calculateToDel(15)
		// Just verify it doesn't panic and sets nextReclaimTime
		assert.False(t, m.nextReclaimTime.IsZero())
	})
}

func TestManager(t *testing.T) {
	pool := &daemon.PoolConfig{
		Capacity:         10,
		MaxENI:           3,
		ReclaimBatchSize: 3,
		ReclaimInterval:  time.Minute,
		ReclaimAfter:     time.Minute,
		ReclaimFactor:    0.1,
	}

	k8s := k8smocks.NewKubernetes(t)
	k8s.On("PatchNodeIPResCondition", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mgr := NewManager(pool, 10*time.Second, []NetworkInterface{&NoOpNetworkInterface{}}, daemon.EniSelectionPolicyMostIPs, k8s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wg := &sync.WaitGroup{}
	err := mgr.Run(ctx, wg, []daemon.PodResources{})
	assert.NoError(t, err)

	resources, err := mgr.Allocate(ctx, &daemon.CNI{}, &AllocRequest{
		ResourceRequests: []ResourceRequest{NewLocalIPRequest()},
	})
	assert.NoError(t, err)
	assert.NotNil(t, resources)

	err = mgr.Release(ctx, &daemon.CNI{}, &ReleaseRequest{
		[]NetworkResource{
			&LocalIPResource{},
		},
	})
	assert.NoError(t, err)
}

type NoOpNetworkInterface struct {
}

func (n *NoOpNetworkInterface) Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	resp := make(chan *AllocResp)
	go func() {
		resp <- &AllocResp{}
	}()
	return resp, nil
}

func (n *NoOpNetworkInterface) Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) (bool, error) {
	return true, nil
}

func (n *NoOpNetworkInterface) Priority() int {
	return 0
}

func (n *NoOpNetworkInterface) Dispose(int) int {
	return 0
}

func (n *NoOpNetworkInterface) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	return nil
}
