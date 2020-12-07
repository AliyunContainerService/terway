package deviceplugin

import (
	"net"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"fmt"
	"io/ioutil"
	"path"
	"regexp"
	"sync"
	"syscall"

	log "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	// DefaultResourceName aliyun eni resource name in kubernetes container resource
	DefaultResourceName = "aliyun/eni"
	serverSock          = pluginapi.DevicePluginPath + "%d-" + "eni.sock"
)

var eniServerSockRegex = regexp.MustCompile("^.*" + "-eni.sock")

// EniDevicePlugin implements the Kubernetes device plugin API
type EniDevicePlugin struct {
	socket string
	server *grpc.Server
	count  int
	stop   chan struct{}
	sync.Locker
}

// NewEniDevicePlugin returns an initialized EniDevicePlugin
func NewEniDevicePlugin(count int) *EniDevicePlugin {
	pluginEndpoint := fmt.Sprintf(serverSock, time.Now().Unix())
	return &EniDevicePlugin{
		socket: pluginEndpoint,
		count:  count,
	}
}

// dial establishes the gRPC communication with the registered device plugin.
func dial(unixSocketPath string, timeout time.Duration) (*grpc.ClientConn, func(), error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	c, err := grpc.DialContext(timeoutCtx, unixSocketPath, grpc.WithInsecure(), grpc.WithBlock(),
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
func (m *EniDevicePlugin) Start() error {
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
			log.Errorf("error start device plugin server, %+v", err)
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
func (m *EniDevicePlugin) GetDevicePluginOptions(context.Context, *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{GetPreferredAllocationAvailable: false}, nil
}

// PreStartContainer return container prestart hook
func (m *EniDevicePlugin) PreStartContainer(context.Context, *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

// Stop stops the gRPC server
func (m *EniDevicePlugin) Stop() error {
	if m.server == nil {
		return nil
	}

	m.server.Stop()
	m.server = nil
	close(m.stop)

	return m.cleanup()
}

// Register registers the device plugin for the given resourceName with Kubelet.
func (m *EniDevicePlugin) Register(request pluginapi.RegisterRequest) error {
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
func (m *EniDevicePlugin) ListAndWatch(e *pluginapi.Empty, s pluginapi.DevicePlugin_ListAndWatchServer) error {
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
			log.Debugf("send list and watch res: %+v", devs)
			err := s.Send(&pluginapi.ListAndWatchResponse{Devices: devs})
			if err != nil {
				log.Errorf("error send device informance: error: %v", err)
			}
		case <-m.stop:
			return nil
		}
	}
}

// Allocate which return list of devices.
func (m *EniDevicePlugin) Allocate(ctx context.Context, r *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	response := pluginapi.AllocateResponse{
		ContainerResponses: []*pluginapi.ContainerAllocateResponse{},
	}

	log.Infof("Request Containers: %v", r.GetContainerRequests())
	for range r.GetContainerRequests() {
		response.ContainerResponses = append(response.ContainerResponses,
			&pluginapi.ContainerAllocateResponse{},
		)
	}

	return &response, nil
}

func (m *EniDevicePlugin) cleanup() error {
	preSocks, err := ioutil.ReadDir(pluginapi.DevicePluginPath)
	if err != nil {
		return err
	}

	for _, preSock := range preSocks {
		log.Infof("device plugin file info: %+v", preSock)
		if eniServerSockRegex.Match([]byte(preSock.Name())) && preSock.Mode()&os.ModeSocket != 0 {
			if err = syscall.Unlink(path.Join(pluginapi.DevicePluginPath, preSock.Name())); err != nil {
				log.Errorf("error on clean up previous device plugin listens, %+v", err)
			}
		}
	}
	return nil
}

func (m *EniDevicePlugin) watchKubeletRestart() {
	//fsWatcher, err := fsnotify.NewWatcher()
	//if err != nil {
	//	log.Fatalf("error create fs watcher, %+v", err)
	//}
	//defer fsWatcher.Close()
	//err = fsWatcher.Add(m.socket)
	//if err != nil {
	//	fsWatcher.Close()
	//	log.Fatalf("error watch socket file: %+v", err)
	//}
	//log.Infof("watching %s fs event", m.socket)
	//for {
	//	select {
	//	case event := <-fsWatcher.Events:
	//		log.Infof("watch fs event: %+v", event)
	//		if event.Name == m.socket && event.Op&fsnotify.Remove == fsnotify.Remove {
	//			log.Printf("inotify: %s removed, restarting.", m.socket)
	//			m.Stop()
	//			err := m.Start()
	//			if err != nil {
	//				log.Fatalf("error restart device plugin after kubelet restart %+v", err)
	//			}
	//		}
	//	}
	//}

	wait.Until(func() {
		_, err := os.Stat(m.socket)
		if err == nil {
			return
		}
		if os.IsNotExist(err) {
			log.Infof("device plugin socket %s removed, restarting.", m.socket)
			err := m.Stop()
			if err != nil {
				log.Errorf("stop current device plugin server with error: %v", err)
			}
			err = m.Start()
			if err != nil {
				log.Fatalf("error restart device plugin after kubelet restart %+v", err)
			}
			err = m.Register(
				pluginapi.RegisterRequest{
					Version:      pluginapi.Version,
					Endpoint:     path.Base(m.socket),
					ResourceName: DefaultResourceName,
				},
			)
			if err != nil {
				log.Fatalf("error register device plugin after kubelet restart %+v", err)
			}
			return
		}
		log.Fatalf("error stat socket: %+v", err)
	}, time.Second*30, make(chan struct{}, 1))
}

// Serve starts the gRPC server and register the device plugin to Kubelet
func (m *EniDevicePlugin) Serve(resourceName string) error {
	err := m.Start()
	if err != nil {
		log.Errorf("Could not start device plugin: %v", err)
		return err
	}
	time.Sleep(5 * time.Second)
	log.Infof("Starting to serve on %s", m.socket)

	err = m.Register(
		pluginapi.RegisterRequest{
			Version:      pluginapi.Version,
			Endpoint:     path.Base(m.socket),
			ResourceName: resourceName,
		},
	)
	if err != nil {
		log.Errorf("Could not register device plugin: %v", err)
		err := m.Stop()
		if err != nil {
			log.Errorf("stop current device plugin server with error: %v", err)
		}
		return err
	}
	log.Infof("Registered device plugin with Kubelet")
	go m.watchKubeletRestart()

	return nil
}

func (m *EniDevicePlugin) GetPreferredAllocation(ctx context.Context, request *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	panic("GetPreferredAllocation not implemented")
}