package client

import (
	"context"
	"net/netip"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	ecs20140526 "github.com/alibabacloud-go/ecs-20140526/v7/client"
	eflo20220530 "github.com/alibabacloud-go/eflo-20220530/v2/client"
	eflocontroller20221215 "github.com/alibabacloud-go/eflo-controller-20221215/v2/client"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
)

// Mock ClientSet for testing
type mockClientSetForAPI struct {
	ecsClient *ecs.Client
}

func (m *mockClientSetForAPI) ECS() *ecs.Client {
	return m.ecsClient
}

func (m *mockClientSetForAPI) ECSV2() *ecs20140526.Client {
	return nil
}

func (m *mockClientSetForAPI) VPC() *vpc.Client {
	return nil
}

func (m *mockClientSetForAPI) EFLO() *eflo.Client {
	return nil
}

func (m *mockClientSetForAPI) EFLOV2() *eflo20220530.Client {
	return nil
}

func (m *mockClientSetForAPI) EFLOController() *eflocontroller20221215.Client {
	return nil
}

// Helper function to create ECSService for testing
func createTestECSServiceForAPI() *ECSService {
	rateLimiter := NewRateLimiter(LimitConfig{})
	tracer := otel.Tracer("test")
	return &ECSService{
		RateLimiter:      rateLimiter,
		Tracer:           tracer,
		ClientSet:        &mockClientSetForAPI{ecsClient: &ecs.Client{}},
		IdempotentKeyGen: NewIdempotentKeyGenerator(),
	}
}

// Option functions for DescribeNetworkInterface
func WithVPCID(vpcID string) DescribeNetworkInterfaceOption {
	return &DescribeNetworkInterfaceOptions{VPCID: &vpcID}
}

func WithNetworkInterfaceIDs(ids []string) DescribeNetworkInterfaceOption {
	return &DescribeNetworkInterfaceOptions{NetworkInterfaceIDs: &ids}
}

func WithInstanceID(instanceID string) DescribeNetworkInterfaceOption {
	return &DescribeNetworkInterfaceOptions{InstanceID: &instanceID}
}

func WithInstanceType(instanceType string) DescribeNetworkInterfaceOption {
	return &DescribeNetworkInterfaceOptions{InstanceType: &instanceType}
}

func WithStatus(status string) DescribeNetworkInterfaceOption {
	return &DescribeNetworkInterfaceOptions{Status: &status}
}

func WithTags(tags map[string]string) DescribeNetworkInterfaceOption {
	return &DescribeNetworkInterfaceOptions{Tags: &tags}
}

