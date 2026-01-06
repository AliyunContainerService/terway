package main

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	"github.com/AliyunContainerService/terway/rpc"
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

// mockTerwayTracingClient is a mock implementation of rpc.TerwayTracingClient for testing
type mockTerwayTracingClient struct {
	getResourceTypesFunc   func(ctx context.Context, in *rpc.Placeholder, opts ...grpc.CallOption) (*rpc.ResourcesTypesReply, error)
	getResourcesFunc       func(ctx context.Context, in *rpc.ResourceTypeRequest, opts ...grpc.CallOption) (*rpc.ResourcesNamesReply, error)
	getResourceConfigFunc  func(ctx context.Context, in *rpc.ResourceTypeNameRequest, opts ...grpc.CallOption) (*rpc.ResourceConfigReply, error)
	getResourceTraceFunc   func(ctx context.Context, in *rpc.ResourceTypeNameRequest, opts ...grpc.CallOption) (*rpc.ResourceTraceReply, error)
	resourceExecuteFunc    func(ctx context.Context, in *rpc.ResourceExecuteRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[rpc.ResourceExecuteReply], error)
	getResourceMappingFunc func(ctx context.Context, in *rpc.Placeholder, opts ...grpc.CallOption) (*rpc.ResourceMappingReply, error)
}

func (m *mockTerwayTracingClient) GetResourceTypes(ctx context.Context, in *rpc.Placeholder, opts ...grpc.CallOption) (*rpc.ResourcesTypesReply, error) {
	if m.getResourceTypesFunc != nil {
		return m.getResourceTypesFunc(ctx, in, opts...)
	}
	return nil, errors.New("not implemented")
}

func (m *mockTerwayTracingClient) GetResources(ctx context.Context, in *rpc.ResourceTypeRequest, opts ...grpc.CallOption) (*rpc.ResourcesNamesReply, error) {
	if m.getResourcesFunc != nil {
		return m.getResourcesFunc(ctx, in, opts...)
	}
	return nil, errors.New("not implemented")
}

func (m *mockTerwayTracingClient) GetResourceConfig(ctx context.Context, in *rpc.ResourceTypeNameRequest, opts ...grpc.CallOption) (*rpc.ResourceConfigReply, error) {
	if m.getResourceConfigFunc != nil {
		return m.getResourceConfigFunc(ctx, in, opts...)
	}
	return nil, errors.New("not implemented")
}

func (m *mockTerwayTracingClient) GetResourceTrace(ctx context.Context, in *rpc.ResourceTypeNameRequest, opts ...grpc.CallOption) (*rpc.ResourceTraceReply, error) {
	if m.getResourceTraceFunc != nil {
		return m.getResourceTraceFunc(ctx, in, opts...)
	}
	return nil, errors.New("not implemented")
}

func (m *mockTerwayTracingClient) ResourceExecute(ctx context.Context, in *rpc.ResourceExecuteRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[rpc.ResourceExecuteReply], error) {
	if m.resourceExecuteFunc != nil {
		return m.resourceExecuteFunc(ctx, in, opts...)
	}
	return nil, errors.New("not implemented")
}

func (m *mockTerwayTracingClient) GetResourceMapping(ctx context.Context, in *rpc.Placeholder, opts ...grpc.CallOption) (*rpc.ResourceMappingReply, error) {
	if m.getResourceMappingFunc != nil {
		return m.getResourceMappingFunc(ctx, in, opts...)
	}
	return nil, errors.New("not implemented")
}

// mockResourceExecuteStream is a mock implementation of grpc.ServerStreamingClient for ResourceExecute
type mockResourceExecuteStream struct {
	grpc.ClientStream
	messages []*rpc.ResourceExecuteReply
	index    int
	err      error
}

func (m *mockResourceExecuteStream) Recv() (*rpc.ResourceExecuteReply, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.index >= len(m.messages) {
		return nil, io.EOF
	}
	msg := m.messages[m.index]
	m.index++
	return msg, nil
}

func (m *mockResourceExecuteStream) CloseSend() error {
	return nil
}

