package client

import (
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestFilterNetworkInterfacesByTags(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tags   []ecs.Tag
		filter map[string]string
		want   bool
	}{
		{name: "no filter", want: true},
		{name: "empty filter", filter: map[string]string{}, want: true},
		{name: "key value", tags: []ecs.Tag{{Key: "app", Value: "terway"}}, filter: map[string]string{"app": "terway"}, want: true},
		{name: "tag key value", tags: []ecs.Tag{{TagKey: "app", TagValue: "terway"}}, filter: map[string]string{"app": "terway"}, want: true},
		{name: "mixed formats and extra tag", tags: []ecs.Tag{{Key: "app", Value: "terway"}, {TagKey: "cluster", TagValue: "test"}, {Key: "extra", Value: "value"}}, filter: map[string]string{"app": "terway", "cluster": "test"}, want: true},
		{name: "all filters required", tags: []ecs.Tag{{Key: "app", Value: "terway"}}, filter: map[string]string{"app": "terway", "cluster": "test"}},
		{name: "missing tags", filter: map[string]string{"app": "terway"}},
		{name: "wrong value", tags: []ecs.Tag{{Key: "app", Value: "other"}}, filter: map[string]string{"app": "terway"}},
		{name: "both pairs first matches", tags: []ecs.Tag{{Key: "app", Value: "terway", TagKey: "app", TagValue: "other"}}, filter: map[string]string{"app": "terway"}, want: true},
		{name: "both pairs second matches", tags: []ecs.Tag{{Key: "app", Value: "other", TagKey: "app", TagValue: "terway"}}, filter: map[string]string{"app": "terway"}, want: true},
		{name: "no cross pair matching", tags: []ecs.Tag{{Key: "app", TagValue: "terway"}}, filter: map[string]string{"app": "terway"}},
		{name: "no reverse cross pair matching", tags: []ecs.Tag{{TagKey: "app", Value: "terway"}}, filter: map[string]string{"app": "terway"}},
		{name: "empty value", tags: []ecs.Tag{{Key: "app"}}, filter: map[string]string{"app": ""}, want: true},
		{name: "empty tag value", tags: []ecs.Tag{{TagKey: "app"}}, filter: map[string]string{"app": ""}, want: true},
		{name: "empty value must match", tags: []ecs.Tag{{Key: "app", Value: "terway"}}, filter: map[string]string{"app": ""}},
		{name: "empty value missing key", tags: []ecs.Tag{{Key: "other"}}, filter: map[string]string{"app": ""}},
		{name: "empty key does not match absent pair", tags: []ecs.Tag{{Key: "app"}}, filter: map[string]string{"": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eni := &NetworkInterface{NetworkInterfaceID: "eni-test", Tags: tc.tags}
			got := filterNetworkInterfacesByTags([]*NetworkInterface{eni}, tc.filter)
			if tc.want {
				assert.Equal(t, []*NetworkInterface{eni}, got)
			} else {
				assert.Empty(t, got)
			}
			assert.Equal(t, tc.tags, eni.Tags)
		})
	}
	assert.Nil(t, filterNetworkInterfacesByTags(nil, nil))
}
