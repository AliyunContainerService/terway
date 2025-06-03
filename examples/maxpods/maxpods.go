package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
	"github.com/AliyunContainerService/terway/pkg/aliyun/instance"
)

var (
	accessKeyID     string
	accessKeySecret string
	credentialPath  string
	region          string
	mode            string
	ipStack         string
)

func init() {
	flag.StringVar(&accessKeyID, "access-key-id", "", "AlibabaCloud Access Key ID")
	flag.StringVar(&accessKeySecret, "access-key-secret", "", "AlibabaCloud Access Key Secret")
	flag.StringVar(&credentialPath, "credential-path", "", "AlibabaCloud credential path")
	flag.StringVar(&region, "region", "", "AlibabaCloud Access Key Secret")
	flag.StringVar(&mode, "mode", "terway-eniip", "max pod cal mode: eni-ip|eni")
	flag.StringVar(&ipStack, "ip-stack", "ipv4", "ip stack")
}

func main() {
	flag.Parse()
	log.SetOutput(io.Discard)
	regionID, err := instance.GetInstanceMeta().GetRegionID()
	if err != nil {
		panic(err)
	}
	instanceType, err := instance.GetInstanceMeta().GetInstanceType()
	if err != nil {
		panic(err)
	}

	prov := provider.NewChainProvider(
		provider.NewAccessKeyProvider(accessKeyID, accessKeySecret),
		provider.NewEncryptedFileProvider(provider.EncryptedFileProviderOptions{
			FilePath:      credentialPath,
			RefreshPeriod: 30 * time.Minute,
		}),
		provider.NewECSMetadataProvider(provider.ECSMetadataProviderOptions{}),
	)

	c, err := credential.InitializeClientMgr(regionID, prov)
	if err != nil {
		panic(err)
	}

	api := client.NewAPIFacade(c, nil)

	if mode == "terway-eniip" {
		limit, err := client.LimitProviders["ecs"].GetLimit(api.GetECS(), instanceType)
		if err != nil {
			panic(err)
		}
		fmt.Println(limit.IPv4PerAdapter * (limit.Adapters - 1))
	} else if mode == "terway-eni" {
		limit, err := client.LimitProviders["ecs"].GetLimit(api.GetECS(), instanceType)
		if err != nil {
			panic(err)
		}
		fmt.Println(limit.Adapters - 1)
	}
}
