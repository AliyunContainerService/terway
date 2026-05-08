package client

import (
	"testing"

	ecs20140526 "github.com/alibabacloud-go/ecs-20140526/v7/client"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"k8s.io/utils/ptr"
)

func TestFromCreateResp(t *testing.T) {
	resp := &ecs.CreateNetworkInterfaceResponse{
		Status:             "Available",
		MacAddress:         "02:11:22:33:44:55",
		NetworkInterfaceId: "eni-1",
		VSwitchId:          "vsw-1",
		PrivateIpAddress:   "10.0.0.1",
		PrivateIpSets: ecs.PrivateIpSetsInCreateNetworkInterface{
			PrivateIpSet: []ecs.PrivateIpSet{{PrivateIpAddress: "10.0.0.1", Primary: true}},
		},
		ZoneId:           "cn-hangzhou-a",
		SecurityGroupIds: ecs.SecurityGroupIdsInCreateNetworkInterface{SecurityGroupId: []string{"sg-1"}},
		Ipv6Sets: ecs.Ipv6SetsInCreateNetworkInterface{
			Ipv6Set: []ecs.Ipv6Set{{Ipv6Address: "fe80::1"}},
		},
		Tags: ecs.TagsInCreateNetworkInterface{
			Tag: []ecs.Tag{{Key: "k", Value: "v"}},
		},
		Type:            "Primary",
		ResourceGroupId: "rg-1",
	}
	ni := FromCreateResp(resp)
	if ni.Status != "Available" || ni.MacAddress != "02:11:22:33:44:55" || ni.NetworkInterfaceID != "eni-1" || ni.VSwitchID != "vsw-1" || ni.PrivateIPAddress != "10.0.0.1" {
		t.Errorf("unexpected fields: %+v", ni)
	}
	if len(ni.PrivateIPSets) != 1 || ni.PrivateIPSets[0].IPAddress != "10.0.0.1" || !ni.PrivateIPSets[0].Primary {
		t.Errorf("unexpected PrivateIPSets: %+v", ni.PrivateIPSets)
	}
	if len(ni.IPv6Set) != 1 || ni.IPv6Set[0].IPAddress != "fe80::1" {
		t.Errorf("unexpected IPv6Set: %+v", ni.IPv6Set)
	}
	if len(ni.SecurityGroupIDs) != 1 || ni.SecurityGroupIDs[0] != "sg-1" {
		t.Errorf("unexpected SecurityGroupIDs: %+v", ni.SecurityGroupIDs)
	}
	if len(ni.Tags) != 1 || ni.Tags[0].Key != "k" || ni.Tags[0].Value != "v" {
		t.Errorf("unexpected Tags: %+v", ni.Tags)
	}
	if ni.Type != "Primary" || ni.ResourceGroupID != "rg-1" {
		t.Errorf("unexpected Type/ResourceGroupID: %+v", ni)
	}
}

func TestFromDescribeResp(t *testing.T) {
	resp := &ecs.NetworkInterfaceSet{
		Status:             "InUse",
		MacAddress:         "02:11:22:33:44:66",
		NetworkInterfaceId: "eni-2",
		VSwitchId:          "vsw-2",
		PrivateIpAddress:   "10.0.0.2",
		ZoneId:             "cn-hangzhou-b",
		SecurityGroupIds:   ecs.SecurityGroupIdsInDescribeNetworkInterfaces{SecurityGroupId: []string{"sg-2"}},
		Ipv6Sets: ecs.Ipv6SetsInDescribeNetworkInterfaces{
			Ipv6Set: []ecs.Ipv6Set{{Ipv6Address: "fe80::2"}},
		},
		PrivateIpSets: ecs.PrivateIpSetsInDescribeNetworkInterfaces{
			PrivateIpSet: []ecs.PrivateIpSet{{PrivateIpAddress: "10.0.0.2", Primary: false}},
		},
		Tags: ecs.TagsInDescribeNetworkInterfaces{
			Tag: []ecs.Tag{{Key: "k2", Value: "v2"}},
		},
		Type:         "Secondary",
		CreationTime: "2024-01-01T00:00:00Z",
		Attachment: ecs.Attachment{
			InstanceId:              "i-123",
			TrunkNetworkInterfaceId: "eni-trunk",
			DeviceIndex:             1,
		},
		NetworkInterfaceTrafficMode: "Standard",
	}
	ni := FromDescribeResp(resp)
	if ni.Status != "InUse" || ni.MacAddress != "02:11:22:33:44:66" || ni.NetworkInterfaceID != "eni-2" || ni.VSwitchID != "vsw-2" || ni.PrivateIPAddress != "10.0.0.2" {
		t.Errorf("unexpected fields: %+v", ni)
	}
	if len(ni.PrivateIPSets) != 1 || ni.PrivateIPSets[0].IPAddress != "10.0.0.2" || ni.PrivateIPSets[0].Primary {
		t.Errorf("unexpected PrivateIPSets: %+v", ni.PrivateIPSets)
	}
	if len(ni.IPv6Set) != 1 || ni.IPv6Set[0].IPAddress != "fe80::2" {
		t.Errorf("unexpected IPv6Set: %+v", ni.IPv6Set)
	}
	if len(ni.SecurityGroupIDs) != 1 || ni.SecurityGroupIDs[0] != "sg-2" {
		t.Errorf("unexpected SecurityGroupIDs: %+v", ni.SecurityGroupIDs)
	}
	if len(ni.Tags) != 1 || ni.Tags[0].Key != "k2" || ni.Tags[0].Value != "v2" {
		t.Errorf("unexpected Tags: %+v", ni.Tags)
	}
	if ni.Type != "Secondary" || ni.CreationTime != "2024-01-01T00:00:00Z" {
		t.Errorf("unexpected Type/CreationTime: %+v", ni)
	}
	if ni.InstanceID != "i-123" || ni.TrunkNetworkInterfaceID != "eni-trunk" || ni.DeviceIndex != 1 {
		t.Errorf("unexpected Attachment fields: %+v", ni)
	}
	if ni.NetworkInterfaceTrafficMode != "Standard" {
		t.Errorf("unexpected NetworkInterfaceTrafficMode: %+v", ni)
	}
}

