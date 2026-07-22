package client

import (
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

// ENITagBlockListItem is an alias for the canonical CRD type so callers within
// the aliyun/client package can refer to it without importing the APIs package.
type ENITagBlockListItem = networkv1beta1.ENITagBlockListItem

// DefaultENITagBlockList enumerates the well-known tags that mark an ENI as
// out-of-scope for terway. The list is opt-out via configuration but applied
// by default so users do not have to know about these tags.
//
// Currently we recognize:
//   - alibabacloud-erdma-controller's own creator tag, and
//   - a generic "please skip me" tag that any future controller can use.
var DefaultENITagBlockList = []ENITagBlockListItem{
	{Key: "creator", Value: "alibabacloud-erdma-controller"},
	{Key: "terway.alibabacloud.com/excluded", Value: "true"},
}

// IsENIBlocked reports whether any (key, value) pair on the ENI matches any
// rule in blockList. Match semantics: case-sensitive, both key and value must
// equal. An empty blockList always returns false.
func IsENIBlocked(tags []ecs.Tag, blockList []ENITagBlockListItem) bool {
	if len(blockList) == 0 || len(tags) == 0 {
		return false
	}
	for _, rule := range blockList {
		for _, t := range tags {
			if t.TagKey == rule.Key && t.TagValue == rule.Value {
				return true
			}
		}
	}
	return false
}
