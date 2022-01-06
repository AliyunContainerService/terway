package daemon

import (
	"os"
	"syscall"
)

var stackTriggerSignals = []os.Signal{
	syscall.SIGUSR1,
}

var syscallUmask = syscall.Umask
