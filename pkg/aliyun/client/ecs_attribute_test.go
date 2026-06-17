package client

import (
	"context"
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	ecs20140526 "github.com/alibabacloud-go/ecs-20140526/v7/client"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/utils/ptr"
)

func TestIsSingleENIQuery(t *testing.T) {
	tests := []struct {
		name       string
		opts       []DescribeNetworkInterfaceOption
		wantID     string
		wantSingle bool
	}{
		{
			name: "single ENI ID, no filters",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-123"},
				},
			},
			wantID:     "eni-123",
			wantSingle: true,
		},
		{
			name: "multiple ENI IDs",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-1", "eni-2"},
				},
			},
			wantSingle: false,
		},
		{
			name:       "no options",
			opts:       nil,
			wantSingle: false,
		},
		{
			name: "nil NetworkInterfaceIDs",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{},
			},
			wantSingle: false,
		},
		{
			name: "empty NetworkInterfaceIDs",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{},
				},
			},
			wantSingle: false,
		},
		{
			name: "single ENI ID with InstanceID filter",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-123"},
					InstanceID:          strToPtr("i-abc"),
				},
			},
			wantSingle: false,
		},
		{
			name: "single ENI ID with VPCID filter",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-123"},
					VPCID:               strToPtr("vpc-abc"),
				},
			},
			wantSingle: false,
		},
		{
			name: "single ENI ID with Tags filter",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-123"},
					Tags:                &map[string]string{"k": "v"},
				},
			},
			wantSingle: false,
		},
		{
			name: "single ENI ID with Status filter",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-123"},
					Status:              strToPtr("InUse"),
				},
			},
			wantSingle: false,
		},
		{
			name: "single ENI ID with InstanceType filter",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-123"},
					InstanceType:        strToPtr("Secondary"),
				},
			},
			wantSingle: false,
		},
		{
			name: "single ENI ID with RawStatus only (no additional filters)",
			opts: []DescribeNetworkInterfaceOption{
				&DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{"eni-123"},
					RawStatus:           boolToPtr(true),
				},
			},
			wantID:     "eni-123",
			wantSingle: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotSingle := isSingleENIQuery(tt.opts)
			assert.Equal(t, tt.wantSingle, gotSingle)
			if tt.wantSingle {
				assert.Equal(t, tt.wantID, gotID)
			}
		})
	}
}

func strToPtr(s string) *string {
	return &s
}

func boolToPtr(b bool) *bool {
	return &b
}

func createTestECSServiceWithV2() *ECSService {
	rateLimiter := NewRateLimiter(LimitConfig{})
	tracer := otel.Tracer("test")
	return &ECSService{
		RateLimiter: rateLimiter,
		Tracer:      tracer,
		ClientSet: &mockClientSetForAPI{
			ecsV2Client: &ecs20140526.Client{},
		},
		IdempotentKeyGen: NewIdempotentKeyGenerator(),
	}
}

func TestECSService_DescribeNetworkInterfaceAttribute_APIError(t *testing.T) {
	ecsService := createTestECSServiceWithV2()

	patches := gomonkey.ApplyFunc(
		(*ecs20140526.Client).DescribeNetworkInterfaceAttribute,
		func(_ *ecs20140526.Client, _ *ecs20140526.DescribeNetworkInterfaceAttributeRequest) (*ecs20140526.DescribeNetworkInterfaceAttributeResponse, error) {
			return nil, fmt.Errorf("network error")
		},
	)
	defer patches.Reset()

	ni, err := ecsService.DescribeNetworkInterfaceAttribute(context.Background(), "eni-test-001")
	assert.Error(t, err)
	assert.Nil(t, ni)
}