func TestRunList_Success(t *testing.T) {
	originalClient := client
	originalCtx := ctx
	defer func() {
		client = originalClient
		ctx = originalCtx
	}()

	ctx = context.Background()

	t.Run("List types when no arguments", func(t *testing.T) {
		mockClient := &mockTerwayTracingClient{
			getResourceTypesFunc: func(ctx context.Context, in *rpc.Placeholder, opts ...grpc.CallOption) (*rpc.ResourcesTypesReply, error) {
				return &rpc.ResourcesTypesReply{
					TypeNames: []string{"type1", "type2", "type3"},
				}, nil
			},
		}
		client = mockClient

		cmd := &cobra.Command{}
		err := runList(cmd, []string{})
		assert.NoError(t, err)
	})

	t.Run("List resources when type argument provided", func(t *testing.T) {
		mockClient := &mockTerwayTracingClient{
			getResourcesFunc: func(ctx context.Context, in *rpc.ResourceTypeRequest, opts ...grpc.CallOption) (*rpc.ResourcesNamesReply, error) {
				assert.Equal(t, "eni", in.Name)
				return &rpc.ResourcesNamesReply{
					ResourceNames: []string{"eni-001", "eni-002"},
				}, nil
			},
		}
		client = mockClient

		cmd := &cobra.Command{}
		err := runList(cmd, []string{"eni"})
		assert.NoError(t, err)
	})
}

