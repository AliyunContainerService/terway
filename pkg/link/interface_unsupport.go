//+build !linux

package link

import (
	"github.com/pkg/errors"
)

func GetDeviceNumber(mac string) (int32, error) {
	return 0, errors.Errorf("not supported arch")
}

func GetDeviceName(mac string) (string, error) {
	return "", errors.Errorf("not supported arch")
}