func TestECSService_CreateNetworkInterface_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.CreateNetworkInterfaceResponse{
		RequestId:          "test-request-id",
		NetworkInterfaceId: "eni-test-001",
		Status:             "Available",
		MacAddress:         "02:11:22:33:44:55",
		VSwitchId:          "vsw-test-001",
		PrivateIpAddress:   "10.0.0.100",
		ZoneId:             "cn-hangzhou-a",
		SecurityGroupIds: ecs.SecurityGroupIdsInCreateNetworkInterface{
			SecurityGroupId: []string{"sg-test-001"},
		},
		PrivateIpSets: ecs.PrivateIpSetsInCreateNetworkInterface{
			PrivateIpSet: []ecs.PrivateIpSet{
				{
					PrivateIpAddress: "10.0.0.100",
					Primary:          true,
				},
			},
		},
		Tags: ecs.TagsInCreateNetworkInterface{
			Tag: []ecs.Tag{
				{
					Key:   "Name",
					Value: "test-eni",
				},
			},
		},
		Type:            "Secondary",
		ResourceGroupId: "rg-test-001",
	}

	// Mock CreateNetworkInterface method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).CreateNetworkInterface,
		func(client *ecs.Client, request *ecs.CreateNetworkInterfaceRequest) (*ecs.CreateNetworkInterfaceResponse, error) {
			// Verify request parameters
			assert.Equal(t, "vsw-test-001", request.VSwitchId)
			assert.Equal(t, "Secondary", request.InstanceType)
			assert.Equal(t, 1, len(*request.SecurityGroupIds))
			assert.Equal(t, "sg-test-001", (*request.SecurityGroupIds)[0])
			assert.Equal(t, "rg-test-001", request.ResourceGroupId)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := ecsService.CreateNetworkInterface(
		ctx,
		WithVSwitchIDForCreate("vsw-test-001"),
		WithSecurityGroupIDsForCreate([]string{"sg-test-001"}),
		WithResourceGroupIDForCreate("rg-test-001"),
		WithInstanceTypeForCreate("Secondary"),
		WithTagsForCreate(map[string]string{"Name": "test-eni"}),
	)

	// Verify result
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "Available", result.Status)
	assert.Equal(t, "02:11:22:33:44:55", result.MacAddress)
	assert.Equal(t, "eni-test-001", result.NetworkInterfaceID)
	assert.Equal(t, "vsw-test-001", result.VSwitchID)
	assert.Equal(t, "10.0.0.100", result.PrivateIPAddress)
	assert.Equal(t, "cn-hangzhou-a", result.ZoneID)
	assert.Len(t, result.SecurityGroupIDs, 1)
	assert.Equal(t, "sg-test-001", result.SecurityGroupIDs[0])
	assert.Len(t, result.PrivateIPSets, 1)
	assert.Equal(t, "10.0.0.100", result.PrivateIPSets[0].IPAddress)
	assert.True(t, result.PrivateIPSets[0].Primary)
	assert.Len(t, result.Tags, 1)
	assert.Equal(t, "Name", result.Tags[0].Key)
	assert.Equal(t, "test-eni", result.Tags[0].Value)
	assert.Equal(t, "Secondary", result.Type)
	assert.Equal(t, "rg-test-001", result.ResourceGroupID)
}

func TestECSService_AttachNetworkInterface_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.AttachNetworkInterfaceResponse{
		RequestId: "test-request-id",
	}

	// Mock AttachNetworkInterface method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AttachNetworkInterface,
		func(client *ecs.Client, request *ecs.AttachNetworkInterfaceRequest) (*ecs.AttachNetworkInterfaceResponse, error) {
			// Verify request parameters
			assert.Equal(t, "eni-test-001", request.NetworkInterfaceId)
			assert.Equal(t, "i-test-001", request.InstanceId)
			assert.Equal(t, "eni-trunk-001", request.TrunkNetworkInstanceId)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := ecsService.AttachNetworkInterface(
		ctx,
		WithNetworkInterfaceIDForAttach("eni-test-001"),
		WithInstanceIDForAttach("i-test-001"),
		WithTrunkNetworkInstanceIDForAttach("eni-trunk-001"),
		WithNetworkCardIndexForAttach(1),
	)

	// Verify result
	assert.NoError(t, err)
}

func TestECSService_DetachNetworkInterface_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.DetachNetworkInterfaceResponse{
		RequestId: "test-request-id",
	}

	// Mock DetachNetworkInterface method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).DetachNetworkInterface,
		func(client *ecs.Client, request *ecs.DetachNetworkInterfaceRequest) (*ecs.DetachNetworkInterfaceResponse, error) {
			// Verify request parameters
			assert.Equal(t, "eni-test-001", request.NetworkInterfaceId)
			assert.Equal(t, "i-test-001", request.InstanceId)
			assert.Equal(t, "eni-trunk-001", request.TrunkNetworkInstanceId)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := ecsService.DetachNetworkInterface(
		ctx,
		"eni-test-001",
		"i-test-001",
		"eni-trunk-001",
	)

	// Verify result
	assert.NoError(t, err)
}

func TestECSService_DeleteNetworkInterface_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.DeleteNetworkInterfaceResponse{
		RequestId: "test-request-id",
	}

	// Mock DeleteNetworkInterface method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).DeleteNetworkInterface,
		func(client *ecs.Client, request *ecs.DeleteNetworkInterfaceRequest) (*ecs.DeleteNetworkInterfaceResponse, error) {
			// Verify request parameters
			assert.Equal(t, "eni-test-001", request.NetworkInterfaceId)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := ecsService.DeleteNetworkInterface(
		ctx,
		"eni-test-001",
	)

	// Verify result
	assert.NoError(t, err)
}

