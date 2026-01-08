package client

import (
	"context"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	ecs20140526 "github.com/alibabacloud-go/ecs-20140526/v7/client"
	eflo20220530 "github.com/alibabacloud-go/eflo-20220530/v2/client"
	eflocontroller20221215 "github.com/alibabacloud-go/eflo-controller-20221215/v2/client"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"k8s.io/apimachinery/pkg/util/wait"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
)

// Mock ClientSet for EFLO Service testing
type mockClientSetForEFLO struct {
	efloClient   *eflo.Client
	efloV2Client *eflo20220530.Client
}

func (m *mockClientSetForEFLO) ECS() *ecs.Client {
	return nil
}

func (m *mockClientSetForEFLO) ECSV2() *ecs20140526.Client {
	return nil
}

func (m *mockClientSetForEFLO) VPC() *vpc.Client {
	return nil
}

func (m *mockClientSetForEFLO) EFLO() *eflo.Client {
	return m.efloClient
}

func (m *mockClientSetForEFLO) EFLOV2() *eflo20220530.Client {
	return m.efloV2Client
}

func (m *mockClientSetForEFLO) EFLOController() *eflocontroller20221215.Client {
	return nil
}

// Helper function to create EFLOService for testing
func createTestEFLOServiceForAPI() *EFLOService {
	rateLimiter := NewRateLimiter(LimitConfig{})
	tracer := otel.Tracer("test")
	return &EFLOService{
		RateLimiter:      rateLimiter,
		Tracer:           tracer,
		ClientSet:        &mockClientSetForEFLO{efloClient: &eflo.Client{}, efloV2Client: &eflo20220530.Client{}},
		IdempotentKeyGen: NewIdempotentKeyGenerator(),
	}
}

