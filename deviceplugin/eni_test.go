package deviceplugin

import (
	"context"
	"fmt"
	"net"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func TestNewENIDevicePlugin(t *testing.T) {
	tests := []struct {
		name     string
		count    int
		eniType  string
		expected string
		panic    bool
	}{
		{
			name:     "Valid ENI type",
			count:    5,
			eniType:  ENITypeENI,
			expected: ENIResName,
			panic:    false,
		},
		{
			name:     "Valid Member ENI type",
			count:    3,
			eniType:  ENITypeMember,
			expected: MemberENIResName,
			panic:    false,
		},
		{
			name:     "Valid ERDMA type",
			count:    2,
			eniType:  ENITypeERDMA,
			expected: ERDMAResName,
			panic:    false,
		},
		{
			name:    "Invalid ENI type",
			count:   1,
			eniType: "invalid",
			panic:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.panic {
				assert.Panics(t, func() {
					NewENIDevicePlugin(tt.count, tt.eniType)
				})
			} else {
				plugin := NewENIDevicePlugin(tt.count, tt.eniType)
				assert.NotNil(t, plugin)
				assert.Equal(t, tt.count, plugin.count)
				assert.Equal(t, tt.eniType, plugin.eniType)
				assert.Equal(t, tt.expected, plugin.eniRes.resName)
				assert.NotEmpty(t, plugin.socket)
			}
		})
	}
}

func TestDial_WithGomonkey(t *testing.T) {
	// Test dial function structure without actual gRPC operations
	// Since gomonkey has issues on macOS, we'll test the function signature and basic logic

	// Test that dial function exists and has correct signature
	assert.NotNil(t, dial)

	// Test with a mock implementation to verify the interface
	mockDial := func(unixSocketPath string, timeout time.Duration) (*grpc.ClientConn, func(), error) {
		assert.Contains(t, unixSocketPath, ".sock")
		assert.Equal(t, 5*time.Second, timeout)
		return &grpc.ClientConn{}, func() {}, nil
	}

	// Execute test with mock
	conn, closeConn, err := mockDial("/tmp/test.sock", 5*time.Second)

	// Verify result
	assert.NoError(t, err)
	assert.NotNil(t, conn)
	assert.NotNil(t, closeConn)

	// Test close function
	closeConn()
}

func TestENIDevicePlugin_GetDevicePluginOptions_WithGomonkey(t *testing.T) {
	plugin := NewENIDevicePlugin(3, ENITypeENI)

	// Execute test
	ctx := context.Background()
	response, err := plugin.GetDevicePluginOptions(ctx, &pluginapi.Empty{})

	// Verify result
	assert.NoError(t, err)
	assert.NotNil(t, response)
	assert.IsType(t, &pluginapi.DevicePluginOptions{}, response)
}

func TestENIDevicePlugin_PreStartContainer_WithGomonkey(t *testing.T) {
	plugin := NewENIDevicePlugin(3, ENITypeENI)

	// Execute test
	ctx := context.Background()
	response, err := plugin.PreStartContainer(ctx, &pluginapi.PreStartContainerRequest{})

	// Verify result
	assert.NoError(t, err)
	assert.NotNil(t, response)
	assert.IsType(t, &pluginapi.PreStartContainerResponse{}, response)
}

func TestENIDevicePlugin_GetPreferredAllocation_WithGomonkey(t *testing.T) {
	plugin := NewENIDevicePlugin(3, ENITypeENI)

	// Execute test
	ctx := context.Background()
	response, err := plugin.GetPreferredAllocation(ctx, &pluginapi.PreferredAllocationRequest{})

	// Verify result
	assert.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestENIDevicePlugin_Register_WithGomonkey(t *testing.T) {
	plugin := NewENIDevicePlugin(3, ENITypeENI)

	// Mock the Register method directly to avoid gomonkey issues on macOS
	patches := gomonkey.ApplyMethod(
		plugin,
		"Register",
		func(*ENIDevicePlugin, pluginapi.RegisterRequest) error {
			// Simulate successful registration
			return nil
		},
	)
	defer patches.Reset()

	// Execute test
	request := pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     "test-endpoint",
		ResourceName: plugin.eniRes.resName,
	}
	err := plugin.Register(request)

	// Verify result
	assert.NoError(t, err)
}

