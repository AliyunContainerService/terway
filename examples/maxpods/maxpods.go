package main

import (
	"flag"
	"fmt"
	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/denverdino/aliyungo/common"
	"os"
)

func debug(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
}

var (
	accessKeyID     string
	accessKeySecret string
	region          string
)

func init() {
	flag.StringVar(&accessKeyID, "access-key-id", "", "AlibabaCloud Access Key ID")
	flag.StringVar(&accessKeySecret, "access-key-secret", "", "AlibabaCloud Access Key Secret")
	flag.StringVar(&region, "region", "", "AlibabaCloud Access Key Secret")
}

func main() {
	flag.Parse()
	ecs, err := aliyun.NewECS(accessKeyID, accessKeySecret, common.Region(region))
	if err != nil {
		panic(err)
	}

	instanceID, err := aliyun.GetLocalInstanceID()
	if err != nil {
		panic(err)
	}

	maxPrivateIP, err := ecs.GetInstanceMaxPrivateIP(instanceID)
	if err != nil {
		panic(err)
	}

	fmt.Println(maxPrivateIP)
}
