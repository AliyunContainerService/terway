//go:build default_build

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/util/flowcontrol"
)

type describeTestClient struct {
	credential.Client
	ecsClient *ecs.Client
}

func (c describeTestClient) ECS() *ecs.Client { return c.ecsClient }

func TestDescribeNetworkInterfaceLocalTagsPagination(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		for key := range r.Form {
			require.False(t, strings.HasPrefix(key, "Tag."), "unexpected ECS tag filter: %s", key)
		}
		for key, value := range map[string]string{"VpcId": "vpc-test", "NetworkInterfaceId.1": "eni-test", "InstanceId": "i-test", "Type": "Secondary", "Status": "InUse", "MaxResults": "500"} {
			require.Equal(t, value, r.Form.Get(key), key)
		}
		calls++
		resp := ecs.CreateDescribeNetworkInterfacesResponse()
		switch calls {
		case 1:
			require.Empty(t, r.Form.Get("NextToken"))
			resp.NetworkInterfaceSets.NetworkInterfaceSet = make([]ecs.NetworkInterfaceSet, maxSinglePageSize)
			resp.NextToken = "page-2"
		case 2:
			require.Equal(t, "page-2", r.Form.Get("NextToken"))
			resp.NextToken = "unused-token"
			resp.NetworkInterfaceSets.NetworkInterfaceSet = []ecs.NetworkInterfaceSet{
				{NetworkInterfaceId: "eni-first", Tags: ecs.TagsInDescribeNetworkInterfaces{Tag: []ecs.Tag{{Key: "app", Value: "terway"}, {TagKey: "cluster", TagValue: "test"}}}},
				{NetworkInterfaceId: "eni-partial", Tags: ecs.TagsInDescribeNetworkInterfaces{Tag: []ecs.Tag{{Key: "app", Value: "terway"}}}},
				{NetworkInterfaceId: "eni-second", Tags: ecs.TagsInDescribeNetworkInterfaces{Tag: []ecs.Tag{{TagKey: "app", TagValue: "terway"}, {Key: "cluster", Value: "test"}}}},
			}
		default:
			t.Error("unexpected additional page")
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer server.Close()
	ecsClient, err := ecs.NewClientWithAccessKey("cn-hangzhou", "test", "test")
	require.NoError(t, err)
	ecsClient.Domain = strings.TrimPrefix(server.URL, "http://")
	ecsClient.GetConfig().Scheme = "HTTP"
	api := &OpenAPI{ClientSet: describeTestClient{ecsClient: ecsClient}, ReadOnlyRateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()}
	result, err := api.DescribeNetworkInterface(context.Background(), "vpc-test", []string{"eni-test"}, "i-test", "Secondary", "InUse", map[string]string{"app": "terway", "cluster": "test"})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Len(t, result, 2)
	require.Equal(t, "eni-first", result[0].NetworkInterfaceID)
	require.Equal(t, "eni-second", result[1].NetworkInterfaceID)
}
