package main

import (
	"flag"
	"fmt"
	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/denverdino/aliyungo/common"
	"io/ioutil"
	"log"
	"os"
)

func debug(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
}

var (
	accessKeyID     string
	accessKeySecret string
	region          string
	mode            string
)

func init() {
	flag.StringVar(&accessKeyID, "access-key-id", "", "AlibabaCloud Access Key ID")
	flag.StringVar(&accessKeySecret, "access-key-secret", "", "AlibabaCloud Access Key Secret")
	flag.StringVar(&region, "region", "", "AlibabaCloud Access Key Secret")
	flag.StringVar(&mode, "mode", "terway-eniip", "max pod cal mode: eni-ip|eni")
}

func main() {
	flag.Parse()
	log.SetOutput(ioutil.Discard)
	ecs, err := aliyun.NewECS(accessKeyID, accessKeySecret, common.Region(region))
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
