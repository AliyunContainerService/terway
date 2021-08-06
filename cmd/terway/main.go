package main

import (
	"flag"

	"github.com/AliyunContainerService/terway/daemon"
	log "github.com/sirupsen/logrus"
)

const defaultConfigPath = "/etc/eni/eni.json"
const defaultPidPath = "/var/run/eni/eni.pid"
const defaultSocketPath = "/var/run/eni/eni.socket"
const debugSocketPath = "unix:///var/run/eni/eni_debug.socket"

var (
	gitVer         string
	logLevel       string
	daemonMode     string
	readonlyListen string
	kubeconfig     string
	master         string
)

func init() {
	flag.StringVar(&daemonMode, "daemon-mode", "VPC", "terway network mode")
	flag.StringVar(&logLevel, "log-level", "info", "terway log level")
	flag.StringVar(&readonlyListen, "readonly-listen", debugSocketPath, "terway readonly listen")
	flag.StringVar(&master, "master", "", "The address of the Kubernetes API server (overrides any value in kubeconfig).")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file with authorization and master location information.")

}

func main() {
	flag.Parse()
	log.Infof("Starting terway of version: %s", gitVer)
	if err := daemon.Run(defaultPidPath, defaultSocketPath, readonlyListen, defaultConfigPath, kubeconfig, master, daemonMode, logLevel); err != nil {
		log.Fatal(err)
	}
}
