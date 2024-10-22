package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/alexflint/go-filemutex"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2/textlogger"

	"github.com/AliyunContainerService/terway/pkg/utils"
)

const (
	fileLockTimeOut = 11 * time.Second
)

var Log = logr.Discard()
var once sync.Once

func InitLog(debug bool) logr.Logger {
	var opts []textlogger.ConfigOption
	once.Do(func() {
		if debug {
			var file, err = os.OpenFile(utils.NormalizePath("/var/log/terway.cni.log"), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
			if err != nil {
				panic(err)
			}
			opts = append(opts, textlogger.Verbosity(4), textlogger.Output(io.MultiWriter(file, os.Stderr)))

		}
		Log = textlogger.NewLogger(textlogger.NewConfig(opts...))
	})

	return Log
}

// JSONStr json to str
func JSONStr(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

type Locker struct {
	m *filemutex.FileMutex
}

// Close close
func (l *Locker) Close() error {
	if l.m != nil {
		return l.m.Unlock()
	}
	return nil
}

// GrabFileLock get file lock with timeout 11seconds
func GrabFileLock(lockfilePath string) (*Locker, error) {
	var m, err = filemutex.New(utils.NormalizePath(lockfilePath))
	if err != nil {
		return nil, fmt.Errorf("failed to open lock %s: %v", lockfilePath, err)
	}

	err = wait.PollUntilContextTimeout(context.Background(), 200*time.Millisecond, fileLockTimeOut, true, func(ctx context.Context) (bool, error) {
		if err := m.Lock(); err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to acquire lock: %v", err)
	}

	return &Locker{
		m: m,
	}, nil
}
