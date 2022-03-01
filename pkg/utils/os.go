package utils

import "runtime"

func IsWindowsOS() bool {
	return runtime.GOOS == "windows"
}
