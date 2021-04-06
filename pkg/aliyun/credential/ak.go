package credential

import (
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
)

type AKPairProvider struct {
	accessKeyID     string
	accessKeySecret string
}

func NewAKPairProvider(ak string, sk string) *AKPairProvider {
	return &AKPairProvider{accessKeyID: ak, accessKeySecret: sk}
}

func (a *AKPairProvider) Resolve() (*Credential, error) {
	if a.accessKeySecret == "" || a.accessKeyID == "" {
		return nil, nil
	}
	return &Credential{
		Credential: credentials.NewAccessKeyCredential(a.accessKeyID, a.accessKeySecret),
		Expiration: time.Now().AddDate(100, 0, 0),
	}, nil
}

func (a *AKPairProvider) Name() string {
	return "AKPairProvider"
}