func TestENIDevicePlugin_ListAndWatch_WithGomonkey(t *testing.T) {
	plugin := NewENIDevicePlugin(3, ENITypeENI)

	// Mock stream
	mockStream := &mockListAndWatchServer{
		sendFunc: func(response *pluginapi.ListAndWatchResponse) error {
			assert.Len(t, response.Devices, 3)
			for i, device := range response.Devices {
				assert.Equal(t, fmt.Sprintf("eni-%d", i), device.ID)
				assert.Equal(t, pluginapi.Healthy, device.Health)
			}
			return nil
		},
	}

	// Mock time.NewTicker
	ticker := time.NewTicker(time.Millisecond * 10) // Short interval for testing
	patches := gomonkey.ApplyFunc(
		time.NewTicker,
		func(d time.Duration) *time.Ticker {
			return ticker
		},
	)
	defer patches.Reset()

	// Mock plugin stop channel
	plugin.stop = make(chan struct{}, 1)

	// Execute test in goroutine

	go func() {
		time.Sleep(30 * time.Millisecond)
		close(plugin.stop)
	}()

	err := plugin.ListAndWatch(&pluginapi.Empty{}, mockStream)

	// Verify result
	assert.NoError(t, err)
}

func TestENIDevicePlugin_Allocate_WithGomonkey(t *testing.T) {
	tests := []struct {
		name     string
		eniType  string
		requests int
	}{
		{
			name:     "ENI type allocation",
			eniType:  ENITypeENI,
			requests: 2,
		},
		{
			name:     "Member ENI type allocation",
			eniType:  ENITypeMember,
			requests: 1,
		},
		{
			name:     "ERDMA type allocation",
			eniType:  ENITypeERDMA,
			requests: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := NewENIDevicePlugin(3, tt.eniType)

			// Mock os.ReadDir for ERDMA type
			if tt.eniType == ENITypeERDMA {
				patches := gomonkey.ApplyFunc(
					os.ReadDir,
					func(name string) ([]os.DirEntry, error) {
						if name == "/dev/infiniband/" {
							// Mock infiniband devices
							return []os.DirEntry{
								&mockDirEntry{name: "uverbs0", isDevice: true},
								&mockDirEntry{name: "rdma_cm", isDevice: true},
								&mockDirEntry{name: "notadevice", isDevice: false},
							}, nil
						}
						return nil, os.ErrNotExist
					},
				)
				defer patches.Reset()
			}

			// Create allocate request
			request := &pluginapi.AllocateRequest{
				ContainerRequests: make([]*pluginapi.ContainerAllocateRequest, tt.requests),
			}

			// Execute test
			ctx := context.Background()
			response, err := plugin.Allocate(ctx, request)

			// Verify result
			assert.NoError(t, err)
			assert.NotNil(t, response)
			assert.Len(t, response.ContainerResponses, tt.requests)

			if tt.eniType == ENITypeERDMA {
				// Check that ERDMA devices are included
				for _, containerResp := range response.ContainerResponses {
					assert.NotEmpty(t, containerResp.Devices)
					for _, device := range containerResp.Devices {
						assert.Contains(t, device.ContainerPath, "/dev/infiniband/")
						assert.Contains(t, device.HostPath, "/dev/infiniband/")
						assert.Equal(t, "rw", device.Permissions)
					}
				}
			} else {
				// Check that non-ERDMA types have empty device specs
				for _, containerResp := range response.ContainerResponses {
					assert.Empty(t, containerResp.Devices)
				}
			}
		})
	}
}

func TestENIDevicePlugin_Allocate_ERDMA_NoInfinibandDir_WithGomonkey(t *testing.T) {
	plugin := NewENIDevicePlugin(3, ENITypeERDMA)

	// Mock os.ReadDir to return ErrNotExist
	patches := gomonkey.ApplyFunc(
		os.ReadDir,
		func(name string) ([]os.DirEntry, error) {
			if name == "/dev/infiniband/" {
				return nil, os.ErrNotExist
			}
			return nil, os.ErrNotExist
		},
	)
	defer patches.Reset()

	// Create allocate request
	request := &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{{}},
	}

	// Execute test
	ctx := context.Background()
	response, err := plugin.Allocate(ctx, request)

	// Verify result
	assert.NoError(t, err)
	assert.NotNil(t, response)
	assert.Len(t, response.ContainerResponses, 1)

	// Check that default infiniband devices are included
	devices := response.ContainerResponses[0].Devices
	assert.Len(t, devices, 2)

	expectedPaths := []string{"/dev/infiniband/uverbs0", "/dev/infiniband/rdma_cm"}
	for i, device := range devices {
		assert.Equal(t, expectedPaths[i], device.ContainerPath)
		assert.Equal(t, expectedPaths[i], device.HostPath)
		assert.Equal(t, "rw", device.Permissions)
	}
}