func TestECSService_AssignPrivateIPAddress_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.AssignPrivateIpAddressesResponse{
		RequestId: "test-request-id",
		AssignedPrivateIpAddressesSet: ecs.AssignedPrivateIpAddressesSet{
			NetworkInterfaceId: "eni-test-001",
			PrivateIpSet: ecs.PrivateIpSetInAssignPrivateIpAddresses{
				PrivateIpAddress: []string{"10.0.0.101", "10.0.0.102"},
			},
		},
	}

	// Mock AssignPrivateIpAddresses method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AssignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.AssignPrivateIpAddressesRequest) (*ecs.AssignPrivateIpAddressesResponse, error) {
			// Verify request parameters
			assert.Equal(t, "eni-test-001", request.NetworkInterfaceId)
			val, _ := request.SecondaryPrivateIpAddressCount.GetValue()
			assert.Equal(t, 2, val)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := ecsService.AssignPrivateIPAddress(
		ctx,
		WithNetworkInterfaceIDForAssign("eni-test-001"),
		WithIPCountForAssign(2),
	)

	// Verify result
	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, netip.MustParseAddr("10.0.0.101"), result[0])
	assert.Equal(t, netip.MustParseAddr("10.0.0.102"), result[1])
}

func TestECSService_UnAssignPrivateIPAddresses_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.UnassignPrivateIpAddressesResponse{
		RequestId: "test-request-id",
	}

	// Mock UnassignPrivateIpAddresses method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).UnassignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.UnassignPrivateIpAddressesRequest) (*ecs.UnassignPrivateIpAddressesResponse, error) {
			// Verify request parameters
			assert.Equal(t, "eni-test-001", request.NetworkInterfaceId)
			assert.Len(t, *request.PrivateIpAddress, 2)
			assert.Equal(t, "10.0.0.101", (*request.PrivateIpAddress)[0])
			assert.Equal(t, "10.0.0.102", (*request.PrivateIpAddress)[1])

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := ecsService.UnAssignPrivateIPAddresses(
		ctx,
		"eni-test-001",
		[]netip.Addr{
			netip.MustParseAddr("10.0.0.101"),
			netip.MustParseAddr("10.0.0.102"),
		},
	)

	// Verify result
	assert.NoError(t, err)
}

func TestECSService_AssignIpv6Addresses_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.AssignIpv6AddressesResponse{
		RequestId: "test-request-id",
		Ipv6Sets: ecs.Ipv6SetsInAssignIpv6Addresses{
			Ipv6Address: []string{"2001:db8::1", "2001:db8::2"},
		},
	}

	// Mock AssignIpv6Addresses method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AssignIpv6Addresses,
		func(client *ecs.Client, request *ecs.AssignIpv6AddressesRequest) (*ecs.AssignIpv6AddressesResponse, error) {
			// Verify request parameters
			assert.Equal(t, "eni-test-001", request.NetworkInterfaceId)
			val, _ := request.Ipv6AddressCount.GetValue()
			assert.Equal(t, 2, val)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := ecsService.AssignIpv6Addresses(
		ctx,
		WithNetworkInterfaceIDForAssignIPv6("eni-test-001"),
		WithIPv6CountForAssignIPv6(2),
	)

	// Verify result
	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, netip.MustParseAddr("2001:db8::1"), result[0])
	assert.Equal(t, netip.MustParseAddr("2001:db8::2"), result[1])
}

