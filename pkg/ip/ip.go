package ip

import (
	"fmt"
	"net"

	"k8s.io/apimachinery/pkg/util/sets"
)

// ToIP parse str to net.IP and return error is parse failed
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

func ToIPMap(addrs []net.IP) map[string]net.IP {
	result := make(map[string]net.IP)

	for _, addr := range addrs {
		result[addr.String()] = addr
	}

	return result
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

func IPs2str(ips []net.IP) []string {
	var result []string
	for _, ip := range ips {
		result = append(result, ip.String())
	}
	return result
}

// IPsIntersect return is 2 set is intersect
func IPsIntersect(a []net.IP, b []net.IP) bool {
	return sets.NewString(IPs2str(a)...).HasAny(IPs2str(b)...)
}

// IPsHasAll return true if all b is in a
func IPsHasAll(a []net.IP, b []net.IP) bool {
	return sets.NewString(IPs2str(a)...).HasAll(IPs2str(b)...)
}

// DeriveGatewayIP gateway ip from cidr
func DeriveGatewayIP(cidr string) string {
	if cidr == "" {
		return ""
	}
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}
	gw := GetIPAtIndex(*ipNet, int64(-3))
	if gw == nil {
		return ""
	}
	return gw.String()
}
