package aliyun

import (
	"fmt"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/denverdino/aliyungo/common"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

const (
	metadataBase     = "http://100.100.100.200/latest/meta-data/"
	mainEniPath      = "mac"
	enisPath         = "network/interfaces/macs/"
	eniIDPath        = "network/interfaces/macs/%s/network-interface-id"
	eniAddrPath      = "network/interfaces/macs/%s/primary-ip-address"
	eniNetmaskPath   = "network/interfaces/macs/%s/netmask"
	eniGatewayPath   = "network/interfaces/macs/%s/gateway"
	eniPrivateIPs    = "network/interfaces/macs/%s/private-ipv4s"
	instanceTypePath = "instance/instance-type"
	instanceIdPath   = "instance-id"
	regionIdPath     = "region-id"
	zoneIdPath       = "zone-id"
	vswitchIdPath    = "vswitch-id"
	vpcIdPath        = "vpc-id"
)

func metadataValue(url string) (string, error) {
	if !strings.HasPrefix(url, metadataBase) {
		url = metadataBase + url
	}
	var (
		start = time.Now()
		err   error
	)
	defer func() {
		metric.MetadataLatency.WithLabelValues(url, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	}()
	resp, err := http.DefaultClient.Get(url)
	if err != nil {
		return "", err
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("error get url: %s from metaserver, code: %v", url, resp.StatusCode)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	result := strings.Split(string(body), "\n")
	trimResult := strings.Trim(result[0], "/")
	return trimResult, nil
}

func metadataArray(url string) ([]string, error) {
	if !strings.HasPrefix(url, metadataBase) {
		url = metadataBase + url
	}
	var (
		start = time.Now()
		err   error
	)
	defer func() {
		metric.MetadataLatency.WithLabelValues(url, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	}()

	resp, err := http.DefaultClient.Get(url)
	if err != nil {
		return []string{}, err
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return []string{}, fmt.Errorf("error get url: %s from metaserver, code: %v", url, resp.StatusCode)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return []string{}, err
	}
	result := strings.Split(string(body), "\n")
	for i, str := range result {
		result[i] = strings.Trim(str, "/")
	}
	return result, nil
}

func GetLocalInstanceId() (string, error) {
	return metadataValue(instanceIdPath)
}

func GetLocalRegion() (common.Region, error) {
	region, err := metadataValue(regionIdPath)
	return common.Region(region), err
}

func GetLocalZone() (string, error) {
	return metadataValue(zoneIdPath)
}

func GetLocalVswitch() (string, error) {
	return metadataValue(vswitchIdPath)
}

func GetLocalVPC() (string, error) {
	return metadataValue(vpcIdPath)
}