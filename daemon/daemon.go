package daemon

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"syscall"
)

func Run(pidFilePath string, socketFilePath string, configFilePath string) error {
	runtime.LockOSThread()

	log.SetLevel(log.DebugLevel)

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

	eni, err := newENIService(configFilePath)
	if err != nil {
		return err
	}
	log.Debugf("init eni service config: %+v", eni)
	if err := eni.initialize(); err != nil {
		return err
	}
	rpc.Register(eni)
	rpc.HandleHTTP()
	return http.Serve(l, nil)
}
