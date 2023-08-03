package main

import (
	"flag"
	"math/rand"
	"os"
	"time"

	"github.com/AliyunContainerService/terway/daemon"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/pkg/version"

	"k8s.io/klog/v2"
	"k8s.io/klog/v2/klogr"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	log = ctrl.Log.WithName("setup")
)

const defaultConfigPath = "/etc/eni/eni.json"
const defaultSocketPath = "/var/run/eni/eni.socket"
const debugSocketPath = "unix:///var/run/eni/eni_debug.socket"

var (
	logLevel       string
	daemonMode     string
	readonlyListen string
)

func main() {
	rand.New(rand.NewSource(time.Now().UnixNano()))
	klog.InitFlags(nil)
	defer klog.Flush()

	ctrl.SetLogger(klogr.New())
	log.Info(version.Version)

	fs := flag.NewFlagSet("terway", flag.ExitOnError)

	fs.StringVar(&daemonMode, "daemon-mode", "VPC", "terway network mode")
	fs.StringVar(&logLevel, "log-level", "info", "terway log level")
	fs.StringVar(&readonlyListen, "readonly-listen", utils.NormalizePath(debugSocketPath), "terway readonly listen")
	err := fs.Parse(os.Args[1:])
	if err != nil {
		panic(err)
	}

	ctx := ctrl.SetupSignalHandler()
	err = daemon.Run(ctx, utils.NormalizePath(defaultSocketPath), readonlyListen, utils.NormalizePath(defaultConfigPath), daemonMode, logLevel)

	if err != nil {
		klog.Fatal(err)
	}
}
