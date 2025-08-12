package credential

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	ecs20140526 "github.com/alibabacloud-go/ecs-20140526/v7/client"
	eflo20220530 "github.com/alibabacloud-go/eflo-20220530/v2/client"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	credential "github.com/aliyun/credentials-go/credentials"
	"golang.org/x/net/context"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

type Client interface {
	ECS() *ecs.Client
	ECSV2() *ecs20140526.Client
	VPC() *vpc.Client
	EFLO() *eflo.Client
	EFLOV2() *eflo20220530.Client
}

var (
	kubernetesAlicloudIdentity = "Kubernetes.Alicloud"
)

var _ credentials.CredentialsProvider = &V1Warp{}

type V1Warp struct {
	providers provider.CredentialsProvider
}

func (a *V1Warp) GetCredentials() (*credentials.Credentials, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cr, err := a.providers.Credentials(ctx)
	if err != nil {
		return nil, err
	}
	cre := &credentials.Credentials{
		AccessKeyId:     cr.AccessKeyId,
		AccessKeySecret: cr.AccessKeySecret,
		SecurityToken:   cr.SecurityToken,
	}

	return cre, nil
}
func (a *V1Warp) GetProviderName() string {
	return "chaining"
}

type headerTransport struct {
	headers map[string]string
}

func (m *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range m.headers {
		req.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(req)
}

func provideSDKConfig(config ClientConfig) *sdk.Config {
	sdkConfig := &sdk.Config{
		Timeout:   20 * time.Second,
		Transport: http.DefaultTransport,
		UserAgent: kubernetesAlicloudIdentity,
		Scheme:    config.Scheme,
	}
	if os.Getenv("X-ACSPROXY-ASCM-CONTEXT") != "" {
		sdkConfig.Transport = &headerTransport{
			headers: map[string]string{
				"x-acsproxy-ascm-context": os.Getenv("X-ACSPROXY-ASCM-CONTEXT"),
			},
		}
	}

	return sdkConfig
}

func provideSDKV2Config(config ClientConfig, credential credential.Credential) *openapi.Config {
	klog.Infof("provideSDKV2Config %#v", config)
	return &openapi.Config{
		UserAgent:    ptr.To(kubernetesAlicloudIdentity),
		Protocol:     ptr.To(config.Scheme),
		RegionId:     ptr.To(config.RegionID),
		Network:      ptr.To(config.NetworkType),
		Credential:   credential,
		EndpointType: ptr.To(config.EndpointType),
	}
}

func ProviderV1(providers provider.CredentialsProvider) auth.Credential {
	return &V1Warp{providers: providers}
}

func ProviderV2(providers provider.CredentialsProvider) credential.Credential {
	return provider.NewCredentialForV2SDK(providers, provider.CredentialForV2SDKOptions{
		CredentialRetrievalTimeout: 10 * time.Minute,
	})
}

// ClientMgr manager of aliyun openapi clientset
type ClientMgr struct {
	provider provider.CredentialsProvider

	ecsClient    ECSClient
	ecsV2Client  ECSV2Client
	vpcClient    VPCClient
	efloClient   EFLOClient
	efloV2Client EFLOV2Client

	sync.RWMutex
}

func (c *ClientMgr) ECS() *ecs.Client {
	return c.ecsClient.GetClient()
}
func (c *ClientMgr) VPC() *vpc.Client {
	return c.vpcClient.GetClient()
}
func (c *ClientMgr) EFLO() *eflo.Client {
	return c.efloClient.GetClient()
}
func (c *ClientMgr) EFLOV2() *eflo20220530.Client {
	return c.efloV2Client.GetClient()
}

type ClientScheme string
type NetworkType string

func parseURL(str string) (string, error) {
	if str == "" {
		return "", nil
	}
	if !strings.HasPrefix(str, "http") {
		str = "http://" + str
	}
	u, err := url.Parse(str)
	if err != nil {
		return "", err
	}
	return u.Host, nil
}
