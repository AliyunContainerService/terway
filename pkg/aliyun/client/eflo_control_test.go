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

// Mock ClientSet for EFLO Control testing
type mockClientSetForEFLOControl struct {
	efloControllerClient *eflocontroller20221215.Client
}

func (m *mockClientSetForEFLOControl) ECS() *ecs.Client {
	return nil
}

func (m *mockClientSetForEFLOControl) ECSV2() *ecs20140526.Client {
	return nil
}

func (m *mockClientSetForEFLOControl) VPC() *vpc.Client {
	return nil
}

func (m *mockClientSetForEFLOControl) EFLO() *eflo.Client {
	return nil
}

func (m *mockClientSetForEFLOControl) EFLOV2() *eflo20220530.Client {
	return nil
}

func (m *mockClientSetForEFLOControl) EFLOController() *eflocontroller20221215.Client {
	return m.efloControllerClient
}

// Helper function to create EFLOControlService for testing
func createTestEFLOControlServiceForAPI() *EFLOControlService {
	rateLimiter := NewRateLimiter(LimitConfig{})
	tracer := otel.Tracer("test")
	return &EFLOControlService{
		RateLimiter: rateLimiter,
		Tracer:      tracer,
		ClientSet:   &mockClientSetForEFLOControl{efloControllerClient: &eflocontroller20221215.Client{}},
	}
}

func TestEFLOControlService_DescribeNode_WithGomonkey(t *testing.T) {
	efloControlService := createTestEFLOControlServiceForAPI()

	// Mock response
	mockResponse := &eflocontroller20221215.DescribeNodeResponse{
		Body: &eflocontroller20221215.DescribeNodeResponseBody{
			NodeId:    stringPtr("node-test-001"),
			ZoneId:    stringPtr("cn-hangzhou-a"),
			NodeType:  stringPtr("standard"),
			RequestId: stringPtr("test-request-id"),
		},
	}

	// Mock DescribeNode method
	patches := gomonkey.ApplyFunc(
		(*eflocontroller20221215.Client).DescribeNode,
		func(client *eflocontroller20221215.Client, request *eflocontroller20221215.DescribeNodeRequest) (*eflocontroller20221215.DescribeNodeResponse, error) {
			// Verify request parameters
			assert.Equal(t, "node-test-001", *request.NodeId)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := efloControlService.DescribeNode(
		ctx,
		WithNodeID("node-test-001"),
	)

	// Verify result
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "node-test-001", result.NodeID)
	assert.Equal(t, "cn-hangzhou-a", result.ZoneID)
	assert.Equal(t, "standard", result.NodeType)
}

func TestEFLOControlService_DescribeNodeType_WithGomonkey(t *testing.T) {
	efloControlService := createTestEFLOControlServiceForAPI()

	// Mock response
	mockResponse := &eflocontroller20221215.DescribeNodeTypeResponse{
		Body: &eflocontroller20221215.DescribeNodeTypeResponseBody{
			EniQuantity:                 intPtr(3),
			EniPrivateIpAddressQuantity: intPtr(10),
			EniIpv6AddressQuantity:      intPtr(10),
			EniHighDenseQuantity:        intPtr(1),
			RequestId:                   stringPtr("test-request-id"),
		},
	}

	// Mock DescribeNodeType method
	patches := gomonkey.ApplyFunc(
		(*eflocontroller20221215.Client).DescribeNodeType,
		func(client *eflocontroller20221215.Client, request *eflocontroller20221215.DescribeNodeTypeRequest) (*eflocontroller20221215.DescribeNodeTypeResponse, error) {
			// Verify request parameters
			assert.Equal(t, "node-type-001", *request.NodeType)

			return mockResponse, nil
		},
	)
	defer patches.Reset()

	// Execute test
	ctx := context.Background()
	result, err := efloControlService.DescribeNodeType(
		ctx,
		WithNodeTypeID("node-type-001"),
	)

	// Verify result
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 3, result.EniQuantity)
	assert.Equal(t, 10, result.EniPrivateIpAddressQuantity)
	assert.Equal(t, 10, result.EniIpv6AddressQuantity)
	assert.Equal(t, 1, result.EniHighDenseQuantity)
}

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func intPtr(i int) *int32 {
	val := int32(i)
	return &val
}

// Option functions for DescribeNode
func WithNodeID(nodeID string) DescribeNodeRequestOption {
	return &DescribeNodeRequestOptions{
		NodeID: &nodeID,
	}
}

// Option functions for DescribeNodeType
func WithNodeTypeID(nodeTypeID string) DescribeNodeTypeRequestOption {
	return &DescribeNodeTypeRequestOptions{
		NodeType: &nodeTypeID,
	}
}

func TestNewEFLOControlService(t *testing.T) {
	rateLimiter := NewRateLimiter(nil)
	svc := NewEFLOControlService(nil, rateLimiter, nil)
	assert.NotNil(t, svc)
	assert.Equal(t, rateLimiter, svc.RateLimiter)
	assert.NotNil(t, svc.IdempotentKeyGen)
}

func TestEFLOControlService_DescribeNode_NilBody(t *testing.T) {
	svc := createTestEFLOControlServiceForAPI()
	patches := gomonkey.ApplyFunc(
		(*eflocontroller20221215.Client).DescribeNode,
		func(_ *eflocontroller20221215.Client, _ *eflocontroller20221215.DescribeNodeRequest) (*eflocontroller20221215.DescribeNodeResponse, error) {
			return &eflocontroller20221215.DescribeNodeResponse{Body: nil}, nil
		},
	)
	defer patches.Reset()

	_, err := svc.DescribeNode(context.Background(), WithNodeID("n-1"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestEFLOControlService_DescribeNodeType_NilBody(t *testing.T) {
	svc := createTestEFLOControlServiceForAPI()
	patches := gomonkey.ApplyFunc(
		(*eflocontroller20221215.Client).DescribeNodeType,
		func(_ *eflocontroller20221215.Client, _ *eflocontroller20221215.DescribeNodeTypeRequest) (*eflocontroller20221215.DescribeNodeTypeResponse, error) {
			return &eflocontroller20221215.DescribeNodeTypeResponse{Body: nil}, nil
		},
	)
	defer patches.Reset()

	_, err := svc.DescribeNodeType(context.Background(), WithNodeTypeID("t-1"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}
