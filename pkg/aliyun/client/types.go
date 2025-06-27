package client

import (
	"errors"
	"reflect"
	"strings"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/go-logr/logr"
	"github.com/samber/lo"
)

var ErrInvalidArgs = errors.New("invalid args")

// log fields
const (
	LogFieldAPI       = "api"
	LogFieldRequestID = "requestID"
	LogFieldENIID     = "eni"
)

const (
	eniDescription    = "interface create by terway"
	maxSinglePageSize = 500
)

// status for eni
const (
	ENIStatusInUse     string = "InUse"
	ENIStatusAvailable string = "Available"
	ENIStatusAttaching string = "Attaching"
	ENIStatusDetaching string = "Detaching"
	ENIStatusDeleting  string = "Deleting"
)

const (
	LENIStatusAvailable    string = "Available"
	LENIStatusUnattached   string = "Unattached"
	LENIStatusExecuting    string = "Executing"
	LENIStatusAttaching    string = "Attaching"
	LENIStatusDetaching    string = "Detaching"
	LENIStatusCreateFailed string = "Create Failed"
	LENIStatusAttachFailed string = "Attach Failed"
	LENIStatusDeleteFailed string = "Delete Failed"
	LENIStatusDetachFailed string = "Detach Failed"
	LENIStatusDeleting     string = "Deleting"

	LENIIPStatusAvailable string = "Available"
)

const (
	ENITypePrimary   string = "Primary"
	ENITypeSecondary string = "Secondary"
	ENITypeTrunk     string = "Trunk"
	ENITypeMember    string = "Member"
)

const (
	ENITrafficModeRDMA     string = "HighPerformance"
	ENITrafficModeStandard string = "Standard"
)

const EIPInstanceTypeNetworkInterface = "NetworkInterface"

// NetworkInterface openAPI result for ecs.CreateNetworkInterfaceResponse and ecs.NetworkInterfaceSet
type NetworkInterface struct {
	Status             string    `json:"status,omitempty"`
	MacAddress         string    `json:"mac_address,omitempty"`
	NetworkInterfaceID string    `json:"network_interface_id,omitempty"`
	VPCID              string    `json:"vpc_ic,omitempty"`
	VSwitchID          string    `json:"v_switch_id,omitempty"`
	PrivateIPAddress   string    `json:"private_ip_address,omitempty"`
	PrivateIPSets      []IPSet   `json:"private_ip_sets"`
	ZoneID             string    `json:"zone_id,omitempty"`
	SecurityGroupIDs   []string  `json:"security_group_ids,omitempty"`
	ResourceGroupID    string    `json:"resource_group_id,omitempty"`
	IPv6Set            []IPSet   `json:"ipv6_set,omitempty"`
	Tags               []ecs.Tag `json:"tags,omitempty"`

	// fields for DescribeNetworkInterface
	Type                        string `json:"type,omitempty"`
	InstanceID                  string `json:"instance_id,omitempty"`
	TrunkNetworkInterfaceID     string `json:"trunk_network_interface_id,omitempty"`
	NetworkInterfaceTrafficMode string `json:"network_interface_traffic_mode"`
	DeviceIndex                 int    `json:"device_index,omitempty"`
	CreationTime                string `json:"creation_time,omitempty"`
	NetworkCardIndex            int    `json:"network_card_index,omitempty"`

	VfID *uint32 `json:"vf_id,omitempty"`
}

type IPSet struct {
	Primary   bool
	IPAddress string
	IPName    string
	IPStatus  string
}

func FromCreateResp(in *ecs.CreateNetworkInterfaceResponse) *NetworkInterface {
	r := &NetworkInterface{
		Status:             in.Status,
		MacAddress:         in.MacAddress,
		NetworkInterfaceID: in.NetworkInterfaceId,
		VSwitchID:          in.VSwitchId,
		PrivateIPAddress:   in.PrivateIpAddress,
		PrivateIPSets: lo.Map(in.PrivateIpSets.PrivateIpSet, func(item ecs.PrivateIpSet, _ int) IPSet {
			return IPSet{
				IPAddress: item.PrivateIpAddress,
				Primary:   item.Primary,
			}
		}),
		ZoneID:           in.ZoneId,
		SecurityGroupIDs: in.SecurityGroupIds.SecurityGroupId,
		IPv6Set: lo.Map(in.Ipv6Sets.Ipv6Set, func(item ecs.Ipv6Set, _ int) IPSet {
			return IPSet{
				IPAddress: item.Ipv6Address,
			}
		}),
		Tags:            in.Tags.Tag,
		Type:            in.Type,
		ResourceGroupID: in.ResourceGroupId,
	}
	if r.Type == "" {
		r.Type = ENITypeSecondary
	}
	return r
}

func FromDescribeResp(in *ecs.NetworkInterfaceSet) *NetworkInterface {
	ins := in.InstanceId
	if in.InstanceId == "" {
		ins = in.Attachment.InstanceId
	}

	return &NetworkInterface{
		Status:             in.Status,
		MacAddress:         in.MacAddress,
		NetworkInterfaceID: in.NetworkInterfaceId,
		InstanceID:         ins,
		VSwitchID:          in.VSwitchId,
		PrivateIPAddress:   in.PrivateIpAddress,
		ZoneID:             in.ZoneId,
		SecurityGroupIDs:   in.SecurityGroupIds.SecurityGroupId,
		IPv6Set: lo.Map(in.Ipv6Sets.Ipv6Set, func(item ecs.Ipv6Set, _ int) IPSet {
			return IPSet{
				IPAddress: item.Ipv6Address,
			}
		}),
		PrivateIPSets: lo.Map(in.PrivateIpSets.PrivateIpSet, func(item ecs.PrivateIpSet, _ int) IPSet {
			return IPSet{
				IPAddress: item.PrivateIpAddress,
				Primary:   item.Primary,
			}
		}),
		Tags:                        in.Tags.Tag,
		TrunkNetworkInterfaceID:     in.Attachment.TrunkNetworkInterfaceId,
		NetworkInterfaceTrafficMode: in.NetworkInterfaceTrafficMode,
		DeviceIndex:                 in.Attachment.DeviceIndex,
		Type:                        in.Type,
		CreationTime:                in.CreationTime,
	}
}

// LogFields function enhances the provided logger with key-value pairs extracted from the fields of the given object.
//
// Parameters:
// l     - The original logr.Logger instance to be augmented with object field information.
// obj   - An arbitrary object whose fields will be inspected for logging. Must be of a struct type.
//
// Return Value:
// Returns an updated logr.Logger instance that includes key-value pairs for non-empty, non-zero fields of the input object.
// The original logger `l` is modified in place, and the returned logger is a reference to the same instance.
func LogFields(l logr.Logger, obj any) logr.Logger {
	r := l
	t := reflect.TypeOf(obj)

	realObj := obj
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
		objValue := reflect.ValueOf(obj).Elem()
		realObj = objValue.Interface()
	}

	if t.Kind() == reflect.Struct {
		r = r.WithValues(LogFieldAPI, strings.TrimSuffix(t.Name(), "Request"))
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		tagValue := field.Tag.Get("name")
		if tagValue == "" {
			continue
		}

		fieldValue := reflect.ValueOf(realObj).FieldByName(field.Name)
		if !fieldValue.IsValid() || fieldValue.IsZero() {
			continue
		}

		r = r.WithValues(field.Name, fieldValue.Interface())
	}
	return r
}

func FromPtr[T any](ptr *T) T {
	if ptr == nil {
		var zero T
		return zero
	}
	return *ptr
}
