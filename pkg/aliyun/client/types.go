package client

import (
	"encoding/hex"
	"math/rand"
	"time"

	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
)

var log = logger.DefaultLogger.WithField("subSys", "openAPI")

// log fields
const (
	LogFieldAPI              = "api"
	LogFieldRequestID        = "requestID"
	LogFieldInstanceID       = "instanceID"
	LogFieldSecondaryIPCount = "secondaryIPCount"
	LogFieldENIID            = "eni"
	LogFieldEIPID            = "eip"
	LogFieldPrivateIP        = "privateIP"
	LogFieldVSwitchID        = "vSwitchID"
	LogFieldSgID             = "securityGroupID"
	LogFieldResourceGroupID  = "resourceGroupID"
)

const (
	eniNamePrefix     = "eni-cni-"
	eniDescription    = "interface create by terway"
	maxSinglePageSize = 500
)

func generateEniName() string {
	b := make([]byte, 3)
	rand.Seed(time.Now().UnixNano())
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}
	return eniNamePrefix + hex.EncodeToString(b)
}

// status for eni
const (
	ENIStatusInUse     string = "InUse"
	ENIStatusAvailable string = "Available"
)

const (
	ENITypePrimary   string = "Primary"
	ENITypeSecondary string = "Secondary"
	ENITypeTrunk     string = "Trunk"
	ENITypeMember    string = "Member"
)

const EIPInstanceTypeNetworkInterface = "NetworkInterface"

// NetworkInterface openAPI result for ecs.CreateNetworkInterfaceResponse and ecs.NetworkInterfaceSet
type NetworkInterface struct {
	Status             string             `json:"status,omitempty"`
	MacAddress         string             `json:"mac_address,omitempty"`
	NetworkInterfaceID string             `json:"network_interface_id,omitempty"`
	VSwitchID          string             `json:"v_switch_id,omitempty"`
	PrivateIPAddress   string             `json:"private_ip_address,omitempty"`
	PrivateIPSets      []ecs.PrivateIpSet `json:"private_ip_sets"`
	ZoneID             string             `json:"zone_id,omitempty"`
	SecurityGroupIDs   []string           `json:"security_group_ids,omitempty"`
	ResourceGroupID    string             `json:"resource_group_id,omitempty"`
	IPv6Set            []ecs.Ipv6Set      `json:"ipv6_set,omitempty"`
	Tags               []ecs.Tag          `json:"tags,omitempty"`

	// fields for DescribeNetworkInterface
	Type                    string `json:"type,omitempty"`
	InstanceID              string `json:"instance_id,omitempty"`
	TrunkNetworkInterfaceID string `json:"trunk_network_interface_id,omitempty"`
	DeviceIndex             int    `json:"device_index,omitempty"`
	CreationTime            string `json:"creation_time,omitempty"`
}

func FromCreateResp(in *ecs.CreateNetworkInterfaceResponse) *NetworkInterface {
	return &NetworkInterface{
		Status:             in.Status,
		MacAddress:         in.MacAddress,
		NetworkInterfaceID: in.NetworkInterfaceId,
		VSwitchID:          in.VSwitchId,
		PrivateIPAddress:   in.PrivateIpAddress,
		PrivateIPSets:      in.PrivateIpSets.PrivateIpSet,
		ZoneID:             in.ZoneId,
		SecurityGroupIDs:   in.SecurityGroupIds.SecurityGroupId,
		IPv6Set:            in.Ipv6Sets.Ipv6Set,
		Tags:               in.Tags.Tag,
		Type:               in.Type,
		ResourceGroupID:    in.ResourceGroupId,
	}
}

func FromDescribeResp(in *ecs.NetworkInterfaceSet) *NetworkInterface {
	ins := in.InstanceId
	if in.InstanceId == "" {
		ins = in.Attachment.InstanceId
	}

	return &NetworkInterface{
		Status:                  in.Status,
		MacAddress:              in.MacAddress,
		NetworkInterfaceID:      in.NetworkInterfaceId,
		InstanceID:              ins,
		VSwitchID:               in.VSwitchId,
		PrivateIPAddress:        in.PrivateIpAddress,
		ZoneID:                  in.ZoneId,
		SecurityGroupIDs:        in.SecurityGroupIds.SecurityGroupId,
		IPv6Set:                 in.Ipv6Sets.Ipv6Set,
		PrivateIPSets:           in.PrivateIpSets.PrivateIpSet,
		Tags:                    in.Tags.Tag,
		TrunkNetworkInterfaceID: in.Attachment.TrunkNetworkInterfaceId,
		DeviceIndex:             in.Attachment.DeviceIndex,
		Type:                    in.Type,
		CreationTime:            in.CreationTime,
	}
}