func TestEFLOService_CreateElasticNetworkInterfaceV2_WithGomonkey(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response
	mockResponse := &eflo.CreateElasticNetworkInterfaceResponse{
		Code:      0,
		Message:   "Success",
		RequestId: "test-request-id",
		Content: eflo.Content{
			ElasticNetworkInterfaceId: "leni-test-001",
			Status:                    "Available",
			ZoneId:                    "cn-hangzhou-a",
			VpcId:                     "vpc-test-001",
			VSwitchId:                 "vsw-test-001",
			PrivateIpAddress:          "10.0.0.100",
			Mac:                       "02:11:22:33:44:55",
		},
	}

	// Mock CreateElasticNetworkInterface method
	patches := gomonkey.ApplyFunc(
		(*eflo.Client).CreateElasticNetworkInterface,
		func(client *eflo.Client, request *eflo.CreateElasticNetworkInterfaceRequest) (*eflo.CreateElasticNetworkInterfaceResponse, error) {
			// Verify request parameters
			assert.Equal(t, "vsw-test-001", request.VSwitchId)
			assert.Equal(t, "sg-test-001", request.SecurityGroupId)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := efloService.CreateElasticNetworkInterfaceV2(
		ctx,
		WithVSwitchIDForEFLO("vsw-test-001"),
		WithSecurityGroupIDsForEFLO([]string{"sg-test-001"}),
		WithZoneIDForEFLO("cn-hangzhou-a"),
	)

	// Verify result
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "leni-test-001", result.NetworkInterfaceID)
	assert.Equal(t, ENITypeSecondary, result.Type)
	assert.Equal(t, ENITrafficModeStandard, result.NetworkInterfaceTrafficMode)
}

func TestEFLOService_CreateElasticNetworkInterfaceV2_WithGomonkey_ErrorCode(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response with non-zero code
	mockResponse := &eflo.CreateElasticNetworkInterfaceResponse{
		Code:      1001,
		Message:   "Resource not found",
		RequestId: "test-request-id",
		Content:   eflo.Content{},
	}

	// Mock CreateElasticNetworkInterface method
	patches := gomonkey.ApplyFunc(
		(*eflo.Client).CreateElasticNetworkInterface,
		func(client *eflo.Client, request *eflo.CreateElasticNetworkInterfaceRequest) (*eflo.CreateElasticNetworkInterfaceResponse, error) {
			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := efloService.CreateElasticNetworkInterfaceV2(
		ctx,
		WithVSwitchIDForEFLO("vsw-test-001"),
		WithSecurityGroupIDsForEFLO([]string{"sg-test-001"}),
		WithZoneIDForEFLO("cn-hangzhou-a"),
	)

	// Verify result
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, apiErr.IsEfloCode(err, 1001))
}

func TestEFLOService_DescribeLeniNetworkInterface_WithGomonkey(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response
	mockResponse := &eflo.ListElasticNetworkInterfacesResponse{
		Code:      0,
		Message:   "Success",
		RequestId: "test-request-id",
		Content: eflo.Content{
			Data: []eflo.DataItem{
				{
					ElasticNetworkInterfaceId: "leni-test-001",
					Status:                    "Available",
					ZoneId:                    "cn-hangzhou-a",
					VpcId:                     "vpc-test-001",
					VSwitchId:                 "vsw-test-001",
					Ip:                        "10.0.0.100",
					Mac:                       "02:11:22:33:44:55",
					Type:                      "CUSTOM",
					SecurityGroupId:           "sg-test-001",
					NodeId:                    "node-test-001",
					ResourceGroupId:           "rg-test-001",
				},
			},
		},
	}

	// Mock ListElasticNetworkInterfaces method
	patches1 := gomonkey.ApplyFunc(
		(*eflo.Client).ListElasticNetworkInterfaces,
		func(client *eflo.Client, request *eflo.ListElasticNetworkInterfacesRequest) (*eflo.ListElasticNetworkInterfacesResponse, error) {
			// Verify request parameters
			assert.Equal(t, "vpc-test-001", request.VpcId)
			assert.Equal(t, "leni-test-001", request.ElasticNetworkInterfaceId)
			assert.Equal(t, "Available", request.Status)

			return mockResponse, nil
		},
	)
	defer patches1.Reset()

	// Mock ListLeniPrivateIPAddresses method
	patches2 := gomonkey.ApplyFunc(
		(*eflo.Client).ListLeniPrivateIpAddresses,
		func(client *eflo.Client, request *eflo.ListLeniPrivateIpAddressesRequest) (*eflo.ListLeniPrivateIpAddressesResponse, error) {
			// Mock response for ListLeniPrivateIPAddresses
			mockIPResponse := &eflo.ListLeniPrivateIpAddressesResponse{
				Code:      0,
				Message:   "Success",
				RequestId: "test-request-id-ips",
				Content: eflo.Content{
					Data: []eflo.DataItem{
						{
							PrivateIpAddress: "10.0.0.101",
						},
					},
				},
			}
			return mockIPResponse, nil
		},
	)
	defer patches2.Reset()

	// Execute test
	ctx := context.Background()
	result, err := efloService.DescribeLeniNetworkInterface(
		ctx,
		WithVPCIDForEFLO("vpc-test-001"),
		WithNetworkInterfaceIDsForEFLO([]string{"leni-test-001"}),
		WithStatusForEFLO("Available"),
	)

	// Verify result
	assert.NoError(t, err)
	assert.Len(t, result, 1)

	leni := result[0]
	assert.Equal(t, "leni-test-001", leni.NetworkInterfaceID)
	assert.Equal(t, "InUse", leni.Status) // EFLO converts "Available" to "InUse"
	assert.Equal(t, "cn-hangzhou-a", leni.ZoneID)
	assert.Equal(t, "vpc-test-001", leni.VPCID)
	assert.Equal(t, "vsw-test-001", leni.VSwitchID)
	assert.Equal(t, "10.0.0.100", leni.PrivateIPAddress)
	assert.Equal(t, "02:11:22:33:44:55", leni.MacAddress)
}

func TestEFLOService_DescribeLeniNetworkInterface_WithGomonkey_ErrorCode(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response with non-zero code
	mockResponse := &eflo.ListElasticNetworkInterfacesResponse{
		Code:      1001,
		Message:   "Resource not found",
		RequestId: "test-request-id",
		Content:   eflo.Content{},
	}

	// Mock ListElasticNetworkInterfaces method
	patches := gomonkey.ApplyFunc(
		(*eflo.Client).ListElasticNetworkInterfaces,
		func(client *eflo.Client, request *eflo.ListElasticNetworkInterfacesRequest) (*eflo.ListElasticNetworkInterfacesResponse, error) {
			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := efloService.DescribeLeniNetworkInterface(
		ctx,
		WithVPCIDForEFLO("vpc-test-001"),
		WithNetworkInterfaceIDsForEFLO([]string{"leni-test-001"}),
		WithStatusForEFLO("Available"),
	)

	// Verify result
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, apiErr.IsEfloCode(err, 1001))
}

func TestEFLOService_AssignLeniPrivateIPAddress2_WithGomonkey(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response
	mockResponse := &eflo.AssignLeniPrivateIpAddressResponse{
		Code:      0,
		Message:   "Success",
		RequestId: "test-request-id",
		Content: eflo.Content{
			ElasticNetworkInterfaceId: "leni-test-001",
			IpName:                    "ip-test-001",
		},
	}

	// Mock AssignLeniPrivateIpAddress method
	patches1 := gomonkey.ApplyFunc(
		(*eflo.Client).AssignLeniPrivateIpAddress,
		func(client *eflo.Client, request *eflo.AssignLeniPrivateIpAddressRequest) (*eflo.AssignLeniPrivateIpAddressResponse, error) {
			// Verify request parameters
			assert.Equal(t, "leni-test-001", request.ElasticNetworkInterfaceId)

			return mockResponse, nil
		},
	)
	defer patches1.Reset()

	// Mock ListLeniPrivateIPAddresses method
	patches2 := gomonkey.ApplyFunc(
		(*eflo.Client).ListLeniPrivateIpAddresses,
		func(client *eflo.Client, request *eflo.ListLeniPrivateIpAddressesRequest) (*eflo.ListLeniPrivateIpAddressesResponse, error) {
			// Mock response for ListLeniPrivateIPAddresses
			mockIPResponse := &eflo.ListLeniPrivateIpAddressesResponse{
				Code:      0,
				Message:   "Success",
				RequestId: "test-request-id-ips",
				Content: eflo.Content{
					Data: []eflo.DataItem{
						{
							PrivateIpAddress: "10.0.0.101",
							IpName:           "ip-test-001",
							Status:           "Available",
						},
					},
				},
			}
			return mockIPResponse, nil
		},
	)
	defer patches2.Reset()

	// Execute test
	ctx := context.Background()
	result, err := efloService.AssignLeniPrivateIPAddress2(
		ctx,
		WithNetworkInterfaceIDForEFLOAssign("leni-test-001"),
	)

	// Verify result
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "10.0.0.101", result[0].IPAddress)
	assert.False(t, result[0].Primary)
}

func TestEFLOService_AssignLeniPrivateIPAddress2_WithGomonkey_ErrorCode(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response with non-zero code
	mockResponse := &eflo.AssignLeniPrivateIpAddressResponse{
		Code:      1001,
		Message:   "Resource not found",
		RequestId: "test-request-id",
		Content:   eflo.Content{},
	}

	// Mock AssignLeniPrivateIpAddress method
	patches := gomonkey.ApplyFunc(
		(*eflo.Client).AssignLeniPrivateIpAddress,
		func(client *eflo.Client, request *eflo.AssignLeniPrivateIpAddressRequest) (*eflo.AssignLeniPrivateIpAddressResponse, error) {
			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := efloService.AssignLeniPrivateIPAddress2(
		ctx,
		WithNetworkInterfaceIDForEFLOAssign("leni-test-001"),
	)

	// Verify result
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, apiErr.IsEfloCode(err, 1001))
}

func TestEFLOService_UnAssignLeniPrivateIPAddresses2_WithGomonkey(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response
	mockResponse := &eflo.UnassignLeniPrivateIpAddressResponse{
		Code:      0,
		Message:   "Success",
		RequestId: "test-request-id",
	}

	// Mock UnassignLeniPrivateIpAddress method
	patches := gomonkey.ApplyFunc(
		(*eflo.Client).UnassignLeniPrivateIpAddress,
		func(client *eflo.Client, request *eflo.UnassignLeniPrivateIpAddressRequest) (*eflo.UnassignLeniPrivateIpAddressResponse, error) {
			// Verify request parameters
			assert.Equal(t, "leni-test-001", request.ElasticNetworkInterfaceId)
			assert.Equal(t, "10.0.0.101", request.IpName)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := efloService.UnAssignLeniPrivateIPAddresses2(
		ctx,
		"leni-test-001",
		[]IPSet{
			{IPAddress: "10.0.0.101", Primary: false},
			{IPAddress: "10.0.0.102", Primary: false},
		},
	)

	// Verify result
	assert.NoError(t, err)
}

func TestEFLOService_AttachLeni_WithGomonkey(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response
	mockResponse := &eflo20220530.AttachElasticNetworkInterfaceResponse{
		Body: &eflo20220530.AttachElasticNetworkInterfaceResponseBody{
			Code:      int32Ptr(0),
			Message:   stringPtr("Success"),
			RequestId: stringPtr("test-request-id"),
		},
	}

	// Mock AttachElasticNetworkInterface method
	patches := gomonkey.ApplyFunc(
		(*eflo20220530.Client).AttachElasticNetworkInterface,
		func(client *eflo20220530.Client, request *eflo20220530.AttachElasticNetworkInterfaceRequest) (*eflo20220530.AttachElasticNetworkInterfaceResponse, error) {
			// Verify request parameters
			assert.Equal(t, "leni-test-001", *request.ElasticNetworkInterfaceId)
			assert.Equal(t, "i-test-001", *request.NodeId)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := efloService.AttachLeni(
		ctx,
		WithNetworkInterfaceIDForEFLOAttach("leni-test-001"),
		WithInstanceIDForEFLOAttach("i-test-001"),
		WithNetworkCardIndexForEFLOAttach(1),
	)

	// Verify result
	assert.NoError(t, err)
}

func TestEFLOService_AttachLeni_WithGomonkey_ErrorCode(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response with non-zero code
	errorCode := int32(1001)
	mockResponse := &eflo20220530.AttachElasticNetworkInterfaceResponse{
		Body: &eflo20220530.AttachElasticNetworkInterfaceResponseBody{
			Code:      &errorCode,
			Message:   stringPtr("Resource not found"),
			RequestId: stringPtr("test-request-id"),
		},
	}

	// Mock AttachElasticNetworkInterface method
	patches := gomonkey.ApplyFunc(
		(*eflo20220530.Client).AttachElasticNetworkInterface,
		func(client *eflo20220530.Client, request *eflo20220530.AttachElasticNetworkInterfaceRequest) (*eflo20220530.AttachElasticNetworkInterfaceResponse, error) {
			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := efloService.AttachLeni(
		ctx,
		WithNetworkInterfaceIDForEFLOAttach("leni-test-001"),
		WithInstanceIDForEFLOAttach("i-test-001"),
		WithNetworkCardIndexForEFLOAttach(1),
	)

	// Verify result
	assert.Error(t, err)
	assert.True(t, apiErr.IsEfloCode(err, 1001))
}

func TestEFLOService_DetachLeni_WithGomonkey(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response
	mockResponse := &eflo20220530.DetachElasticNetworkInterfaceResponse{
		Body: &eflo20220530.DetachElasticNetworkInterfaceResponseBody{
			Code:      int32Ptr(0),
			Message:   stringPtr("Success"),
			RequestId: stringPtr("test-request-id"),
		},
	}

	// Mock DetachElasticNetworkInterface method
	patches := gomonkey.ApplyFunc(
		(*eflo20220530.Client).DetachElasticNetworkInterface,
		func(client *eflo20220530.Client, request *eflo20220530.DetachElasticNetworkInterfaceRequest) (*eflo20220530.DetachElasticNetworkInterfaceResponse, error) {
			// Verify request parameters
			assert.Equal(t, "leni-test-001", *request.ElasticNetworkInterfaceId)
			assert.Equal(t, "i-test-001", *request.NodeId)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := efloService.DetachLeni(
		ctx,
		WithNetworkInterfaceIDForEFLODetach("leni-test-001"),
		WithInstanceIDForEFLODetach("i-test-001"),
	)

	// Verify result
	assert.NoError(t, err)
}

func TestEFLOService_DetachLeni_WithGomonkey_ErrorCode(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response with non-zero code
	errorCode := int32(1001)
	mockResponse := &eflo20220530.DetachElasticNetworkInterfaceResponse{
		Body: &eflo20220530.DetachElasticNetworkInterfaceResponseBody{
			Code:      &errorCode,
			Message:   stringPtr("Resource not found"),
			RequestId: stringPtr("test-request-id"),
		},
	}

	// Mock DetachElasticNetworkInterface method
	patches := gomonkey.ApplyFunc(
		(*eflo20220530.Client).DetachElasticNetworkInterface,
		func(client *eflo20220530.Client, request *eflo20220530.DetachElasticNetworkInterfaceRequest) (*eflo20220530.DetachElasticNetworkInterfaceResponse, error) {
			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := efloService.DetachLeni(
		ctx,
		WithNetworkInterfaceIDForEFLODetach("leni-test-001"),
		WithInstanceIDForEFLODetach("i-test-001"),
	)

	// Verify result
	assert.Error(t, err)
	assert.True(t, apiErr.IsEfloCode(err, 1001))
}

func TestEFLOService_DeleteElasticNetworkInterface_WithGomonkey(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response
	mockResponse := &eflo.DeleteElasticNetworkInterfaceResponse{
		Code:      0,
		Message:   "Success",
		RequestId: "test-request-id",
	}

	// Mock DeleteElasticNetworkInterface method
	patches := gomonkey.ApplyFunc(
		(*eflo.Client).DeleteElasticNetworkInterface,
		func(client *eflo.Client, request *eflo.DeleteElasticNetworkInterfaceRequest) (*eflo.DeleteElasticNetworkInterfaceResponse, error) {
			// Verify request parameters
			assert.Equal(t, "leni-test-001", request.ElasticNetworkInterfaceId)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := efloService.DeleteElasticNetworkInterface(
		ctx,
		"leni-test-001",
	)

	// Verify result
	assert.NoError(t, err)
}

func TestEFLOService_DeleteElasticNetworkInterface_WithGomonkey_ErrorCode(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response with non-zero code (not 1011, so it should return error)
	mockResponse := &eflo.DeleteElasticNetworkInterfaceResponse{
		Code:      1001,
		Message:   "Resource not found",
		RequestId: "test-request-id",
		Content:   eflo.Content{},
	}

	// Mock DeleteElasticNetworkInterface method
	patches := gomonkey.ApplyFunc(
		(*eflo.Client).DeleteElasticNetworkInterface,
		func(client *eflo.Client, request *eflo.DeleteElasticNetworkInterfaceRequest) (*eflo.DeleteElasticNetworkInterfaceResponse, error) {
			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := efloService.DeleteElasticNetworkInterface(
		ctx,
		"leni-test-001",
	)

	// Verify result
	assert.Error(t, err)
	assert.True(t, apiErr.IsEfloCode(err, 1001))
}

func TestEFLOService_ListLeniPrivateIPAddresses_WithGomonkey(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response
	mockResponse := &eflo.ListLeniPrivateIpAddressesResponse{
		Code:      0,
		Message:   "Success",
		RequestId: "test-request-id",
		Content: eflo.Content{
			ElasticNetworkInterfaceId: "leni-test-001",
			PrivateIpAddresses: []eflo.PrivateIpAddress{
				{
					ElasticNetworkInterfaceId: "leni-test-001",
					PrivateIpAddress:          "10.0.0.100",
					Status:                    "Available",
				},
				{
					ElasticNetworkInterfaceId: "leni-test-001",
					PrivateIpAddress:          "10.0.0.101",
					Status:                    "Available",
				},
			},
		},
	}

	// Mock ListLeniPrivateIpAddresses method
	patches := gomonkey.ApplyFunc(
		(*eflo.Client).ListLeniPrivateIpAddresses,
		func(client *eflo.Client, request *eflo.ListLeniPrivateIpAddressesRequest) (*eflo.ListLeniPrivateIpAddressesResponse, error) {
			// Verify request parameters
			assert.Equal(t, "leni-test-001", request.ElasticNetworkInterfaceId)
			assert.Equal(t, "ip-test-001", request.IpName)
			assert.Equal(t, "10.0.0.100", request.PrivateIpAddress)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := efloService.ListLeniPrivateIPAddresses(
		ctx,
		"leni-test-001",
		"ip-test-001",
		"10.0.0.100",
	)

	// Verify result
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "leni-test-001", result.ElasticNetworkInterfaceId)
	assert.Len(t, result.PrivateIpAddresses, 2)
	assert.Equal(t, "10.0.0.100", result.PrivateIpAddresses[0].PrivateIpAddress)
	assert.Equal(t, "10.0.0.101", result.PrivateIpAddresses[1].PrivateIpAddress)
}

func TestEFLOService_ListLeniPrivateIPAddresses_WithGomonkey_ErrorCode(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response with non-zero code
	mockResponse := &eflo.ListLeniPrivateIpAddressesResponse{
		Code:      1001,
		Message:   "Resource not found",
		RequestId: "test-request-id",
		Content:   eflo.Content{},
	}

	// Mock ListLeniPrivateIpAddresses method
	patches := gomonkey.ApplyFunc(
		(*eflo.Client).ListLeniPrivateIpAddresses,
		func(client *eflo.Client, request *eflo.ListLeniPrivateIpAddressesRequest) (*eflo.ListLeniPrivateIpAddressesResponse, error) {
			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := efloService.ListLeniPrivateIPAddresses(
		ctx,
		"leni-test-001",
		"ip-test-001",
		"10.0.0.100",
	)

	// Verify result
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, apiErr.IsEfloCode(err, 1001))
}

func TestEFLOService_GetNodeInfoForPod_WithGomonkey(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response
	mockResponse := &eflo.GetNodeInfoForPodResponse{
		Code:      0,
		Message:   "Success",
		RequestId: "test-request-id",
		Content: eflo.Content{
			NodeId:           "node-test-001",
			ZoneId:           "cn-hangzhou-a",
			VpcId:            "vpc-test-001",
			VSwitchId:        "vsw-test-001",
			PrivateIpAddress: "10.0.0.100",
		},
	}

	// Mock GetNodeInfoForPod method
	patches := gomonkey.ApplyFunc(
		(*eflo.Client).GetNodeInfoForPod,
		func(client *eflo.Client, request *eflo.GetNodeInfoForPodRequest) (*eflo.GetNodeInfoForPodResponse, error) {
			// Verify request parameters
			assert.Equal(t, "node-test-001", request.NodeId)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := efloService.GetNodeInfoForPod(
		ctx,
		"node-test-001",
	)

	// Verify result
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "node-test-001", result.NodeId)
	assert.Equal(t, "cn-hangzhou-a", result.ZoneId)
	assert.Equal(t, "vpc-test-001", result.VpcId)
	assert.Equal(t, "vsw-test-001", result.VSwitchId)
	assert.Equal(t, "10.0.0.100", result.PrivateIpAddress)
}

func TestEFLOService_GetNodeInfoForPod_WithGomonkey_ErrorCode(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response with non-zero code
	mockResponse := &eflo.GetNodeInfoForPodResponse{
		Code:      1001,
		Message:   "Resource not found",
		RequestId: "test-request-id",
		Content:   eflo.Content{},
	}

	// Mock GetNodeInfoForPod method
	patches := gomonkey.ApplyFunc(
		(*eflo.Client).GetNodeInfoForPod,
		func(client *eflo.Client, request *eflo.GetNodeInfoForPodRequest) (*eflo.GetNodeInfoForPodResponse, error) {
			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := efloService.GetNodeInfoForPod(
		ctx,
		"node-test-001",
	)

	// Verify result
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, apiErr.IsEfloCode(err, 1001))
}

func TestEFLOService_UnassignLeniPrivateIPAddress_WithGomonkey_ErrorCode(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response with non-zero code (not 1011, so it should return error)
	mockResponse := &eflo.UnassignLeniPrivateIpAddressResponse{
		Code:      1001,
		Message:   "Resource not found",
		RequestId: "test-request-id",
		Content:   eflo.Content{},
	}

	// Mock UnassignLeniPrivateIpAddress method
	patches := gomonkey.ApplyFunc(
		(*eflo.Client).UnassignLeniPrivateIpAddress,
		func(client *eflo.Client, request *eflo.UnassignLeniPrivateIpAddressRequest) (*eflo.UnassignLeniPrivateIpAddressResponse, error) {
			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := efloService.UnassignLeniPrivateIPAddress(
		ctx,
		"leni-test-001",
		"ip-test-001",
	)

	// Verify result
	assert.Error(t, err)
	assert.True(t, apiErr.IsEfloCode(err, 1001))
}

// Helper functions
func int32Ptr(i int32) *int32 {
	return &i
}

// Option functions for EFLO Service
func WithVSwitchIDForEFLO(vswitchID string) CreateNetworkInterfaceOption {
	return &CreateNetworkInterfaceOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			VSwitchID: vswitchID,
		},
	}
}

func WithSecurityGroupIDsForEFLO(securityGroupIDs []string) CreateNetworkInterfaceOption {
	return &CreateNetworkInterfaceOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			SecurityGroupIDs: securityGroupIDs,
		},
	}
}

func WithZoneIDForEFLO(zoneID string) CreateNetworkInterfaceOption {
	return &CreateNetworkInterfaceOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			ZoneID: zoneID,
		},
	}
}

func WithVPCIDForEFLO(vpcID string) DescribeNetworkInterfaceOption {
	return &DescribeNetworkInterfaceOptions{
		VPCID: &vpcID,
	}
}

func WithNetworkInterfaceIDsForEFLO(networkInterfaceIDs []string) DescribeNetworkInterfaceOption {
	return &DescribeNetworkInterfaceOptions{
		NetworkInterfaceIDs: &networkInterfaceIDs,
	}
}

func WithStatusForEFLO(status string) DescribeNetworkInterfaceOption {
	return &DescribeNetworkInterfaceOptions{
		Status: &status,
	}
}

func WithNetworkInterfaceIDForEFLOAssign(networkInterfaceID string) AssignPrivateIPAddressOption {
	return &AssignPrivateIPAddressOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: networkInterfaceID,
			IPCount:            1, // EFLO only supports single IP assignment
		},
	}
}

func WithIPCountForEFLOAssign(ipCount int) AssignPrivateIPAddressOption {
	return &AssignPrivateIPAddressOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			IPCount: ipCount,
		},
	}
}

func WithNetworkInterfaceIDForEFLOAttach(networkInterfaceID string) AttachNetworkInterfaceOption {
	return &AttachNetworkInterfaceOptions{
		NetworkInterfaceID: &networkInterfaceID,
	}
}

func WithInstanceIDForEFLOAttach(instanceID string) AttachNetworkInterfaceOption {
	return &AttachNetworkInterfaceOptions{
		InstanceID: &instanceID,
	}
}

func WithNetworkCardIndexForEFLOAttach(networkCardIndex int) AttachNetworkInterfaceOption {
	return &AttachNetworkInterfaceOptions{
		NetworkCardIndex: &networkCardIndex,
	}
}

func WithNetworkInterfaceIDForEFLODetach(networkInterfaceID string) DetachNetworkInterfaceOption {
	return &DetachNetworkInterfaceOptions{
		NetworkInterfaceID: &networkInterfaceID,
	}
}

func WithInstanceIDForEFLODetach(instanceID string) DetachNetworkInterfaceOption {
	return &DetachNetworkInterfaceOptions{
		InstanceID: &instanceID,
	}
}

func TestEFLOService_UnassignLeniPrivateIPAddress_WithGomonkey(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response
	mockResponse := &eflo.UnassignLeniPrivateIpAddressResponse{
		Code:      0,
		Message:   "Success",
		RequestId: "test-request-id",
	}

	// Mock UnassignLeniPrivateIpAddress method
	patches := gomonkey.ApplyFunc(
		(*eflo.Client).UnassignLeniPrivateIpAddress,
		func(client *eflo.Client, request *eflo.UnassignLeniPrivateIpAddressRequest) (*eflo.UnassignLeniPrivateIpAddressResponse, error) {
			// Verify request parameters
			assert.Equal(t, "leni-test-001", request.ElasticNetworkInterfaceId)
			assert.Equal(t, "ip-test-001", request.IpName)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := efloService.UnassignLeniPrivateIPAddress(
		ctx,
		"leni-test-001",
		"ip-test-001",
	)

	// Verify result
	assert.NoError(t, err)
}

func TestEFLOService_WaitForLeniNetworkInterface_WithGomonkey(t *testing.T) {
	efloService := createTestEFLOServiceForAPI()

	// Mock response for DescribeLeniNetworkInterface
	mockResponse := &eflo.ListElasticNetworkInterfacesResponse{
		Code:      0,
		Message:   "Success",
		RequestId: "test-request-id",
		Content: eflo.Content{
			Data: []eflo.DataItem{
				{
					ElasticNetworkInterfaceId: "leni-test-001",
					Status:                    "Available",
					ZoneId:                    "cn-hangzhou-a",
					VpcId:                     "vpc-test-001",
					VSwitchId:                 "vsw-test-001",
					Ip:                        "10.0.0.100",
					Mac:                       "02:11:22:33:44:55",
					Type:                      "CUSTOM",
					SecurityGroupId:           "sg-test-001",
					NodeId:                    "node-test-001",
					ResourceGroupId:           "rg-test-001",
				},
			},
		},
	}

	// Mock ListElasticNetworkInterfaces method
	patches1 := gomonkey.ApplyFunc(
		(*eflo.Client).ListElasticNetworkInterfaces,
		func(client *eflo.Client, request *eflo.ListElasticNetworkInterfacesRequest) (*eflo.ListElasticNetworkInterfacesResponse, error) {
			// Verify request parameters
			assert.Equal(t, "leni-test-001", request.ElasticNetworkInterfaceId)
			assert.Equal(t, "Available", request.Status)

			return mockResponse, nil
		},
	)
	defer patches1.Reset()

	// Mock ListLeniPrivateIPAddresses method
	patches2 := gomonkey.ApplyFunc(
		(*eflo.Client).ListLeniPrivateIpAddresses,
		func(client *eflo.Client, request *eflo.ListLeniPrivateIpAddressesRequest) (*eflo.ListLeniPrivateIpAddressesResponse, error) {
			// Mock response for ListLeniPrivateIPAddresses
			mockIPResponse := &eflo.ListLeniPrivateIpAddressesResponse{
				Code:      0,
				Message:   "Success",
				RequestId: "test-request-id-ips",
				Content: eflo.Content{
					Data: []eflo.DataItem{
						{
							PrivateIpAddress: "10.0.0.101",
							IpName:           "ip-test-001",
							Status:           "Available",
						},
					},
				},
			}
			return mockIPResponse, nil
		},
	)
	defer patches2.Reset()

	// Execute test
	ctx := context.Background()
	backoff := wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   1,
		Jitter:   0,
		Steps:    1,
	}
	result, err := efloService.WaitForLeniNetworkInterface(
		ctx,
		"leni-test-001",
		"InUse", // EFLO converts "Available" to "InUse"
		backoff,
		false,
	)

	// Verify result
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "leni-test-001", result.NetworkInterfaceID)
	assert.Equal(t, "InUse", result.Status) // EFLO converts "Available" to "InUse"
	assert.Equal(t, "cn-hangzhou-a", result.ZoneID)
	assert.Equal(t, "vpc-test-001", result.VPCID)
	assert.Equal(t, "vsw-test-001", result.VSwitchID)
	assert.Equal(t, "10.0.0.100", result.PrivateIPAddress)
	assert.Equal(t, "02:11:22:33:44:55", result.MacAddress)
}
