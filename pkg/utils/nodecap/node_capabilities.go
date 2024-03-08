package nodecap

import (
	"os"
	"path/filepath"

	"gopkg.in/ini.v1"
)

const (
	nodeCapabilitiesFile = "/var/run/eni/node_capabilities"
	NodeCapabilityERDMA  = "erdma"
)

var cachedNodeCapabilities = map[string]string{}

func init() {
	file, err := ini.Load(nodeCapabilitiesFile)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		panic(err)
	}
	for _, key := range file.Section("").Keys() {
		cachedNodeCapabilities[key.Name()] = key.Value()
	}
}

// SetNodeCapabilities set node capability , test purpose
func SetNodeCapabilities(capName, val string) {
	cachedNodeCapabilities[capName] = val
}

func GetNodeCapabilities(capName string) string {
	return cachedNodeCapabilities[capName]
}

func WriteNodeCapabilities(capName, value string) error {
	err := os.MkdirAll(filepath.Dir(nodeCapabilitiesFile), 0700)
	if err != nil {
		return err
	}

	file, err := ini.Load(nodeCapabilitiesFile)
	if err != nil {
		if os.IsNotExist(err) {
			file = ini.Empty()
		} else {
			return err
		}
	}
	file.Section("").Key(capName).SetValue(value)

	err = file.SaveTo(nodeCapabilitiesFile)
	return err
}