func TestECSService_UnAssignIpv6Addresses_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.UnassignIpv6AddressesResponse{
		RequestId: "test-request-id",
	}

	// Mock UnassignIpv6Addresses method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).UnassignIpv6Addresses,
		func(client *ecs.Client, request *ecs.UnassignIpv6AddressesRequest) (*ecs.UnassignIpv6AddressesResponse, error) {
			// Verify request parameters
			assert.Equal(t, "eni-test-001", request.NetworkInterfaceId)
			assert.Len(t, *request.Ipv6Address, 2)
			assert.Equal(t, "2001:db8::1", (*request.Ipv6Address)[0])
			assert.Equal(t, "2001:db8::2", (*request.Ipv6Address)[1])

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := ecsService.UnAssignIpv6Addresses(
		ctx,
		"eni-test-001",
		[]netip.Addr{
			netip.MustParseAddr("2001:db8::1"),
			netip.MustParseAddr("2001:db8::2"),
		},
	)

	// Verify result
	assert.NoError(t, err)
}

func TestECSService_DescribeInstanceTypes_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.DescribeInstanceTypesResponse{
		RequestId: "test-request-id",
		InstanceTypes: ecs.InstanceTypesInDescribeInstanceTypes{
			InstanceType: []ecs.InstanceType{
				{
					InstanceTypeId:              "ecs.c6.large",
					CpuCoreCount:                2,
					MemorySize:                  4.0,
					InstanceFamilyLevel:         "EnterpriseLevel",
					InstanceTypeFamily:          "ecs.c6",
					GPUAmount:                   0,
					GPUSpec:                     "",
					InitialCredit:               0,
					BaselineCredit:              0,
					EniQuantity:                 3,
					EniPrivateIpAddressQuantity: 10,
					EniIpv6AddressQuantity:      10,
					LocalStorageAmount:          0,
					LocalStorageCapacity:        0,
					LocalStorageCategory:        "",
					Generation:                  "ecs-6",
				},
			},
		},
	}

	// Mock DescribeInstanceTypes method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).DescribeInstanceTypes,
		func(client *ecs.Client, request *ecs.DescribeInstanceTypesRequest) (*ecs.DescribeInstanceTypesResponse, error) {
			// Verify request parameters
			assert.Len(t, *request.InstanceTypes, 1)
			assert.Equal(t, "ecs.c6.large", (*request.InstanceTypes)[0])

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := ecsService.DescribeInstanceTypes(
		ctx,
		[]string{"ecs.c6.large"},
	)

	// Verify result
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "ecs.c6.large", result[0].InstanceTypeId)
	assert.Equal(t, 2, result[0].CpuCoreCount)
	assert.Equal(t, 4.0, result[0].MemorySize)
	assert.Equal(t, "EnterpriseLevel", result[0].InstanceFamilyLevel)
	assert.Equal(t, "ecs.c6", result[0].InstanceTypeFamily)
	assert.Equal(t, 3, result[0].EniQuantity)
	assert.Equal(t, 10, result[0].EniPrivateIpAddressQuantity)
	assert.Equal(t, 10, result[0].EniIpv6AddressQuantity)
	assert.Equal(t, "ecs-6", result[0].Generation)
}

// Option functions for testing
func WithVSwitchIDForCreate(vswitchID string) CreateNetworkInterfaceOption {
	return &CreateNetworkInterfaceOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			VSwitchID: vswitchID,
		},
	}
}

func WithSecurityGroupIDsForCreate(securityGroupIDs []string) CreateNetworkInterfaceOption {
	return &CreateNetworkInterfaceOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			SecurityGroupIDs: securityGroupIDs,
		},
	}
}

func WithResourceGroupIDForCreate(resourceGroupID string) CreateNetworkInterfaceOption {
	return &CreateNetworkInterfaceOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			ResourceGroupID: resourceGroupID,
		},
	}
}

func WithInstanceTypeForCreate(instanceType string) CreateNetworkInterfaceOption {
	return &CreateNetworkInterfaceOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			InstanceType: instanceType,
		},
	}
}

func WithTagsForCreate(tags map[string]string) CreateNetworkInterfaceOption {
	return &CreateNetworkInterfaceOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			Tags: tags,
		},
	}
}

func WithNetworkInterfaceIDForAttach(networkInterfaceID string) AttachNetworkInterfaceOption {
	return &AttachNetworkInterfaceOptions{
		NetworkInterfaceID: &networkInterfaceID,
	}
}

