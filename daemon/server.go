package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof" // import pprof for diagnose
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/rpc"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
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
			log.Printf("dump stacks: %s\n", string(buf))
		}
	}(sigchain)

	signal.Notify(sigchain, stackTriggerSignals...)
}

// Run terway daemon
func Run(ctx context.Context, socketFilePath, debugSocketListen, configFilePath, daemonMode, logLevel string) error {
	level, err := log.ParseLevel(logLevel)
	if err != nil {
		return fmt.Errorf("error set log level: %s, %w", logLevel, err)
	}
	logger.DefaultLogger.SetLevel(level)
	if !utils.IsWindowsOS() {
		// NB(thxCode): hcsshim lib introduces much noise.
		log.SetLevel(level)
	}

	err = os.MkdirAll(filepath.Dir(socketFilePath), 0700)
	if err != nil {
		return fmt.Errorf("error create socket dir: %s, %w", filepath.Dir(socketFilePath), err)
	}
	err = syscall.Unlink(socketFilePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error unlink socket file: %s, %w", socketFilePath, err)
	}
	mask := syscallUmask(0777)
	defer syscallUmask(mask)

	l, err := net.Listen("unix", socketFilePath)
	if err != nil {
		return fmt.Errorf("error listen at %s: %v", socketFilePath, err)
	}

	networkService, err := newNetworkService(ctx, configFilePath, daemonMode)
	if err != nil {
		return err
	}

	grpcServer := grpc.NewServer()
	rpc.RegisterTerwayBackendServer(grpcServer, networkService)
	rpc.RegisterTerwayTracingServer(grpcServer, tracing.DefaultRPCServer())

	stop := make(chan struct{})

	stackTriger()
	err = runDebugServer(debugSocketListen)
	if err != nil {
		return err
	}

	go func() {
		serviceLog.Infof("start serving on %s", socketFilePath)
		err = grpcServer.Serve(l)
		if err != nil {
			log.Errorf("error start grpc server: %v", err)
			close(stop)
		}
	}()

	select {
	case <-ctx.Done():
	case <-stop:
	}
	grpcServer.Stop()
	return nil
}

func runDebugServer(debugSocketListen string) error {
	var (
		l   net.Listener
		err error
	)
	if strings.HasPrefix(debugSocketListen, "unix://") {
		debugSocketListen = strings.TrimPrefix(debugSocketListen, "unix://")
		if err := os.MkdirAll(filepath.Dir(debugSocketListen), 0700); err != nil {
			return err
		}

		if err := syscall.Unlink(debugSocketListen); err != nil && !os.IsNotExist(err) {
			return err
		}

		l, err = net.Listen("unix", debugSocketListen)
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
			log.Errorf("error start debug server: %v", err)
		}
	}()

	return nil
}

// RegisterPrometheus register metrics to prometheus server
func registerPrometheus() {
	prometheus.MustRegister(metric.RPCLatency)
	prometheus.MustRegister(metric.OpenAPILatency)
	prometheus.MustRegister(metric.MetadataLatency)
	// ResourcePool
	prometheus.MustRegister(metric.ResourcePoolTotal)
	prometheus.MustRegister(metric.ResourcePoolIdle)
	prometheus.MustRegister(metric.ResourcePoolDisposed)
	// ENIIP
	prometheus.MustRegister(metric.ENIIPFactoryIPCount)
	prometheus.MustRegister(metric.ENIIPFactoryENICount)
	prometheus.MustRegister(metric.ENIIPFactoryIPAllocCount)
}