func TestFromCreateResp_WithPrefixes(t *testing.T) {
	resp := &ecs.CreateNetworkInterfaceResponse{
		Status:             "Available",
		MacAddress:         "02:aa:bb:cc:dd:ee",
		NetworkInterfaceId: "eni-prefix-create",
		VSwitchId:          "vsw-1",
		PrivateIpAddress:   "10.0.0.1",
		PrivateIpSets: ecs.PrivateIpSetsInCreateNetworkInterface{
			PrivateIpSet: []ecs.PrivateIpSet{{PrivateIpAddress: "10.0.0.1", Primary: true}},
		},
		Ipv4PrefixSets: ecs.Ipv4PrefixSetsInCreateNetworkInterface{
			Ipv4PrefixSet: []ecs.Ipv4PrefixSet{
				{Ipv4Prefix: "10.0.0.16/28"},
				{Ipv4Prefix: "10.0.0.32/28"},
			},
		},
		Ipv6PrefixSets: ecs.Ipv6PrefixSetsInCreateNetworkInterface{
			Ipv6PrefixSet: []ecs.Ipv6PrefixSet{
				{Ipv6Prefix: "2408:4002:10c0::/80"},
			},
		},
	}
	ni := FromCreateResp(resp)
	if len(ni.IPv4PrefixSets) != 2 {
		t.Fatalf("expected 2 IPv4PrefixSets, got %d", len(ni.IPv4PrefixSets))
	}
	if ni.IPv4PrefixSets[0] != Prefix("10.0.0.16/28") || ni.IPv4PrefixSets[1] != Prefix("10.0.0.32/28") {
		t.Errorf("unexpected IPv4PrefixSets: %+v", ni.IPv4PrefixSets)
	}
	if len(ni.IPv6PrefixSets) != 1 {
		t.Fatalf("expected 1 IPv6PrefixSets, got %d", len(ni.IPv6PrefixSets))
	}
	if ni.IPv6PrefixSets[0] != Prefix("2408:4002:10c0::/80") {
		t.Errorf("unexpected IPv6PrefixSets: %+v", ni.IPv6PrefixSets)
	}
	if ni.Type != ENITypeSecondary {
		t.Errorf("expected Type=%s when response Type is empty, got %s", ENITypeSecondary, ni.Type)
	}
}

