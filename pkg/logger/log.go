package logger

import (
	"github.com/sirupsen/logrus"
)

// DefaultLogger default log
var DefaultLogger = NewDefaultLogger()

func NewDefaultLogger() *logrus.Logger {
	l := logrus.New()
	l.Formatter = &logrus.TextFormatter{
		DisableTimestamp: false,
		DisableColors:    true,
		DisableQuote:     true,
	}
	l.SetLevel(logrus.InfoLevel)
	return l
}

func SetLevel(level string) error {
	l, err := logrus.ParseLevel(level)
	if err != nil {
		return err
	}
	DefaultLogger.Infof("set log level %s", level)
	DefaultLogger.SetLevel(l)
	return nil
}
