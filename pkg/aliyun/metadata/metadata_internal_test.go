package metadata

import (
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
)

func TestGetENIPrimaryIP_Abnormal(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
		return "invalid-ip", nil
	})

	ip, err := GetENIPrimaryIP("mac1")
	assert.Error(t, err)
	assert.Nil(t, ip)
	assert.Contains(t, err.Error(), "failed to parse ip")
}

func TestGetENIPrivateIPs_Abnormal(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
			return "invalid-json", nil
		})

		ips, err := GetENIPrivateIPs("mac1")
		assert.Error(t, err)
		assert.Nil(t, ips)
		assert.Contains(t, err.Error(), "failed to parse ip")
	})

	t.Run("invalid ip in json", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
			return `["192.168.1.1", "invalid-ip"]`, nil
		})

		ips, err := GetENIPrivateIPs("mac1")
		assert.Error(t, err)
		assert.Nil(t, ips)
		assert.Contains(t, err.Error(), "failed to parse ip invalid-ip")
	})
}

func TestGetENIPrivateIPv6IPs_Abnormal(t *testing.T) {
	t.Run("invalid ipv6 format", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
			return "[invalid-ipv6]", nil
		})

		ips, err := GetENIPrivateIPv6IPs("mac1")
		assert.Error(t, err)
		assert.Nil(t, ips)
		assert.Contains(t, err.Error(), "failed to parse ip invalid-ipv6")
	})

	t.Run("metadata error 404", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
			return "", &Error{Code: "404"}
		})

		ips, err := GetENIPrivateIPv6IPs("mac1")
		assert.NoError(t, err)
		assert.Nil(t, ips)
	})

	t.Run("metadata error other", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
			return "", &Error{Code: "500"}
		})

		ips, err := GetENIPrivateIPv6IPs("mac1")
		assert.Error(t, err)
		assert.Nil(t, ips)
	})
}

func TestGetVSwitchCIDR_Abnormal(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
		return "invalid-cidr", nil
	})

	cidr, err := GetVSwitchCIDR("mac1")
	assert.Error(t, err)
	assert.Nil(t, cidr)
	assert.Contains(t, err.Error(), "failed to parse cidr")
}

func TestGetENIsMAC_Abnormal(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(getArray, func(urlStr string) ([]string, error) {
		return nil, fmt.Errorf("network error")
	})

	macs, err := GetENIsMAC()
	assert.Error(t, err)
	assert.Nil(t, macs)
	assert.Equal(t, "network error", err.Error())
}

func TestGetIPv4ByMac_Abnormal(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
			return "invalid-json", nil
		})

		ips, err := GetIPv4ByMac("mac1")
		assert.Error(t, err)
		assert.Nil(t, ips)
	})

	t.Run("invalid addr in json", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
			return `["invalid-addr"]`, nil
		})

		ips, err := GetIPv4ByMac("mac1")
		assert.Error(t, err)
		assert.Nil(t, ips)
	})
}

func TestGetIPv6ByMac_Abnormal(t *testing.T) {
	t.Run("invalid addr", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
			return "[invalid-addr]", nil
		})

		ips, err := GetIPv6ByMac("mac1")
		assert.Error(t, err)
		assert.Nil(t, ips)
	})

	t.Run("metadata error 404", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()

		patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
			return "", &Error{Code: "404"}
		})

		ips, err := GetIPv6ByMac("mac1")
		assert.NoError(t, err)
		assert.Nil(t, ips)
	})
}

func TestGetVSwitchIPv6CIDR_Abnormal(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
		return "invalid-cidr", nil
	})

	cidr, err := GetVSwitchIPv6CIDR("mac1")
	assert.Error(t, err)
	assert.Nil(t, cidr)
	assert.Contains(t, err.Error(), "failed to parse cidr")
}

func TestGetENIPrimaryAddr_Abnormal(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
		return "invalid-addr", nil
	})

	addr, err := GetENIPrimaryAddr("mac1")
	assert.Error(t, err)
	assert.False(t, addr.IsValid())
}

func TestGetENIGatewayAddr_Abnormal(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
		return "invalid-addr", nil
	})

	addr, err := GetENIGatewayAddr("mac1")
	assert.Error(t, err)
	assert.False(t, addr.IsValid())
}

func TestGetENIV6GatewayAddr_Abnormal(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
		return "invalid-addr", nil
	})

	addr, err := GetENIV6GatewayAddr("mac1")
	assert.Error(t, err)
	assert.False(t, addr.IsValid())
}

func TestGetVSwitchPrefix_Abnormal(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
		return "invalid-prefix", nil
	})

	prefix, err := GetVSwitchPrefix("mac1")
	assert.Error(t, err)
	assert.False(t, prefix.IsValid())
}

func TestGetVSwitchIPv6Prefix_Abnormal(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
		return "invalid-prefix", nil
	})

	prefix, err := GetVSwitchIPv6Prefix("mac1")
	assert.Error(t, err)
	assert.False(t, prefix.IsValid())
}

func TestGetENIGateway_Abnormal(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
		return "invalid-ip", nil
	})

	gw, err := GetENIGateway("mac1")
	assert.Error(t, err)
	assert.Nil(t, gw)
}

func TestGetENIV6Gateway_Abnormal(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(getValue, func(urlStr string) (string, error) {
		return "invalid-ip", nil
	})

	gw, err := GetENIV6Gateway("mac1")
	assert.Error(t, err)
	assert.Nil(t, gw)
}
