package types_test

import (
	"fmt"
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/AliyunContainerService/terway/rpc"
	"github.com/AliyunContainerService/terway/types"
)

func TestIPSet_SetIP(t *testing.T) {
	ipSet := &types.IPSet{}
	assert.Equal(t, "127.0.0.1", ipSet.SetIP("127.0.0.1").IPv4.String())
	assert.NotNil(t, ipSet.IPv4)
	assert.Equal(t, "127.0.0.1", ipSet.SetIP("127.0.0.x").IPv4.String())
	assert.Nil(t, ipSet.IPv6)
	assert.Equal(t, "fd00::100", ipSet.SetIP("fd00::100").IPv6.String())
	assert.NotNil(t, ipSet.IPv6)
}

func TestIPNetSet_SetIPNet(t *testing.T) {
	ipNetSet := &types.IPNetSet{}
	assert.Equal(t, "127.0.0.1/32", ipNetSet.SetIPNet("127.0.0.1/32").IPv4.String())
	assert.NotNil(t, ipNetSet.IPv4)
	assert.Equal(t, "127.0.0.0/24", ipNetSet.SetIPNet("127.0.0.1/24").IPv4.String())
	assert.NotNil(t, ipNetSet.IPv4)
	assert.Equal(t, "127.0.0.0/24", ipNetSet.SetIPNet("127.0.0.x").IPv4.String(), "no change")
	assert.Nil(t, ipNetSet.IPv6)
	assert.Equal(t, "fd00::/120", ipNetSet.SetIPNet("fd00::/120").IPv6.String())
	assert.NotNil(t, ipNetSet.IPv6)
}

func TestErrorReturnsCorrectErrorMessage(t *testing.T) {
	err := &types.Error{
		Code: types.ErrInternalError,
		Msg:  "An internal error occurred",
	}

	assert.Equal(t, "code: InternalError, msg: An internal error occurred", err.Error())
}

func TestErrorUnwrapReturnsUnderlyingError(t *testing.T) {
	underlyingError := fmt.Errorf("underlying error")
	err := &types.Error{
		Code: types.ErrInternalError,
		Msg:  "An internal error occurred",
		R:    underlyingError,
	}

	assert.Equal(t, underlyingError, err.Unwrap())
}

func TestErrorUnwrapReturnsNilWhenNoUnderlyingError(t *testing.T) {
	err := &types.Error{
		Code: types.ErrInternalError,
		Msg:  "An internal error occurred",
	}

	assert.Nil(t, err.Unwrap())
}

func TestIPSet2_String(t *testing.T) {
	tests := []struct {
		name     string
		ipset2   types.IPSet2
		expected string
	}{
		{
			name:     "IPv4 and IPv6 valid",
			ipset2:   types.IPSet2{IPv4: netip.MustParseAddr("192.0.2.1"), IPv6: netip.MustParseAddr("2001:db8::1")},
			expected: "192.0.2.1-2001:db8::1",
		},
		{
			name:     "Only IPv4 valid",
			ipset2:   types.IPSet2{IPv4: netip.MustParseAddr("192.0.2.1")},
			expected: "192.0.2.1",
		},
		{
			name:     "Only IPv6 valid",
			ipset2:   types.IPSet2{IPv6: netip.MustParseAddr("2001:db8::1")},
			expected: "2001:db8::1",
		},
		{
			name:     "Both invalid",
			ipset2:   types.IPSet2{},
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.ipset2.String()
			assert.Equal(t, test.expected, result)
		})
	}
}

