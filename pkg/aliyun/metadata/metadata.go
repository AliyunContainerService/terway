package metadata

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
	"k8s.io/apimachinery/pkg/util/cache"
	"k8s.io/apimachinery/pkg/util/wait"
)

// Reference https://help.aliyun.com/knowledge_detail/49122.html

var (
	MetadataBase = "http://100.100.100.200/latest/meta-data/"
	TokenURL     = "http://100.100.100.200/latest/api/token"
)

const (
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

	tokenTimeout = 21600
)

var tokenCache *cache.Expiring
var defaultClient *http.Client
var single singleflight.Group

type Error struct {
	URL  string
	Code string
	R    error
}

func (e *Error) Error() string {
	return fmt.Sprintf("get from metaserver failed code: %s, url: %s, err: %s", e.Code, e.URL, e.R)
}

func init() {
	tokenCache = cache.NewExpiring()
	defaultClient = &http.Client{
		Transport:     nil,
		CheckRedirect: nil,
		Jar:           nil,
		Timeout:       30 * time.Second,
	}
}

func withRetry(url string, headers [][]string) ([]byte, error) {
	var innerErr error
	var body []byte
	err := wait.ExponentialBackoff(wait.Backoff{
		Duration: 500 * time.Millisecond,
		Factor:   1.2,
		Jitter:   0.1,
		Steps:    4,
	}, func() (bool, error) {
		var (
			start = time.Now()
			err   error
		)
		defer func() {
			MetadataLatency.WithLabelValues(url, fmt.Sprint(err != nil)).Observe(MsSince(start))
		}()

		method := "GET"
		if url == TokenURL {
			method = "PUT"
		}
		req, err := http.NewRequest(method, url, nil)
		if err != nil {
			innerErr = &Error{
				URL: url,
				R:   err,
			}
			return false, nil
		}

		for _, h := range headers {
			if len(h) != 2 {
				return false, fmt.Errorf("invalid header")
			}
			req.Header.Set(h[0], h[1])
		}

		resp, err := defaultClient.Do(req)
		if err != nil {
			// retryable err
			innerErr = &Error{
				URL: url,
				R:   err,
			}
			return false, nil
		}
		defer resp.Body.Close()

		// retryable err
		if resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode >= http.StatusInternalServerError {
			innerErr = &Error{
				URL:  url,
				Code: strconv.Itoa(resp.StatusCode),
				R:    nil,
			}
			return false, nil
		}

		if resp.StatusCode >= http.StatusBadRequest {
			innerErr = &Error{
				URL:  url,
				Code: strconv.Itoa(resp.StatusCode),
				R:    nil,
			}
			return false, innerErr
		}

		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return false, err
		}
		return true, nil
	})
	if err != nil {
		if innerErr != nil {
			return nil, innerErr
		}
		return nil, err
	}
	return body, nil
}

func getWithToken(url string) ([]byte, error) {

	skipRetry := false
retry:
	var token string
	v, ok := tokenCache.Get(TokenURL)
	if !ok {
		vv, err, _ := single.Do(TokenURL, func() (interface{}, error) {
			out, err := withRetry(TokenURL, [][]string{
				{
					"X-aliyun-ecs-metadata-token-ttl-seconds", strconv.Itoa(tokenTimeout),
				},
			})
			if err != nil {
				return nil, err
			}
			return string(out), nil
		})
		if err != nil {
			return nil, err
		}

		token = vv.(string)

		tokenCache.Set(TokenURL, token, tokenTimeout*time.Second/2)
	} else {
		token = v.(string)
	}

	out, err := withRetry(url, [][]string{
		{
			"X-aliyun-ecs-metadata-token", token,
		},
	})
	if err != nil {
		var typedErr *Error
		ok := errors.As(err, &typedErr)

		if ok && !skipRetry {
			if typedErr.Code == strconv.Itoa(http.StatusUnauthorized) {
				skipRetry = true

				tokenCache.Delete(TokenURL)
				goto retry
			}
		}

		return nil, err
	}

	return out, err
}

