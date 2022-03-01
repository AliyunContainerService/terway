package main

import (
	"flag"
	"os"

	"github.com/AliyunContainerService/terway/daemon"
	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/AliyunContainerService/terway/pkg/utils"

	"k8s.io/klog/v2"

	"k8s.io/client-go/pkg/version"
)

const defaultConfigPath = "/etc/eni/eni.json"
const defaultPidPath = "/var/run/eni/eni.pid"
const defaultSocketPath = "/var/run/eni/eni.socket"
const debugSocketPath = "unix:///var/run/eni/eni_debug.socket"

var (
	logLevel       string
	daemonMode     string
	readonlyListen string
	kubeconfig     string
	master         string
)

func main() {
	fs := flag.NewFlagSet("terway", flag.ExitOnError)

	fs.StringVar(&daemonMode, "daemon-mode", "VPC", "terway network mode")
	fs.StringVar(&logLevel, "log-level", "info", "terway log level")
	fs.StringVar(&readonlyListen, "readonly-listen", utils.NormalizePath(debugSocketPath), "terway readonly listen")
	fs.StringVar(&master, "master", "", "The address of the Kubernetes API server (overrides any value in kubeconfig).")
	fs.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file with authorization and master location information.")
	err := fs.Parse(os.Args[1:])
	if err != nil {
		panic(err)
	}

	klog.SetOutput(os.Stderr)

	logger.DefaultLogger.Infof("GitCommit %s BuildDate %s Platform %s",
		version.Get().GitCommit, version.Get().BuildDate, version.Get().Platform)
	if err := daemon.Run(utils.NormalizePath(defaultPidPath), utils.NormalizePath(defaultSocketPath), readonlyListen, utils.NormalizePath(defaultConfigPath), kubeconfig, master, daemonMode, logLevel); err != nil {
		logger.DefaultLogger.Fatal(err)
	}
}
