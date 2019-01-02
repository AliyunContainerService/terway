package main

import (
	"fmt"
	"os"
	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"flag"
	"github.com/denverdino/aliyungo/common"
)

func debug(format string, args ... interface{}) {
	fmt.Fprintf(os.Stderr, format, args)
}

var (
	accessKeyId     string
	accessKeySecret string
	region          string
)

func init() {
	flag.StringVar(&accessKeyId, "access-key-id", "", "AlibabaCloud Access Key ID")
	flag.StringVar(&accessKeySecret, "access-key-secret", "", "AlibabaCloud Access Key Secret")
	flag.StringVar(&region, "region", "", "AlibabaCloud Access Key Secret")
}

func main() {
	flag.Parse()
	ecs, err := aliyun.NewECS(accessKeyId, accessKeySecret, common.Region(region))
	if err != nil {
		panic(err)
	}

	instanceID, err := aliyun.GetLocalInstanceId()
	if err != nil {
		panic(err)
	}

	maxPrivateIP, err := ecs.GetInstanceMaxPrivateIP(instanceID)
	if err != nil {
		panic(err)
	}

	fmt.Println(maxPrivateIP)
}
