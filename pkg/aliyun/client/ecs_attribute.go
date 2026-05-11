package client

import (
	"context"
	"fmt"
	"time"

	ecs20140526 "github.com/alibabacloud-go/ecs-20140526/v7/client"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/samber/lo"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/utils/ptr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/feature"
	"github.com/AliyunContainerService/terway/pkg/metric"
)

const (
	APIDescribeNetworkInterfaceAttribute = "DescribeNetworkInterfaceAttribute"

	ENIAttributeBasic = "basic"
)

func (a *ECSService) DescribeNetworkInterfaceAttribute(ctx context.Context, eniID string) (*NetworkInterface, error) {
	ctx, span := a.Tracer.Start(ctx, APIDescribeNetworkInterfaceAttribute)
	defer span.End()

	err := a.RateLimiter.Wait(ctx, APIDescribeNetworkInterfaceAttribute)
	if err != nil {
		return nil, err
	}

	l := logf.FromContext(ctx).WithValues(LogFieldAPI, APIDescribeNetworkInterfaceAttribute, LogFieldENIID, eniID)

	req := &ecs20140526.DescribeNetworkInterfaceAttributeRequest{
		RegionId:           ptr.To(a.ClientSet.RegionID()),
		NetworkInterfaceId: &eniID,
		Attribute:          ptr.To(ENIAttributeBasic),
	}

	start := time.Now()
	resp, err := a.ClientSet.ECSV2().DescribeNetworkInterfaceAttribute(req)
	metric.OpenAPILatency.WithLabelValues(APIDescribeNetworkInterfaceAttribute, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError2(err, eniID)
		l.Error(err, "describe eni attribute failed")
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("describe eni attribute %s returned empty response body", eniID)
	}

	l.WithValues(LogFieldRequestID, FromPtr(resp.Body.RequestId)).Info("describe eni attribute")

	return FromAttributeResp(resp.Body), nil
}

func FromAttributeResp(in *ecs20140526.DescribeNetworkInterfaceAttributeResponseBody) *NetworkInterface {
	if in == nil {
		return nil
	}

	if in.NetworkInterfaceId == nil {
		return nil
	}

	ins := FromPtr(in.InstanceId)
	if ins == "" && in.Attachment != nil {
		ins = FromPtr(in.Attachment.InstanceId)
	}

	r := &NetworkInterface{
		Status:                      FromPtr(in.Status),
		MacAddress:                  FromPtr(in.MacAddress),
		NetworkInterfaceID:          FromPtr(in.NetworkInterfaceId),
		VPCID:                       FromPtr(in.VpcId),
		VSwitchID:                   FromPtr(in.VSwitchId),
		InstanceID:                  ins,
		PrivateIPAddress:            FromPtr(in.PrivateIpAddress),
		ZoneID:                      FromPtr(in.ZoneId),
		ResourceGroupID:             FromPtr(in.ResourceGroupId),
		NetworkInterfaceTrafficMode: FromPtr(in.NetworkInterfaceTrafficMode),
		Type:                        FromPtr(in.Type),
		CreationTime:                FromPtr(in.CreationTime),
	}

	if in.SecurityGroupIds != nil {
		r.SecurityGroupIDs = lo.Map(in.SecurityGroupIds.SecurityGroupId, func(item *string, _ int) string {
			return FromPtr(item)
		})
	}

	if in.Ipv6Sets != nil {
		r.IPv6Set = lo.Map(in.Ipv6Sets.Ipv6Set, func(item *ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyIpv6SetsIpv6Set, _ int) IPSet {
			return IPSet{
				IPAddress: FromPtr(item.Ipv6Address),
			}
		})
	}

	if in.PrivateIpSets != nil {
		r.PrivateIPSets = lo.Map(in.PrivateIpSets.PrivateIpSet, func(item *ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyPrivateIpSetsPrivateIpSet, _ int) IPSet {
			return IPSet{
				IPAddress: FromPtr(item.PrivateIpAddress),
				Primary:   FromPtr(item.Primary),
			}
		})
	}

	if in.Tags != nil {
		r.Tags = lo.Map(in.Tags.Tag, func(item *ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyTagsTag, _ int) ecs.Tag {
			return ecs.Tag{
				TagKey:   FromPtr(item.TagKey),
				TagValue: FromPtr(item.TagValue),
			}
		})
	}

	if in.Attachment != nil {
		r.TrunkNetworkInterfaceID = FromPtr(in.Attachment.TrunkNetworkInterfaceId)
		r.DeviceIndex = int(FromPtr(in.Attachment.DeviceIndex))
		r.NetworkCardIndex = int(FromPtr(in.Attachment.NetworkCardIndex))
	}

	if in.Ipv4PrefixSets != nil {
		r.IPv4PrefixSets = lo.Map(in.Ipv4PrefixSets.Ipv4PrefixSet, func(item *ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyIpv4PrefixSetsIpv4PrefixSet, _ int) Prefix {
			return Prefix(FromPtr(item.Ipv4Prefix))
		})
	}

	if in.Ipv6PrefixSets != nil {
		r.IPv6PrefixSets = lo.Map(in.Ipv6PrefixSets.Ipv6PrefixSet, func(item *ecs20140526.DescribeNetworkInterfaceAttributeResponseBodyIpv6PrefixSetsIpv6PrefixSet, _ int) Prefix {
			return Prefix(FromPtr(item.Ipv6Prefix))
		})
	}

	return r
}

// describeNetworkInterfaceByID queries a single ENI by ID, using the fast
// DescribeNetworkInterfaceAttribute(basic) API when the ENIAttributeBasic
// feature gate is enabled, otherwise falling back to DescribeNetworkInterfaces.
func (a *ECSService) describeNetworkInterfaceByID(ctx context.Context, eniID string) ([]*NetworkInterface, error) {
	if utilfeature.DefaultFeatureGate.Enabled(feature.ENIAttributeBasic) {
		ni, err := a.DescribeNetworkInterfaceAttribute(ctx, eniID)
		if err != nil {
			return nil, err
		}
		if ni == nil {
			return nil, nil
		}
		return []*NetworkInterface{ni}, nil
	}
	return a.DescribeNetworkInterface(ctx, "", []string{eniID}, "", "", "", nil)
}
