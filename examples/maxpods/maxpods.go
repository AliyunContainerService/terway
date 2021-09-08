package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"

	"github.com/AliyunContainerService/terway/types"
	"github.com/sirupsen/logrus"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
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
	log.SetOutput(ioutil.Discard)
	logrus.SetOutput(ioutil.Discard)
	ins := aliyun.GetInstanceMeta()
	api, err := aliyun.NewAliyun(accessKeyID, accessKeySecret, ins.RegionID, credentialPath)
	if err != nil {
		panic(err)
	}

	f := &types.IPFamily{}
	if ipStack == "ipv4" || ipStack == "dual" {
		f.IPv4 = true
	}
	if ipStack == "dual" || ipStack == "ipv6" {
		f.IPv6 = true
	}

	err = aliyun.UpdateFromAPI(api.ClientSet.ECS(), aliyun.GetInstanceMeta().InstanceType)
	if err != nil {
		panic(err)
	}
	if mode == "terway-eniip" {
		limit, ok := aliyun.GetLimit(aliyun.GetInstanceMeta().InstanceType)
		if !ok {
			panic(err)
		}
		fmt.Println(limit.IPv4PerAdapter * (limit.Adapters - 1))
	} else if mode == "terway-eni" {
		limit, ok := aliyun.GetLimit(aliyun.GetInstanceMeta().InstanceType)
		if !ok {
			panic(err)
		}
		fmt.Println(limit.Adapters - 1)
	}
}