func getValue(urlStr string) (string, error) {
	body, err := getWithToken(urlStr)
	if err != nil {
		return "", err
	}
	result := strings.Split(string(body), "\n")
	trimResult := strings.Trim(result[0], "/")
	return trimResult, nil
}

func getArray(urlStr string) ([]string, error) {
	body, err := getWithToken(urlStr)
	if err != nil {
		return nil, err
	}
	result := strings.Split(string(body), "\n")
	for i, str := range result {
		result[i] = strings.Trim(str, "/")
	}
	return result, nil
}

// GetLocalInstanceID get instance id of this node
func GetLocalInstanceID() (string, error) {
	u, _ := url.JoinPath(MetadataBase, instanceIDPath)
	return getValue(u)
}

// GetInstanceType get instance type of this node
func GetInstanceType() (string, error) {
	u, _ := url.JoinPath(MetadataBase, instanceTypePath)
	return getValue(u)
}

// GetLocalRegion get region id of this node
func GetLocalRegion() (string, error) {
	u, _ := url.JoinPath(MetadataBase, regionIDPath)
	region, err := getValue(u)
	return region, err
}

// GetLocalZone get zone of this node
func GetLocalZone() (string, error) {
	u, _ := url.JoinPath(MetadataBase, zoneIDPath)
	return getValue(u)
}

// GetLocalVswitch get vswitch id of this node
func GetLocalVswitch() (string, error) {
	u, _ := url.JoinPath(MetadataBase, vswitchIDPath)
	return getValue(u)
}

// GetLocalVPC get vpc id of this node
func GetLocalVPC() (string, error) {
	u, _ := url.JoinPath(MetadataBase, vpcIDPath)
	return getValue(u)
}

// GetLocalVPCCIDR get vpc cidr of this node
func GetLocalVPCCIDR() (string, error) {
	u, _ := url.JoinPath(MetadataBase, vpcCIDRPath)
	return getValue(u)
}

