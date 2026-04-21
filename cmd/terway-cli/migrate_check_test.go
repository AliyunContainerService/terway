package main

import (
	"context"
	"testing"

	sdkErr "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	aliClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	clientMocks "github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
)

func TestCheckOpenAPIPrerequisiteDescribeErrorIncludesRequestID(t *testing.T) {
	t.Parallel()

	ecsClient := clientMocks.NewECS(t)
	ecsClient.On("DescribeNetworkInterface2", mock.Anything, mock.Anything).
		Return(nil, sdkErr.NewServerError(500, "{\"Code\":\"InternalError\"}", "req-123"))

	passed, msgs := checkOpenAPIPrerequisite(context.Background(), ecsClient, "i-123")
	require.False(t, passed)
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0], "failed to describe ENIs for instance i-123")
	assert.Contains(t, msgs[0], "requestID=req-123")
}

func TestCheckOpenAPIPrerequisiteIncludesObservedENIDetails(t *testing.T) {
	t.Parallel()

	ecsClient := clientMocks.NewECS(t)
	ecsClient.On("DescribeNetworkInterface2", mock.Anything, mock.Anything).
		Return([]*aliClient.NetworkInterface{
			{
				NetworkInterfaceID: "eni-001",
				Type:               aliClient.ENITypeSecondary,
				Status:             aliClient.ENIStatusInUse,
				DeviceIndex:        1,
				Tags: []ecs.Tag{
					{TagKey: "acs:ecs:support_eni", TagValue: "false"},
					{TagKey: "owner", TagValue: "migrate-cli"},
				},
			},
		}, nil)

	passed, msgs := checkOpenAPIPrerequisite(context.Background(), ecsClient, "i-123")
	require.False(t, passed)
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0], "no ENI found with tags leni_primary=true AND acs:ecs:support_eni=true")
	assert.Contains(t, msgs[0], "observedENIs=eni-001,type=Secondary,status=InUse,deviceIndex=1")
	assert.Contains(t, msgs[0], "owner=migrate-cli")
	assert.Contains(t, msgs[0], "acs:ecs:support_eni=false")
}

func TestAppendOpenAPIDetailLimitsObservedENIs(t *testing.T) {
	t.Parallel()

	msg := appendOpenAPIDetail(
		"no ENI found with tags leni_primary=true AND acs:ecs:support_eni=true",
		[]string{"req-1", "req-2"},
		[]string{"eni-1", "eni-2", "eni-3", "eni-4", "eni-5", "eni-6"},
	)

	assert.Contains(t, msg, "requestIDs=req-1,req-2")
	assert.Contains(t, msg, "observedENIs=eni-1; eni-2; eni-3; eni-4; eni-5; ...(+1 more)")
}
