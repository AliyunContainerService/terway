package metadata

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun/errors"
	"github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/metric"
)

// Reference https://help.aliyun.com/knowledge_detail/49122.html
const (
	metadataBase     = "http://100.100.100.200/latest/meta-data/"
	mainEniPath      = "mac"
	enisPath         = "network/interfaces/macs/"
	eniIDPath        = "network/interfaces/macs/%s/network-interface-id"
	eniAddrPath      = "network/interfaces/macs/%s/primary-ip-address"
	eniNetmaskPath   = "network/interfaces/macs/%s/netmask"
	eniGatewayPath   = "network/interfaces/macs/%s/gateway"
	eniPrivateIPs    = "network/interfaces/macs/%s/private-ipv4s"
	eniVSwitchPath   = "network/interfaces/macs/%s/vswitch-id"
	instanceIDPath   = "instance-id"
	instanceTypePath = "instance/instance-type"
	regionIDPath     = "region-id"
	zoneIDPath       = "zone-id"
	vswitchIDPath    = "vswitch-id"
	vpcIDPath        = "vpc-id"
	privateIPV4Path  = "private-ipv4"
)

func getValue(url string) (string, error) {
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
	defer resp.Body.Close()

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

func getArray(url string) ([]string, error) {
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
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.ErrNotFound
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
	return getValue(instanceIDPath)
}

// GetInstanceType get instance type of this node
func GetInstanceType() (string, error) {
	return getValue(instanceTypePath)
}

// GetLocalRegion get region id of this node
func GetLocalRegion() (string, error) {
	region, err := getValue(regionIDPath)
	return region, err
}

// GetLocalZone get zone of this node
func GetLocalZone() (string, error) {
	return getValue(zoneIDPath)
}

// GetLocalVswitch get vswitch id of this node
func GetLocalVswitch() (string, error) {
	return getValue(vswitchIDPath)
}

// GetLocalVPC get vpc id of this node
func GetLocalVPC() (string, error) {
	return getValue(vpcIDPath)
}

// GetPrivateIPV4 get private ip for master nic
func GetPrivateIPV4() (net.IP, error) {
	value, err := getValue(privateIPV4Path)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(value)
	if ip == nil {
		return nil, fmt.Errorf("invalid ip format: %s", value)
	}
	return ip, nil
}

// GetENIID by mac
func GetENIID(mac string) (string, error) {
	return getValue(fmt.Sprintf(metadataBase+eniIDPath, mac))
}

// GetENIPrimaryIP by mac
func GetENIPrimaryIP(mac string) (net.IP, error) {
	addr, err := getValue(fmt.Sprintf(metadataBase+eniAddrPath, mac))
	if err != nil {
		return nil, err
	}
	return ip.ToIP(addr)
}

// GetENIPrivateIPs by mac
func GetENIPrivateIPs(mac string) ([]net.IP, error) {
	addressStrList := &[]string{}
	ipsStr, err := getValue(fmt.Sprintf(metadataBase+eniPrivateIPs, mac))
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(ipsStr), addressStrList)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ip, %s, %w", ipsStr, err)
	}
	var ips []net.IP
	for _, ipStr := range *addressStrList {
		i, err := ip.ToIP(ipStr)
		if err != nil {
			return nil, err
		}
		ips = append(ips, i)
	}
	return ips, nil
}

// GetENINetMask by mac
func GetENINetMask(mac string) (string, error) {
	return getValue(fmt.Sprintf(metadataBase+eniNetmaskPath, mac))
}

// GetENIGateway return gateway ip by mac
func GetENIGateway(mac string) (net.IP, error) {
	addr, err := getValue(fmt.Sprintf(metadataBase+eniGatewayPath, mac))
	if err != nil {
		return nil, err
	}
	return ip.ToIP(addr)
}

// GetENIVSwitch by mac
func GetENIVSwitch(mac string) (string, error) {
	return getValue(fmt.Sprintf(metadataBase+eniVSwitchPath, mac))
}

// GetENIsMAC get attached ENIs
func GetENIsMAC() ([]string, error) {
	return getArray(metadataBase + enisPath)
}

// GetPrimaryENIMAC get the main ENI's mac
func GetPrimaryENIMAC() (string, error) {
	return getValue(metadataBase + mainEniPath)
}