// GetENIID by mac
func GetENIID(mac string) (string, error) {
	p := fmt.Sprintf(eniIDPath, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	return getValue(u)
}

// GetENIPrimaryIP by mac
func GetENIPrimaryIP(mac string) (net.IP, error) {
	p := fmt.Sprintf(eniAddrPath, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	addr, err := getValue(u)
	if err != nil {
		return nil, err
	}

	i := net.ParseIP(addr)
	if i == nil {
		return nil, fmt.Errorf("failed to parse ip %s", addr)
	}
	return i, nil
}

// GetENIPrimaryAddr by mac
func GetENIPrimaryAddr(mac string) (netip.Addr, error) {
	p := fmt.Sprintf(eniAddrPath, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	addr, err := getValue(u)
	if err != nil {
		return netip.Addr{}, err
	}
	return netip.ParseAddr(addr)
}

// GetENIPrivateIPs by mac
func GetENIPrivateIPs(mac string) ([]net.IP, error) {
	p := fmt.Sprintf(eniPrivateIPs, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	addressStrList := &[]string{}
	ipsStr, err := getValue(u)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(ipsStr), addressStrList)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ip, %s, %w", ipsStr, err)
	}
	var ips []net.IP
	for _, ipStr := range *addressStrList {
		i := net.ParseIP(ipStr)
		if i == nil {
			return nil, fmt.Errorf("failed to parse ip %s", ipStr)
		}
		ips = append(ips, i)
	}
	return ips, nil
}

func GetIPv4ByMac(mac string) ([]netip.Addr, error) {
	p := fmt.Sprintf(eniPrivateIPs, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	addressStrList := &[]string{}
	ipsStr, err := getValue(u)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(ipsStr), addressStrList)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ip, %s, %w", ipsStr, err)
	}

	var result []netip.Addr
	for _, addr := range *addressStrList {
		i, err := netip.ParseAddr(addr)
		if err != nil {
			return nil, err
		}
		result = append(result, i)
	}

	return result, nil
}

// GetENIPrivateIPv6IPs by mac return [2408::28eb]
func GetENIPrivateIPv6IPs(mac string) ([]net.IP, error) {
	p := fmt.Sprintf(eniPrivateV6IPs, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	ipsStr, err := getValue(u)
	if err != nil {
		// metadata return 404 when no ipv6 is allocated
		var typedErr *Error
		ok := errors.As(err, &typedErr)
		if ok && typedErr.Code == strconv.Itoa(http.StatusNotFound) {
			return nil, nil
		}
		return nil, err
	}
	ipsStr = strings.ReplaceAll(ipsStr, "[", "")
	ipsStr = strings.ReplaceAll(ipsStr, "]", "")
	addressStrList := strings.Split(ipsStr, ",")

	var ips []net.IP
	for _, ipStr := range addressStrList {
		i := net.ParseIP(strings.TrimSpace(ipStr))
		if i == nil {
			return nil, fmt.Errorf("failed to parse ip %s", strings.TrimSpace(ipStr))
		}
		ips = append(ips, i)
	}
	return ips, nil
}

// GetIPv6ByMac by mac return [2408::28eb]
func GetIPv6ByMac(mac string) ([]netip.Addr, error) {
	p := fmt.Sprintf(eniPrivateV6IPs, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	ipsStr, err := getValue(u)
	if err != nil {
		// metadata return 404 when no ipv6 is allocated
		var typedErr *Error
		ok := errors.As(err, &typedErr)
		if ok && typedErr.Code == strconv.Itoa(http.StatusNotFound) {
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
	p := fmt.Sprintf(eniGatewayPath, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	addr, err := getValue(u)
	if err != nil {
		return nil, err
	}

	i := net.ParseIP(addr)
	if i == nil {
		return nil, fmt.Errorf("failed to parse ip %s", addr)
	}
	return i, nil
}

// GetENIGatewayAddr return gateway ip by mac
func GetENIGatewayAddr(mac string) (netip.Addr, error) {
	p := fmt.Sprintf(eniGatewayPath, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	addr, err := getValue(u)
	if err != nil {
		return netip.Addr{}, err
	}
	return netip.ParseAddr(addr)
}

// GetVSwitchCIDR return vSwitch cidr by mac
func GetVSwitchCIDR(mac string) (*net.IPNet, error) {
	p := fmt.Sprintf(eniVSwitchCIDRPath, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	addr, err := getValue(u)
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
	p := fmt.Sprintf(eniVSwitchCIDRPath, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	addr, err := getValue(u)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.ParsePrefix(addr)
}

// GetVSwitchIPv6CIDR return vSwitch cidr by mac
func GetVSwitchIPv6CIDR(mac string) (*net.IPNet, error) {
	p := fmt.Sprintf(eniVSwitchIPv6CIDRPath, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	addr, err := getValue(u)
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
	p := fmt.Sprintf(eniVSwitchIPv6CIDRPath, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	addr, err := getValue(u)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.ParsePrefix(addr)
}

// GetENIV6Gateway return gateway ip by mac
func GetENIV6Gateway(mac string) (net.IP, error) {
	p := fmt.Sprintf(eniV6GatewayPath, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	addr, err := getValue(u)
	if err != nil {
		return nil, err
	}
	i := net.ParseIP(addr)
	if i == nil {
		return nil, fmt.Errorf("failed to parse ip %s", addr)
	}
	return i, nil
}

// GetENIV6GatewayAddr return gateway ip by mac
func GetENIV6GatewayAddr(mac string) (netip.Addr, error) {
	p := fmt.Sprintf(eniV6GatewayPath, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	addr, err := getValue(u)
	if err != nil {
		return netip.Addr{}, err
	}
	return netip.ParseAddr(addr)
}

// GetENIVSwitchID by mac
func GetENIVSwitchID(mac string) (string, error) {
	p := fmt.Sprintf(eniVSwitchPath, mac)
	u, _ := url.JoinPath(MetadataBase, p)
	return getValue(u)
}

// GetENIsMAC get attached ENIs
func GetENIsMAC() ([]string, error) {
	u, _ := url.JoinPath(MetadataBase, enisPath)
	return getArray(u)
}

// GetPrimaryENIMAC get the main ENI's mac
func GetPrimaryENIMAC() (string, error) {
	u, _ := url.JoinPath(MetadataBase, mainEniPath)
	return getValue(u)
}
