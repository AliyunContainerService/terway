package vf

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
)

const (
	defaultNUSAConfigPath = "/etc/sysconfig/pcie_topo"
	defaultSysfsBasePath  = "/sys/bus/pci/devices"

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

func SetupDriver(ctx context.Context, bdfID string) error {
	driverPath := vfBind // Use the predefined driver path

	// Construct the full device path, e.g., /sys/bus/pci/drivers/virtio-pci/0000:01:10.0
	devicePath := filepath.Join(driverPath, bdfID)

	// Check if the device is already bound to the driver
	if _, err := os.Stat(devicePath); err == nil {
		// Device already exists, return nil
		return nil
	}

	// Check PF autoprobe config(sriov_drivers_autoprobe), if not, enable VF driver_override
	pfBDF := getPFBDF(bdfID)
	if pfBDF != "" {
		autoprobeEnabled, err := checkSRIOVAutoprobe(pfBDF)
		if err != nil {
			// If we can't check autoprobe, continue with driver_override as fallback
			logr.FromContextOrDiscard(ctx).Info("Warning: failed to check sriov_drivers_autoprobe for PF", "pfBDF", pfBDF, "err", err)
		} else if !autoprobeEnabled {
			// Set driver_override for the VF to ensure it binds to virtio-pci
			if err := setDriverOverride(bdfID, "virtio-pci"); err != nil {
				return fmt.Errorf("failed to set driver_override for VF %s: %v", bdfID, err)
			}
		}
	}

	// If the device is not bound, try to bind it
	bindFile := filepath.Join(driverPath, "bind")
	if err := os.WriteFile(bindFile, []byte(bdfID), 0644); err != nil {
		return fmt.Errorf("failed to bind device %s: %v", bdfID, err)
	}

	return nil
}

// getPFBDF extracts the PF BDF from VF BDF by reading the physfn symlink
func getPFBDF(vfBDF string) string {
	return getPFBDFWithBasePath(vfBDF, defaultSysfsBasePath)
}

// getPFBDFWithBasePath extracts the PF BDF from VF BDF by reading the physfn symlink with configurable base path
func getPFBDFWithBasePath(vfBDF, basePath string) string {
	physfnPath := filepath.Join(basePath, vfBDF, "physfn")

	// Read the physfn symlink to get the PF device path
	physfnLink, err := os.Readlink(physfnPath)
	if err != nil {
		// If physfn doesn't exist, this might not be a VF or there's an error
		return ""
	}

	// The symlink typically points to "../0000:01:10.0" format
	// Extract the BDF from the path
	pfBDF := filepath.Base(physfnLink)
	return pfBDF
}

// checkSRIOVAutoprobe checks if sriov_drivers_autoprobe is enabled for the PF
func checkSRIOVAutoprobe(pfBDF string) (bool, error) {
	return checkSRIOVAutoprobeWithBasePath(pfBDF, defaultSysfsBasePath)
}

// checkSRIOVAutoprobeWithBasePath checks if sriov_drivers_autoprobe is enabled for the PF with configurable base path
func checkSRIOVAutoprobeWithBasePath(pfBDF, basePath string) (bool, error) {
	autoprobePath := filepath.Join(basePath, pfBDF, "sriov_drivers_autoprobe")

	data, err := os.ReadFile(autoprobePath)
	if err != nil {
		return false, err
	}

	value := strings.TrimSpace(string(data))
	return value == "1", nil
}

// setDriverOverride sets the driver_override for a VF device
func setDriverOverride(vfBDF, driverName string) error {
	return setDriverOverrideWithBasePath(vfBDF, driverName, defaultSysfsBasePath)
}

// setDriverOverrideWithBasePath sets the driver_override for a VF device with configurable base path
func setDriverOverrideWithBasePath(vfBDF, driverName, basePath string) error {
	overridePath := filepath.Join(basePath, vfBDF, "driver_override")

	if err := os.WriteFile(overridePath, []byte(driverName), 0644); err != nil {
		return err
	}

	return nil
}

func SetupDriverAndGetNetInterface(ctx context.Context, vfID int, configPath string) (int, error) {
	// Step 1: get bdf id
	bdfID, err := GetBDFbyVFID(configPath, vfID)
	if err != nil {
		return 0, fmt.Errorf("failed to get BDF by VFID %d: %v", vfID, err)
	}

	// Step 2: set driver to virtio
	if err := SetupDriver(ctx, bdfID); err != nil {
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
