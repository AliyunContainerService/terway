package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/alexflint/go-filemutex"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/AliyunContainerService/terway/pkg/utils"
)

const (
	fileLockTimeOut = 11 * time.Second
)

// Log for default log
var Log = DefaultLogger.WithField("subSys", "terway-cni")

// Hook for log
var Hook = &PodInfoHook{ExtraInfo: make(map[string]string)}
var DefaultLogger = func() *logrus.Logger {
	l := logger.NewDefaultLogger()
	l.AddHook(Hook)
	return l
}()

type PodInfoHook struct {
	ExtraInfo map[string]string
}

func (p *PodInfoHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (p *PodInfoHook) Fire(e *logrus.Entry) error {
	for k, v := range p.ExtraInfo {
		e.Data[k] = v
	}
	return nil
}

func (p *PodInfoHook) AddExtraInfo(k, v string) {
	p.ExtraInfo[k] = v
}

func (p *PodInfoHook) AddExtraInfos(e map[string]string) {
	for k, v := range e {
		p.ExtraInfo[k] = v
	}
}

func SetLogDebug() {
	DefaultLogger.SetLevel(logrus.DebugLevel)

	var file, err = os.OpenFile(utils.NormalizePath("/var/log/terway.cni.log"), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		panic(err)
	}
	DefaultLogger.SetOutput(io.MultiWriter(file, os.Stderr))
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
