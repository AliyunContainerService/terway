package metadata

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/metric"
)

// Reference https://help.aliyun.com/knowledge_detail/49122.html
const (
	metadataBase           = "http://100.100.100.200/latest/meta-data/"
	mainEniPath            = "mac"
	enisPath               = "network/interfaces/macs/"
	eniIDPath              = "network/interfaces/macs/%s/network-interface-id"
	eniAddrPath            = "network/interfaces/macs/%s/primary-ip-address"
	eniGatewayPath         = "network/interfaces/macs/%s/gateway"
	eniV6GatewayPath       = "network/interfaces/macs/%s/ipv6-gateway"
	eniPrivateIPs          = "network/interfaces/macs/%s/private-ipv4s"
	eniPrivateV6IPs        = "network/interfaces/macs/%s/ipv6s"
	eniVSwitchPath         = "network/interfaces/macs/%s/vswitch-id"
	eniVSwitchCIDRPath     = "network/interfaces/macs/%s/vswitch-cidr-block"
	eniVSwitchIPv6CIDRPath = "network/interfaces/macs/%s/vswitch-ipv6-cidr-block"
	instanceIDPath         = "instance-id"
	instanceTypePath       = "instance/instance-type"
	regionIDPath           = "region-id"
	zoneIDPath             = "zone-id"
	vswitchIDPath          = "vswitch-id"
	vpcIDPath              = "vpc-id"
	vpcCIDRPath            = "vpc-cidr-block"
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
		return "", fmt.Errorf("error get url: %s from metaserver. %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("error get url: %s from metaserver, code: %v, %w", url, resp.StatusCode, apiErr.ErrNotFound)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("error get url: %s from metaserver, code: %v", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
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
		return []string{}, fmt.Errorf("error get url: %s from metaserver. %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return []string{}, fmt.Errorf("error get url: %s from metaserver, code: %v, %w", url, resp.StatusCode, apiErr.ErrNotFound)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return []string{}, fmt.Errorf("error get url: %s from metaserver, code: %v", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
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

// GetLocalVPCCIDR get vpc cidr of this node
func GetLocalVPCCIDR() (string, error) {
	return getValue(vpcCIDRPath)
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

// GetENIPrimaryAddr by mac
func GetENIPrimaryAddr(mac string) (netip.Addr, error) {
	addr, err := getValue(fmt.Sprintf(metadataBase+eniAddrPath, mac))
	if err != nil {
		return netip.Addr{}, err
	}
	return netip.ParseAddr(addr)
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

func GetIPv4ByMac(mac string) ([]netip.Addr, error) {
	addressStrList := &[]string{}
	ipsStr, err := getValue(fmt.Sprintf(metadataBase+eniPrivateIPs, mac))
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(ipsStr), addressStrList)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ip, %s, %w", ipsStr, err)
	}

	return ip.ToIPAddrs(*addressStrList)
}

// GetENIPrivateIPv6IPs by mac return [2408::28eb]
func GetENIPrivateIPv6IPs(mac string) ([]net.IP, error) {
	ipsStr, err := getValue(fmt.Sprintf(metadataBase+eniPrivateV6IPs, mac))
	if err != nil {
		// metadata return 404 when no ipv6 is allocated
		if errors.Is(err, apiErr.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	ipsStr = strings.ReplaceAll(ipsStr, "[", "")
	ipsStr = strings.ReplaceAll(ipsStr, "]", "")
	addressStrList := strings.Split(ipsStr, ",")

	var ips []net.IP
	for _, ipStr := range addressStrList {
		i, err := ip.ToIP(strings.TrimSpace(ipStr))
		if err != nil {
			return nil, err
		}
		ips = append(ips, i)
	}
	return ips, nil
}

// GetIPv6ByMac by mac return [2408::28eb]
func GetIPv6ByMac(mac string) ([]netip.Addr, error) {
	ipsStr, err := getValue(fmt.Sprintf(metadataBase+eniPrivateV6IPs, mac))
	if err != nil {
		// metadata return 404 when no ipv6 is allocated
		if errors.Is(err, apiErr.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	ipsStr = strings.ReplaceAll(ipsStr, "[", "")
	ipsStr = strings.ReplaceAll(ipsStr, "]", "")

	var result []netip.Addr
	for _, addr := range strings.Split(ipsStr, ",") {
		i, err := netip.ParseAddr(strings.TrimSpace(addr))
		if err != nil {
			return nil, err
		}
		result = append(result, i)
	}

	return result, nil
}

// GetENIGateway return gateway ip by mac
func GetENIGateway(mac string) (net.IP, error) {
	addr, err := getValue(fmt.Sprintf(metadataBase+eniGatewayPath, mac))
	if err != nil {
		return nil, err
	}
	return ip.ToIP(addr)
}

// GetENIGatewayAddr return gateway ip by mac
func GetENIGatewayAddr(mac string) (netip.Addr, error) {
	addr, err := getValue(fmt.Sprintf(metadataBase+eniGatewayPath, mac))
	if err != nil {
		return netip.Addr{}, err
	}
	return netip.ParseAddr(addr)
}

// GetVSwitchCIDR return vSwitch cidr by mac
func GetVSwitchCIDR(mac string) (*net.IPNet, error) {
	addr, err := getValue(fmt.Sprintf(metadataBase+eniVSwitchCIDRPath, mac))
	if err != nil {
		return nil, err
	}
	_, ipNet, err := net.ParseCIDR(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cidr %s", addr)
	}
	return ipNet, nil
}

// GetVSwitchPrefix return vSwitch cidr by mac
func GetVSwitchPrefix(mac string) (netip.Prefix, error) {
	addr, err := getValue(fmt.Sprintf(metadataBase+eniVSwitchCIDRPath, mac))
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.ParsePrefix(addr)
}

// GetVSwitchIPv6CIDR return vSwitch cidr by mac
func GetVSwitchIPv6CIDR(mac string) (*net.IPNet, error) {
	addr, err := getValue(fmt.Sprintf(metadataBase+eniVSwitchIPv6CIDRPath, mac))
	if err != nil {
		return nil, err
	}
	_, ipNet, err := net.ParseCIDR(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cidr %s", addr)
	}
	return ipNet, nil
}

// GetVSwitchIPv6Prefix return vSwitch cidr by mac
func GetVSwitchIPv6Prefix(mac string) (netip.Prefix, error) {
	addr, err := getValue(fmt.Sprintf(metadataBase+eniVSwitchIPv6CIDRPath, mac))
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.ParsePrefix(addr)
}

// GetENIV6Gateway return gateway ip by mac
func GetENIV6Gateway(mac string) (net.IP, error) {
	addr, err := getValue(fmt.Sprintf(metadataBase+eniV6GatewayPath, mac))
	if err != nil {
		return nil, err
	}
	return ip.ToIP(addr)
}

// GetENIV6GatewayAddr return gateway ip by mac
func GetENIV6GatewayAddr(mac string) (netip.Addr, error) {
	addr, err := getValue(fmt.Sprintf(metadataBase+eniV6GatewayPath, mac))
	if err != nil {
		return netip.Addr{}, err
	}
	return netip.ParseAddr(addr)
}

// GetENIVSwitchID by mac
func GetENIVSwitchID(mac string) (string, error) {
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