func WithInstanceIDForAttach(instanceID string) AttachNetworkInterfaceOption {
	return &AttachNetworkInterfaceOptions{
		InstanceID: &instanceID,
	}
}

func WithTrunkNetworkInstanceIDForAttach(trunkNetworkInstanceID string) AttachNetworkInterfaceOption {
	return &AttachNetworkInterfaceOptions{
		TrunkNetworkInstanceID: &trunkNetworkInstanceID,
	}
}

func WithNetworkCardIndexForAttach(networkCardIndex int) AttachNetworkInterfaceOption {
	return &AttachNetworkInterfaceOptions{
		NetworkCardIndex: &networkCardIndex,
	}
}

func WithNetworkInterfaceIDForAssign(networkInterfaceID string) AssignPrivateIPAddressOption {
	return &AssignPrivateIPAddressOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: networkInterfaceID,
		},
	}
}

func WithIPCountForAssign(ipCount int) AssignPrivateIPAddressOption {
	return &AssignPrivateIPAddressOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: "eni-test-001",
			IPCount:            ipCount,
		},
	}
}

func WithNetworkInterfaceIDForAssignIPv6(networkInterfaceID string) AssignIPv6AddressesOption {
	return &AssignIPv6AddressesOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: networkInterfaceID,
		},
	}
}

func WithIPv6CountForAssignIPv6(ipv6Count int) AssignIPv6AddressesOption {
	return &AssignIPv6AddressesOptions{
		NetworkInterfaceOptions: &NetworkInterfaceOptions{
			NetworkInterfaceID: "eni-test-001",
			IPv6Count:          ipv6Count,
		},
	}
}

func TestECSService_AssignPrivateIPAddress2_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.AssignPrivateIpAddressesResponse{
		RequestId: "test-request-id",
		AssignedPrivateIpAddressesSet: ecs.AssignedPrivateIpAddressesSet{
			NetworkInterfaceId: "eni-test-001",
			PrivateIpSet: ecs.PrivateIpSetInAssignPrivateIpAddresses{
				PrivateIpAddress: []string{"10.0.0.101", "10.0.0.102"},
			},
		},
	}

	// Mock AssignPrivateIpAddresses method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AssignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.AssignPrivateIpAddressesRequest) (*ecs.AssignPrivateIpAddressesResponse, error) {
			// Verify request parameters
			assert.Equal(t, "eni-test-001", request.NetworkInterfaceId)
			val, _ := request.SecondaryPrivateIpAddressCount.GetValue()
			assert.Equal(t, 2, val)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := ecsService.AssignPrivateIPAddress2(
		ctx,
		WithNetworkInterfaceIDForAssign("eni-test-001"),
		WithIPCountForAssign(2),
	)

	// Verify result
	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "10.0.0.101", result[0].IPAddress)
	assert.False(t, result[0].Primary)
	assert.Equal(t, "10.0.0.102", result[1].IPAddress)
	assert.False(t, result[1].Primary)
}

func TestECSService_UnAssignPrivateIPAddresses2_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.UnassignPrivateIpAddressesResponse{
		RequestId: "test-request-id",
	}

	// Mock UnassignPrivateIpAddresses method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).UnassignPrivateIpAddresses,
		func(client *ecs.Client, request *ecs.UnassignPrivateIpAddressesRequest) (*ecs.UnassignPrivateIpAddressesResponse, error) {
			// Verify request parameters
			assert.Equal(t, "eni-test-001", request.NetworkInterfaceId)
			assert.Len(t, *request.PrivateIpAddress, 2)
			assert.Equal(t, "10.0.0.101", (*request.PrivateIpAddress)[0])
			assert.Equal(t, "10.0.0.102", (*request.PrivateIpAddress)[1])

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := ecsService.UnAssignPrivateIPAddresses2(
		ctx,
		"eni-test-001",
		[]IPSet{
			{IPAddress: "10.0.0.101", Primary: false},
			{IPAddress: "10.0.0.102", Primary: false},
		},
	)

	// Verify result
	assert.NoError(t, err)
}

