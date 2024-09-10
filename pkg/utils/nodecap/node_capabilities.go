//go:generate mockery --name NodeCapabilitiesStore

package nodecap

import (
	"os"
	"path/filepath"

	"gopkg.in/ini.v1"
)

const (
	nodeCapabilitiesFile       = "/var/run/eni/node_capabilities"
	NodeCapabilityERDMA        = "erdma"
	NodeCapabilityExclusiveENI = "cni_exclusive_eni"
	NodeCapabilityIPv6         = "cni_ipv6_stack"
)

// NodeCapabilitiesStore defines an interface for node capabilities operations
type NodeCapabilitiesStore interface {
	Load() error
	Save() error
	Set(capName, value string)
	Get(capName string) string
}

// FileNodeCapabilities is a concrete implementation of NodeCapabilitiesStore
type FileNodeCapabilities struct {
	filePath     string
	capabilities map[string]string
}

// NewFileNodeCapabilities creates a new FileNodeCapabilities instance
func NewFileNodeCapabilities(filePath string) *FileNodeCapabilities {
	return &FileNodeCapabilities{
		filePath:     filePath,
		capabilities: make(map[string]string),
	}
}

// Load loads capabilities from the INI file
func (store *FileNodeCapabilities) Load() error {
	file, err := ini.Load(store.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	store.capabilities = make(map[string]string)
	for _, key := range file.Section("").Keys() {
		store.capabilities[key.Name()] = key.Value()
	}
	return nil
}

// Save saves the capabilities to the INI file
func (store *FileNodeCapabilities) Save() error {
	err := os.MkdirAll(filepath.Dir(store.filePath), 0700)
	if err != nil {
		return err
	}
	file, err := ini.Load(store.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			file = ini.Empty()
		} else {
			return err
		}
	}

	for _, key := range file.Section("").Keys() {
		file.Section("").DeleteKey(key.Name())
	}

	for key, value := range store.capabilities {
		file.Section("").Key(key).SetValue(value)
	}
	return file.SaveTo(store.filePath)
}

// Set sets a node capability
func (store *FileNodeCapabilities) Set(capName, value string) {
	store.capabilities[capName] = value
}

// Get retrieves a node capability
func (store *FileNodeCapabilities) Get(capName string) string {
	return store.capabilities[capName]
}

// Global instance for convenient access
var capabilitiesStore NodeCapabilitiesStore = NewFileNodeCapabilities(nodeCapabilitiesFile)

// Init initializes the global capabilities store
func init() {
	if err := capabilitiesStore.Load(); err != nil {
		panic(err)
	}
}

// SetNodeCapabilities for unit test purpose
func SetNodeCapabilities(capName, val string) {
	capabilitiesStore.Set(capName, val)
}

// GetNodeCapabilities retrieves a capability
func GetNodeCapabilities(capName string) string {
	return capabilitiesStore.Get(capName)
}

// WriteNodeCapabilities writes a capability to the file
func WriteNodeCapabilities(capName, value string) error {
	capabilitiesStore.Set(capName, value)
	return capabilitiesStore.Save()
}