func TestFromDescribeResp_WithPrefixes(t *testing.T) {
	resp := &ecs.NetworkInterfaceSet{
		Status:             "InUse",
		MacAddress:         "02:aa:bb:cc:dd:ff",
		NetworkInterfaceId: "eni-prefix-desc",
		VSwitchId:          "vsw-2",
		PrivateIpAddress:   "10.0.1.1",
		Type:               "Secondary",
		Ipv4PrefixSets: ecs.Ipv4PrefixSetsInDescribeNetworkInterfaces{
			Ipv4PrefixSet: []ecs.Ipv4PrefixSet{
				{Ipv4Prefix: "10.0.1.0/28"},
			},
		},
		Ipv6PrefixSets: ecs.Ipv6PrefixSetsInDescribeNetworkInterfaces{
			Ipv6PrefixSet: []ecs.Ipv6PrefixSet{
				{Ipv6Prefix: "fd00::/80"},
				{Ipv6Prefix: "fd01::/80"},
			},
		},
	}
	ni := FromDescribeResp(resp)
	if len(ni.IPv4PrefixSets) != 1 || ni.IPv4PrefixSets[0] != Prefix("10.0.1.0/28") {
		t.Errorf("unexpected IPv4PrefixSets: %+v", ni.IPv4PrefixSets)
	}
	if len(ni.IPv6PrefixSets) != 2 {
		t.Fatalf("expected 2 IPv6PrefixSets, got %d", len(ni.IPv6PrefixSets))
	}
	if ni.IPv6PrefixSets[0] != Prefix("fd00::/80") || ni.IPv6PrefixSets[1] != Prefix("fd01::/80") {
		t.Errorf("unexpected IPv6PrefixSets: %+v", ni.IPv6PrefixSets)
	}
}

func TestFromDescribeResp_InstanceIDFromAttachment(t *testing.T) {
	resp := &ecs.NetworkInterfaceSet{
		NetworkInterfaceId: "eni-attach",
		Attachment: ecs.Attachment{
			InstanceId: "i-from-attachment",
		},
	}
	ni := FromDescribeResp(resp)
	if ni.InstanceID != "i-from-attachment" {
		t.Errorf("expected InstanceID from Attachment, got %s", ni.InstanceID)
	}
}

func TestFromPtr(t *testing.T) {
	// nil pointer returns zero value
	var nilStr *string
	if got := FromPtr(nilStr); got != "" {
		t.Errorf("FromPtr(nil *string) = %q, want zero value", got)
	}

	// non-nil pointer returns dereferenced value
	s := "hello"
	if got := FromPtr(&s); got != "hello" {
		t.Errorf("FromPtr(&s) = %q, want \"hello\"", got)
	}

	// int pointer
	var nilInt *int
	if got := FromPtr(nilInt); got != 0 {
		t.Errorf("FromPtr(nil *int) = %d, want 0", got)
	}
	i := 42
	if got := FromPtr(&i); got != 42 {
		t.Errorf("FromPtr(&i) = %d, want 42", got)
	}
}

