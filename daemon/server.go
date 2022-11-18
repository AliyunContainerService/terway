package daemon

import (
	"fmt"
	"io/ioutil"
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

	"github.com/pkg/errors"
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
func Run(pidFilePath, socketFilePath, debugSocketListen, configFilePath, kubeconfig, master, daemonMode, logLevel string) error {
	level, err := log.ParseLevel(logLevel)
	if err != nil {
		return errors.Wrapf(err, "error set log level: %s", logLevel)
	}
	logger.DefaultLogger.SetLevel(level)
	if !utils.IsWindowsOS() {
		// NB(thxCode): hcsshim lib introduces much noise.
		log.SetLevel(level)
	}
	// Write the pidfile
	if pidFilePath != "" {
		if !filepath.IsAbs(pidFilePath) {
			return fmt.Errorf("error writing pidfile %q: path not absolute", pidFilePath)
		}

		if _, err := os.Stat(filepath.Dir(pidFilePath)); err != nil && os.IsNotExist(err) {
			if err = os.MkdirAll(filepath.Dir(pidFilePath), 0666); err != nil {
				return fmt.Errorf("error create pid file: %+v", err)
			}
		}
		if err := ioutil.WriteFile(pidFilePath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
			return fmt.Errorf("error writing pidfile %q: %v", pidFilePath, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(socketFilePath), 0700); err != nil {
		return err
	}

	if err := syscall.Unlink(socketFilePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	mask := syscallUmask(0777)
	defer syscallUmask(mask)

	l, err := net.Listen("unix", socketFilePath)
	if err != nil {
		return fmt.Errorf("error listen at %s: %v", socketFilePath, err)
	}

	networkService, err := newNetworkService(configFilePath, kubeconfig, master, daemonMode)
	if err != nil {
		return err
	}

	grpcServer := grpc.NewServer()
	rpc.RegisterTerwayBackendServer(grpcServer, networkService)
	rpc.RegisterTerwayTracingServer(grpcServer, tracing.DefaultRPCServer())

	stop := make(chan struct{})

	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigs
		log.Infof("got system signal: %v, exiting", sig)
		stop <- struct{}{}
	}()

	stackTriger()
	err = runDebugServer(debugSocketListen)
	if err != nil {
		return err
	}

	go func() {
		err = grpcServer.Serve(l)
		if err != nil {
			log.Errorf("error start grpc server: %v", err)
			stop <- struct{}{}
		}
	}()

	<-stop
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
