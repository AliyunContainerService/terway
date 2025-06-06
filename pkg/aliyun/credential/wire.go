//go:build wireinject

package credential

import (
	"os"

	"github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
	"github.com/google/wire"
)

// InitializeClientMgr init ClientMgr
func InitializeClientMgr(regionID string, credProvider provider.CredentialsProvider) (*ClientMgr, error) {
	wire.Build(
		NewScheme,
		NewNetworkType,
		NewClientConfig,
		NewECSClient,
		NewVPCClient,
		NewEFLOClient,
		NewEFLOV2Client,
		NewClientMgr,
		ProviderV2,
		ProviderV1,
	)
	return &ClientMgr{}, nil
}

func NewClientMgr(credProvider provider.CredentialsProvider,
	ecsClient ECSClient, vpcClient VPCClient, efloClient EFLOClient,
	efloV2Client EFLOV2Client) *ClientMgr {

	return &ClientMgr{

		provider:     credProvider,
		ecsClient:    ecsClient,
		vpcClient:    vpcClient,
		efloClient:   efloClient,
		efloV2Client: efloV2Client,
	}
}

func NewNetworkType() NetworkType {
	networkType := "vpc"
	if os.Getenv("ALICLOUD_ENDPOINT_TYPE") == "public" {
		networkType = "public"
	}
	return NetworkType(networkType)
}

func NewScheme() ClientScheme {
	scheme := "HTTPS"
	if os.Getenv("ALICLOUD_CLIENT_SCHEME") == "HTTP" {
		scheme = "HTTP"
	}
	return ClientScheme(scheme)
}

func NewClientConfig(regionID string, scheme ClientScheme, networkType NetworkType) ClientConfig {
	return ClientConfig{
		RegionID:     regionID,
		Scheme:       string(scheme),
		EndpointType: "regional",
		NetworkType:  string(networkType),
	}
}
