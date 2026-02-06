package client_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
)

func TestGetInstanceType(t *testing.T) {
	tests := []struct {
		name     string
		input    *ecs.InstanceType
		expected *client.Limits
	}{
		{
			name: "Basic instance type",
			input: &ecs.InstanceType{
				EniQuantity:                 4,
				EniPrivateIpAddressQuantity: 5,
				EniIpv6AddressQuantity:      10,
				EniTotalQuantity:            6,
				EriQuantity:                 2,
				InstanceBandwidthRx:         1000,
				InstanceBandwidthTx:         500,
				EniTrunkSupported:           true,
			},
			expected: &client.Limits{
				Adapters:              4,
				TotalAdapters:         6,
				IPv4PerAdapter:        5,
				IPv6PerAdapter:        10,
				MemberAdapterLimit:    2,
				MaxMemberAdapterLimit: 4,
				ERdmaAdapters:         2,
				InstanceBandwidthRx:   1000,
				InstanceBandwidthTx:   500,
			},
		},
		{
			name: "Trunk not supported",
			input: &ecs.InstanceType{
				EniQuantity:                 4,
				EniPrivateIpAddressQuantity: 5,
				EniIpv6AddressQuantity:      10,
				EniTotalQuantity:            6,
				EriQuantity:                 2,
				InstanceBandwidthRx:         1000,
				InstanceBandwidthTx:         500,
				EniTrunkSupported:           false,
			},
			expected: &client.Limits{
				Adapters:              4,
				TotalAdapters:         6,
				IPv4PerAdapter:        5,
				IPv6PerAdapter:        10,
				MemberAdapterLimit:    0,
				MaxMemberAdapterLimit: 0,
				ERdmaAdapters:         2,
				InstanceBandwidthRx:   1000,
				InstanceBandwidthTx:   500,
			},
		},
		{
			name: "multi card",
			input: &ecs.InstanceType{
				EniQuantity:                 4,
				EniPrivateIpAddressQuantity: 5,
				EniIpv6AddressQuantity:      10,
				EniTotalQuantity:            6,
				EriQuantity:                 2,
				InstanceBandwidthRx:         1000,
				InstanceBandwidthTx:         500,
				EniTrunkSupported:           false,
				NetworkCards: ecs.NetworkCards{
					NetworkCardInfo: []ecs.NetworkCardInfo{
						{
							NetworkCardIndex: 0,
						},
						{
							NetworkCardIndex: 1,
						},
					},
				},
			},
			expected: &client.Limits{
				Adapters:              4,
				TotalAdapters:         6,
				IPv4PerAdapter:        5,
				IPv6PerAdapter:        10,
				MemberAdapterLimit:    0,
				MaxMemberAdapterLimit: 0,
				ERdmaAdapters:         2,
				InstanceBandwidthRx:   1000,
				InstanceBandwidthTx:   500,
				NetworkCards: []client.NetworkCard{
					{
						Index: 0,
					},
					{
						Index: 1,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := client.GetInstanceType(tt.input)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestGetERIRes(t *testing.T) {
	tests := []struct {
		name     string
		input    *ecs.InstanceType
		expected int
	}{
		{
			name: "not support instance type",
			input: &ecs.InstanceType{
				EniQuantity:                 2,
				EniPrivateIpAddressQuantity: 5,
				EniIpv6AddressQuantity:      10,
				EniTotalQuantity:            6,
				EriQuantity:                 0,
				InstanceBandwidthRx:         1000,
				InstanceBandwidthTx:         500,
				EniTrunkSupported:           true,
			},
			expected: 0,
		},
		{
			name: "Small instance type",
			input: &ecs.InstanceType{
				EniQuantity:                 2,
				EniPrivateIpAddressQuantity: 5,
				EniIpv6AddressQuantity:      10,
				EniTotalQuantity:            6,
				EriQuantity:                 2,
				InstanceBandwidthRx:         1000,
				InstanceBandwidthTx:         500,
				EniTrunkSupported:           true,
			},
			expected: 0,
		},
		{
			name: "Basic instance type",
			input: &ecs.InstanceType{
				EniQuantity:                 4,
				EniPrivateIpAddressQuantity: 5,
				EniIpv6AddressQuantity:      10,
				EniTotalQuantity:            6,
				EriQuantity:                 2,
				InstanceBandwidthRx:         1000,
				InstanceBandwidthTx:         500,
				EniTrunkSupported:           true,
			},
			expected: 1,
		},
		{
			name: "giant instance type only one eri",
			input: &ecs.InstanceType{
				EniQuantity:                 8,
				EniPrivateIpAddressQuantity: 5,
				EniIpv6AddressQuantity:      10,
				EniTotalQuantity:            10,
				EriQuantity:                 1,
				InstanceBandwidthRx:         1000,
				InstanceBandwidthTx:         500,
				EniTrunkSupported:           true,
			},
			expected: 1,
		},
		{
			name: "giant instance type",
			input: &ecs.InstanceType{
				EniQuantity:                 8,
				EniPrivateIpAddressQuantity: 5,
				EniIpv6AddressQuantity:      10,
				EniTotalQuantity:            10,
				EriQuantity:                 4,
				InstanceBandwidthRx:         1000,
				InstanceBandwidthTx:         500,
				EniTrunkSupported:           true,
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := client.GetInstanceType(tt.input)
			assert.Equal(t, tt.expected, actual.ERDMARes())
		})
	}
}

func TestLimits_ExclusiveENIPod(t *testing.T) {
	l := &client.Limits{Adapters: 5}
	assert.Equal(t, 4, l.ExclusiveENIPod())

	l.Adapters = 1
	assert.Equal(t, 0, l.ExclusiveENIPod())
}

func TestProvider_GetLimit_UnsupportedClient(t *testing.T) {
	provider := client.NewProvider()
	_, err := provider.GetLimit("not a client", "ecs.g7.large")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestProvider_GetLimitFromAnno_InvalidJSON(t *testing.T) {
	d := &client.Provider{}
	_, err := d.GetLimitFromAnno(map[string]string{"alibabacloud.com/instance-type-info": "invalid-json"})
	assert.Error(t, err)
}

func TestECSLimitProvider_GetLimitFromAnno(t *testing.T) {

	type args struct {
		anno map[string]string
	}
	tests := []struct {
		name    string
		args    args
		want    *client.Limits
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "test value",
			args: args{
				anno: map[string]string{
					"alibabacloud.com/instance-type-info": "{\"InstancePpsTx\":24000000,\"NvmeSupport\":\"unsupported\",\"PrimaryEniQueueNumber\":32,\"TotalEniQueueQuantity\":528,\"EniTrunkSupported\":true,\"InstanceTypeFamily\":\"ecs.ebmre7p\",\"InstancePpsRx\":24000000,\"EriQuantity\":0,\"InstanceBandwidthRx\":65536000,\"EnhancedNetwork\":{},\"InstanceBandwidthTx\":65536000,\"SecondaryEniQueueNumber\":16,\"LocalStorageCategory\":\"\",\"InstanceTypeId\":\"ecs.ebmre7p.32xlarge\",\"EniIpv6AddressQuantity\":1,\"EniTotalQuantity\":110,\"EniQuantity\":32,\"DiskQuantity\":17,\"EniPrivateIpAddressQuantity\":15}",
				},
			},
			want: &client.Limits{
				InstanceTypeID:        "ecs.ebmre7p.32xlarge",
				Adapters:              32,
				TotalAdapters:         110,
				IPv4PerAdapter:        15,
				IPv6PerAdapter:        1,
				MemberAdapterLimit:    78,
				MaxMemberAdapterLimit: 108,
				ERdmaAdapters:         0,
				InstanceBandwidthRx:   65536000,
				InstanceBandwidthTx:   65536000,
			},
			wantErr: assert.NoError,
		},
		{
			name: "test empty",
			args: args{
				anno: map[string]string{},
			},
			want:    nil,
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &client.Provider{}
			got, err := d.GetLimitFromAnno(tt.args.anno)
			if !tt.wantErr(t, err, fmt.Sprintf("GetLimitFromAnno(%v)", tt.args.anno)) {
				return
			}
			assert.Equalf(t, tt.want, got, "GetLimitFromAnno(%v)", tt.args.anno)
		})
	}
}

func TestProvider_GetLimit_ECS_Integration(t *testing.T) {
	mockECS := &mocks.ECS{}

	// Set up mock expectations
	mockECS.On("DescribeInstanceTypes", mock.Anything, []string{"ecs.g7.large"}).Return([]ecs.InstanceType{
		{
			InstanceTypeId:              "ecs.g7.large",
			EniQuantity:                 4,
			EniPrivateIpAddressQuantity: 5,
			EniIpv6AddressQuantity:      10,
			EniTotalQuantity:            6,
			EriQuantity:                 2,
			InstanceBandwidthRx:         1000,
			InstanceBandwidthTx:         500,
			EniTrunkSupported:           false,
		},
	}, nil)

	provider := client.NewProvider()

	// First call, should fetch from API and cache
	limit1, err := provider.GetLimit(mockECS, "ecs.g7.large")
	assert.NoError(t, err)
	assert.NotNil(t, limit1)
	assert.Equal(t, 4, limit1.Adapters)
	assert.Equal(t, 5, limit1.IPv4PerAdapter)

	// Second call, should get from cache
	limit2, err := provider.GetLimit(mockECS, "ecs.g7.large")
	assert.NoError(t, err)
	assert.NotNil(t, limit2)
	assert.Equal(t, limit1, limit2)

	// Verify mock was only called once
	mockECS.AssertNumberOfCalls(t, "DescribeInstanceTypes", 1)
}

func TestProvider_GetLimit_EFLO_Integration(t *testing.T) {
	// Test full integration scenario for EFLO client
	mockEFLO := &mocks.EFLO{}

	// Set up mock expectations
	mockEFLO.On("GetNodeInfoForPod", mock.Anything, "node-123").Return(&eflo.Content{
		LeniQuota:   8,
		LniSipQuota: 20,
	}, nil)

	provider := client.NewProvider()

	// Call method
	limit, err := provider.GetLimit(mockEFLO, "node-123")
	assert.NoError(t, err)
	assert.NotNil(t, limit)
	assert.Equal(t, 8, limit.Adapters)
	assert.Equal(t, 8, limit.TotalAdapters)
	assert.Equal(t, 20, limit.IPv4PerAdapter)

	// Verify mock was called correctly
	mockEFLO.AssertExpectations(t)
}

func TestProvider_GetLimit_EFLOControl_Integration(t *testing.T) {
	// Test full integration scenario for EFLOControl client
	mockEFLOControl := &mocks.EFLOControl{}

	// Set up mock expectations
	mockEFLOControl.On("DescribeNodeType", mock.Anything, mock.MatchedBy(func(req *client.DescribeNodeTypeRequestOptions) bool {
		return req.NodeType != nil && *req.NodeType == "node-type-123"
	})).Return(&client.DescribeNodeTypeResponse{
		EniQuantity:                 4,
		EniPrivateIpAddressQuantity: 10,
		EniHighDenseQuantity:        2,
	}, nil)

	provider := client.NewProvider()

	// First call, should fetch from API and cache
	limit1, err := provider.GetLimit(mockEFLOControl, "node-type-123")
	assert.NoError(t, err)
	assert.NotNil(t, limit1)
	assert.Equal(t, 4, limit1.Adapters)
	assert.Equal(t, 10, limit1.IPv4PerAdapter)
	assert.Equal(t, 2, limit1.HighDenseQuantity)

	// Second call, should get from cache
	limit2, err := provider.GetLimit(mockEFLOControl, "node-type-123")
	assert.NoError(t, err)
	assert.NotNil(t, limit2)
	assert.Equal(t, limit1, limit2)

	// Verify mock was only called once
	mockEFLOControl.AssertNumberOfCalls(t, "DescribeNodeType", 1)
}

func TestProvider_GetLimit_Concurrency(t *testing.T) {
	// Test cache and singleflight mechanism in concurrent scenarios
	mockECS := &mocks.ECS{}

	// Set up mock expectations, simulate API delay
	mockECS.On("DescribeInstanceTypes", mock.Anything, []string{"ecs.g7.large"}).Return([]ecs.InstanceType{
		{
			InstanceTypeId:              "ecs.g7.large",
			EniQuantity:                 4,
			EniPrivateIpAddressQuantity: 5,
			EniIpv6AddressQuantity:      10,
			EniTotalQuantity:            6,
			EriQuantity:                 2,
			InstanceBandwidthRx:         1000,
			InstanceBandwidthTx:         500,
			EniTrunkSupported:           false,
		},
	}, nil).Once() // Ensure it's only called once

	provider := client.NewProvider()

	// Concurrent calls
	const numGoroutines = 10
	results := make(chan *client.Limits, numGoroutines)
	errors := make(chan error, numGoroutines)

	// Use a barrier to ensure all goroutines start at roughly the same time
	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	start := make(chan struct{})

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			<-start // Wait for start signal
			limit, err := provider.GetLimit(mockECS, "ecs.g7.large")
			if err != nil {
				errors <- err
				return
			}
			results <- limit
		}()
	}

	// Start all goroutines at once to maximize concurrency
	close(start)
	wg.Wait()

	// Collect results
	var limits []*client.Limits
	for i := 0; i < numGoroutines; i++ {
		select {
		case limit := <-results:
			limits = append(limits, limit)
		case err := <-errors:
			t.Errorf("Unexpected error: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("Test timeout")
		}
	}

	// Verify all results are the same
	assert.Len(t, limits, numGoroutines)
	for i := 1; i < len(limits); i++ {
		assert.Equal(t, limits[0], limits[i])
	}

	// Verify API was only called once (singleflight mechanism)
	mockECS.AssertNumberOfCalls(t, "DescribeInstanceTypes", 1)
}

func TestLimitsMethods(t *testing.T) {
	l := &client.Limits{
		Adapters:              4,
		IPv4PerAdapter:        5,
		IPv6PerAdapter:        5,
		MemberAdapterLimit:    2,
		MaxMemberAdapterLimit: 4,
	}

	assert.True(t, l.SupportMultiIPIPv6())
	assert.True(t, l.SupportIPv6())
	assert.Equal(t, 2, l.TrunkPod())
	assert.Equal(t, 4, l.MaximumTrunkPod())
	assert.Equal(t, 15, l.MultiIPPod()) // (4-1) * 5

	l.IPv6PerAdapter = 0
	assert.False(t, l.SupportMultiIPIPv6())
	assert.False(t, l.SupportIPv6())

	l.IPv6PerAdapter = 10
	assert.False(t, l.SupportMultiIPIPv6())
	assert.True(t, l.SupportIPv6())
}

func TestGetLimitProvider(t *testing.T) {
	p1 := client.GetLimitProvider()
	assert.NotNil(t, p1)

	p2 := client.GetLimitProvider()
	assert.Equal(t, p1, p2)
}
