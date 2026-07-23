package client

import (
	"testing"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/stretchr/testify/assert"
)

func TestDefaultENITagBlockList_RecognizesErdmaController(t *testing.T) {
	// The two default entries are the contract with alibabacloud-erdma-controller;
	// changing them is a breaking change for that integration.
	expected := []ENITagBlockListItem{
		{Key: "creator", Value: "alibabacloud-erdma-controller"},
		{Key: "terway.alibabacloud.com/excluded", Value: "true"},
	}
	assert.Equal(t, expected, DefaultENITagBlockList)
}

func TestIsENIBlocked(t *testing.T) {
	rules := []ENITagBlockListItem{
		{Key: "creator", Value: "alibabacloud-erdma-controller"},
		{Key: "terway.alibabacloud.com/excluded", Value: "true"},
	}

	cases := []struct {
		name string
		tags []ecs.Tag
		want bool
	}{
		{name: "no tags", tags: nil, want: false},
		{
			name: "matches first rule",
			tags: []ecs.Tag{{TagKey: "creator", TagValue: "alibabacloud-erdma-controller"}},
			want: true,
		},
		{
			name: "matches second rule",
			tags: []ecs.Tag{{TagKey: "terway.alibabacloud.com/excluded", TagValue: "true"}},
			want: true,
		},
		{
			name: "same key, different value — does NOT match",
			tags: []ecs.Tag{{TagKey: "creator", TagValue: "someone-else"}},
			want: false,
		},
		{
			name: "extra unrelated tags around a matching tag — still matches",
			tags: []ecs.Tag{
				{TagKey: "env", TagValue: "prod"},
				{TagKey: "creator", TagValue: "alibabacloud-erdma-controller"},
				{TagKey: "app", TagValue: "foo"},
			},
			want: true,
		},
		{
			name: "case-sensitive value match",
			tags: []ecs.Tag{{TagKey: "terway.alibabacloud.com/excluded", TagValue: "TRUE"}},
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, IsENIBlocked(c.tags, rules))
		})
	}
}

func TestIsENIBlocked_EmptyBlockListIsAlwaysFalse(t *testing.T) {
	tags := []ecs.Tag{{TagKey: "creator", TagValue: "alibabacloud-erdma-controller"}}
	assert.False(t, IsENIBlocked(tags, nil))
	assert.False(t, IsENIBlocked(tags, []ENITagBlockListItem{}))
}
