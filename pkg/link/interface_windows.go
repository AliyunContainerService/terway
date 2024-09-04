package link

import (
	"github.com/pkg/errors"

	"github.com/AliyunContainerService/terway/pkg/windows/iface"
)

// GetDeviceNumber get interface device number by mac address
func GetDeviceNumber(mac string) (int32, error) {
	macIface, err := iface.GetInterfaceByMAC(mac, true)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to get dev by MAC %s", mac)
	}
	return int32(macIface.Index), nil
}
