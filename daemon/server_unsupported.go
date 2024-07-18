//go:build !linux && !windows

package daemon

import (
	"os"
)

var stackTriggerSignals = []os.Signal{}

var syscallUmask = func(int) int {
	// nothing to do
	return 0
}