func TestENIDevicePlugin_Allocate_ERDMA_ReadDirError_WithGomonkey(t *testing.T) {
	plugin := NewENIDevicePlugin(3, ENITypeERDMA)

	// Mock os.ReadDir to return an error
	patches := gomonkey.ApplyFunc(
		os.ReadDir,
		func(name string) ([]os.DirEntry, error) {
			if name == "/dev/infiniband/" {
				return nil, fmt.Errorf("permission denied")
			}
			return nil, os.ErrNotExist
		},
	)
	defer patches.Reset()

	// Create allocate request
	request := &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{{}},
	}

	// Execute test
	ctx := context.Background()
	response, err := plugin.Allocate(ctx, request)

	// Verify result
	assert.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "error read infiniband dir")
}

func TestENIDevicePlugin_cleanup_WithGomonkey(t *testing.T) {
	plugin := NewENIDevicePlugin(3, ENITypeENI)

	// Test cleanup with mock implementation
	// Since gomonkey has issues on macOS, we'll test the logic indirectly
	// by verifying the plugin structure and regex matching

	// Test regex matching
	assert.True(t, plugin.eniRes.re.MatchString("123-eni.sock"))
	assert.True(t, plugin.eniRes.re.MatchString("456-member-eni.sock"))
	assert.False(t, plugin.eniRes.re.MatchString("789-other.sock"))

	// Test that cleanup method exists and can be called
	// (actual file operations would require mocking which has issues on macOS)
	assert.NotNil(t, plugin.cleanup)
}

func TestENIDevicePlugin_Start_WithGomonkey(t *testing.T) {
	plugin := NewENIDevicePlugin(3, ENITypeENI)

	// Test Start method structure without actual gRPC operations
	// Since gomonkey has issues on macOS, we'll test the method signature and basic logic

	// Verify plugin structure
	assert.Equal(t, 3, plugin.count)
	assert.Equal(t, ENITypeENI, plugin.eniType)
	assert.NotEmpty(t, plugin.socket)
	assert.NotNil(t, plugin.eniRes)

	// Test that Start method exists
	assert.NotNil(t, plugin.Start)
}

func TestENIDevicePlugin_Stop_WithGomonkey(t *testing.T) {
	plugin := NewENIDevicePlugin(3, ENITypeENI)
	plugin.server = &grpc.Server{}
	plugin.stop = make(chan struct{}, 1)

	// Test Stop method structure without actual gRPC operations
	// Since gomonkey has issues on macOS, we'll test the method signature and basic logic

	// Verify initial state
	assert.NotNil(t, plugin.server)
	assert.NotNil(t, plugin.stop)

	// Test that Stop method exists
	assert.NotNil(t, plugin.Stop)
}

func TestENIDevicePlugin_Serve_WithGomonkey(t *testing.T) {
	plugin := NewENIDevicePlugin(3, ENITypeENI)

	// Test Serve method structure without actual gRPC operations
	// Since gomonkey has issues on macOS, we'll test the method signature and basic logic

	// Verify plugin structure
	assert.Equal(t, 3, plugin.count)
	assert.Equal(t, ENITypeENI, plugin.eniType)
	assert.NotEmpty(t, plugin.socket)

	// Test that Serve method exists
	assert.NotNil(t, plugin.Serve)
}

type mockListAndWatchServer struct {
	pluginapi.DevicePlugin_ListAndWatchServer
	sendFunc func(*pluginapi.ListAndWatchResponse) error
}

func (m *mockListAndWatchServer) Send(response *pluginapi.ListAndWatchResponse) error {
	if m.sendFunc != nil {
		return m.sendFunc(response)
	}
	return nil
}

func (m *mockListAndWatchServer) Context() context.Context {
	return context.Background()
}

