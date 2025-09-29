package daemon

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	_ "net/http/pprof" // import pprof for diagnose
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	terwayfeature "github.com/AliyunContainerService/terway/pkg/feature"
	"github.com/alexflint/go-filemutex"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/wait"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/rpc"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
)

const daemonRPCTimeout = 118 * time.Second
const lockFile = "/var/run/eni/terwayd.lock"

const (
	prevCNIConfFile   = "10-terway.conf"
	cinConfFile       = "10-terway.conflist"
	tmpCNIConfigPath  = "/etc/cni/net.d" // that is tmpfs
	hostCNIConfigPath = "/host-etc-net.d"
)

// stackTriger print golang stack trace to log
func stackTriger() {
	sigchain := make(chan os.Signal, 1)
	go func(c chan os.Signal) {
		for {
			<-sigchain
			var (
				buf       []byte
				stackSize int
			)
			bufferLen := 16384
			for stackSize == len(buf) {
				buf = make([]byte, bufferLen)
				stackSize = runtime.Stack(buf, true)
				bufferLen *= 2
			}
			buf = buf[:stackSize]

			os.Stdout.Write(buf)
		}
	}(sigchain)

	signal.Notify(sigchain, stackTriggerSignals...)
}

// Run terway daemon
func Run(ctx context.Context, socketFilePath, debugSocketListen, configFilePath, daemonMode string) error {
	err := os.MkdirAll(filepath.Dir(lockFile), 0700)
	if err != nil {
		return err
	}

	lock, err := filemutex.New(lockFile)
	if err != nil {
		return fmt.Errorf("error create lock file: %s, %w", lockFile, err)
	}
	defer func(lock *filemutex.FileMutex) {
		_ = lock.Unlock()
	}(lock)
	err = wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 5*time.Second, true, func(ctx context.Context) (done bool, err error) {
		innerErr := lock.TryLock()
		if innerErr != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("failed to acquire lock: %v", err)
	}
	l, err := newUnixListener(socketFilePath)
	if err != nil {
		return fmt.Errorf("error listen at %s: %v", socketFilePath, err)
	}

	svc, err := newNetworkService(ctx, configFilePath, daemonMode)
	if err != nil {
		return err
	}

	grpcServer := grpc.NewServer(grpc.ChainUnaryInterceptor(
		cniInterceptor,
	))
	rpc.RegisterTerwayBackendServer(grpcServer, svc)
	rpc.RegisterTerwayTracingServer(grpcServer, tracing.DefaultRPCServer())

	stop := make(chan struct{})

	stackTriger()
	err = runDebugServer(ctx, &svc.wg, debugSocketListen)
	if err != nil {
		return err
	}

	go func() {
		serviceLog.Info("start serving", "path", socketFilePath)
		err = grpcServer.Serve(l)
		if err != nil {
			serviceLog.Error(err, "error serving grpc")
			close(stop)
		}
	}()

	err = ensureCNIConfig()
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
	case <-stop:
	}
	grpcServer.Stop()

	svc.wg.Wait()

	return nil
}

func newUnixListener(addr string) (net.Listener, error) {
	err := os.MkdirAll(filepath.Dir(addr), 0700)
	if err != nil {
		return nil, fmt.Errorf("error create socket dir: %s, %w", addr, err)
	}

	err = syscall.Unlink(addr)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("error unlink socket file: %s, %w", addr, err)
	}
	mask := syscallUmask(0777)
	defer syscallUmask(mask)

	l, err := net.Listen("unix", addr)
	if err != nil {
		return nil, err
	}
	err = os.Chmod(addr, 0600)
	if err != nil {
		return nil, err
	}

	return l, nil
}

func ensureCNIConfig() error {
	if utilfeature.DefaultFeatureGate.Enabled(terwayfeature.WriteCNIConfFirst) {
		serviceLog.Info("WriteCNIConfFirst is enabled, skip write cni conf")
		return nil
	}

	src, err := os.Open(filepath.Join(tmpCNIConfigPath, cinConfFile))
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(filepath.Join(hostCNIConfigPath, cinConfFile), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	if err != nil {
		return err
	}

	serviceLog.Info("write cni conf success")

	_ = os.Remove(filepath.Join(hostCNIConfigPath, prevCNIConfFile))
	return nil
}

func runDebugServer(ctx context.Context, wg *sync.WaitGroup, debugSocketListen string) error {
	var (
		l   net.Listener
		err error
	)
	if strings.HasPrefix(debugSocketListen, "unix://") {
		debugSocketListen = strings.TrimPrefix(debugSocketListen, "unix://")
		l, err = newUnixListener(debugSocketListen)
		if err != nil {
			return fmt.Errorf("error listen at %s: %v", debugSocketListen, err)
		}
	} else {
		l, err = net.Listen("tcp", debugSocketListen)
		if err != nil {
			return fmt.Errorf("error listen at %s: %v", debugSocketListen, err)
		}
	}

	registerPrometheus()
	http.DefaultServeMux.Handle("/metrics", promhttp.Handler())

	go func() {
		err := http.Serve(l, http.DefaultServeMux)
		if err != nil {
			serviceLog.Error(err, "error start debug server")
		}
	}()

	wg.Add(1)
	go func() {
		<-ctx.Done()
		_ = l.Close()

		wg.Done()
	}()

	return nil
}

// RegisterPrometheus register metrics to prometheus server
func registerPrometheus() {
	prometheus.MustRegister(metric.RPCLatency)
	prometheus.MustRegister(metric.OpenAPILatency)
	prometheus.MustRegister(metadata.MetadataLatency)
	// ResourcePool
	prometheus.MustRegister(metric.ResourcePoolTotal)
	prometheus.MustRegister(metric.ResourcePoolIdle)
	prometheus.MustRegister(metric.ResourcePoolDisposed)
	// ENIIP
	prometheus.MustRegister(metric.ENIIPFactoryIPCount)
	prometheus.MustRegister(metric.ENIIPFactoryENICount)
	prometheus.MustRegister(metric.ENIIPFactoryIPAllocCount)
}

func cniInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	ctx, cancel := context.WithTimeout(ctx, daemonRPCTimeout)
	defer cancel()

	switch r := req.(type) {
	case *rpc.AllocIPRequest:
		l := logf.FromContext(ctx, "pod", utils.PodInfoKey(r.K8SPodNamespace, r.K8SPodName), "containerID", r.K8SPodInfraContainerId)
		ctx = logr.NewContext(ctx, l)
	case *rpc.ReleaseIPRequest:
		l := logf.FromContext(ctx, "pod", utils.PodInfoKey(r.K8SPodNamespace, r.K8SPodName), "containerID", r.K8SPodInfraContainerId)
		ctx = logr.NewContext(ctx, l)
	case *rpc.GetInfoRequest:
		l := logf.FromContext(ctx, "pod", utils.PodInfoKey(r.K8SPodNamespace, r.K8SPodName), "containerID", r.K8SPodInfraContainerId)
		ctx = logr.NewContext(ctx, l)
	default:
	}
	return handler(ctx, req)
}
