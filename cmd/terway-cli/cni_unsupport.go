//go:build !linux

package main

func switchDataPathV2() bool {
	return true
}

func checkKernelVersion(k, major, minor int) bool {
	return false
}

func allowEBPFNetworkPolicy(enable bool) (bool, error) {
	return enable, nil
}

func isOldNode() (bool, error) {
	return false, nil
}