func TestFromAttributeResp(t *testing.T) {
	resp := &ecs20140526.DescribeNetworkInterfaceAttributeResponseBody{
		Status:             ptr.To("InUse"),
		MacAddress:         ptr.To("02:11:22:33:44:77"),
		NetworkInterfaceId: ptr.To("eni-attr-1"),
		VpcId:              ptr.To("vpc-1"),
		VSwitchId:          ptr.To("vsw-attr"),
		InstanceId:         ptr.To("i-attr-123"),
		PrivateIpAddress:   ptr.To("10.0.0.3"),
		ZoneId:             ptr.To("cn-hangzhou-c"),
		ResourceGroupId:    ptr.To("rg-attr"),
		NetworkInterfaceTrafficMode: ptr.To("Standard"),
		Type:         ptr.To("Secondary"),
		CreationTime: ptr.To("2025-01-01T00:00:00Z"),
		SecurityGroupIds: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodySecurityGroupIds{
			SecurityGroupId: []*string{ptr.To("sg-attr-1"), ptr.To("sg-attr-2")},
		},
		Ipv6Sets: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyIpv6Sets{
			Ipv6Set: []*ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyIpv6SetsIpv6Set{
				{Ipv6Address: ptr.To("fe80::3")},
			},
		},
		PrivateIpSets: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyPrivateIpSets{
			PrivateIpSet: []*ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyPrivateIpSetsPrivateIpSet{
				{PrivateIpAddress: ptr.To("10.0.0.3"), Primary: ptr.To(true)},
				{PrivateIpAddress: ptr.To("10.0.0.4"), Primary: ptr.To(false)},
			},
		},
		Tags: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyTags{
			Tag: []*ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyTagsTag{
				{TagKey: ptr.To("env"), TagValue: ptr.To("prod")},
			},
		},
		Attachment: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyAttachment{
			TrunkNetworkInterfaceId: ptr.To("eni-trunk-attr"),
			DeviceIndex:             ptr.To(int32(2)),
			NetworkCardIndex:        ptr.To(int32(1)),
		},
		Ipv4PrefixSets: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyIpv4PrefixSets{
			Ipv4PrefixSet: []*ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyIpv4PrefixSetsIpv4PrefixSet{
				{Ipv4Prefix: ptr.To("10.0.0.0/28")},
			},
		},
		Ipv6PrefixSets: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyIpv6PrefixSets{
			Ipv6PrefixSet: []*ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyIpv6PrefixSetsIpv6PrefixSet{
				{Ipv6Prefix: ptr.To("fd00::/80")},
			},
		},
	}

	ni := FromAttributeResp(resp)

	if ni.Status != "InUse" || ni.MacAddress != "02:11:22:33:44:77" || ni.NetworkInterfaceID != "eni-attr-1" {
		t.Errorf("unexpected basic fields: %+v", ni)
	}
	if ni.VPCID != "vpc-1" || ni.VSwitchID != "vsw-attr" || ni.InstanceID != "i-attr-123" {
		t.Errorf("unexpected VPC/VSwitch/Instance: %+v", ni)
	}
	if ni.PrivateIPAddress != "10.0.0.3" || ni.ZoneID != "cn-hangzhou-c" || ni.ResourceGroupID != "rg-attr" {
		t.Errorf("unexpected IP/Zone/RG: %+v", ni)
	}
	if ni.NetworkInterfaceTrafficMode != "Standard" || ni.Type != "Secondary" || ni.CreationTime != "2025-01-01T00:00:00Z" {
		t.Errorf("unexpected TrafficMode/Type/CreationTime: %+v", ni)
	}
	if len(ni.SecurityGroupIDs) != 2 || ni.SecurityGroupIDs[0] != "sg-attr-1" || ni.SecurityGroupIDs[1] != "sg-attr-2" {
		t.Errorf("unexpected SecurityGroupIDs: %+v", ni.SecurityGroupIDs)
	}
	if len(ni.IPv6Set) != 1 || ni.IPv6Set[0].IPAddress != "fe80::3" {
		t.Errorf("unexpected IPv6Set: %+v", ni.IPv6Set)
	}
	if len(ni.PrivateIPSets) != 2 || ni.PrivateIPSets[0].IPAddress != "10.0.0.3" || !ni.PrivateIPSets[0].Primary {
		t.Errorf("unexpected PrivateIPSets: %+v", ni.PrivateIPSets)
	}
	if ni.PrivateIPSets[1].IPAddress != "10.0.0.4" || ni.PrivateIPSets[1].Primary {
		t.Errorf("unexpected PrivateIPSets[1]: %+v", ni.PrivateIPSets[1])
	}
	if len(ni.Tags) != 1 || ni.Tags[0].TagKey != "env" || ni.Tags[0].TagValue != "prod" {
		t.Errorf("unexpected Tags: %+v", ni.Tags)
	}
	if ni.TrunkNetworkInterfaceID != "eni-trunk-attr" || ni.DeviceIndex != 2 || ni.NetworkCardIndex != 1 {
		t.Errorf("unexpected Attachment fields: trunk=%s dev=%d card=%d", ni.TrunkNetworkInterfaceID, ni.DeviceIndex, ni.NetworkCardIndex)
	}
	if len(ni.IPv4PrefixSets) != 1 || ni.IPv4PrefixSets[0] != Prefix("10.0.0.0/28") {
		t.Errorf("unexpected IPv4PrefixSets: %+v", ni.IPv4PrefixSets)
	}
	if len(ni.IPv6PrefixSets) != 1 || ni.IPv6PrefixSets[0] != Prefix("fd00::/80") {
		t.Errorf("unexpected IPv6PrefixSets: %+v", ni.IPv6PrefixSets)
	}
}

func TestFromAttributeResp_Nil(t *testing.T) {
	ni := FromAttributeResp(nil)
	if ni != nil {
		t.Errorf("expected nil for nil input, got %+v", ni)
	}
}

func TestFromAttributeResp_InstanceIDFromAttachment(t *testing.T) {
	resp := &ecs20140526.DescribeNetworkInterfaceAttributeResponseBody{
		NetworkInterfaceId: ptr.To("eni-attach-attr"),
		Attachment: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyAttachment{
			InstanceId: ptr.To("i-from-attachment-attr"),
		},
	}
	ni := FromAttributeResp(resp)
	if ni.InstanceID != "i-from-attachment-attr" {
		t.Errorf("expected InstanceID from Attachment, got %s", ni.InstanceID)
	}
}

func TestFromAttributeResp_InstanceIDDirectTakesPrecedence(t *testing.T) {
	resp := &ecs20140526.DescribeNetworkInterfaceAttributeResponseBody{
		NetworkInterfaceId: ptr.To("eni-both"),
		InstanceId:         ptr.To("i-direct"),
		Attachment: &ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyAttachment{
			InstanceId: ptr.To("i-attachment"),
		},
	}
	ni := FromAttributeResp(resp)
	if ni.InstanceID != "i-direct" {
		t.Errorf("expected direct InstanceID to take precedence, got %s", ni.InstanceID)
	}
}