func TestECSService_AssignIpv6Addresses2_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.AssignIpv6AddressesResponse{
		RequestId: "test-request-id",
		Ipv6Sets: ecs.Ipv6SetsInAssignIpv6Addresses{
			Ipv6Address: []string{"2001:db8::1", "2001:db8::2"},
		},
	}

	// Mock AssignIpv6Addresses method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).AssignIpv6Addresses,
		func(client *ecs.Client, request *ecs.AssignIpv6AddressesRequest) (*ecs.AssignIpv6AddressesResponse, error) {
			// Verify request parameters
			assert.Equal(t, "eni-test-001", request.NetworkInterfaceId)
			val, _ := request.Ipv6AddressCount.GetValue()
			assert.Equal(t, 2, val)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := ecsService.AssignIpv6Addresses2(
		ctx,
		WithNetworkInterfaceIDForAssignIPv6("eni-test-001"),
		WithIPv6CountForAssignIPv6(2),
	)

	// Verify result
	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "2001:db8::1", result[0].IPAddress)
	assert.Equal(t, "2001:db8::2", result[1].IPAddress)
}

func TestECSService_UnAssignIpv6Addresses2_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.UnassignIpv6AddressesResponse{
		RequestId: "test-request-id",
	}

	// Mock UnassignIpv6Addresses method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).UnassignIpv6Addresses,
		func(client *ecs.Client, request *ecs.UnassignIpv6AddressesRequest) (*ecs.UnassignIpv6AddressesResponse, error) {
			// Verify request parameters
			assert.Equal(t, "eni-test-001", request.NetworkInterfaceId)
			assert.Len(t, *request.Ipv6Address, 2)
			assert.Equal(t, "2001:db8::1", (*request.Ipv6Address)[0])
			assert.Equal(t, "2001:db8::2", (*request.Ipv6Address)[1])

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := ecsService.UnAssignIpv6Addresses2(
		ctx,
		"eni-test-001",
		[]IPSet{
			{IPAddress: "2001:db8::1"},
			{IPAddress: "2001:db8::2"},
		},
	)

	// Verify result
	assert.NoError(t, err)
}

func TestECSService_DetachNetworkInterface2_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.DetachNetworkInterfaceResponse{
		RequestId: "test-request-id",
	}

	// Mock DetachNetworkInterface method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).DetachNetworkInterface,
		func(client *ecs.Client, request *ecs.DetachNetworkInterfaceRequest) (*ecs.DetachNetworkInterfaceResponse, error) {
			// Verify request parameters
			assert.Equal(t, "eni-test-001", request.NetworkInterfaceId)
			assert.Equal(t, "i-test-001", request.InstanceId)
			assert.Equal(t, "eni-trunk-001", request.TrunkNetworkInstanceId)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	err := ecsService.DetachNetworkInterface2(
		ctx,
		WithNetworkInterfaceIDForDetach("eni-test-001"),
		WithInstanceIDForDetach("i-test-001"),
		WithTrunkNetworkInstanceIDForDetach("eni-trunk-001"),
	)

	// Verify result
	assert.NoError(t, err)
}

// Option functions for DetachNetworkInterface2
func WithNetworkInterfaceIDForDetach(networkInterfaceID string) DetachNetworkInterfaceOption {
	return &DetachNetworkInterfaceOptions{
		NetworkInterfaceID: &networkInterfaceID,
	}
}

func WithInstanceIDForDetach(instanceID string) DetachNetworkInterfaceOption {
	return &DetachNetworkInterfaceOptions{
		InstanceID: &instanceID,
	}
}

func WithTrunkNetworkInstanceIDForDetach(trunkNetworkInstanceID string) DetachNetworkInterfaceOption {
	return &DetachNetworkInterfaceOptions{
		TrunkID: &trunkNetworkInstanceID,
	}
}

