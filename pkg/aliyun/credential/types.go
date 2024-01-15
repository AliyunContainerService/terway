package credential

import (
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth"

	"github.com/AliyunContainerService/terway/pkg/logger"
)

var log = logger.DefaultLogger.WithField("subSys", "credential")

type Credential struct {
	Credential auth.Credential
	Expiration time.Time
}

type Interface interface {
	Resolve() (*Credential, error)
	Name() string
}
