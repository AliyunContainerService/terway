package deviceplugin

import (
	"net"
	"os"
	"path"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	pluginapi "k8s.io/kubernetes/pkg/kubelet/apis/deviceplugin/v1alpha"
	"syscall"
	"sync"
	"fmt"
)

const (
	DefaultResourceName = "alicloud/eni"
	serverSock          = pluginapi.DevicePluginPath + "eni.sock"
)

// EniDevicePlugin implements the Kubernetes device plugin API
type EniDevicePlugin struct {
	socket string
	server *grpc.Server
	count int
	sync.Locker
}

// NewEniDevicePlugin returns an initialized EniDevicePlugin
func NewEniDevicePlugin(count int) *EniDevicePlugin {
	return &EniDevicePlugin{
		socket: serverSock,
		count: count,
	}
}

// dial establishes the gRPC communication with the registered device plugin.
func dial(unixSocketPath string, timeout time.Duration) (*grpc.ClientConn, error) {
	c, err := grpc.Dial(unixSocketPath, grpc.WithInsecure(), grpc.WithBlock(),
		grpc.WithTimeout(timeout),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	)

	if err != nil {
		return nil, err
	}

	return c, nil
}

// Start starts the gRPC server of the device plugin
func (m *EniDevicePlugin) Start() error {
	err := m.cleanup()
	if err != nil {
		return err
	}

	sock, err := net.Listen("unix", m.socket)
	if err != nil {
		return err
	}

	m.server = grpc.NewServer([]grpc.ServerOption{}...)
	pluginapi.RegisterDevicePluginServer(m.server, m)

	go m.server.Serve(sock)

	// Wait for server to start by launching a blocking connection
	conn, err := dial(m.socket, 5*time.Second)
	if err != nil {
		return err
	}
	conn.Close()

	return nil
}

// Stop stops the gRPC server
func (m *EniDevicePlugin) Stop() error {
	if m.server == nil {
		return nil
	}

	m.server.Stop()
	m.server = nil
//	close(m.stop)

	return m.cleanup()
}

// Register registers the device plugin for the given resourceName with Kubelet.
func (m *EniDevicePlugin) Register(kubeletEndpoint, resourceName string) error {
	conn, err := dial(kubeletEndpoint, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pluginapi.NewRegistrationClient(conn)
	reqt := &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     path.Base(m.socket),
		ResourceName: resourceName,
	}

	_, err = client.Register(context.Background(), reqt)
	if err != nil {
		return err
	}
	return nil
}

// ListAndWatch lists devices and update that list according to the health status
func (m *EniDevicePlugin) ListAndWatch(e *pluginapi.Empty, s pluginapi.DevicePlugin_ListAndWatchServer) error {
	var devs []*pluginapi.Device
	for i:=0; i< m.count; i++ {
		devs = append(devs, &pluginapi.Device {ID: fmt.Sprintf("eni-%d", i), Health: pluginapi.Healthy})
	}
	s.Send(&pluginapi.ListAndWatchResponse{Devices: devs})
	ticker := time.NewTicker(time.Second * 5)
	for {
		select {
		case <-ticker.C:
				s.Send(&pluginapi.ListAndWatchResponse{Devices: devs})
		}
	}
	return nil
}

// Allocate which return list of devices.
func (m *EniDevicePlugin) Allocate(ctx context.Context, r *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	response := pluginapi.AllocateResponse{}

	log.Infof("Request IDs: %v", r.DevicesIDs)
	var devicesList []*pluginapi.DeviceSpec
	for range r.DevicesIDs {
		ds := &pluginapi.DeviceSpec{
		}
		devicesList = append(devicesList, ds)
	}

	return &response, nil
}

func (m *EniDevicePlugin) cleanup() error {
	if err := syscall.Unlink(m.socket); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// Serve starts the gRPC server and register the device plugin to Kubelet
func (m *EniDevicePlugin) Serve(resourceName string) error {
	err := m.Start()
	if err != nil {
		log.Errorf("Could not start device plugin: %v", err)
		return err
	}
	log.Infof("Starting to serve on %s", m.socket)

	err = m.Register(pluginapi.KubeletSocket, resourceName)
	if err != nil {
		log.Errorf("Could not register device plugin: %v", err)
		m.Stop()
		return err
	}
	log.Infof("Registered device plugin with Kubelet")

	return nil
}

func main(){
	plg := NewEniDevicePlugin(1)
	err := plg.Serve(DefaultResourceName)
	if err != nil {
		log.Fatalf("error serve plugin, %+v", err)
	}

	for {
		time.Sleep(time.Hour)
	}
}
