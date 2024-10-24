package main

import (
	"flag"
	"math/rand"
	"os"
	"strings"
	"time"

	"k8s.io/klog/v2/textlogger"

	"github.com/AliyunContainerService/terway/daemon"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/pkg/version"

	"k8s.io/klog/v2"
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

	fs := flag.NewFlagSet("terway", flag.ExitOnError)
	fs.StringVar(&daemonMode, "daemon-mode", "VPC", "terway network mode")
	fs.StringVar(&logLevel, "log-level", "info", "terway log level")
	fs.StringVar(&readonlyListen, "readonly-listen", utils.NormalizePath(debugSocketPath), "terway readonly listen")
	ctrl.RegisterFlags(fs)

	err := fs.Parse(os.Args[1:])
	if err != nil {
		panic(err)
	}
	var opts []textlogger.ConfigOption

	if strings.ToLower(logLevel) == "debug" {
		opts = append(opts, textlogger.Verbosity(4))
	}
	ctrl.SetLogger(textlogger.NewLogger(textlogger.NewConfig(opts...)))

	log.Info(version.Version)

	ctx := ctrl.SetupSignalHandler()
	err = daemon.Run(ctx, utils.NormalizePath(defaultSocketPath), readonlyListen, utils.NormalizePath(defaultConfigPath), daemonMode)

	if err != nil {
		klog.Fatal(err)
	}
}
