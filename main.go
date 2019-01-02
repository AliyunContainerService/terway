package main

import (
	"flag"
	"github.com/AliyunContainerService/terway/daemon"
	log "github.com/sirupsen/logrus"
)

const DEFAULT_CONFIG_PATH = "/etc/eni/eni.json"
const DEFAULT_PID_PATH = "/var/run/eni/eni.pid"
const DEFAULT_SOCKET_PATH = "/var/run/eni/eni.socket"
const DEBUG_SOCKET_PATH = "unix:///var/run/eni/eni_debug.socket"

var (
	gitVer string
	logLevel string
	daemonMode string
	readonlyListen string
)

func init() {
	flag.StringVar(&daemonMode, "daemon-mode", "VPC", "terway network mode")
	flag.StringVar(&logLevel, "log-level", "info", "terway log level")
	flag.StringVar(&readonlyListen, "readonly-listen", DEBUG_SOCKET_PATH, "terway readonly listen")
}

func main() {
	flag.Parse()
	log.Infof("Starting terway of version: %s", gitVer)
	if err := daemon.Run(DEFAULT_PID_PATH, DEFAULT_SOCKET_PATH, readonlyListen, DEFAULT_CONFIG_PATH, daemonMode, logLevel); err != nil {
		log.Fatal(err)
	}
}
