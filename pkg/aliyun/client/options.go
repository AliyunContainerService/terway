package client

import (
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
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

	ZoneID string
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
	if c.NetworkInterfaceOptions != nil {
		if options.NetworkInterfaceOptions == nil {
			options.NetworkInterfaceOptions = &NetworkInterfaceOptions{}
		}

		if c.NetworkInterfaceOptions.Trunk {
			options.NetworkInterfaceOptions.Trunk = true
		}
		if c.NetworkInterfaceOptions.ERDMA {
			options.NetworkInterfaceOptions.ERDMA = true
		}
		if c.NetworkInterfaceOptions.VSwitchID != "" {
			options.NetworkInterfaceOptions.VSwitchID = c.NetworkInterfaceOptions.VSwitchID
		}
		if c.NetworkInterfaceOptions.ResourceGroupID != "" {
			options.NetworkInterfaceOptions.ResourceGroupID = c.NetworkInterfaceOptions.ResourceGroupID
		}
		if c.NetworkInterfaceOptions.SecurityGroupIDs != nil {
			options.NetworkInterfaceOptions.SecurityGroupIDs = c.NetworkInterfaceOptions.SecurityGroupIDs
		}
		if c.NetworkInterfaceOptions.IPCount >= 1 {
			options.NetworkInterfaceOptions.IPCount = c.NetworkInterfaceOptions.IPCount
		}

		if c.NetworkInterfaceOptions.IPv6Count >= 1 {
			options.NetworkInterfaceOptions.IPv6Count = c.NetworkInterfaceOptions.IPv6Count
		}
		if c.NetworkInterfaceOptions.Tags != nil {
			options.NetworkInterfaceOptions.Tags = c.NetworkInterfaceOptions.Tags
		}
		if c.NetworkInterfaceOptions.InstanceID != "" {
			options.NetworkInterfaceOptions.InstanceID = c.NetworkInterfaceOptions.InstanceID
		}
		if c.NetworkInterfaceOptions.InstanceType != "" {
			options.NetworkInterfaceOptions.InstanceType = c.NetworkInterfaceOptions.InstanceType
		}
		if c.NetworkInterfaceOptions.Status != "" {
			options.NetworkInterfaceOptions.Status = c.NetworkInterfaceOptions.Status
		}
		if c.NetworkInterfaceOptions.NetworkInterfaceID != "" {
			options.NetworkInterfaceOptions.NetworkInterfaceID = c.NetworkInterfaceOptions.NetworkInterfaceID
		}
		if c.NetworkInterfaceOptions.DeleteENIOnECSRelease != nil {
			options.NetworkInterfaceOptions.DeleteENIOnECSRelease = c.NetworkInterfaceOptions.DeleteENIOnECSRelease
		}

		if c.NetworkInterfaceOptions.ZoneID != "" {
			options.NetworkInterfaceOptions.ZoneID = c.NetworkInterfaceOptions.ZoneID
		}
	}
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

	if c.Backoff == nil {
		c.Backoff = &wait.Backoff{
			Steps: 1,
		}
	}

	return req, func() {
		idempotentKeyGen.PutBack(argsHash, req.ClientToken)
	}, nil
}

