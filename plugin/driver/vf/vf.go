package vf

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultNUSAConfigPath = "/etc/sysconfig/pcie_topo"

	vfBind = "/sys/bus/pci/drivers/virtio-pci"
)

type Config struct {
	PfID int    `json:"pf_id"`
	VfID int    `json:"vf_id"`
	BDF  string `json:"bdf"`
}
type Configs struct {
	EniVFs []*Config `json:"eniVFs"`
}

func parse(config []byte) (*Configs, error) {
	var configs Configs
	err := json.Unmarshal(config, &configs)
	if err != nil {
		return nil, err
	}
	return &configs, nil
}

func GetBDFbyVFID(path string, vfID int) (string, error) {
	if path == "" {
		path = defaultNUSAConfigPath
	}
	configContent, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	config, err := parse(configContent)
	if err != nil {
		return "", err
	}
	for _, vf := range config.EniVFs {
		if vf.VfID == vfID {
			return vf.BDF, nil
		}
	}
	return "", fmt.Errorf("not found specified vfID %d", vfID)
}

func SetupDriver(bdfID string) error {
	driverPath := vfBind // Use the predefined driver path

	// Construct the full device path, e.g., /sys/bus/pci/drivers/virtio-pci/0000:01:10.0
	devicePath := filepath.Join(driverPath, bdfID)

	// Check if the device is already bound to the driver
	if _, err := os.Stat(devicePath); err == nil {
		// Device already exists, return nil
		return nil
	}

	// If the device is not bound, try to bind it
	bindFile := filepath.Join(driverPath, "bind")
	if err := os.WriteFile(bindFile, []byte(bdfID), 0644); err != nil {
		return fmt.Errorf("failed to bind device %s: %v", bdfID, err)
	}

	return nil
}

func SetupDriverAndGetNetInterface(vfID int, configPath string) (int, error) {
	// Step 1: get bdf id
	bdfID, err := GetBDFbyVFID(configPath, vfID)
	if err != nil {
		return 0, fmt.Errorf("failed to get BDF by VFID %d: %v", vfID, err)
	}

	// Step 2: set driver to virtio
	if err := SetupDriver(bdfID); err != nil {
		return 0, fmt.Errorf("failed to setup driver for BDF %s: %v", bdfID, err)
	}

	// Step 3: get the network interface index
	netDir := fmt.Sprintf("/sys/bus/pci/drivers/virtio-pci/%s/virtio*/net/*/ifindex", bdfID)
	matches, _ := filepath.Glob(netDir)

	if len(matches) == 0 {
		return 0, fmt.Errorf("no network interface found for BDF %s", bdfID)
	}

	out, err := os.ReadFile(matches[0])
	if err != nil {
		return 0, err
	}

	return strconv.Atoi(strings.TrimSpace(string(out)))
}
