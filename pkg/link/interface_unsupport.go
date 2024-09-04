//go:build !linux && !windows
// +build !linux,!windows

package link

// GetDeviceNumber get interface device number by mac address
func GetDeviceNumber(mac string) (int32, error) {
	return 0, ErrUnsupported
}
