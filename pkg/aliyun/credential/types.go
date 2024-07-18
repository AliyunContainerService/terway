package credential

import (
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth"
	ctrl "sigs.k8s.io/controller-runtime"
)

var log = ctrl.Log.WithName("credential")

type Credential struct {
	Credential auth.Credential
	Expiration time.Time
}

type Interface interface {
	Resolve() (*Credential, error)
	Name() string
}
