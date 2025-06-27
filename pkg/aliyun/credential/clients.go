package credential

import (
	"os"

	eflo20220530 "github.com/alibabacloud-go/eflo-20220530/v2/client"
	vpc20160428 "github.com/alibabacloud-go/vpc-20160428/v6/client"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
	credential "github.com/aliyun/credentials-go/credentials"
	"k8s.io/utils/ptr"
)

type ECSClient interface {
	GetClient() *ecs.Client
}

type VPCClient interface {
	GetClient() *vpc20160428.Client
}

type EFLOClient interface {
	GetClient() *eflo.Client
}

type EFLOV2Client interface {
	GetClient() *eflo20220530.Client
}

type ClientConfig struct {
	RegionID     string
	Scheme       string
	EndpointType string
	NetworkType  string
	Domain       string
}

type ecsClientImpl struct {
	config ClientConfig
	client *ecs.Client
}

type vpcClientImpl struct {
	config ClientConfig
	client *vpc20160428.Client
}

type efloClientImpl struct {
	config ClientConfig
	client *eflo.Client
}

type efloV2ClientImpl struct {
	config ClientConfig
	client *eflo20220530.Client
}

func NewECSClient(config ClientConfig, credential auth.Credential) (ECSClient, error) {
	domain, err := parseURL(os.Getenv("ECS_ENDPOINT"))
	if err != nil {
		return nil, err
	}
	if domain != "" {
		config.Domain = domain
	}

	sdkConfig := provideSDKConfig(config)

	client, err := ecs.NewClientWithOptions(config.RegionID, sdkConfig, credential)
	if err != nil {
		return nil, err
	}
	client.SetEndpointRules(client.EndpointMap, config.EndpointType, config.NetworkType)
	if config.Domain != "" {
		client.Domain = config.Domain
	}

	return &ecsClientImpl{
		config: config,
		client: client,
	}, nil
}

func NewVPCClient(config ClientConfig, credential credential.Credential) (VPCClient, error) {
	domain, err := parseURL(os.Getenv("VPC_ENDPOINT"))
	if err != nil {
		return nil, err
	}
	if domain != "" {
		config.Domain = domain
	}

	sdkConfig := provideSDKV2Config(config, credential)

	client, err := vpc20160428.NewClient(sdkConfig)
	if err != nil {
		return nil, err
	}
	var endpoint *string
	if config.Domain != "" {
		endpoint = &config.Domain
	}
	ep, err := client.GetEndpoint(ptr.To("vpc"), &config.RegionID, &config.EndpointType, &config.NetworkType, nil, nil, endpoint)
	if err != nil {
		return nil, err
	}
	client.Endpoint = ep

	return &vpcClientImpl{
		config: config,
		client: client,
	}, nil
}

func NewEFLOClient(config ClientConfig, credential auth.Credential) (EFLOClient, error) {
	domain, err := parseURL(os.Getenv("EFLO_ENDPOINT"))
	if err != nil {
		return nil, err
	}
	if domain != "" {
		config.Domain = domain
	}

	regionID := os.Getenv("EFLO_REGION_ID")
	if regionID != "" {
		config.RegionID = regionID
	}
	sdkConfig := provideSDKConfig(config)
	client, err := eflo.NewClientWithOptions(config.RegionID, sdkConfig, credential)
	if err != nil {
		return nil, err
	}
	client.SetEndpointRules(client.EndpointMap, config.EndpointType, config.NetworkType)
	if config.Domain != "" {
		client.Domain = config.Domain
	}

	return &efloClientImpl{
		config: config,
		client: client,
	}, nil
}

func NewEFLOV2Client(config ClientConfig, credential credential.Credential) (EFLOV2Client, error) {
	domain, err := parseURL(os.Getenv("EFLO_ENDPOINT"))
	if err != nil {
		return nil, err
	}

	if domain != "" {
		config.Domain = domain
	}

	regionID := os.Getenv("EFLO_REGION_ID")
	if regionID != "" {
		config.RegionID = regionID
	}

	sdkConfig := provideSDKV2Config(config, credential)

	client, err := eflo20220530.NewClient(sdkConfig)
	if err != nil {
		return nil, err
	}

	var endpoint *string
	if config.Domain != "" {
		endpoint = &config.Domain
	}
	ep, err := client.GetEndpoint(ptr.To("eflo"), &config.RegionID, &config.EndpointType, &config.NetworkType, nil, nil, endpoint)
	if err != nil {
		return nil, err
	}
	client.Endpoint = ep

	return &efloV2ClientImpl{
		config: config,
		client: client,
	}, nil
}

func (e *ecsClientImpl) GetClient() *ecs.Client {
	return e.client
}

func (v *vpcClientImpl) GetClient() *vpc20160428.Client {
	return v.client
}

func (e *efloClientImpl) GetClient() *eflo.Client {
	return e.client
}

func (e *efloV2ClientImpl) GetClient() *eflo20220530.Client {
	return e.client
}
