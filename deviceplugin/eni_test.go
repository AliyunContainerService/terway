package deviceplugin

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func TestNewENIDevicePlugin(t *testing.T) {
	// Test ENI
	pENI := NewENIDevicePlugin(1, ENITypeENI)
	assert.NotNil(t, pENI)
	assert.Equal(t, ENITypeENI, pENI.eniType)
	assert.Equal(t, ENIResName, pENI.eniRes.resName)
	assert.Contains(t, pENI.socket, "-eni.sock")

	// Test Member ENI
	pMember := NewENIDevicePlugin(1, ENITypeMember)
	assert.NotNil(t, pMember)
	assert.Equal(t, ENITypeMember, pMember.eniType)
	assert.Equal(t, MemberENIResName, pMember.eniRes.resName)
	assert.Contains(t, pMember.socket, "-member-eni.sock")

	// Test ERDMA
	pERDMA := NewENIDevicePlugin(1, ENITypeERDMA)
	assert.NotNil(t, pERDMA)
	assert.Equal(t, ENITypeERDMA, pERDMA.eniType)
	assert.Equal(t, ERDMAResName, pERDMA.eniRes.resName)
	assert.Contains(t, pERDMA.socket, "-erdma-eni.sock")

	// Test panic on unsupported type
	assert.Panics(t, func() {
		NewENIDevicePlugin(1, "unsupported")
	})
}

func TestENIDevicePlugin_Allocate(t *testing.T) {
	ctx := context.Background()

	t.Run("non-erdma", func(t *testing.T) {
		p := NewENIDevicePlugin(1, ENITypeENI)
		req := &pluginapi.AllocateRequest{
			ContainerRequests: []*pluginapi.ContainerAllocateRequest{
				{
					DevicesIDs: []string{"eni-0"},
				},
			},
		}
		resp, err := p.Allocate(ctx, req)
		require.NoError(t, err)
		require.Len(t, resp.ContainerResponses, 1)
		assert.Empty(t, resp.ContainerResponses[0].Devices)
	})

	t.Run("erdma no infiniband dir", func(t *testing.T) {
		p := NewENIDevicePlugin(1, ENITypeERDMA)

		infinibandPath := "/dev/infiniband"
		_, err := os.Stat(infinibandPath)
		if !os.IsNotExist(err) {
			t.Skipf("Skipping test because %s exists", infinibandPath)
		}

		req := &pluginapi.AllocateRequest{
			ContainerRequests: []*pluginapi.ContainerAllocateRequest{
				{
					DevicesIDs: []string{"eni-0"},
				},
			},
		}
		resp, err := p.Allocate(ctx, req)
		require.NoError(t, err)
		require.Len(t, resp.ContainerResponses, 1)
		expectedDevices := []*pluginapi.DeviceSpec{
			{
				ContainerPath: "/dev/infiniband/uverbs0",
				HostPath:      "/dev/infiniband/uverbs0",
				Permissions:   "rw",
			},
			{
				ContainerPath: "/dev/infiniband/rdma_cm",
				HostPath:      "/dev/infiniband/rdma_cm",
				Permissions:   "rw",
			},
		}
		assert.Equal(t, expectedDevices, resp.ContainerResponses[0].Devices)
	})
}

type mockDevicePlugin_ListAndWatchServer struct {
	grpc.ServerStream
	Sent chan *pluginapi.ListAndWatchResponse
	Ctx  context.Context
}

func (x *mockDevicePlugin_ListAndWatchServer) Send(m *pluginapi.ListAndWatchResponse) error {
	x.Sent <- m
	return nil
}

func (x *mockDevicePlugin_ListAndWatchServer) Context() context.Context {
	return x.Ctx
}

func TestENIDevicePlugin_ListAndWatch(t *testing.T) {
	p := NewENIDevicePlugin(3, ENITypeENI)
	p.stop = make(chan struct{})

	server := &mockDevicePlugin_ListAndWatchServer{
		Sent: make(chan *pluginapi.ListAndWatchResponse, 1),
		Ctx:  context.Background(),
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := p.ListAndWatch(&pluginapi.Empty{}, server)
		assert.NoError(t, err)
	}()

	select {
	case resp := <-server.Sent:
		assert.Len(t, resp.Devices, 3)
		for i, dev := range resp.Devices {
			assert.Equal(t, fmt.Sprintf("eni-%d", i), dev.ID)
			assert.Equal(t, pluginapi.Healthy, dev.Health)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for initial ListAndWatch response")
	}

	close(p.stop)
	wg.Wait()
}
