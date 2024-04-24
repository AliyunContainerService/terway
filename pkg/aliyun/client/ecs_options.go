package client

import (
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"k8s.io/apimachinery/pkg/util/wait"
)

// NetworkInterfaceOptions represents the common options for network interface operations.
type NetworkInterfaceOptions struct {
	Trunk                 bool
	ERDMA                 bool
	VSwitchID             string
	SecurityGroupIDs      []string
	ResourceGroupID       string
	IPCount               int
	IPv6Count             int
	Tags                  map[string]string
	InstanceID            string
	InstanceType          string
	Status                string
	NetworkInterfaceID    string
	DeleteENIOnECSRelease *bool
}

type CreateNetworkInterfaceOption interface {
	ApplyCreateNetworkInterface(*CreateNetworkInterfaceOptions)
}

var _ CreateNetworkInterfaceOption = &CreateNetworkInterfaceOptions{}

type CreateNetworkInterfaceOptions struct {
	NetworkInterfaceOptions *NetworkInterfaceOptions
	Backoff                 *wait.Backoff
}

func (c *CreateNetworkInterfaceOptions) ApplyCreateNetworkInterface(options *CreateNetworkInterfaceOptions) {
	if c.Backoff != nil {
		options.Backoff = c.Backoff
	}
	options.NetworkInterfaceOptions = c.NetworkInterfaceOptions
}

func (c *CreateNetworkInterfaceOptions) Finish(idempotentKeyGen IdempotentKeyGen) (*ecs.CreateNetworkInterfaceRequest, func(), error) {
	if c.NetworkInterfaceOptions == nil ||
		c.NetworkInterfaceOptions.VSwitchID == "" ||
		len(c.NetworkInterfaceOptions.SecurityGroupIDs) == 0 {
		return nil, nil, ErrInvalidArgs
	}

	req := ecs.CreateCreateNetworkInterfaceRequest()
	req.VSwitchId = c.NetworkInterfaceOptions.VSwitchID
	req.InstanceType = ENITypeSecondary
	if c.NetworkInterfaceOptions.Trunk {
		req.InstanceType = ENITypeTrunk
	}
	if c.NetworkInterfaceOptions.ERDMA {
		req.NetworkInterfaceTrafficMode = ENITrafficModeRDMA
	}
	req.SecurityGroupIds = &c.NetworkInterfaceOptions.SecurityGroupIDs
	req.ResourceGroupId = c.NetworkInterfaceOptions.ResourceGroupID
	req.Description = eniDescription
	if c.NetworkInterfaceOptions.IPCount > 1 {
		req.SecondaryPrivateIpAddressCount = requests.NewInteger(c.NetworkInterfaceOptions.IPCount - 1)
	}
	if c.NetworkInterfaceOptions.IPv6Count > 0 {
		req.Ipv6AddressCount = requests.NewInteger(c.NetworkInterfaceOptions.IPv6Count)
	}

	if c.NetworkInterfaceOptions.DeleteENIOnECSRelease != nil {
		req.DeleteOnRelease = requests.NewBoolean(*c.NetworkInterfaceOptions.DeleteENIOnECSRelease)
	}

	var tags []ecs.CreateNetworkInterfaceTag
	for k, v := range c.NetworkInterfaceOptions.Tags {
		tags = append(tags, ecs.CreateNetworkInterfaceTag{
			Key:   k,
			Value: v,
		})
	}
	req.Tag = &tags

	argsHash := md5Hash(req)
	req.ClientToken = idempotentKeyGen.GenerateKey(argsHash)

	if c.Backoff != nil {
		c.Backoff = &wait.Backoff{
			Steps: 1,
		}
	}

	return req, func() {
		idempotentKeyGen.PutBack(argsHash, req.ClientToken)
	}, nil
}

type AssignPrivateIPAddressOption interface {
	ApplyAssignPrivateIPAddress(*AssignPrivateIPAddressOptions)
}

var _ AssignPrivateIPAddressOption = &AssignPrivateIPAddressOptions{}

type AssignPrivateIPAddressOptions struct {
	NetworkInterfaceOptions *NetworkInterfaceOptions
	Backoff                 *wait.Backoff
}

func (c *AssignPrivateIPAddressOptions) ApplyAssignPrivateIPAddress(options *AssignPrivateIPAddressOptions) {
	if c.Backoff != nil {
		options.Backoff = c.Backoff
	}
	options.NetworkInterfaceOptions = c.NetworkInterfaceOptions
}

func (c *AssignPrivateIPAddressOptions) Finish(idempotentKeyGen IdempotentKeyGen) (*ecs.AssignPrivateIpAddressesRequest, func(), error) {
	if c.NetworkInterfaceOptions == nil || c.NetworkInterfaceOptions.NetworkInterfaceID == "" || c.NetworkInterfaceOptions.IPCount <= 0 {
		return nil, nil, ErrInvalidArgs
	}

	req := ecs.CreateAssignPrivateIpAddressesRequest()
	req.NetworkInterfaceId = c.NetworkInterfaceOptions.NetworkInterfaceID
	req.SecondaryPrivateIpAddressCount = requests.NewInteger(c.NetworkInterfaceOptions.IPCount)

	argsHash := md5Hash(req)
	req.ClientToken = idempotentKeyGen.GenerateKey(argsHash)

	if c.Backoff != nil {
		c.Backoff = &wait.Backoff{
			Steps: 1,
		}
	}

	return req, func() {
		idempotentKeyGen.PutBack(argsHash, req.ClientToken)
	}, nil
}

type AssignIPv6AddressesOption interface {
	ApplyAssignIPv6Addresses(*AssignIPv6AddressesOptions)
}

var _ AssignIPv6AddressesOption = &AssignIPv6AddressesOptions{}

type AssignIPv6AddressesOptions struct {
	NetworkInterfaceOptions *NetworkInterfaceOptions
	Backoff                 *wait.Backoff
}

func (c *AssignIPv6AddressesOptions) ApplyAssignIPv6Addresses(options *AssignIPv6AddressesOptions) {
	if c.Backoff != nil {
		options.Backoff = c.Backoff
	}
	options.NetworkInterfaceOptions = c.NetworkInterfaceOptions
}

func (c *AssignIPv6AddressesOptions) Finish(idempotentKeyGen IdempotentKeyGen) (*ecs.AssignIpv6AddressesRequest, func(), error) {
	if c.NetworkInterfaceOptions == nil || c.NetworkInterfaceOptions.NetworkInterfaceID == "" || c.NetworkInterfaceOptions.IPv6Count <= 0 {
		return nil, nil, ErrInvalidArgs
	}

	req := ecs.CreateAssignIpv6AddressesRequest()
	req.NetworkInterfaceId = c.NetworkInterfaceOptions.NetworkInterfaceID
	req.Ipv6AddressCount = requests.NewInteger(c.NetworkInterfaceOptions.IPv6Count)

	argsHash := md5Hash(req)
	req.ClientToken = idempotentKeyGen.GenerateKey(argsHash)

	if c.Backoff != nil {
		c.Backoff = &wait.Backoff{
			Steps: 1,
		}
	}

	return req, func() {
		idempotentKeyGen.PutBack(argsHash, req.ClientToken)
	}, nil
}