func TestIPSet2_ToRPC(t *testing.T) {
	tests := []struct {
		name     string
		ipset2   types.IPSet2
		expected *rpc.IPSet
	}{
		{
			name: "IPv4 and IPv6 valid",
			ipset2: types.IPSet2{
				IPv4: netip.MustParseAddr("192.0.2.1"),
				IPv6: netip.MustParseAddr("2001:db8::1"),
			},
			expected: &rpc.IPSet{
				IPv4: "192.0.2.1",
				IPv6: "2001:db8::1",
			},
		},
		{
			name: "Only IPv4 valid",
			ipset2: types.IPSet2{
				IPv4: netip.MustParseAddr("192.0.2.1"),
			},
			expected: &rpc.IPSet{
				IPv4: "192.0.2.1",
				IPv6: "",
			},
		},
		{
			name: "Only IPv6 valid",
			ipset2: types.IPSet2{
				IPv6: netip.MustParseAddr("2001:db8::1"),
			},
			expected: &rpc.IPSet{
				IPv4: "",
				IPv6: "2001:db8::1",
			},
		},
		{
			name:   "Both invalid",
			ipset2: types.IPSet2{},
			expected: &rpc.IPSet{
				IPv4: "",
				IPv6: "",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.ipset2.ToRPC()
			assert.Equal(t, test.expected, result)
		})
	}
}

func TestIPSet2_GetIPv4(t *testing.T) {
	tests := []struct {
		name     string
		ipset2   types.IPSet2
		expected string
	}{
		{
			name:     "IPv4 valid",
			ipset2:   types.IPSet2{IPv4: netip.MustParseAddr("192.0.2.1")},
			expected: "192.0.2.1",
		},
		{
			name:     "IPv4 invalid",
			ipset2:   types.IPSet2{},
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.ipset2.GetIPv4()
			assert.Equal(t, test.expected, result)
		})
	}
}

func TestIPSet2_GetIPv6(t *testing.T) {
	tests := []struct {
		name     string
		ipset2   types.IPSet2
		expected string
	}{
		{
			name:     "IPv6 valid",
			ipset2:   types.IPSet2{IPv6: netip.MustParseAddr("2001:db8::1")},
			expected: "2001:db8::1",
		},
		{
			name:     "IPv6 invalid",
			ipset2:   types.IPSet2{},
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.ipset2.GetIPv6()
			assert.Equal(t, test.expected, result)
		})
	}
}

func TestIPSet_String(t *testing.T) {
	tests := []struct {
		name     string
		ipset    types.IPSet
		expected string
	}{
		{
			name: "IPv4 and IPv6 valid",
			ipset: types.IPSet{
				IPv4: net.ParseIP("192.0.2.1"),
				IPv6: net.ParseIP("2001:db8::1"),
			},
			expected: "192.0.2.1-2001:db8::1",
		},
		{
			name: "Only IPv4 valid",
			ipset: types.IPSet{
				IPv4: net.ParseIP("192.0.2.1"),
			},
			expected: "192.0.2.1",
		},
		{
			name: "Only IPv6 valid",
			ipset: types.IPSet{
				IPv6: net.ParseIP("2001:db8::1"),
			},
			expected: "2001:db8::1",
		},
		{
			name:     "Both nil",
			ipset:    types.IPSet{},
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.ipset.String()
			assert.Equal(t, test.expected, result)
		})
	}
}

func TestIPSet_ToRPC(t *testing.T) {
	tests := []struct {
		name     string
		ipset    types.IPSet
		expected *rpc.IPSet
	}{
		{
			name: "IPv4 and IPv6 valid",
			ipset: types.IPSet{
				IPv4: net.ParseIP("192.0.2.1"),
				IPv6: net.ParseIP("2001:db8::1"),
			},
			expected: &rpc.IPSet{
				IPv4: "192.0.2.1",
				IPv6: "2001:db8::1",
			},
		},
		{
			name: "Only IPv4 valid",
			ipset: types.IPSet{
				IPv4: net.ParseIP("192.0.2.1"),
			},
			expected: &rpc.IPSet{
				IPv4: "192.0.2.1",
				IPv6: "",
			},
		},
		{
			name: "Only IPv6 valid",
			ipset: types.IPSet{
				IPv6: net.ParseIP("2001:db8::1"),
			},
			expected: &rpc.IPSet{
				IPv4: "",
				IPv6: "2001:db8::1",
			},
		},
		{
			name:  "Both nil",
			ipset: types.IPSet{},
			expected: &rpc.IPSet{
				IPv4: "",
				IPv6: "",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.ipset.ToRPC()
			assert.Equal(t, test.expected, result)
		})
	}
}