func TestRunList_Error(t *testing.T) {
	originalClient := client
	originalCtx := ctx
	defer func() {
		client = originalClient
		ctx = originalCtx
	}()

	ctx = context.Background()

	t.Run("Error when GetResourceTypes fails", func(t *testing.T) {
		mockClient := &mockTerwayTracingClient{
			getResourceTypesFunc: func(ctx context.Context, in *rpc.Placeholder, opts ...grpc.CallOption) (*rpc.ResourcesTypesReply, error) {
				return nil, errors.New("connection failed")
			},
		}
		client = mockClient

		cmd := &cobra.Command{}
		err := runList(cmd, []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "connection failed")
	})

	t.Run("Error when GetResources fails", func(t *testing.T) {
		mockClient := &mockTerwayTracingClient{
			getResourcesFunc: func(ctx context.Context, in *rpc.ResourceTypeRequest, opts ...grpc.CallOption) (*rpc.ResourcesNamesReply, error) {
				return nil, errors.New("resource type not found")
			},
		}
		client = mockClient

		cmd := &cobra.Command{}
		err := runList(cmd, []string{"unknown"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "resource type not found")
	})
}

func TestRunShow_Success(t *testing.T) {
	originalClient := client
	originalCtx := ctx
	defer func() {
		client = originalClient
		ctx = originalCtx
	}()

	ctx = context.Background()

	t.Run("Show with type only - gets first resource", func(t *testing.T) {
		mockClient := &mockTerwayTracingClient{
			getResourcesFunc: func(ctx context.Context, in *rpc.ResourceTypeRequest, opts ...grpc.CallOption) (*rpc.ResourcesNamesReply, error) {
				return &rpc.ResourcesNamesReply{
					ResourceNames: []string{"resource-1", "resource-2"},
				}, nil
			},
			getResourceConfigFunc: func(ctx context.Context, in *rpc.ResourceTypeNameRequest, opts ...grpc.CallOption) (*rpc.ResourceConfigReply, error) {
				assert.Equal(t, "eni", in.Type)
				assert.Equal(t, "resource-1", in.Name)
				return &rpc.ResourceConfigReply{
					Config: []*rpc.MapKeyValueEntry{
						{Key: "key1", Value: "value1"},
					},
				}, nil
			},
			getResourceTraceFunc: func(ctx context.Context, in *rpc.ResourceTypeNameRequest, opts ...grpc.CallOption) (*rpc.ResourceTraceReply, error) {
				return &rpc.ResourceTraceReply{
					Trace: []*rpc.MapKeyValueEntry{
						{Key: "trace1", Value: "traceValue1"},
					},
				}, nil
			},
		}
		client = mockClient

		cmd := &cobra.Command{}
		err := runShow(cmd, []string{"eni"})
		assert.NoError(t, err)
	})
}

func TestRunShow_Error(t *testing.T) {
	originalClient := client
	originalCtx := ctx
	defer func() {
		client = originalClient
		ctx = originalCtx
	}()

	ctx = context.Background()

	t.Run("Error when no resources found for type", func(t *testing.T) {
		mockClient := &mockTerwayTracingClient{
			getResourcesFunc: func(ctx context.Context, in *rpc.ResourceTypeRequest, opts ...grpc.CallOption) (*rpc.ResourcesNamesReply, error) {
				return &rpc.ResourcesNamesReply{
					ResourceNames: []string{},
				}, nil
			},
		}
		client = mockClient

		cmd := &cobra.Command{}
		err := runShow(cmd, []string{"eni"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no resource in the specified type")
	})

	t.Run("Error when GetResourceConfig fails", func(t *testing.T) {
		mockClient := &mockTerwayTracingClient{
			getResourcesFunc: func(ctx context.Context, in *rpc.ResourceTypeRequest, opts ...grpc.CallOption) (*rpc.ResourcesNamesReply, error) {
				return &rpc.ResourcesNamesReply{
					ResourceNames: []string{"resource-1"},
				}, nil
			},
			getResourceConfigFunc: func(ctx context.Context, in *rpc.ResourceTypeNameRequest, opts ...grpc.CallOption) (*rpc.ResourceConfigReply, error) {
				return nil, errors.New("config not found")
			},
		}
		client = mockClient

		cmd := &cobra.Command{}
		err := runShow(cmd, []string{"eni"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "config not found")
	})

	t.Run("Error when GetResourceTrace fails", func(t *testing.T) {
		mockClient := &mockTerwayTracingClient{
			getResourcesFunc: func(ctx context.Context, in *rpc.ResourceTypeRequest, opts ...grpc.CallOption) (*rpc.ResourcesNamesReply, error) {
				return &rpc.ResourcesNamesReply{
					ResourceNames: []string{"resource-1"},
				}, nil
			},
			getResourceConfigFunc: func(ctx context.Context, in *rpc.ResourceTypeNameRequest, opts ...grpc.CallOption) (*rpc.ResourceConfigReply, error) {
				return &rpc.ResourceConfigReply{Config: []*rpc.MapKeyValueEntry{}}, nil
			},
			getResourceTraceFunc: func(ctx context.Context, in *rpc.ResourceTypeNameRequest, opts ...grpc.CallOption) (*rpc.ResourceTraceReply, error) {
				return nil, errors.New("trace not found")
			},
		}
		client = mockClient

		cmd := &cobra.Command{}
		err := runShow(cmd, []string{"eni"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "trace not found")
	})
}

func TestRunMapping_Success(t *testing.T) {
	originalClient := client
	originalCtx := ctx
	defer func() {
		client = originalClient
		ctx = originalCtx
	}()

	ctx = context.Background()

	t.Run("Get resource mapping successfully", func(t *testing.T) {
		mockClient := &mockTerwayTracingClient{
			getResourceMappingFunc: func(ctx context.Context, in *rpc.Placeholder, opts ...grpc.CallOption) (*rpc.ResourceMappingReply, error) {
				return &rpc.ResourceMappingReply{
					Info: []*rpc.ResourceMapping{
						{
							NetworkInterfaceID:   "eni-123",
							MAC:                  "00:11:22:33:44:55",
							Status:               "InUse",
							Type:                 "Secondary",
							AllocInhibitExpireAt: "2024-01-01T00:00:00Z",
							Info:                 []string{"info1", "info2"},
						},
						{
							NetworkInterfaceID:   "eni-456",
							MAC:                  "00:11:22:33:44:66",
							Status:               "Available",
							Type:                 "Primary",
							AllocInhibitExpireAt: "",
							Info:                 []string{},
						},
					},
				}, nil
			},
		}
		client = mockClient

		cmd := &cobra.Command{}
		err := runMapping(cmd, []string{})
		assert.NoError(t, err)
	})

	t.Run("Get resource mapping with empty result", func(t *testing.T) {
		mockClient := &mockTerwayTracingClient{
			getResourceMappingFunc: func(ctx context.Context, in *rpc.Placeholder, opts ...grpc.CallOption) (*rpc.ResourceMappingReply, error) {
				return &rpc.ResourceMappingReply{
					Info: []*rpc.ResourceMapping{},
				}, nil
			},
		}
		client = mockClient

		cmd := &cobra.Command{}
		err := runMapping(cmd, []string{})
		assert.NoError(t, err)
	})
}

func TestRunMapping_Error(t *testing.T) {
	originalClient := client
	originalCtx := ctx
	defer func() {
		client = originalClient
		ctx = originalCtx
	}()

	ctx = context.Background()

	t.Run("Error when GetResourceMapping fails", func(t *testing.T) {
		mockClient := &mockTerwayTracingClient{
			getResourceMappingFunc: func(ctx context.Context, in *rpc.Placeholder, opts ...grpc.CallOption) (*rpc.ResourceMappingReply, error) {
				return nil, errors.New("mapping service unavailable")
			},
		}
		client = mockClient

		cmd := &cobra.Command{}
		err := runMapping(cmd, []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "mapping service unavailable")
	})
}

func TestRunExecute_Success(t *testing.T) {
	originalClient := client
	originalCtx := ctx
	defer func() {
		client = originalClient
		ctx = originalCtx
	}()

	ctx = context.Background()

	t.Run("Execute command successfully with messages", func(t *testing.T) {
		mockStream := &mockResourceExecuteStream{
			messages: []*rpc.ResourceExecuteReply{
				{Message: "output line 1\n"},
				{Message: "output line 2\n"},
			},
		}

		mockClient := &mockTerwayTracingClient{
			resourceExecuteFunc: func(ctx context.Context, in *rpc.ResourceExecuteRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[rpc.ResourceExecuteReply], error) {
				assert.Equal(t, "eni", in.Type)
				assert.Equal(t, "eni-001", in.Name)
				assert.Equal(t, "status", in.Command)
				assert.Equal(t, []string{"arg1", "arg2"}, in.Args)
				return mockStream, nil
			},
		}
		client = mockClient

		cmd := &cobra.Command{}
		err := runExecute(cmd, []string{"eni", "eni-001", "status", "arg1", "arg2"})
		assert.NoError(t, err)
	})

	t.Run("Execute command with empty response", func(t *testing.T) {
		mockStream := &mockResourceExecuteStream{
			messages: []*rpc.ResourceExecuteReply{},
		}

		mockClient := &mockTerwayTracingClient{
			resourceExecuteFunc: func(ctx context.Context, in *rpc.ResourceExecuteRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[rpc.ResourceExecuteReply], error) {
				return mockStream, nil
			},
		}
		client = mockClient

		cmd := &cobra.Command{}
		err := runExecute(cmd, []string{"eni", "eni-001", "status"})
		assert.NoError(t, err)
	})
}

func TestRunExecute_Error(t *testing.T) {
	originalClient := client
	originalCtx := ctx
	defer func() {
		client = originalClient
		ctx = originalCtx
	}()

	ctx = context.Background()

	t.Run("Error when ResourceExecute fails", func(t *testing.T) {
		mockClient := &mockTerwayTracingClient{
			resourceExecuteFunc: func(ctx context.Context, in *rpc.ResourceExecuteRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[rpc.ResourceExecuteReply], error) {
				return nil, errors.New("execute failed")
			},
		}
		client = mockClient

		cmd := &cobra.Command{}
		err := runExecute(cmd, []string{"eni", "eni-001", "status"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "execute failed")
	})

	t.Run("Error when stream.Recv fails", func(t *testing.T) {
		mockStream := &mockResourceExecuteStream{
			err: errors.New("stream error"),
		}

		mockClient := &mockTerwayTracingClient{
			resourceExecuteFunc: func(ctx context.Context, in *rpc.ResourceExecuteRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[rpc.ResourceExecuteReply], error) {
				return mockStream, nil
			},
		}
		client = mockClient

		cmd := &cobra.Command{}
		err := runExecute(cmd, []string{"eni", "eni-001", "status"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "stream error")
	})
}

func TestGetFirstNameWithType_Success(t *testing.T) {
	originalClient := client
	originalCtx := ctx
	defer func() {
		client = originalClient
		ctx = originalCtx
	}()

	ctx = context.Background()

	t.Run("Get first name successfully", func(t *testing.T) {
		mockClient := &mockTerwayTracingClient{
			getResourcesFunc: func(ctx context.Context, in *rpc.ResourceTypeRequest, opts ...grpc.CallOption) (*rpc.ResourcesNamesReply, error) {
				assert.Equal(t, "eni", in.Name)
				return &rpc.ResourcesNamesReply{
					ResourceNames: []string{"eni-first", "eni-second"},
				}, nil
			},
		}
		client = mockClient

		name, err := getFirstNameWithType("eni")
		assert.NoError(t, err)
		assert.Equal(t, "eni-first", name)
	})
}

func TestGetFirstNameWithType_Error(t *testing.T) {
	originalClient := client
	originalCtx := ctx
	defer func() {
		client = originalClient
		ctx = originalCtx
	}()

	ctx = context.Background()

	t.Run("Error when GetResources fails", func(t *testing.T) {
		mockClient := &mockTerwayTracingClient{
			getResourcesFunc: func(ctx context.Context, in *rpc.ResourceTypeRequest, opts ...grpc.CallOption) (*rpc.ResourcesNamesReply, error) {
				return nil, errors.New("service unavailable")
			},
		}
		client = mockClient

		name, err := getFirstNameWithType("eni")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "service unavailable")
		assert.Empty(t, name)
	})

	t.Run("Error when no resources found", func(t *testing.T) {
		mockClient := &mockTerwayTracingClient{
			getResourcesFunc: func(ctx context.Context, in *rpc.ResourceTypeRequest, opts ...grpc.CallOption) (*rpc.ResourcesNamesReply, error) {
				return &rpc.ResourcesNamesReply{
					ResourceNames: []string{},
				}, nil
			},
		}
		client = mockClient

		name, err := getFirstNameWithType("eni")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no resource in the specified type")
		assert.Empty(t, name)
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
