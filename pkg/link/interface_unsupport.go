//+build !linux

package link

import (
	"github.com/pkg/errors"
)

// GetDeviceNumber get interface device number by mac address
func GetDeviceNumber(mac string) (int32, error) {
	return 0, errors.Errorf("not supported arch")
}

// GetDeviceName get interface device name by mac address
func GetDeviceName(mac string) (string, error) {
	return "", errors.Errorf("not supported arch")
}
