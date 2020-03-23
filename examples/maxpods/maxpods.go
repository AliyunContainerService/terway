package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"

	"github.com/sirupsen/logrus"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/denverdino/aliyungo/common"
)

var (
	accessKeyID       string
	accessKeySecret   string
	credentialPath    string
	credentialKeyPath string
	region            string
	mode              string
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
	ecs, err := aliyun.NewECS(accessKeyID, accessKeySecret, credentialPath, common.Region(region))
	if err != nil {
		panic(err)
	}

	instanceID, err := aliyun.GetLocalInstanceID()
	if err != nil {
		panic(err)
	}

	if mode == "terway-eniip" {
		maxPrivateIP, err := ecs.GetInstanceMaxPrivateIP(instanceID)
		if err != nil {
			panic(err)
		}
		fmt.Println(maxPrivateIP)
	} else if mode == "terway-eni" {
		maxPrivateIP, err := ecs.GetInstanceMaxENI(instanceID)
		if err != nil {
			panic(err)
		}
		fmt.Println(maxPrivateIP - 1)
	}
}