func TestIPSet_GetIPv4(t *testing.T) {
	tests := []struct {
		name     string
		ipset    types.IPSet
		expected string
	}{
		{
			name: "IPv4 valid",
			ipset: types.IPSet{
				IPv4: net.ParseIP("192.0.2.1"),
			},
			expected: "192.0.2.1",
		},
		{
			name:     "IPv4 nil",
			ipset:    types.IPSet{},
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.ipset.GetIPv4()
			assert.Equal(t, test.expected, result)
		})
	}
}

func TestIPSet_GetIPv6(t *testing.T) {
	tests := []struct {
		name     string
		ipset    types.IPSet
		expected string
	}{
		{
			name: "IPv6 valid",
			ipset: types.IPSet{
				IPv6: net.ParseIP("2001:db8::1"),
			},
			expected: "2001:db8::1",
		},
		{
			name:     "IPv6 nil",
			ipset:    types.IPSet{},
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.ipset.GetIPv6()
			assert.Equal(t, test.expected, result)
		})
	}
}

func TestIPNetSet_String(t *testing.T) {
	_, ipv4Net, _ := net.ParseCIDR("192.0.2.0/24")
	_, ipv6Net, _ := net.ParseCIDR("2001:db8::/64")

	tests := []struct {
		name     string
		ipNetSet types.IPNetSet
		expected string
	}{
		{
			name: "IPv4 and IPv6 valid",
			ipNetSet: types.IPNetSet{
				IPv4: ipv4Net,
				IPv6: ipv6Net,
			},
			expected: "192.0.2.0/24-2001:db8::/64",
		},
		{
			name: "Only IPv4 valid",
			ipNetSet: types.IPNetSet{
				IPv4: ipv4Net,
			},
			expected: "192.0.2.0/24",
		},
		{
			name: "Only IPv6 valid",
			ipNetSet: types.IPNetSet{
				IPv6: ipv6Net,
			},
			expected: "2001:db8::/64",
		},
		{
			name:     "Both nil",
			ipNetSet: types.IPNetSet{},
			expected: "",
		},
		{
			name:     "IPNetSet is nil",
			ipNetSet: types.IPNetSet{},
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.ipNetSet.String()
			assert.Equal(t, test.expected, result)
		})
	}
}

func TestIPNetSet_ToRPC(t *testing.T) {
	_, ipv4Net, _ := net.ParseCIDR("192.0.2.0/24")
	_, ipv6Net, _ := net.ParseCIDR("2001:db8::/64")

	tests := []struct {
		name     string
		ipNetSet types.IPNetSet
		expected *rpc.IPSet
	}{
		{
			name: "IPv4 and IPv6 valid",
			ipNetSet: types.IPNetSet{
				IPv4: ipv4Net,
				IPv6: ipv6Net,
			},
			expected: &rpc.IPSet{
				IPv4: "192.0.2.0/24",
				IPv6: "2001:db8::/64",
			},
		},
		{
			name: "Only IPv4 valid",
			ipNetSet: types.IPNetSet{
				IPv4: ipv4Net,
			},
			expected: &rpc.IPSet{
				IPv4: "192.0.2.0/24",
				IPv6: "",
			},
		},
		{
			name: "Only IPv6 valid",
			ipNetSet: types.IPNetSet{
				IPv6: ipv6Net,
			},
			expected: &rpc.IPSet{
				IPv4: "",
				IPv6: "2001:db8::/64",
			},
		},
		{
			name:     "Both nil",
			ipNetSet: types.IPNetSet{},
			expected: &rpc.IPSet{
				IPv4: "",
				IPv6: "",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.ipNetSet.ToRPC()
			assert.Equal(t, test.expected, result)
		})
	}
}
