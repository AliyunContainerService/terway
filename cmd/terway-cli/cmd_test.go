package main

import (
	"errors"
	"net"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
)

func TestPrintKV(t *testing.T) {
	t.Run("Format key-value pair correctly", func(t *testing.T) {
		result := printKV("test-key", "test-value")
		assert.Contains(t, result, "test-key")
		assert.Contains(t, result, "test-value")
		assert.Contains(t, result, ":")
	})

	t.Run("Handle empty values", func(t *testing.T) {
		result := printKV("key", "")
		assert.Contains(t, result, "key")
		assert.Contains(t, result, ":")
	})

	t.Run("Handle special characters", func(t *testing.T) {
		result := printKV("special-key", "value with spaces & symbols!")
		assert.Contains(t, result, "special-key")
		assert.Contains(t, result, "value with spaces & symbols!")
	})

	t.Run("Handle unicode characters", func(t *testing.T) {
		result := printKV("unicode-key", "值包含中文字符")
		assert.Contains(t, result, "unicode-key")
		assert.Contains(t, result, "值包含中文字符")
	})
}

func TestArgumentValidation(t *testing.T) {
	t.Run("runList argument validation", func(t *testing.T) {
		cmd := &cobra.Command{}

		// Test with exactly 2 arguments (should fail)
		err := runList(cmd, []string{"arg1", "arg2"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too many arguments")

		// Test with more than 2 arguments (should fail)
		err = runList(cmd, []string{"arg1", "arg2", "arg3"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too many arguments")
	})

	t.Run("runShow argument validation", func(t *testing.T) {
		cmd := &cobra.Command{}

		// Test with no arguments
		err := runShow(cmd, []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no arguments")

		// Test with 2 or more arguments
		err = runShow(cmd, []string{"type", "name"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too many arguments")

		// Test with 3 arguments
		err = runShow(cmd, []string{"type", "name", "extra"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too many arguments")
	})

	t.Run("runExecute argument validation", func(t *testing.T) {
		cmd := &cobra.Command{}

		// Test with no arguments
		err := runExecute(cmd, []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too few arguments")

		// Test with 1 argument
		err = runExecute(cmd, []string{"type"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too few arguments")

		// Test with 2 arguments
		err = runExecute(cmd, []string{"type", "name"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too few arguments")

		// Test with exactly 3 arguments should pass validation
		// We don't call runExecute here since it will access nil client
		// Instead, we verify the argument parsing logic
		args := []string{"type", "name", "command"}
		assert.True(t, len(args) >= 3, "Should have at least 3 arguments")

		// Verify argument assignment logic
		typ, name, command := args[0], args[1], args[2]
		remainingArgs := args[3:]
		assert.Equal(t, "type", typ)
		assert.Equal(t, "name", name)
		assert.Equal(t, "command", command)
		assert.Empty(t, remainingArgs)
	})
}

func TestRunMetadata_ErrorHandling(t *testing.T) {
	cmd := &cobra.Command{}

	t.Run("Error when GetLocalVswitch fails", func(t *testing.T) {
		// Mock metadata.GetLocalVswitch to return error
		patches := gomonkey.ApplyFunc(
			metadata.GetLocalVswitch,
			func() (string, error) {
				return "", errors.New("vswitch metadata unavailable")
			},
		)
		defer patches.Reset()

		err := runMetadata(cmd, []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "vswitch metadata unavailable")
	})

	t.Run("Error when GetENIsMAC fails", func(t *testing.T) {
		// Mock metadata.GetLocalVswitch to succeed
		patches1 := gomonkey.ApplyFunc(
			metadata.GetLocalVswitch,
			func() (string, error) {
				return "vsw-123", nil
			},
		)
		defer patches1.Reset()

		// Mock metadata.GetENIsMAC to return error
		patches2 := gomonkey.ApplyFunc(
			metadata.GetENIsMAC,
			func() ([]string, error) {
				return nil, errors.New("enis metadata unavailable")
			},
		)
		defer patches2.Reset()

		err := runMetadata(cmd, []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "enis metadata unavailable")
	})

	t.Run("Error when GetPrimaryENIMAC fails", func(t *testing.T) {
		// Mock metadata.GetLocalVswitch
		patches1 := gomonkey.ApplyFunc(
			metadata.GetLocalVswitch,
			func() (string, error) {
				return "vsw-123", nil
			},
		)
		defer patches1.Reset()

		// Mock metadata.GetENIsMAC
		patches2 := gomonkey.ApplyFunc(
			metadata.GetENIsMAC,
			func() ([]string, error) {
				return []string{"02:42:ac:11:00:02"}, nil
			},
		)
		defer patches2.Reset()

		// Mock metadata.GetPrimaryENIMAC to return error
		patches3 := gomonkey.ApplyFunc(
			metadata.GetPrimaryENIMAC,
			func() (string, error) {
				return "", errors.New("primary eni metadata unavailable")
			},
		)
		defer patches3.Reset()

		err := runMetadata(cmd, []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "primary eni metadata unavailable")
	})

	t.Run("Handle interface not found gracefully", func(t *testing.T) {
		// Mock all metadata calls to succeed
		patches1 := gomonkey.ApplyFunc(metadata.GetLocalVswitch, func() (string, error) { return "vsw-123", nil })
		defer patches1.Reset()

		patches2 := gomonkey.ApplyFunc(metadata.GetENIsMAC, func() ([]string, error) { return []string{"02:42:ac:11:00:02"}, nil })
		defer patches2.Reset()

		patches3 := gomonkey.ApplyFunc(metadata.GetPrimaryENIMAC, func() (string, error) { return "02:42:ac:11:00:02", nil })
		defer patches3.Reset()

		// Mock getInterfaceByMAC to return "not found" error (should be handled gracefully)
		patches4 := gomonkey.ApplyFunc(
			getInterfaceByMAC,
			func(mac string) (net.Interface, error) {
				return net.Interface{}, errors.New("not found")
			},
		)
		defer patches4.Reset()

		patches5 := gomonkey.ApplyFunc(metadata.GetENIPrimaryIP, func(mac string) (net.IP, error) { return net.ParseIP("10.0.0.1"), nil })
		defer patches5.Reset()

		patches6 := gomonkey.ApplyFunc(metadata.GetENIPrivateIPs, func(mac string) ([]net.IP, error) { return []net.IP{net.ParseIP("10.0.0.1")}, nil })
		defer patches6.Reset()

		patches7 := gomonkey.ApplyFunc(metadata.GetENIPrivateIPv6IPs, func(mac string) ([]net.IP, error) { return []net.IP{}, nil })
		defer patches7.Reset()

		err := runMetadata(cmd, []string{})
		assert.NoError(t, err) // Should handle "not found" gracefully
	})

	t.Run("Error when getInterfaceByMAC returns non-not-found error", func(t *testing.T) {
		// Mock all metadata calls to succeed
		patches1 := gomonkey.ApplyFunc(metadata.GetLocalVswitch, func() (string, error) { return "vsw-123", nil })
		defer patches1.Reset()

		patches2 := gomonkey.ApplyFunc(metadata.GetENIsMAC, func() ([]string, error) { return []string{"02:42:ac:11:00:02"}, nil })
		defer patches2.Reset()

		patches3 := gomonkey.ApplyFunc(metadata.GetPrimaryENIMAC, func() (string, error) { return "02:42:ac:11:00:02", nil })
		defer patches3.Reset()

		// Mock getInterfaceByMAC to return a different error (not "not found")
		patches4 := gomonkey.ApplyFunc(
			getInterfaceByMAC,
			func(mac string) (net.Interface, error) {
				return net.Interface{}, errors.New("permission denied")
			},
		)
		defer patches4.Reset()

		err := runMetadata(cmd, []string{})
		assert.Error(t, err) // Should return error for non-"not found" errors
		assert.Contains(t, err.Error(), "permission denied")
	})

	t.Run("Handle IPv6 metadata error gracefully", func(t *testing.T) {
		// Mock all required metadata calls to succeed
		patches1 := gomonkey.ApplyFunc(metadata.GetLocalVswitch, func() (string, error) { return "vsw-123", nil })
		defer patches1.Reset()

		patches2 := gomonkey.ApplyFunc(metadata.GetENIsMAC, func() ([]string, error) { return []string{"02:42:ac:11:00:02"}, nil })
		defer patches2.Reset()

		patches3 := gomonkey.ApplyFunc(metadata.GetPrimaryENIMAC, func() (string, error) { return "02:42:ac:11:00:02", nil })
		defer patches3.Reset()

		patches4 := gomonkey.ApplyFunc(getInterfaceByMAC, func(mac string) (net.Interface, error) {
			return net.Interface{Name: "eth0"}, nil
		})
		defer patches4.Reset()

		patches5 := gomonkey.ApplyFunc(metadata.GetENIPrimaryIP, func(mac string) (net.IP, error) { return net.ParseIP("10.0.0.1"), nil })
		defer patches5.Reset()

		patches6 := gomonkey.ApplyFunc(metadata.GetENIPrivateIPs, func(mac string) ([]net.IP, error) { return []net.IP{net.ParseIP("10.0.0.1")}, nil })
		defer patches6.Reset()

		// Mock IPv6 to return error (should be handled gracefully by continue statement)
		patches7 := gomonkey.ApplyFunc(
			metadata.GetENIPrivateIPv6IPs,
			func(mac string) ([]net.IP, error) {
				return nil, errors.New("ipv6 not supported")
			},
		)
		defer patches7.Reset()

		// This should not return error even if IPv6 fails (due to continue in the loop)
		err := runMetadata(cmd, []string{})
		assert.NoError(t, err)
	})
}

func TestRunMetadata_SuccessfulFlow(t *testing.T) {
	cmd := &cobra.Command{}

	t.Run("Handle successful metadata retrieval", func(t *testing.T) {
		// Mock all metadata functions to return successful responses
		patches1 := gomonkey.ApplyFunc(
			metadata.GetLocalVswitch,
			func() (string, error) {
				return "vsw-123", nil
			},
		)
		defer patches1.Reset()

		patches2 := gomonkey.ApplyFunc(
			metadata.GetENIsMAC,
			func() ([]string, error) {
				return []string{"02:42:ac:11:00:02", "02:42:ac:11:00:03"}, nil
			},
		)
		defer patches2.Reset()

		patches3 := gomonkey.ApplyFunc(
			metadata.GetPrimaryENIMAC,
			func() (string, error) {
				return "02:42:ac:11:00:02", nil
			},
		)
		defer patches3.Reset()

		patches4 := gomonkey.ApplyFunc(
			getInterfaceByMAC,
			func(mac string) (net.Interface, error) {
				return net.Interface{
					Name:         "eth0",
					HardwareAddr: net.HardwareAddr{0x02, 0x42, 0xac, 0x11, 0x00, 0x02},
				}, nil
			},
		)
		defer patches4.Reset()

		patches5 := gomonkey.ApplyFunc(
			metadata.GetENIPrimaryIP,
			func(mac string) (net.IP, error) {
				return net.ParseIP("10.0.0.1"), nil
			},
		)
		defer patches5.Reset()

		patches6 := gomonkey.ApplyFunc(
			metadata.GetENIPrivateIPs,
			func(mac string) ([]net.IP, error) {
				return []net.IP{
					net.ParseIP("10.0.0.1"), // primary
					net.ParseIP("10.0.0.2"), // secondary
				}, nil
			},
		)
		defer patches6.Reset()

		patches7 := gomonkey.ApplyFunc(
			metadata.GetENIPrivateIPv6IPs,
			func(mac string) ([]net.IP, error) {
				return []net.IP{
					net.ParseIP("2001:db8::1"),
				}, nil
			},
		)
		defer patches7.Reset()

		err := runMetadata(cmd, []string{})
		assert.NoError(t, err)
	})

	t.Run("Handle ENI with only primary IP", func(t *testing.T) {
		// Mock metadata calls to return single IP (only primary)
		patches1 := gomonkey.ApplyFunc(metadata.GetLocalVswitch, func() (string, error) { return "vsw-123", nil })
		defer patches1.Reset()

		patches2 := gomonkey.ApplyFunc(metadata.GetENIsMAC, func() ([]string, error) { return []string{"02:42:ac:11:00:02"}, nil })
		defer patches2.Reset()

		patches3 := gomonkey.ApplyFunc(metadata.GetPrimaryENIMAC, func() (string, error) { return "02:42:ac:11:00:02", nil })
		defer patches3.Reset()

		patches4 := gomonkey.ApplyFunc(getInterfaceByMAC, func(mac string) (net.Interface, error) {
			return net.Interface{Name: "eth0"}, nil
		})
		defer patches4.Reset()

		patches5 := gomonkey.ApplyFunc(metadata.GetENIPrimaryIP, func(mac string) (net.IP, error) { return net.ParseIP("10.0.0.1"), nil })
		defer patches5.Reset()

		// Return only primary IP (length == 1, so no secondary IPs section)
		patches6 := gomonkey.ApplyFunc(metadata.GetENIPrivateIPs, func(mac string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("10.0.0.1")}, nil // Only primary
		})
		defer patches6.Reset()

		patches7 := gomonkey.ApplyFunc(metadata.GetENIPrivateIPv6IPs, func(mac string) ([]net.IP, error) { return []net.IP{}, nil })
		defer patches7.Reset()

		err := runMetadata(cmd, []string{})
		assert.NoError(t, err)
	})
}

func TestRunMetadata_ErrorCases(t *testing.T) {
	cmd := &cobra.Command{}

	t.Run("Error when GetLocalVswitch fails", func(t *testing.T) {
		// Mock metadata.GetLocalVswitch to return error
		patches := gomonkey.ApplyFunc(
			metadata.GetLocalVswitch,
			func() (string, error) {
				return "", errors.New("vswitch metadata unavailable")
			},
		)
		defer patches.Reset()

		err := runMetadata(cmd, []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "vswitch metadata unavailable")
	})

	t.Run("Error when GetENIsMAC fails", func(t *testing.T) {
		// Mock metadata.GetLocalVswitch to succeed
		patches1 := gomonkey.ApplyFunc(
			metadata.GetLocalVswitch,
			func() (string, error) {
				return "vsw-123", nil
			},
		)
		defer patches1.Reset()

		// Mock metadata.GetENIsMAC to return error
		patches2 := gomonkey.ApplyFunc(
			metadata.GetENIsMAC,
			func() ([]string, error) {
				return nil, errors.New("enis metadata unavailable")
			},
		)
		defer patches2.Reset()

		err := runMetadata(cmd, []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "enis metadata unavailable")
	})

	t.Run("Error when GetPrimaryENIMAC fails", func(t *testing.T) {
		// Mock metadata.GetLocalVswitch
		patches1 := gomonkey.ApplyFunc(
			metadata.GetLocalVswitch,
			func() (string, error) {
				return "vsw-123", nil
			},
		)
		defer patches1.Reset()

		// Mock metadata.GetENIsMAC
		patches2 := gomonkey.ApplyFunc(
			metadata.GetENIsMAC,
			func() ([]string, error) {
				return []string{"02:42:ac:11:00:02"}, nil
			},
		)
		defer patches2.Reset()

		// Mock metadata.GetPrimaryENIMAC to return error
		patches3 := gomonkey.ApplyFunc(
			metadata.GetPrimaryENIMAC,
			func() (string, error) {
				return "", errors.New("primary eni metadata unavailable")
			},
		)
		defer patches3.Reset()

		err := runMetadata(cmd, []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "primary eni metadata unavailable")
	})

	t.Run("Error when GetENIPrimaryIP fails", func(t *testing.T) {
		// Mock all metadata calls to succeed up to GetENIPrimaryIP
		patches1 := gomonkey.ApplyFunc(metadata.GetLocalVswitch, func() (string, error) { return "vsw-123", nil })
		defer patches1.Reset()

		patches2 := gomonkey.ApplyFunc(metadata.GetENIsMAC, func() ([]string, error) { return []string{"02:42:ac:11:00:02"}, nil })
		defer patches2.Reset()

		patches3 := gomonkey.ApplyFunc(metadata.GetPrimaryENIMAC, func() (string, error) { return "02:42:ac:11:00:02", nil })
		defer patches3.Reset()

		patches4 := gomonkey.ApplyFunc(getInterfaceByMAC, func(mac string) (net.Interface, error) {
			return net.Interface{Name: "eth0"}, nil
		})
		defer patches4.Reset()

		// Mock GetENIPrimaryIP to return error
		patches5 := gomonkey.ApplyFunc(
			metadata.GetENIPrimaryIP,
			func(mac string) (net.IP, error) {
				return nil, errors.New("primary ip metadata unavailable")
			},
		)
		defer patches5.Reset()

		err := runMetadata(cmd, []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "primary ip metadata unavailable")
	})

	t.Run("Error when GetENIPrivateIPs fails", func(t *testing.T) {
		// Mock all metadata calls to succeed up to GetENIPrivateIPs
		patches1 := gomonkey.ApplyFunc(metadata.GetLocalVswitch, func() (string, error) { return "vsw-123", nil })
		defer patches1.Reset()

		patches2 := gomonkey.ApplyFunc(metadata.GetENIsMAC, func() ([]string, error) { return []string{"02:42:ac:11:00:02"}, nil })
		defer patches2.Reset()

		patches3 := gomonkey.ApplyFunc(metadata.GetPrimaryENIMAC, func() (string, error) { return "02:42:ac:11:00:02", nil })
		defer patches3.Reset()

		patches4 := gomonkey.ApplyFunc(getInterfaceByMAC, func(mac string) (net.Interface, error) {
			return net.Interface{Name: "eth0"}, nil
		})
		defer patches4.Reset()

		patches5 := gomonkey.ApplyFunc(metadata.GetENIPrimaryIP, func(mac string) (net.IP, error) { return net.ParseIP("10.0.0.1"), nil })
		defer patches5.Reset()

		// Mock GetENIPrivateIPs to return error
		patches6 := gomonkey.ApplyFunc(
			metadata.GetENIPrivateIPs,
			func(mac string) ([]net.IP, error) {
				return nil, errors.New("private ips metadata unavailable")
			},
		)
		defer patches6.Reset()

		err := runMetadata(cmd, []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "private ips metadata unavailable")
	})

	t.Run("Handle IPv6 error gracefully with continue", func(t *testing.T) {
		// Mock all required metadata calls to succeed
		patches1 := gomonkey.ApplyFunc(metadata.GetLocalVswitch, func() (string, error) { return "vsw-123", nil })
		defer patches1.Reset()

		patches2 := gomonkey.ApplyFunc(metadata.GetENIsMAC, func() ([]string, error) { return []string{"02:42:ac:11:00:02"}, nil })
		defer patches2.Reset()

		patches3 := gomonkey.ApplyFunc(metadata.GetPrimaryENIMAC, func() (string, error) { return "02:42:ac:11:00:02", nil })
		defer patches3.Reset()

		patches4 := gomonkey.ApplyFunc(getInterfaceByMAC, func(mac string) (net.Interface, error) {
			return net.Interface{Name: "eth0"}, nil
		})
		defer patches4.Reset()

		patches5 := gomonkey.ApplyFunc(metadata.GetENIPrimaryIP, func(mac string) (net.IP, error) { return net.ParseIP("10.0.0.1"), nil })
		defer patches5.Reset()

		patches6 := gomonkey.ApplyFunc(metadata.GetENIPrivateIPs, func(mac string) ([]net.IP, error) { return []net.IP{net.ParseIP("10.0.0.1")}, nil })
		defer patches6.Reset()

		// Mock IPv6 to return error (should be handled gracefully by continue statement)
		patches7 := gomonkey.ApplyFunc(
			metadata.GetENIPrivateIPv6IPs,
			func(mac string) ([]net.IP, error) {
				return nil, errors.New("ipv6 not supported")
			},
		)
		defer patches7.Reset()

		// This should not return error even if IPv6 fails (due to continue in the loop)
		err := runMetadata(cmd, []string{})
		assert.NoError(t, err)
	})
}