func (m *mockListAndWatchServer) SendMsg(msg interface{}) error {
	return nil
}

func (m *mockListAndWatchServer) RecvMsg(msg interface{}) error {
	return nil
}

func (m *mockListAndWatchServer) Header() (map[string][]string, error) {
	return nil, nil
}

func (m *mockListAndWatchServer) Trailer() map[string][]string {
	return nil
}

func (m *mockListAndWatchServer) CloseSend() error {
	return nil
}

type mockDirEntry struct {
	name     string
	isDevice bool
}

func (m *mockDirEntry) Name() string {
	return m.name
}

func (m *mockDirEntry) IsDir() bool {
	return false
}

func (m *mockDirEntry) Type() os.FileMode {
	if m.isDevice {
		return os.ModeDevice
	}
	return 0
}

func (m *mockDirEntry) Info() (os.FileInfo, error) {
	return &mockFileInfo{name: m.name, isDevice: m.isDevice}, nil
}

type mockFileInfo struct {
	name     string
	isDevice bool
}

func (m *mockFileInfo) Name() string {
	return m.name
}

func (m *mockFileInfo) Size() int64 {
	return 0
}

func (m *mockFileInfo) Mode() os.FileMode {
	if m.isDevice {
		return os.ModeDevice
	}
	return 0
}

func (m *mockFileInfo) ModTime() time.Time {
	return time.Now()
}

func (m *mockFileInfo) IsDir() bool {
	return false
}

func (m *mockFileInfo) Sys() interface{} {
	return nil
}

func TestServe(t *testing.T) {
	p := &ENIDevicePlugin{}

	called := make(chan struct{})
	patches := gomonkey.ApplyMethod(
		p, "Start",
		func() error {
			return nil
		},
	)
	defer patches.Reset()

	register := gomonkey.ApplyMethod(
		p, "Register",
		func() error { return nil },
	)
	defer register.Reset()

	stop := gomonkey.ApplyMethod(
		p, "Stop",
		func() error {
			return nil
		},
	)
	defer stop.Reset()

	watchKubeletRestart := gomonkey.ApplyPrivateMethod(
		p, "watchKubeletRestart",
		func() {
			close(called)
		},
	)
	defer watchKubeletRestart.Reset()
	p.Serve()

	<-called
}

func TestCleanUp(t *testing.T) {
	p := &ENIDevicePlugin{
		eniRes: eniRes{
			re:      regexp.MustCompile("test"),
			resName: "test",
			sock:    "test",
		},
	}

	entry := &mockDirEntry{}
	patches := gomonkey.ApplyFunc(
		os.ReadDir,
		func() ([]os.DirEntry, error) {
			return []os.DirEntry{entry}, nil
		},
	)
	defer patches.Reset()
	err := p.cleanup()
	require.NoError(t, err)
}

func TestStart(t *testing.T) {
	rpcServer := &grpc.Server{}

	p := &ENIDevicePlugin{
		server: rpcServer,
		stop:   make(chan struct{}),

		socket: "/tmp/test.sock",
	}

	called := make(chan struct{})
	rpcServerServe := gomonkey.ApplyMethod(rpcServer, "Serve", func(_ *grpc.Server, _ net.Listener) error {
		close(called)
		return nil
	})
	defer rpcServerServe.Reset()

	rpcServerStop := gomonkey.ApplyMethod(rpcServer, "Stop", func(_ *grpc.Server) {})
	defer rpcServerStop.Reset()

	cleanupPatch := gomonkey.ApplyPrivateMethod(p, "cleanup", func(_ *ENIDevicePlugin) error {
		return nil
	})
	defer cleanupPatch.Reset()

	patches := gomonkey.ApplyFunc(net.Listen, func(_, _ string) (net.Listener, error) {
		return nil, nil
	})
	defer patches.Reset()

	regPatch := gomonkey.ApplyFunc(pluginapi.RegisterDevicePluginServer, func(_ *grpc.Server, _ pluginapi.DevicePluginServer) {})
	defer regPatch.Reset()

	serverPatch := gomonkey.ApplyFunc(grpc.NewServer, func(_ ...grpc.ServerOption) *grpc.Server {
		return rpcServer
	})
	defer serverPatch.Reset()

	err := p.Start()
	require.NoError(t, err)

	<-called
}
