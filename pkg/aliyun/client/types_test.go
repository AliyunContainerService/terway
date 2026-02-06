package client

import (
	"testing"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
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
