package main

import (
	"github.com/AliyunContainerService/terway/daemon"
	log "github.com/sirupsen/logrus"
)

const DEFAULT_CONFIG_PATH = "/etc/eni/eni.json"
const DEFAULT_PID_PATH = "/var/run/eni/eni.pid"
const DEFAULT_SOCKET_PATH = "/var/run/eni/eni.socket"

var gitVer string

func main() {
	log.Infof("Starting terway of version: %s", gitVer)
	log.Fatal(daemon.Run(DEFAULT_PID_PATH, DEFAULT_SOCKET_PATH, DEFAULT_CONFIG_PATH))
}
