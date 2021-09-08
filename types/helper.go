package types

import (
	"fmt"
	"net"

	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/rpc"
)

func BuildIPNet(ip, subnet *rpc.IPSet) (*IPNetSet, error) {
	ipnet := &IPNetSet{}
	if ip == nil || subnet == nil {
		return ipnet, nil
	}
	exec := func(ip, subnet string) (*net.IPNet, error) {
		i, err := terwayIP.ToIP(ip)
		if err != nil {
			return nil, err
		}
		_, sub, err := net.ParseCIDR(subnet)
		if err != nil {
			return nil, err
		}
		sub.IP = i
		return sub, nil
	}
	var err error
	if ip.IPv4 != "" && subnet.IPv4 != "" {
		ipnet.IPv4, err = exec(ip.IPv4, subnet.IPv4)
		if err != nil {
			return nil, err
		}
	}
	if ip.IPv6 != "" && subnet.IPv6 != "" {
		ipnet.IPv6, err = exec(ip.IPv6, subnet.IPv6)
		if err != nil {
			return nil, err
		}
	}
	return ipnet, nil
}

func ToIPSet(ip *rpc.IPSet) (*IPSet, error) {
	if ip == nil {
		return nil, fmt.Errorf("ip is nil")
	}
	ipSet := &IPSet{}
	var err error
	if ip.IPv4 != "" {
		ipSet.IPv4, err = terwayIP.ToIP(ip.IPv4)
		if err != nil {
			return nil, err
		}
	}
	if ip.IPv6 != "" {
		ipSet.IPv6, err = terwayIP.ToIP(ip.IPv6)
		if err != nil {
			return nil, err
		}
	}
	return ipSet, nil
}

func ToIPNetSet(ip *rpc.IPSet) (*IPNetSet, error) {
	if ip == nil {
		return nil, fmt.Errorf("ip is nil")
	}
	ipNetSet := &IPNetSet{}
	var err error
	if ip.IPv4 != "" {
		_, ipNetSet.IPv4, err = net.ParseCIDR(ip.IPv4)
		if err != nil {
			return nil, err
		}
	}
	if ip.IPv6 != "" {
		_, ipNetSet.IPv6, err = net.ParseCIDR(ip.IPv6)
		if err != nil {
			return nil, err
		}
	}
	return ipNetSet, nil
}
