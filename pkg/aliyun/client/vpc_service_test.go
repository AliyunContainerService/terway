package client

import (
	"context"
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

// Mock ClientSet for VPC Service testing
type mockClientSetForVPC struct {
	vpcClient *vpc.Client
}

func (m *mockClientSetForVPC) ECS() *ecs.Client {
	return nil
}

func (m *mockClientSetForVPC) ECSV2() *ecs20140526.Client {
	return nil
}

func (m *mockClientSetForVPC) VPC() *vpc.Client {
	return m.vpcClient
}

func (m *mockClientSetForVPC) EFLO() *eflo.Client {
	return nil
}

func (m *mockClientSetForVPC) EFLOV2() *eflo20220530.Client {
	return nil
}

func (m *mockClientSetForVPC) EFLOController() *eflocontroller20221215.Client {
	return nil
}

// Helper function to create VPCService for testing
func createTestVPCServiceForAPI() *VPCService {
	rateLimiter := NewRateLimiter(LimitConfig{})
	tracer := otel.Tracer("test")
	return &VPCService{
		RateLimiter: rateLimiter,
		Tracer:      tracer,
		ClientSet:   &mockClientSetForVPC{vpcClient: &vpc.Client{}},
	}
}

func TestVPCService_DescribeVSwitchByID_WithGomonkey(t *testing.T) {
	vpcService := createTestVPCServiceForAPI()

	// Mock response
	mockResponse := &vpc.DescribeVSwitchesResponse{
		RequestId: "test-request-id",
		VSwitches: vpc.VSwitches{
			VSwitch: []vpc.VSwitch{
				{
					VSwitchId:               "vsw-test-001",
					VpcId:                   "vpc-test-001",
					Status:                  "Available",
					CidrBlock:               "10.0.0.0/24",
					ZoneId:                  "cn-hangzhou-a",
					AvailableIpAddressCount: 250,
					Description:             "test-vswitch",
					VSwitchName:             "test-vswitch",
					CreationTime:            "2024-01-01T00:00:00Z",
					IsDefault:               false,
					ResourceGroupId:         "rg-test-001",
					Tags: vpc.TagsInDescribeVSwitches{
						Tag: []vpc.Tag{
							{
								Key:   "Name",
								Value: "test-vswitch",
							},
						},
					},
				},
			},
		},
	}

	// Mock DescribeVSwitches method
	patches := gomonkey.ApplyFunc(
		(*vpc.Client).DescribeVSwitches,
		func(client *vpc.Client, request *vpc.DescribeVSwitchesRequest) (*vpc.DescribeVSwitchesResponse, error) {
			// Verify request parameters
			assert.Equal(t, "vsw-test-001", request.VSwitchId)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := vpcService.DescribeVSwitchByID(
		ctx,
		"vsw-test-001",
	)

	// Verify result
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "vsw-test-001", result.VSwitchId)
	assert.Equal(t, "vpc-test-001", result.VpcId)
	assert.Equal(t, "Available", result.Status)
	assert.Equal(t, "10.0.0.0/24", result.CidrBlock)
	assert.Equal(t, "cn-hangzhou-a", result.ZoneId)
	assert.Equal(t, int64(250), result.AvailableIpAddressCount)
	assert.Equal(t, "test-vswitch", result.Description)
	assert.Equal(t, "test-vswitch", result.VSwitchName)
	assert.Equal(t, "2024-01-01T00:00:00Z", result.CreationTime)
	assert.False(t, result.IsDefault)
	assert.Equal(t, "rg-test-001", result.ResourceGroupId)
	assert.Len(t, result.Tags.Tag, 1)
	assert.Equal(t, "Name", result.Tags.Tag[0].Key)
	assert.Equal(t, "test-vswitch", result.Tags.Tag[0].Value)
}
