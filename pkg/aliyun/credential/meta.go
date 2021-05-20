package credential

import (
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
	"github.com/denverdino/aliyungo/metadata"
)

type MetadataProvider struct {
}

// NewMetadataProvider get ramRole from metadata
func NewMetadataProvider() *MetadataProvider {
	return &MetadataProvider{}
}

func (m *MetadataProvider) Resolve() (*Credential, error) {
	client := metadata.NewMetaData(nil)
	roleName, err := client.RoleName()
	if err != nil {
		return nil, err
	}
	token, err := client.RamRoleToken(roleName)
	if err != nil {
		return nil, err
	}
	return &Credential{
		Credential: credentials.NewStsTokenCredential(token.AccessKeyId, token.AccessKeySecret, token.SecurityToken),
		Expiration: token.Expiration,
	}, nil
}

func (m *MetadataProvider) Name() string {
	return "MetadataProvider"
}
