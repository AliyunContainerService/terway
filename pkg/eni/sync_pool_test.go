package eni

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/AliyunContainerService/terway/types/daemon"
)

type mockNetworkInterface struct {
	priority      int
	idle          int
	inuse         int
	disposeCalled int
	disposeReturn int
}

func (m *mockNetworkInterface) Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	return nil, nil
}

func (m *mockNetworkInterface) Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) (bool, error) {
	return true, nil
}

func (m *mockNetworkInterface) Priority() int {
	return m.priority
}

func (m *mockNetworkInterface) Dispose(n int) int {
	m.disposeCalled += n
	return m.disposeReturn
}

func (m *mockNetworkInterface) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	return nil
}

func (m *mockNetworkInterface) Usage() (int, int, error) {
	return m.idle, m.inuse, nil
}

type niWithoutUsage struct {
}

func (n *niWithoutUsage) Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	return nil, nil
}

func (n *niWithoutUsage) Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) (bool, error) {
	return true, nil
}

func (n *niWithoutUsage) Priority() int {
	return 0
}

func (n *niWithoutUsage) Dispose(int) int {
	return 0
}

func (n *niWithoutUsage) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	return nil
}

func TestManager_SyncPool_ToDel(t *testing.T) {
	ni := &mockNetworkInterface{
		priority:      10,
		idle:          20,
		inuse:         5,
		disposeReturn: 5,
	}

	m := &Manager{
		networkInterfaces: []NetworkInterface{ni},
		maxIdles:          10,
		selectionPolicy:   daemon.EniSelectionPolicyLeastIPs,
	}

	// Mock Usage
	patches := gomonkey.ApplyMethod(ni, "Usage", func(_ *mockNetworkInterface) (int, int, error) {
		return ni.idle, ni.inuse, nil
	})
	defer patches.Reset()

	// Mock ni.Dispose
	patches.ApplyMethod(ni, "Dispose", func(_ *mockNetworkInterface, n int) int {
		ni.disposeCalled += n
		return ni.disposeReturn
	})

	m.syncPool(context.Background())

	assert.True(t, ni.disposeCalled > 0)
}

func TestManager_SyncPool_ToAdd(t *testing.T) {
	ni := &mockNetworkInterface{
		priority: 10,
		idle:     2,
		inuse:    5,
	}

	m := &Manager{
		networkInterfaces: []NetworkInterface{ni},
		minIdles:          10,
		total:             20,
		selectionPolicy:   daemon.EniSelectionPolicyMostIPs,
	}

	// Mock Usage
	patches := gomonkey.ApplyMethod(ni, "Usage", func(_ *mockNetworkInterface) (int, int, error) {
		return ni.idle, ni.inuse, nil
	})
	defer patches.Reset()

	var allocateCalled int32
	// Mock m.Allocate
	patches.ApplyMethod(m, "Allocate", func(_ *Manager, ctx context.Context, cni *daemon.CNI, req *AllocRequest) (NetworkResources, error) {
		atomic.AddInt32(&allocateCalled, 1)
		return nil, nil
	})

	m.syncPool(context.Background())

	// toAdd = minIdles (10) - idle (2) = 8
	assert.Equal(t, int32(8), atomic.LoadInt32(&allocateCalled))
}

func TestManager_SyncPool_OtherPaths(t *testing.T) {
	t.Run("idles+inuses >= m.total", func(t *testing.T) {
		ni := &mockNetworkInterface{
			idle:  10,
			inuse: 10,
		}
		m := &Manager{
			networkInterfaces: []NetworkInterface{ni},
			total:             20,
			minIdles:          15, // would normally add 5
		}

		patches := gomonkey.ApplyMethod(ni, "Usage", func(_ *mockNetworkInterface) (int, int, error) {
			return ni.idle, ni.inuse, nil
		})
		defer patches.Reset()

		allocateCalled := false
		patches.ApplyMethod(m, "Allocate", func(_ *Manager, ctx context.Context, cni *daemon.CNI, req *AllocRequest) (NetworkResources, error) {
			allocateCalled = true
			return nil, nil
		})

		m.syncPool(context.Background())
		assert.False(t, allocateCalled)
	})

	t.Run("usage returns error", func(t *testing.T) {
		ni := &mockNetworkInterface{}
		m := &Manager{
			networkInterfaces: []NetworkInterface{ni},
		}

		patches := gomonkey.ApplyMethod(ni, "Usage", func(_ *mockNetworkInterface) (int, int, error) {
			return 0, 0, assert.AnError
		})
		defer patches.Reset()

		m.syncPool(context.Background())
		// Should not panic, just log error
	})

	t.Run("allocate returns error", func(t *testing.T) {
		ni := &mockNetworkInterface{
			idle:  2,
			inuse: 5,
		}
		m := &Manager{
			networkInterfaces: []NetworkInterface{ni},
			minIdles:          3,
			total:             20,
		}

		patches := gomonkey.ApplyMethod(ni, "Usage", func(_ *mockNetworkInterface) (int, int, error) {
			return ni.idle, ni.inuse, nil
		})
		defer patches.Reset()

		patches.ApplyMethod(m, "Allocate", func(_ *Manager, ctx context.Context, cni *daemon.CNI, req *AllocRequest) (NetworkResources, error) {
			return nil, assert.AnError
		})

		m.syncPool(context.Background())
		// Should not panic, just log error
	})

	t.Run("ni does not implement Usage", func(t *testing.T) {
		ni := &niWithoutUsage{}
		m := &Manager{
			networkInterfaces: []NetworkInterface{ni},
		}

		m.syncPool(context.Background())
		// Should skip
	})
}
