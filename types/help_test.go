package types

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/AliyunContainerService/terway/rpc"
)

func TestBuildIPNetReturnsEmptyWhenIPAndSubnetAreNil(t *testing.T) {
	ipnet, err := BuildIPNet(nil, nil)
	assert.NoError(t, err)
	assert.NotNil(t, ipnet)
	assert.Nil(t, ipnet.IPv4)
	assert.Nil(t, ipnet.IPv6)
}

func TestBuildIPNetReturnsErrorWhenIPv4IsInvalid(t *testing.T) {
	ip := &rpc.IPSet{IPv4: "invalid-ip"}
	subnet := &rpc.IPSet{IPv4: "192.168.0.0/24"}
	_, err := BuildIPNet(ip, subnet)
	assert.Error(t, err)
}

func TestBuildIPNetReturnsErrorWhenIPv6IsInvalid(t *testing.T) {
	ip := &rpc.IPSet{IPv6: "invalid-ip"}
	subnet := &rpc.IPSet{IPv6: "fd00::/64"}
	_, err := BuildIPNet(ip, subnet)
	assert.Error(t, err)
}

func TestBuildIPNetReturnsErrorWhenSubnetIPv4IsInvalid(t *testing.T) {
	ip := &rpc.IPSet{IPv4: "192.168.0.1"}
	subnet := &rpc.IPSet{IPv4: "invalid-subnet"}
	_, err := BuildIPNet(ip, subnet)
	assert.Error(t, err)
}

func TestBuildIPNetReturnsErrorWhenSubnetIPv6IsInvalid(t *testing.T) {
	ip := &rpc.IPSet{IPv6: "fd00::1"}
	subnet := &rpc.IPSet{IPv6: "invalid-subnet"}
	_, err := BuildIPNet(ip, subnet)
	assert.Error(t, err)
}

func TestBuildIPNetReturnsCorrectIPNetForValidIPv4(t *testing.T) {
	ip := &rpc.IPSet{IPv4: "192.168.0.1"}
	subnet := &rpc.IPSet{IPv4: "192.168.0.0/24"}
	ipnet, err := BuildIPNet(ip, subnet)
	assert.NoError(t, err)
	assert.NotNil(t, ipnet.IPv4)
	assert.Equal(t, "192.168.0.1/24", ipnet.IPv4.String())
	assert.Nil(t, ipnet.IPv6)
}

func TestBuildIPNetReturnsCorrectIPNetForValidIPv6(t *testing.T) {
	ip := &rpc.IPSet{IPv6: "fd00::1"}
	subnet := &rpc.IPSet{IPv6: "fd00::/64"}
	ipnet, err := BuildIPNet(ip, subnet)
	assert.NoError(t, err)
	assert.NotNil(t, ipnet.IPv6)
	assert.Equal(t, "fd00::1/64", ipnet.IPv6.String())
	assert.Nil(t, ipnet.IPv4)
}