func TestECSService_DescribeNetworkInterface_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.DescribeNetworkInterfacesResponse{
		RequestId: "test-request-id",
		NetworkInterfaceSets: ecs.NetworkInterfaceSets{
			NetworkInterfaceSet: []ecs.NetworkInterfaceSet{
				{
					Status:             "InUse",
					MacAddress:         "02:11:22:33:44:55",
					NetworkInterfaceId: "eni-test-001",
					VSwitchId:          "vsw-test-001",
					PrivateIpAddress:   "10.0.0.100",
					ZoneId:             "cn-hangzhou-a",
					SecurityGroupIds: ecs.SecurityGroupIdsInDescribeNetworkInterfaces{
						SecurityGroupId: []string{"sg-test-001"},
					},
					PrivateIpSets: ecs.PrivateIpSetsInDescribeNetworkInterfaces{
						PrivateIpSet: []ecs.PrivateIpSet{
							{
								PrivateIpAddress: "10.0.0.100",
								Primary:          true,
							},
						},
					},
					Tags: ecs.TagsInDescribeNetworkInterfaces{
						Tag: []ecs.Tag{
							{
								Key:   "Name",
								Value: "test-eni",
							},
						},
					},
					Type:         "Secondary",
					CreationTime: "2024-01-01T00:00:00Z",
					Attachment: ecs.Attachment{
						InstanceId:              "i-test-001",
						TrunkNetworkInterfaceId: "",
						DeviceIndex:             1,
					},
					NetworkInterfaceTrafficMode: "Standard",
				},
			},
		},
	}

	// Mock DescribeNetworkInterfaces method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).DescribeNetworkInterfaces,
		func(client *ecs.Client, request *ecs.DescribeNetworkInterfacesRequest) (*ecs.DescribeNetworkInterfacesResponse, error) {
			// Verify request parameters
			assert.Equal(t, "vpc-test-001", request.VpcId)
			assert.Equal(t, []string{"eni-test-001"}, *request.NetworkInterfaceId)
			assert.Equal(t, "i-test-001", request.InstanceId)
			assert.Equal(t, "Secondary", request.Type)
			assert.Equal(t, "InUse", request.Status)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := ecsService.DescribeNetworkInterface(
		ctx,
		"vpc-test-001",                        // vpcID
		[]string{"eni-test-001"},              // eniID
		"i-test-001",                          // instanceID
		"Secondary",                           // instanceType
		"InUse",                               // status
		map[string]string{"Name": "test-eni"}, // tags
	)

	// Verify result
	assert.NoError(t, err)
	assert.Len(t, result, 1)

	eni := result[0]
	assert.Equal(t, "InUse", eni.Status)
	assert.Equal(t, "02:11:22:33:44:55", eni.MacAddress)
	assert.Equal(t, "eni-test-001", eni.NetworkInterfaceID)
	assert.Equal(t, "vsw-test-001", eni.VSwitchID)
	assert.Equal(t, "10.0.0.100", eni.PrivateIPAddress)
	assert.Equal(t, "cn-hangzhou-a", eni.ZoneID)
	assert.Len(t, eni.SecurityGroupIDs, 1)
	assert.Equal(t, "sg-test-001", eni.SecurityGroupIDs[0])
	assert.Len(t, eni.PrivateIPSets, 1)
	assert.Equal(t, "10.0.0.100", eni.PrivateIPSets[0].IPAddress)
	assert.True(t, eni.PrivateIPSets[0].Primary)
	assert.Len(t, eni.Tags, 1)
	assert.Equal(t, "Name", eni.Tags[0].Key)
	assert.Equal(t, "test-eni", eni.Tags[0].Value)
	assert.Equal(t, "Secondary", eni.Type)
	assert.Equal(t, "2024-01-01T00:00:00Z", eni.CreationTime)
	assert.Equal(t, "i-test-001", eni.InstanceID)
	assert.Equal(t, "", eni.TrunkNetworkInterfaceID)
	assert.Equal(t, 1, eni.DeviceIndex)
	assert.Equal(t, "Standard", eni.NetworkInterfaceTrafficMode)
}

