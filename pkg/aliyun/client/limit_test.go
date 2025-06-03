package client

import (
	"fmt"
	"testing"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/stretchr/testify/assert"
)

func TestGetInstanceType(t *testing.T) {
	tests := []struct {
		name     string
		input    *ecs.InstanceType
		expected *Limits
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
			expected: &Limits{
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
			expected: &Limits{
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
			expected: &Limits{
				Adapters:              4,
				TotalAdapters:         6,
				IPv4PerAdapter:        5,
				IPv6PerAdapter:        10,
				MemberAdapterLimit:    0,
				MaxMemberAdapterLimit: 0,
				ERdmaAdapters:         2,
				InstanceBandwidthRx:   1000,
				InstanceBandwidthTx:   500,
				NetworkCards: []NetworkCard{
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
			actual := getInstanceType(tt.input)
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
			actual := getInstanceType(tt.input)
			assert.Equal(t, tt.expected, actual.ERDMARes())
		})
	}
}

func TestECSLimitProvider_GetLimitFromAnno(t *testing.T) {

	type args struct {
		anno map[string]string
	}
	tests := []struct {
		name    string
		args    args
		want    *Limits
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "test value",
			args: args{
				anno: map[string]string{
					"alibabacloud.com/instance-type-info": "{\"InstancePpsTx\":24000000,\"NvmeSupport\":\"unsupported\",\"PrimaryEniQueueNumber\":32,\"TotalEniQueueQuantity\":528,\"EniTrunkSupported\":true,\"InstanceTypeFamily\":\"ecs.ebmre7p\",\"InstancePpsRx\":24000000,\"EriQuantity\":0,\"InstanceBandwidthRx\":65536000,\"EnhancedNetwork\":{},\"InstanceBandwidthTx\":65536000,\"SecondaryEniQueueNumber\":16,\"LocalStorageCategory\":\"\",\"InstanceTypeId\":\"ecs.ebmre7p.32xlarge\",\"EniIpv6AddressQuantity\":1,\"EniTotalQuantity\":110,\"EniQuantity\":32,\"DiskQuantity\":17,\"EniPrivateIpAddressQuantity\":15}",
				},
			},
			want: &Limits{
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
			d := &Provider{}
			got, err := d.GetLimitFromAnno(tt.args.anno)
			if !tt.wantErr(t, err, fmt.Sprintf("GetLimitFromAnno(%v)", tt.args.anno)) {
				return
			}
			assert.Equalf(t, tt.want, got, "GetLimitFromAnno(%v)", tt.args.anno)
		})
	}
}
