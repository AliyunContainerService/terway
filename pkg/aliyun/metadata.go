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
	metadataBase   = "http://100.100.100.200/latest/meta-data/"
	mainEniPath    = "mac"
	enisPath       = "network/interfaces/macs/"
	eniIDPath      = "network/interfaces/macs/%s/network-interface-id"
	eniAddrPath    = "network/interfaces/macs/%s/primary-ip-address"
	eniNetmaskPath = "network/interfaces/macs/%s/netmask"
	eniGatewayPath = "network/interfaces/macs/%s/gateway"
	eniPrivateIPs  = "network/interfaces/macs/%s/private-ipv4s"
	instanceIDPath = "instance-id"
	regionIDPath   = "region-id"
	zoneIDPath     = "zone-id"
	vswitchIDPath  = "vswitch-id"
	vpcIDPath      = "vpc-id"
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

// GetLocalInstanceID get instance id of this node
func GetLocalInstanceID() (string, error) {
	return metadataValue(instanceIDPath)
}

// GetLocalRegion get region id of this node
func GetLocalRegion() (common.Region, error) {
	region, err := metadataValue(regionIDPath)
	return common.Region(region), err
}

// GetLocalZone get zone of this node
func GetLocalZone() (string, error) {
	return metadataValue(zoneIDPath)
}

// GetLocalVswitch get vswitch id of this node
func GetLocalVswitch() (string, error) {
	return metadataValue(vswitchIDPath)
}

// GetLocalVPC get vpc id of this node
func GetLocalVPC() (string, error) {
	return metadataValue(vpcIDPath)
}
