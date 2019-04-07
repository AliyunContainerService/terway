package daemon

import (
	"fmt"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"io/ioutil"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// stackTriger Print golang stack trace to log
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

	signal.Notify(sigchain, syscall.SIGUSR1)
}

func Run(pidFilePath string, socketFilePath string, debugSocketListen string, configFilePath string, daemonMode string, logLevel string) error {
	level, err := log.ParseLevel(logLevel)
	if err != nil {
		return errors.Wrapf(err, "error set log level: %s", logLevel)
	}
	log.SetLevel(level)
	// Write the pidfile
	if pidFilePath != "" {
		if !filepath.IsAbs(pidFilePath) {
			return fmt.Errorf("error writing pidfile %q: path not absolute", pidFilePath)
		}

		if _, err := os.Stat(path.Dir(pidFilePath)); err != nil && os.IsNotExist(err) {
			os.MkdirAll(path.Dir(pidFilePath), 0666)
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
	mask := syscall.Umask(0777)
	defer syscall.Umask(mask)

	l, err := net.Listen("unix", socketFilePath)
	if err != nil {
		return fmt.Errorf("error listen at %s: %v", socketFilePath, err)
	}

	networkService, err := newNetworkService(configFilePath, daemonMode)
	if err != nil {
		return err
	}

	grpcServer := grpc.NewServer()
	rpc.RegisterTerwayBackendServer(grpcServer, networkService)
	stop := make(chan struct{})

	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigs
		log.Infof("got system signal: %v, exiting", sig)
		stop <- struct{}{}
	}()

	stackTriger()
	err = RunDebugServer(debugSocketListen)
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

func RunDebugServer(debugSocketListen string) error {
	var (
		l   net.Listener
		err error
	)
	if strings.HasPrefix(debugSocketListen, "unix://") {
		debugSocketListen = strings.TrimLeft(debugSocketListen, "unix://")
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

	metric.RegisterPrometheus()
	http.DefaultServeMux.Handle("/metrics", promhttp.Handler())

	go func() {
		err := http.Serve(l, http.DefaultServeMux)
		if err != nil {
			log.Errorf("error start debug server: %v", err)
		}
	}()

	return nil
}
