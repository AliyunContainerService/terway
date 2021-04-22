package credential

import (
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth"
)

type Credential struct {
	Credential auth.Credential
	Expiration time.Time
}

type Interface interface {
	Resolve() (*Credential, error)
	Name() string
}