func TestECSService_DescribeNetworkInterfaceAttribute_Success(t *testing.T) {
	ecsService := createTestECSServiceWithV2()

	patches := gomonkey.ApplyFunc(
		(*ecs20140526.Client).DescribeNetworkInterfaceAttribute,
		func(_ *ecs20140526.Client, _ *ecs20140526.DescribeNetworkInterfaceAttributeRequest) (*ecs20140526.DescribeNetworkInterfaceAttributeResponse, error) {
			return &ecs20140526.DescribeNetworkInterfaceAttributeResponse{
				Body: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBody{
					NetworkInterfaceId: ptr.To("eni-test-001"),
					Status:             ptr.To("InUse"),
					MacAddress:         ptr.To("00:11:22:33:44:55"),
					VpcId:              ptr.To("vpc-001"),
					VSwitchId:          ptr.To("vsw-001"),
					PrivateIpAddress:   ptr.To("10.0.0.1"),
					ZoneId:             ptr.To("cn-hangzhou-a"),
					Attachment: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyAttachment{
						InstanceId:              ptr.To("i-001"),
						TrunkNetworkInterfaceId: ptr.To("eni-trunk-001"),
						DeviceIndex:             ptr.To(int32(1)),
						NetworkCardIndex:        ptr.To(int32(0)),
					},
					SecurityGroupIds: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodySecurityGroupIds{
						SecurityGroupId: []*string{ptr.To("sg-001")},
					},
				},
			}, nil
		},
	)
	defer patches.Reset()

	ni, err := ecsService.DescribeNetworkInterfaceAttribute(context.Background(), "eni-test-001")
	assert.NoError(t, err)
	assert.NotNil(t, ni)
	assert.Equal(t, "eni-test-001", ni.NetworkInterfaceID)
	assert.Equal(t, "i-001", ni.InstanceID)
	assert.Equal(t, "eni-trunk-001", ni.TrunkNetworkInterfaceID)
	assert.Equal(t, 1, ni.DeviceIndex)
}

func TestFromAttributeResp_AllFields(t *testing.T) {
	body := &ecs20140526.DescribeNetworkInterfaceAttributeResponseBody{
		NetworkInterfaceId: ptr.To("eni-001"),
		Status:             ptr.To("InUse"),
		MacAddress:         ptr.To("aa:bb:cc:dd:ee:ff"),
		VpcId:              ptr.To("vpc-001"),
		VSwitchId:          ptr.To("vsw-001"),
		PrivateIpAddress:   ptr.To("10.0.0.2"),
		ZoneId:             ptr.To("cn-hangzhou-a"),
		InstanceId:         ptr.To("i-001"),
		Ipv6Sets: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyIpv6Sets{
			Ipv6Set: []*ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyIpv6SetsIpv6Set{
				{Ipv6Address: ptr.To("::1")},
			},
		},
		PrivateIpSets: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyPrivateIpSets{
			PrivateIpSet: []*ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyPrivateIpSetsPrivateIpSet{
				{PrivateIpAddress: ptr.To("10.0.0.2"), Primary: ptr.To(true)},
			},
		},
		Tags: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyTags{
			Tag: []*ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyTagsTag{
				{TagKey: ptr.To("k1"), TagValue: ptr.To("v1")},
			},
		},
		Ipv4PrefixSets: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyIpv4PrefixSets{
			Ipv4PrefixSet: []*ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyIpv4PrefixSetsIpv4PrefixSet{
				{Ipv4Prefix: ptr.To("10.0.1.0/24")},
			},
		},
		Ipv6PrefixSets: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyIpv6PrefixSets{
			Ipv6PrefixSet: []*ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyIpv6PrefixSetsIpv6PrefixSet{
				{Ipv6Prefix: ptr.To("fd00::/64")},
			},
		},
	}

	ni := FromAttributeResp(body)
	require.NotNil(t, ni)
	assert.Equal(t, "eni-001", ni.NetworkInterfaceID)
	assert.Equal(t, "i-001", ni.InstanceID)
	assert.Len(t, ni.IPv6Set, 1)
	assert.Equal(t, "::1", ni.IPv6Set[0].IPAddress)
	assert.Len(t, ni.PrivateIPSets, 1)
	assert.Len(t, ni.Tags, 1)
	assert.Equal(t, "k1", ni.Tags[0].TagKey)
	assert.Len(t, ni.IPv4PrefixSets, 1)
	assert.Equal(t, Prefix("10.0.1.0/24"), ni.IPv4PrefixSets[0])
	assert.Len(t, ni.IPv6PrefixSets, 1)
	assert.Equal(t, Prefix("fd00::/64"), ni.IPv6PrefixSets[0])
}

func TestFromAttributeResp_NilInput(t *testing.T) {
	ni := FromAttributeResp(nil)
	assert.Nil(t, ni)
}

func TestFromAttributeResp_NilNetworkInterfaceID(t *testing.T) {
	body := &ecs20140526.DescribeNetworkInterfaceAttributeResponseBody{}
	ni := FromAttributeResp(body)
	assert.Nil(t, ni)
}

func TestFromAttributeResp_AttachmentInstanceID(t *testing.T) {
	// InstanceId is empty, should fall back to Attachment.InstanceId
	body := &ecs20140526.DescribeNetworkInterfaceAttributeResponseBody{
		NetworkInterfaceId: ptr.To("eni-001"),
		Attachment: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyAttachment{
			InstanceId: ptr.To("i-from-attachment"),
		},
	}
	ni := FromAttributeResp(body)
	require.NotNil(t, ni)
	assert.Equal(t, "i-from-attachment", ni.InstanceID)
}

