//go:build linux

package utils

import (
	"github.com/pmorjan/kmod"
)

func OSSupportERDMA() (bool, error) {
	return queryOSModule("erdma")
}
func OSSupportSMCR() (bool, error) {
	return queryOSModule("smc")
}
func queryOSModule(moduleName string) (bool, error) {
	modprobe, err := kmod.New()
	if err != nil {
		return false, err
	}
	err = modprobe.Load(moduleName, "", 0)
	if err != nil {
		if err == kmod.ErrModuleNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
