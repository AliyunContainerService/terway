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