func (c *CreateNetworkInterfaceOptions) EFLO(idempotentKeyGen IdempotentKeyGen) (*eflo.CreateElasticNetworkInterfaceRequest, func(), error) {
	if c.NetworkInterfaceOptions == nil {
		return nil, nil, ErrInvalidArgs
	}

	if c.NetworkInterfaceOptions.IPCount > 1 {
		// eflo does not support multi ip in create
		return nil, nil, ErrInvalidArgs
	}

	if c.NetworkInterfaceOptions.VSwitchID == "" {
		return nil, nil, ErrInvalidArgs
	}

	req := eflo.CreateCreateElasticNetworkInterfaceRequest()
	if c.NetworkInterfaceOptions.VSwitchID != "" {
		req.VSwitchId = c.NetworkInterfaceOptions.VSwitchID
	}
	if len(c.NetworkInterfaceOptions.SecurityGroupIDs) > 0 {
		req.SecurityGroupId = c.NetworkInterfaceOptions.SecurityGroupIDs[0]
	}
	req.Description = eniDescription
	if c.NetworkInterfaceOptions.InstanceID != "" {
		req.NodeId = c.NetworkInterfaceOptions.InstanceID
	}
	if c.NetworkInterfaceOptions.ZoneID != "" {
		req.ZoneId = c.NetworkInterfaceOptions.ZoneID
	}

	if req.SecurityGroupId == "" {
		return nil, nil, ErrInvalidArgs
	}

	argsHash := md5Hash(req)
	req.ClientToken = idempotentKeyGen.GenerateKey(argsHash)

	if c.Backoff == nil {
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

	if c.Backoff == nil {
		c.Backoff = &wait.Backoff{
			Steps: 1,
		}
	}

	return req, func() {
		idempotentKeyGen.PutBack(argsHash, req.ClientToken)
	}, nil
}

func (c *AssignPrivateIPAddressOptions) EFLO(idempotentKeyGen IdempotentKeyGen) (*eflo.AssignLeniPrivateIpAddressRequest, func(), error) {
	if c.NetworkInterfaceOptions == nil ||
		c.NetworkInterfaceOptions.NetworkInterfaceID == "" ||
		c.NetworkInterfaceOptions.IPCount != 1 {
		return nil, nil, ErrInvalidArgs
	}
	req := eflo.CreateAssignLeniPrivateIpAddressRequest()
	req.ElasticNetworkInterfaceId = c.NetworkInterfaceOptions.NetworkInterfaceID

	argsHash := md5Hash(req)
	req.ClientToken = idempotentKeyGen.GenerateKey(argsHash)

	if c.Backoff == nil {
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

	if c.Backoff == nil {
		c.Backoff = &wait.Backoff{
			Steps: 1,
		}
	}

	return req, func() {
		idempotentKeyGen.PutBack(argsHash, req.ClientToken)
	}, nil
}

type DescribeNetworkInterfaceOption interface {
	ApplyTo(*DescribeNetworkInterfaceOptions)
}

type DescribeNetworkInterfaceOptions struct {
	VPCID               *string
	NetworkInterfaceIDs *[]string
	InstanceID          *string
	InstanceType        *string
	Status              *string
	Tags                *map[string]string

	Backoff *wait.Backoff
}

func (o *DescribeNetworkInterfaceOptions) ApplyTo(in *DescribeNetworkInterfaceOptions) {
	if o.VPCID != nil {
		in.VPCID = o.VPCID
	}
	if o.NetworkInterfaceIDs != nil {
		in.NetworkInterfaceIDs = o.NetworkInterfaceIDs
	}
	if o.InstanceID != nil {
		in.InstanceID = o.InstanceID
	}
	if o.InstanceType != nil {
		in.InstanceType = o.InstanceType
	}
	if o.Status != nil {
		in.Status = o.Status
	}
	if o.Tags != nil {
		in.Tags = o.Tags
	}
	if o.Backoff != nil {
		in.Backoff = o.Backoff
	}
}

func (o *DescribeNetworkInterfaceOptions) ECS() *ecs.DescribeNetworkInterfacesRequest {
	req := ecs.CreateDescribeNetworkInterfacesRequest()
	if o.VPCID != nil {
		req.VpcId = *o.VPCID
	}
	if o.NetworkInterfaceIDs != nil {
		req.NetworkInterfaceId = o.NetworkInterfaceIDs
	}
	if o.InstanceID != nil {
		req.InstanceId = *o.InstanceID
	}
	if o.InstanceType != nil {
		req.Type = *o.InstanceType
	}
	if o.Status != nil {
		req.Status = *o.Status
	}
	if o.Tags != nil {
		tags := make([]ecs.DescribeNetworkInterfacesTag, 0)
		for k, v := range *o.Tags {
			tags = append(tags, ecs.DescribeNetworkInterfacesTag{
				Key:   k,
				Value: v,
			})
		}
		req.Tag = &tags
	}

	if o.Backoff == nil {
		o.Backoff = &wait.Backoff{
			Steps: 1,
		}
	}

	return req
}

func (o *DescribeNetworkInterfaceOptions) EFLO() *eflo.ListElasticNetworkInterfacesRequest {
	req := eflo.CreateListElasticNetworkInterfacesRequest()
	if o.VPCID != nil {
		req.VpcId = *o.VPCID
	}

	if o.NetworkInterfaceIDs != nil && len(*o.NetworkInterfaceIDs) > 0 {
		req.ElasticNetworkInterfaceId = (*o.NetworkInterfaceIDs)[0]
	}
	if o.InstanceID != nil {
		req.NodeId = *o.InstanceID
	}
	if o.InstanceType != nil {
		req.Type = *o.InstanceType
	}
	if o.Status != nil {
		req.Status = *o.Status
	}

	if o.Backoff == nil {
		o.Backoff = &wait.Backoff{
			Steps: 1,
		}
	}

	return req
}
