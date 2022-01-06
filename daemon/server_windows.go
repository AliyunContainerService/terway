package daemon

import (
	"os"
	"syscall"
)

var stackTriggerSignals = []os.Signal{
	syscall.SIGINT,
}

var syscallUmask = func(int) int {
	// nothing to do
	return 0
}
