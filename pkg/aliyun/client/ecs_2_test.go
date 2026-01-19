package client

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	sdkErr "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/wait"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
)

func TestECSService_DescribeNetworkInterface2_Error(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).DescribeNetworkInterfaces,
		func(client *ecs.Client, request *ecs.DescribeNetworkInterfacesRequest) (*ecs.DescribeNetworkInterfacesResponse, error) {
			return nil, sdkErr.NewServerError(500, "{\"Code\": \"InternalError\"}", "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	result, err := ecsService.DescribeNetworkInterface2(ctx)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, apiErr.ErrorCodeIs(err, apiErr.ErrInternalError))
}

func TestECSService_AssignPrivateIPAddress2_Error_Retry(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	count := 0
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AssignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.AssignPrivateIpAddressesRequest) (*ecs.AssignPrivateIpAddressesResponse, error) {
			count++
			if count < 2 {
				return nil, sdkErr.NewServerError(403, "{\"Code\": \"Throttling\"}", "request-id")
			}
			return &ecs.AssignPrivateIpAddressesResponse{
				RequestId: "request-id",
				AssignedPrivateIpAddressesSet: ecs.AssignedPrivateIpAddressesSet{
					PrivateIpSet: ecs.PrivateIpSetInAssignPrivateIpAddresses{
						PrivateIpAddress: []string{"10.0.0.1"},
					},
				},
			}, nil
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	result, err := ecsService.AssignPrivateIPAddress2(ctx, &AssignPrivateIPAddressOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: "eni-test-001",
			IPCount:            1,
		},
		Backoff: &wait.Backoff{Steps: 2, Duration: time.Millisecond},
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 2, count)
	assert.Equal(t, "10.0.0.1", result[0].IPAddress)
}

func TestECSService_AssignPrivateIPAddress2_FatalError(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AssignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.AssignPrivateIpAddressesRequest) (*ecs.AssignPrivateIpAddressesResponse, error) {
			return nil, sdkErr.NewServerError(400, "{\"Code\": \"InvalidParameter\"}", "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	result, err := ecsService.AssignPrivateIPAddress2(ctx, &AssignPrivateIPAddressOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: "eni-test-001",
			IPCount:            1,
		},
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, apiErr.ErrorCodeIs(err, "InvalidParameter"))
}

func TestECSService_UnAssignPrivateIPAddresses2_Error(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).UnassignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.UnassignPrivateIpAddressesRequest) (*ecs.UnassignPrivateIpAddressesResponse, error) {
			return nil, sdkErr.NewServerError(500, "{\"Code\": \"InternalError\"}", "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	err := ecsService.UnAssignPrivateIPAddresses2(ctx, "eni-1", []IPSet{{IPAddress: "10.0.0.1"}})
	assert.Error(t, err)
	assert.True(t, apiErr.ErrorCodeIs(err, apiErr.ErrInternalError))
}

func TestECSService_UnAssignPrivateIPAddresses2_NotFound(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).UnassignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.UnassignPrivateIpAddressesRequest) (*ecs.UnassignPrivateIpAddressesResponse, error) {
			return nil, sdkErr.NewServerError(404, fmt.Sprintf("{\"Code\": \"%s\"}", apiErr.ErrInvalidENINotFound), "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	err := ecsService.UnAssignPrivateIPAddresses2(ctx, "eni-1", []IPSet{{IPAddress: "10.0.0.1"}})
	assert.NoError(t, err)
}

func TestECSService_AssignIpv6Addresses2_Error(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AssignIpv6Addresses,
		func(client *ecs.Client, request *ecs.AssignIpv6AddressesRequest) (*ecs.AssignIpv6AddressesResponse, error) {
			return nil, sdkErr.NewServerError(400, "{\"Code\": \"InvalidParameter\"}", "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	result, err := ecsService.AssignIpv6Addresses2(ctx, &AssignIPv6AddressesOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: "eni-test-001",
			IPv6Count:          1,
		},
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, apiErr.ErrorCodeIs(err, "InvalidParameter"))
}

func TestECSService_UnAssignIpv6Addresses2_Error(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).UnassignIpv6Addresses,
		func(client *ecs.Client, request *ecs.UnassignIpv6AddressesRequest) (*ecs.UnassignIpv6AddressesResponse, error) {
			return nil, sdkErr.NewServerError(500, "{\"Code\": \"InternalError\"}", "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	err := ecsService.UnAssignIpv6Addresses2(ctx, "eni-1", []IPSet{{IPAddress: "2001:db8::1"}})
	assert.Error(t, err)
	assert.True(t, apiErr.ErrorCodeIs(err, apiErr.ErrInternalError))
}

func TestECSService_UnAssignIpv6Addresses2_NotFound(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).UnassignIpv6Addresses,
		func(client *ecs.Client, request *ecs.UnassignIpv6AddressesRequest) (*ecs.UnassignIpv6AddressesResponse, error) {
			return nil, sdkErr.NewServerError(404, fmt.Sprintf("{\"Code\": \"%s\"}", apiErr.ErrInvalidENINotFound), "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	err := ecsService.UnAssignIpv6Addresses2(ctx, "eni-1", []IPSet{{IPAddress: "2001:db8::1"}})
	assert.NoError(t, err)
}

func TestECSService_DetachNetworkInterface2_Error(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).DetachNetworkInterface,
		func(client *ecs.Client, request *ecs.DetachNetworkInterfaceRequest) (*ecs.DetachNetworkInterfaceResponse, error) {
			return nil, sdkErr.NewServerError(500, "{\"Code\": \"InternalError\"}", "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	err := ecsService.DetachNetworkInterface2(ctx, &DetachNetworkInterfaceOptions{
		NetworkInterfaceID: ptrTo("eni-1"),
		InstanceID:         ptrTo("i-1"),
	})
	assert.Error(t, err)
	assert.True(t, apiErr.ErrorCodeIs(err, apiErr.ErrInternalError))
}

func TestECSService_DetachNetworkInterface2_NotFound(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).DetachNetworkInterface,
		func(client *ecs.Client, request *ecs.DetachNetworkInterfaceRequest) (*ecs.DetachNetworkInterfaceResponse, error) {
			return nil, sdkErr.NewServerError(404, fmt.Sprintf("{\"Code\": \"%s\"}", apiErr.ErrInvalidENINotFound), "request-id")
		},
	)
	defer patches.Reset()

	ctx := context.Background()
	err := ecsService.DetachNetworkInterface2(ctx, &DetachNetworkInterfaceOptions{
		NetworkInterfaceID: ptrTo("eni-1"),
		InstanceID:         ptrTo("i-1"),
	})
	assert.NoError(t, err)
}

func ptrTo[T any](v T) *T {
	return &v
}
