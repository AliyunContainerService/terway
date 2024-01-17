//go:build !linux

package utils

func OSSupportERDMA() (bool, error) {
	return false, nil
}
func OSSupportSMCR() (bool, error) {
	return false, nil
}
