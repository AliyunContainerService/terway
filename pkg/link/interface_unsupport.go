//+build !linux

package link

// GetDeviceNumber get interface device number by mac address
func GetDeviceNumber(mac string) (int32, error) {
	return 0, ErrUnsupported
}

// GetDeviceName get interface device name by mac address
func GetDeviceName(mac string) (string, error) {
	return "", ErrUnsupported
}