func TestECSService_describeNetworkInterfaceByID_ENIAttributeBasicDisabled(t *testing.T) {
	ecsService := createTestECSServiceForAPI()

	require.NoError(t, utilfeature.DefaultMutableFeatureGate.Set("ENIAttributeBasic=false"))

	patches := gomonkey.ApplyFunc(
		(*ecs.Client).DescribeNetworkInterfaces,
		func(_ *ecs.Client, req *ecs.DescribeNetworkInterfacesRequest) (*ecs.DescribeNetworkInterfacesResponse, error) {
			return &ecs.DescribeNetworkInterfacesResponse{
				NetworkInterfaceSets: ecs.NetworkInterfaceSets{
					NetworkInterfaceSet: []ecs.NetworkInterfaceSet{
						{NetworkInterfaceId: "eni-001"},
					},
				},
			}, nil
		},
	)
	defer patches.Reset()

	result, err := ecsService.describeNetworkInterfaceByID(context.Background(), "eni-001")
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "eni-001", result[0].NetworkInterfaceID)
}

func TestECSService_describeNetworkInterfaceByID_ENIAttributeBasicEnabled_Success(t *testing.T) {
	ecsService := createTestECSServiceWithV2()

	require.NoError(t, utilfeature.DefaultMutableFeatureGate.Set("ENIAttributeBasic=true"))
	defer utilfeature.DefaultMutableFeatureGate.Set("ENIAttributeBasic=false")

	patches := gomonkey.ApplyFunc(
		(*ecs20140526.Client).DescribeNetworkInterfaceAttribute,
		func(_ *ecs20140526.Client, _ *ecs20140526.DescribeNetworkInterfaceAttributeRequest) (*ecs20140526.DescribeNetworkInterfaceAttributeResponse, error) {
			return &ecs20140526.DescribeNetworkInterfaceAttributeResponse{
				Body: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBody{
					NetworkInterfaceId: ptr.To("eni-002"),
					Status:             ptr.To("Available"),
				},
			}, nil
		},
	)
	defer patches.Reset()

	result, err := ecsService.describeNetworkInterfaceByID(context.Background(), "eni-002")
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "eni-002", result[0].NetworkInterfaceID)
}

func TestECSService_describeNetworkInterfaceByID_ENIAttributeBasicEnabled_NilResponse(t *testing.T) {
	ecsService := createTestECSServiceWithV2()

	require.NoError(t, utilfeature.DefaultMutableFeatureGate.Set("ENIAttributeBasic=true"))
	defer utilfeature.DefaultMutableFeatureGate.Set("ENIAttributeBasic=false")

	// Return body with nil NetworkInterfaceId → FromAttributeResp returns nil → describeNetworkInterfaceByID returns nil, nil
	patches := gomonkey.ApplyFunc(
		(*ecs20140526.Client).DescribeNetworkInterfaceAttribute,
		func(_ *ecs20140526.Client, _ *ecs20140526.DescribeNetworkInterfaceAttributeRequest) (*ecs20140526.DescribeNetworkInterfaceAttributeResponse, error) {
			return &ecs20140526.DescribeNetworkInterfaceAttributeResponse{
				Body: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBody{},
			}, nil
		},
	)
	defer patches.Reset()

	result, err := ecsService.describeNetworkInterfaceByID(context.Background(), "eni-003")
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestECSService_describeNetworkInterfaceByID_ENIAttributeBasicEnabled_Error(t *testing.T) {
	ecsService := createTestECSServiceWithV2()

	require.NoError(t, utilfeature.DefaultMutableFeatureGate.Set("ENIAttributeBasic=true"))
	defer utilfeature.DefaultMutableFeatureGate.Set("ENIAttributeBasic=false")

	patches := gomonkey.ApplyFunc(
		(*ecs20140526.Client).DescribeNetworkInterfaceAttribute,
		func(_ *ecs20140526.Client, _ *ecs20140526.DescribeNetworkInterfaceAttributeRequest) (*ecs20140526.DescribeNetworkInterfaceAttributeResponse, error) {
			return nil, fmt.Errorf("api error")
		},
	)
	defer patches.Reset()

	result, err := ecsService.describeNetworkInterfaceByID(context.Background(), "eni-004")
	assert.Error(t, err)
	assert.Nil(t, result)
}
