package ip

import (
	"fmt"
	"net"
	"net/netip"

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

func ToIPAddrs(addrs []string) ([]netip.Addr, error) {
	var result []netip.Addr
	for _, addr := range addrs {
		i, err := netip.ParseAddr(addr)
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

func IPs2str(ips []net.IP) []string {
	var result []string
	for _, ip := range ips {
		result = append(result, ip.String())
	}
	return result
}

func IPAddrs2str(ips []netip.Addr) []string {
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
