package eni

import (
	"context"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/AliyunContainerService/terway/types/daemon"
)

func TestNewTrunk(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-trunk-1", MAC: "00:00:00:00:00:01"}, nil, &daemon.PoolConfig{}, "")

	trunk := NewTrunk(nil, local)

	assert.NotNil(t, trunk)
	assert.Equal(t, local, trunk.local)
	assert.NotNil(t, trunk.remote)
	assert.Equal(t, local.eni, trunk.trunkENI)
}

func TestTrunk_Priority(t *testing.T) {
	trunk := &Trunk{}

	assert.Equal(t, 100, trunk.Priority())
}

func TestTrunk_Allocate_LocalIP(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-trunk-1"}, nil, &daemon.PoolConfig{}, "")
	trunk := &Trunk{
		local:  local,
		remote: &Remote{},
	}

	expectedCh := make(chan *AllocResp)
	expectedTraces := []Trace{{Condition: Full}}

	// Use gomonkey to mock local.Allocate
	patches := gomonkey.ApplyMethod(local, "Allocate",
		func(_ *Local, _ context.Context, _ *daemon.CNI, _ ResourceRequest) (chan *AllocResp, []Trace) {
			return expectedCh, expectedTraces
		})
	defer patches.Reset()

	request := &mockResourceRequest{resourceType: ResourceTypeLocalIP}
	cni := &daemon.CNI{PodID: "pod-1"}

	ch, traces := trunk.Allocate(context.Background(), cni, request)

	assert.Equal(t, expectedCh, ch)
	assert.Equal(t, expectedTraces, traces)
}

func TestTrunk_Allocate_RemoteIP(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-trunk-1"}, nil, &daemon.PoolConfig{}, "")
	remote := &Remote{}
	trunk := &Trunk{
		local:  local,
		remote: remote,
	}

	expectedCh := make(chan *AllocResp)
	expectedTraces := []Trace{{Condition: NetworkInterfaceMismatch}}

	// Use gomonkey to mock remote.Allocate
	patches := gomonkey.ApplyMethod(remote, "Allocate",
		func(_ *Remote, _ context.Context, _ *daemon.CNI, _ ResourceRequest) (chan *AllocResp, []Trace) {
			return expectedCh, expectedTraces
		})
	defer patches.Reset()

	request := &mockResourceRequest{resourceType: ResourceTypeRemoteIP}
	cni := &daemon.CNI{PodID: "pod-1"}

	ch, traces := trunk.Allocate(context.Background(), cni, request)

	assert.Equal(t, expectedCh, ch)
	assert.Equal(t, expectedTraces, traces)
}

func TestTrunk_Allocate_UnknownResourceType(t *testing.T) {
	trunk := &Trunk{
		local:  &Local{},
		remote: &Remote{},
	}

	request := &mockResourceRequest{resourceType: 0} // Unknown type
	cni := &daemon.CNI{PodID: "pod-1"}

	ch, traces := trunk.Allocate(context.Background(), cni, request)

	assert.Nil(t, ch)
	assert.Len(t, traces, 1)
	assert.Equal(t, ResourceTypeMismatch, traces[0].Condition)
}

func TestTrunk_Release_LocalIP(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-trunk-1"}, nil, &daemon.PoolConfig{}, "")
	trunk := &Trunk{
		local:  local,
		remote: &Remote{},
	}

	// Use gomonkey to mock local.Release
	patches := gomonkey.ApplyMethod(local, "Release",
		func(_ *Local, _ context.Context, _ *daemon.CNI, _ NetworkResource) (bool, error) {
			return true, nil
		})
	defer patches.Reset()

	request := &mockNetworkResource{resourceType: ResourceTypeLocalIP}
	cni := &daemon.CNI{PodID: "pod-1"}

	ok, err := trunk.Release(context.Background(), cni, request)

	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestTrunk_Release_RemoteIP(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-trunk-1"}, nil, &daemon.PoolConfig{}, "")
	remote := &Remote{}
	trunk := &Trunk{
		local:  local,
		remote: remote,
	}

	// Use gomonkey to mock remote.Release
	patches := gomonkey.ApplyMethod(remote, "Release",
		func(_ *Remote, _ context.Context, _ *daemon.CNI, _ NetworkResource) (bool, error) {
			return true, nil
		})
	defer patches.Reset()

	request := &mockNetworkResource{resourceType: ResourceTypeRemoteIP}
	cni := &daemon.CNI{PodID: "pod-1"}

	ok, err := trunk.Release(context.Background(), cni, request)

	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestTrunk_Release_UnknownResourceType(t *testing.T) {
	trunk := &Trunk{
		local:  &Local{},
		remote: &Remote{},
	}

	request := &mockNetworkResource{resourceType: 0} // Unknown type
	cni := &daemon.CNI{PodID: "pod-1"}

	ok, err := trunk.Release(context.Background(), cni, request)

	assert.False(t, ok)
	assert.NoError(t, err)
}

func TestTrunk_Dispose(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-trunk-1"}, nil, &daemon.PoolConfig{}, "")
	trunk := &Trunk{
		local: local,
	}

	// Use gomonkey to mock local.Dispose
	patches := gomonkey.ApplyMethod(local, "Dispose",
		func(_ *Local, n int) int {
			return n - 1
		})
	defer patches.Reset()

	result := trunk.Dispose(5)

	assert.Equal(t, 4, result)
}

func TestTrunk_Status(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-trunk-1", MAC: "00:00:00:00:00:01"}, nil, &daemon.PoolConfig{}, "trunk")
	local.status = statusInUse
	trunk := &Trunk{
		local: local,
	}

	expectedStatus := Status{
		Status:             statusInUse.String(),
		Type:               "trunk",
		MAC:                "00:00:00:00:00:01",
		NetworkInterfaceID: "eni-trunk-1",
	}

	// Use gomonkey to mock local.Status
	patches := gomonkey.ApplyMethod(local, "Status",
		func(_ *Local) Status {
			return expectedStatus
		})
	defer patches.Reset()

	status := trunk.Status()

	assert.Equal(t, expectedStatus, status)
}

func TestTrunk_Usage(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-trunk-1"}, nil, &daemon.PoolConfig{}, "")
	trunk := &Trunk{
		local: local,
	}

	// Use gomonkey to mock local.Usage
	patches := gomonkey.ApplyMethod(local, "Usage",
		func(_ *Local) (int, int, error) {
			return 5, 10, nil
		})
	defer patches.Reset()

	idles, inUse, err := trunk.Usage()

	assert.Equal(t, 5, idles)
	assert.Equal(t, 10, inUse)
	assert.NoError(t, err)
}

func TestTrunk_Run(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-trunk-1"}, nil, &daemon.PoolConfig{}, "")
	trunk := &Trunk{
		local: local,
	}

	// Use gomonkey to mock local.Run
	patches := gomonkey.ApplyMethod(local, "Run",
		func(_ *Local, _ context.Context, _ []daemon.PodResources, _ *sync.WaitGroup) error {
			return nil
		})
	defer patches.Reset()

	ctx := context.Background()
	wg := &sync.WaitGroup{}

	err := trunk.Run(ctx, nil, wg)

	assert.NoError(t, err)
}
