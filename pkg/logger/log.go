package logger

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
)

type Format struct{}

func (mf *Format) Format(entry *logrus.Entry) ([]byte, error) {
	var b *bytes.Buffer
	if entry.Buffer != nil {
		b = entry.Buffer
	} else {
		b = &bytes.Buffer{}
	}
	var fileName = ""
	if entry.HasCaller() {
		fileName = fmt.Sprintf("%s:%d", path.Base(entry.Caller.File), entry.Caller.Line)
	}

	b.WriteString(fmt.Sprintf("%s%s %s %s]", strings.ToUpper(entry.Level.String()[:1]), entry.Time.Format("0102"), entry.Time.Format("15:04:05.999999"), fileName))
	if r := kv(entry.Data); len(r) > 0 {
		b.WriteString(" ")
		b.WriteString(r)
	}
	b.WriteString(" ")
	b.WriteString(entry.Message)
	b.WriteString("\n")
	return b.Bytes(), nil
}

func kv(data logrus.Fields) string {
	if len(data) == 0 {
		return ""
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	result := make([]string, 0, len(data))
	for _, key := range keys {
		result = append(result, fmt.Sprintf("%s=%v", key, data[key]))
	}
	return strings.Join(result, " ")
}

// DefaultLogger default log
var DefaultLogger = NewDefaultLogger()

func NewDefaultLogger() *logrus.Logger {
	l := logrus.New()
	l.SetReportCaller(true)
	l.SetLevel(logrus.InfoLevel)
	l.SetFormatter(&Format{})
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
