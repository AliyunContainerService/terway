package ip

import (
	"fmt"
	"net"
)

func ToIP(addr string) (net.IP, error) {
	ip := net.ParseIP(addr)
	if ip == nil {
		return nil, fmt.Errorf("failed to parse ip %s", addr)
	}
	return ip, nil
}

func ToIPs(addrs []string) ([]net.IP, error) {
	var result []net.IP
	for _, addr := range addrs {
		i, err := ToIP(addr)
		if err != nil {
			return nil, err
		}
		result = append(result, i)
	}
	return result, nil
}

func IPv6(ip net.IP) bool {
	return ip.To4() == nil
}

// NetEqual returns true if both IPNet are equal
func NetEqual(ipn1 *net.IPNet, ipn2 *net.IPNet) bool {
	if ipn1 == ipn2 {
		return true
	}
	if ipn1 == nil || ipn2 == nil {
		return false
	}

	return ipn1.String() == ipn2.String()
}
