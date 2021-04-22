package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"

	"github.com/sirupsen/logrus"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
)

var (
	accessKeyID     string
	accessKeySecret string
	credentialPath  string
	region          string
	mode            string
)

func init() {
	flag.StringVar(&accessKeyID, "access-key-id", "", "AlibabaCloud Access Key ID")
	flag.StringVar(&accessKeySecret, "access-key-secret", "", "AlibabaCloud Access Key Secret")
	flag.StringVar(&credentialPath, "credential-path", "", "AlibabaCloud credential path")
	flag.StringVar(&region, "region", "", "AlibabaCloud Access Key Secret")
	flag.StringVar(&mode, "mode", "terway-eniip", "max pod cal mode: eni-ip|eni")
}

func main() {
	flag.Parse()
	log.SetOutput(ioutil.Discard)
	logrus.SetOutput(ioutil.Discard)
	_, err := aliyun.NewECS(accessKeyID, accessKeySecret, credentialPath, false, aliyun.GetInstanceMeta())
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