func TestECSService_DescribeNetworkInterface2_WithGomonkey(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	// Mock response
	mockResponse := &ecs.DescribeNetworkInterfacesResponse{
		RequestId: "test-request-id-2",
		NetworkInterfaceSets: ecs.NetworkInterfaceSets{
			NetworkInterfaceSet: []ecs.NetworkInterfaceSet{
				{
					Status:             "Available",
					MacAddress:         "02:11:22:33:44:66",
					NetworkInterfaceId: "eni-test-002",
					VSwitchId:          "vsw-test-002",
					PrivateIpAddress:   "10.0.0.200",
					ZoneId:             "cn-hangzhou-b",
					SecurityGroupIds: ecs.SecurityGroupIdsInDescribeNetworkInterfaces{
						SecurityGroupId: []string{"sg-test-002"},
					},
					PrivateIpSets: ecs.PrivateIpSetsInDescribeNetworkInterfaces{
						PrivateIpSet: []ecs.PrivateIpSet{
							{
								PrivateIpAddress: "10.0.0.200",
								Primary:          true,
							},
						},
					},
					Tags: ecs.TagsInDescribeNetworkInterfaces{
						Tag: []ecs.Tag{
							{
								Key:   "Environment",
								Value: "test",
							},
						},
					},
					Type:         "Primary",
					CreationTime: "2024-01-02T00:00:00Z",
					Attachment: ecs.Attachment{
						InstanceId:              "",
						TrunkNetworkInterfaceId: "",
						DeviceIndex:             0,
					},
					NetworkInterfaceTrafficMode: "Standard",
				},
			},
		},
	}

	// Mock DescribeNetworkInterfaces method
	patches := gomonkey.ApplyFunc(
		(*ecs.Client).DescribeNetworkInterfaces,
		func(client *ecs.Client, request *ecs.DescribeNetworkInterfacesRequest) (*ecs.DescribeNetworkInterfacesResponse, error) {
			// Verify request parameters
			assert.Equal(t, "vpc-test-002", request.VpcId)
			assert.Equal(t, []string{"eni-test-002"}, *request.NetworkInterfaceId)
			assert.Equal(t, "i-test-002", request.InstanceId)
			assert.Equal(t, "Primary", request.Type)
			assert.Equal(t, "Available", request.Status)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := ecsService.DescribeNetworkInterface2(
		ctx,
		WithVPCID("vpc-test-002"),
		WithNetworkInterfaceIDs([]string{"eni-test-002"}),
		WithInstanceID("i-test-002"),
		WithInstanceType("Primary"),
		WithStatus("Available"),
		WithTags(map[string]string{"Environment": "test"}),
	)

	// Verify result
	assert.NoError(t, err)
	assert.Len(t, result, 1)

	eni := result[0]
	assert.Equal(t, "Available", eni.Status)
	assert.Equal(t, "02:11:22:33:44:66", eni.MacAddress)
	assert.Equal(t, "eni-test-002", eni.NetworkInterfaceID)
	assert.Equal(t, "vsw-test-002", eni.VSwitchID)
	assert.Equal(t, "10.0.0.200", eni.PrivateIPAddress)
	assert.Equal(t, "cn-hangzhou-b", eni.ZoneID)
	assert.Len(t, eni.SecurityGroupIDs, 1)
	assert.Equal(t, "sg-test-002", eni.SecurityGroupIDs[0])
	assert.Len(t, eni.PrivateIPSets, 1)
	assert.Equal(t, "10.0.0.200", eni.PrivateIPSets[0].IPAddress)
	assert.True(t, eni.PrivateIPSets[0].Primary)
	assert.Len(t, eni.Tags, 1)
	assert.Equal(t, "Environment", eni.Tags[0].Key)
	assert.Equal(t, "test", eni.Tags[0].Value)
	assert.Equal(t, "Primary", eni.Type)
	assert.Equal(t, "2024-01-02T00:00:00Z", eni.CreationTime)
	assert.Equal(t, "", eni.InstanceID)
	assert.Equal(t, "", eni.TrunkNetworkInterfaceID)
	assert.Equal(t, 0, eni.DeviceIndex)
	assert.Equal(t, "Standard", eni.NetworkInterfaceTrafficMode)
}
