package utils

import (
	"os"
	"path/filepath"
	"strings"
)

var windowsSystemDrive = mustGetWindowsSystemDrive()

func mustGetWindowsSystemDrive() string {
	if !IsWindowsOS() {
		return ""
	}
	var systemDrive = os.Getenv("SYSTEMDRIVE")
	if systemDrive == "" {
		systemDrive = filepath.VolumeName(os.Getenv("SYSTEMROOT"))
	}
	if systemDrive == "" {
		panic("unable to get windows system driver")
	}
	return systemDrive
}

// NormalizePath returns the normal path in heterogeneous platform.
func NormalizePath(path string) string {
	if IsWindowsOS() {
		// parses the root path with windows system driver.
		if strings.HasPrefix(path, "/") {
			path = filepath.FromSlash(path)
			return windowsSystemDrive + path
		}
	}
	return path
}
