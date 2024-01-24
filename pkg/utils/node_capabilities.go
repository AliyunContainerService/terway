package utils

import (
	"os"
	"strings"
)

const (
	nodeCapabilitiesFile = "/var/run/eni/node_capabilities"
	NodeCapabilityERDMA  = "erdma"
)

var cachedNodeCapabilities = map[string]string{}

func init() {
	capContents, err := os.ReadFile(nodeCapabilitiesFile)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		panic(err)
	}
	for _, line := range strings.Split(string(capContents), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "=")
		if len(parts) == 1 {
			cachedNodeCapabilities[parts[0]] = "true"
		}
		if len(parts) == 2 {
			cachedNodeCapabilities[parts[0]] = parts[1]
		}
	}
}

func GetNodeCapabilities(capName string) string {
	return cachedNodeCapabilities[capName]
}
