package deviceplugin

import (
	"fmt"
	"net"
	"os"
	"path"
	"regexp"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"github.com/AliyunContainerService/terway/pkg/utils"
)

// define resource name
const (
	ENITypeENI    = "eni"
	ENITypeMember = "member"

	// ENIResName aliyun eni resource name in kubernetes container resource
	ENIResName       = "aliyun/eni"
	MemberENIResName = "aliyun/member-eni"
)

type eniRes struct {
	resName string
	re      *regexp.Regexp
	sock    string
}

var eniMap = map[string]eniRes{
	ENITypeENI: {
		resName: ENIResName,
		re:      regexp.MustCompile("^.*" + "-eni.sock"),
		sock:    pluginapi.DevicePluginPath + "%d-" + "eni.sock",
	},
	ENITypeMember: {
		resName: MemberENIResName,
		re:      regexp.MustCompile("^.*" + "-member-eni.sock"),
		sock:    pluginapi.DevicePluginPath + "%d-" + "member-eni.sock",
	},
}

// ENIDevicePlugin implements the Kubernetes device plugin API
type ENIDevicePlugin struct {
	socket string
	server *grpc.Server
	count  int
	stop   chan struct{}
	eniRes eniRes
	sync.Locker
}

// NewENIDevicePlugin returns an initialized ENIDevicePlugin
func NewENIDevicePlugin(count int, eniType string) *ENIDevicePlugin {
	res, ok := eniMap[eniType]
	if !ok {
		panic("unsupported eni type " + eniType)
	}
	pluginEndpoint := fmt.Sprintf(res.sock, time.Now().Unix())
	return &ENIDevicePlugin{
		socket: pluginEndpoint,
		count:  count,
		eniRes: res,
	}
}

// dial establishes the gRPC communication with the registered device plugin.
func dial(unixSocketPath string, timeout time.Duration) (*grpc.ClientConn, func(), error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	c, err := grpc.DialContext(timeoutCtx, unixSocketPath, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	)

	if err != nil {
		cancel()
		return nil, nil, err
	}

	return c, func() {
		err = c.Close()
		cancel()
	}, nil
}

// Start starts the gRPC server of the device plugin
func (m *ENIDevicePlugin) Start() error {
	if m.server != nil {
		close(m.stop)
		m.server.Stop()
	}
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

	m.stop = make(chan struct{}, 1)
	go func() {
		err := m.server.Serve(sock)
		if err != nil {
			klog.Errorf("error start device plugin server, %+v", err)
		}
	}()

	// Wait for server to start by launching a blocking connection
	_, closeConn, err := dial(m.socket, 5*time.Second)
	if err != nil {
		return err
	}
	closeConn()
	return nil
}

// GetDevicePluginOptions return device plugin options
func (m *ENIDevicePlugin) GetDevicePluginOptions(context.Context, *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{}, nil
}

