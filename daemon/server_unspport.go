//go:build !linux && !windows
// +build !linux,!windows

package daemon

var stackTriggerSignals = []os.Signal{}

var syscallUmask = func(int) int {
	// nothing to do
	return 0
}