// PreStartContainer return container prestart hook
func (m *ENIDevicePlugin) PreStartContainer(context.Context, *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

// Stop stops the gRPC server
func (m *ENIDevicePlugin) Stop() error {
	if m.server == nil {
		return nil
	}

	m.server.Stop()
	m.server = nil
	close(m.stop)

	return m.cleanup()
}

// Register registers the device plugin for the given resourceName with Kubelet.
func (m *ENIDevicePlugin) Register(request pluginapi.RegisterRequest) error {
	conn, closeConn, err := dial(pluginapi.KubeletSocket, 5*time.Second)
	if err != nil {
		return err
	}
	defer closeConn()

	client := pluginapi.NewRegistrationClient(conn)

	_, err = client.Register(context.Background(), &request)
	if err != nil {
		return err
	}
	return nil
}

// ListAndWatch lists devices and update that list according to the health status
func (m *ENIDevicePlugin) ListAndWatch(e *pluginapi.Empty, s pluginapi.DevicePlugin_ListAndWatchServer) error {
	var devs []*pluginapi.Device
	for i := 0; i < m.count; i++ {
		devs = append(devs, &pluginapi.Device{ID: fmt.Sprintf("eni-%d", i), Health: pluginapi.Healthy})
	}
	err := s.Send(&pluginapi.ListAndWatchResponse{Devices: devs})
	if err != nil {
		return err
	}
	ticker := time.NewTicker(time.Second * 5)
	for {
		select {
		case <-ticker.C:
			err := s.Send(&pluginapi.ListAndWatchResponse{Devices: devs})
			if err != nil {
				klog.Errorf("error send device informance: error: %v", err)
			}
		case <-m.stop:
			return nil
		}
	}
}

func (m *ENIDevicePlugin) GetPreferredAllocation(context.Context, *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	return nil, fmt.Errorf("unsupported")
}

// Allocate which return list of devices.
func (m *ENIDevicePlugin) Allocate(ctx context.Context, r *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	response := pluginapi.AllocateResponse{
		ContainerResponses: []*pluginapi.ContainerAllocateResponse{},
	}

	klog.Infof("Request Containers: %v", r.GetContainerRequests())
	for range r.GetContainerRequests() {
		response.ContainerResponses = append(response.ContainerResponses,
			&pluginapi.ContainerAllocateResponse{},
		)
	}

	return &response, nil
}

func (m *ENIDevicePlugin) cleanup() error {
	preSocks, err := os.ReadDir(pluginapi.DevicePluginPath)
	if err != nil {
		return err
	}

	for _, preSock := range preSocks {
		klog.Infof("device plugin file info: %+v", preSock)
		if m.eniRes.re.Match([]byte(preSock.Name())) {
			if utils.IsWindowsOS() {
				// NB(thxCode): treat the socket file as normal file
				// and remove them directly.
				err = os.Remove(path.Join(pluginapi.DevicePluginPath, preSock.Name()))
			} else {
				err = syscall.Unlink(path.Join(pluginapi.DevicePluginPath, preSock.Name()))
			}
			if err != nil {
				klog.Errorf("error on clean up previous device plugin listens, %+v", err)
			}
		}
	}
	return nil
}

func (m *ENIDevicePlugin) watchKubeletRestart() {
	wait.Until(func() {
		var err error
		if utils.IsWindowsOS() {
			// NB(thxCode): since os.Stat has not worked as expected,
			// we use os.Lstat instead of os.Stat here,
			// ref to https://github.com/microsoft/Windows-Containers/issues/97#issuecomment-887713195.
			_, err = os.Lstat(m.socket)
		} else {
			_, err = os.Stat(m.socket)
		}
		if err == nil {
			return
		}
		if os.IsNotExist(err) {
			klog.Infof("device plugin socket %s removed, restarting.", m.socket)
			err := m.Stop()
			if err != nil {
				klog.Errorf("stop current device plugin server with error: %v", err)
			}
			err = m.Start()
			if err != nil {
				klog.Fatalf("error restart device plugin after kubelet restart %+v", err)
			}
			err = m.Register(
				pluginapi.RegisterRequest{
					Version:      pluginapi.Version,
					Endpoint:     path.Base(m.socket),
					ResourceName: m.eniRes.resName,
				},
			)
			if err != nil {
				klog.Fatalf("error register device plugin after kubelet restart %+v", err)
			}
			return
		}
		klog.Fatalf("error stat socket: %+v", err)
	}, time.Second*30, make(chan struct{}, 1))
}

// Serve starts the gRPC server and register the device plugin to Kubelet
func (m *ENIDevicePlugin) Serve() {
	err := m.Start()
	if err != nil {
		klog.Fatalf("Could not start device plugin: %v", err)
	}
	time.Sleep(5 * time.Second)
	klog.Infof("Starting to serve on %s", m.socket)

	err = m.Register(
		pluginapi.RegisterRequest{
			Version:      pluginapi.Version,
			Endpoint:     path.Base(m.socket),
			ResourceName: m.eniRes.resName,
		},
	)
	if err != nil {
		klog.Errorf("Could not register device plugin: %v", err)
		stopErr := m.Stop()
		if stopErr != nil {
			klog.Fatalf("stop current device plugin server with error: %v", stopErr)
		}
	}
	klog.Infof("Registered device plugin with Kubelet")
	go m.watchKubeletRestart()
}
