//go:build default_build

package pod

import (
	"context"
	"fmt"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types/controlplane"
)

// ParsePodNetworksFromAnnotation parse alloc
func (m *ReconcilePod) ParsePodNetworksFromAnnotation(ctx context.Context, zoneID string, anno *controlplane.PodNetworksAnnotation) ([]*v1beta1.Allocation, error) {
	if zoneID == "" {
		return nil, fmt.Errorf("zoneID is empty")
	}
	var allocs []*v1beta1.Allocation

	for _, c := range anno.PodNetworks {
		ifName := c.Interface
		if ifName == "" {
			ifName = defaultInterface
		}

		if len(c.VSwitchOptions) == 0 || len(c.SecurityGroupIDs) == 0 {
			return nil, fmt.Errorf("vSwitchOptions or securityGroupIDs is missing")
		}

		alloc := &v1beta1.Allocation{
			ENI: v1beta1.ENI{
				SecurityGroupIDs: c.SecurityGroupIDs,
			},
			Interface:   ifName,
			ExtraConfig: map[string]string{},
		}

		vSwitchSelectPolicy := vswitch.VSwitchSelectionPolicyOrdered

		switch c.VSwitchSelectOptions.VSwitchSelectionPolicy {
		case v1beta1.VSwitchSelectionPolicyRandom:
			vSwitchSelectPolicy = vswitch.VSwitchSelectionPolicyRandom
		case v1beta1.VSwitchSelectionPolicyMost:
			vSwitchSelectPolicy = vswitch.VSwitchSelectionPolicyMost
		}

		sw, err := m.swPool.GetOne(ctx, m.aliyun, zoneID, c.VSwitchOptions,
			&vswitch.SelectOptions{
				IgnoreZone:          false,
				VSwitchSelectPolicy: vSwitchSelectPolicy,
			},
		)
		if err != nil {
			return nil, err
		}
		alloc.ENI.VSwitchID = sw.ID
		alloc.IPv4CIDR = sw.IPv4CIDR
		alloc.IPv6CIDR = sw.IPv6CIDR
		var routes []v1beta1.Route
		for _, r := range c.ExtraRoutes {
			routes = append(routes, v1beta1.Route{Dst: r.Dst})
		}
		alloc.ExtraRoutes = routes

		// for default , leave it blank
		trunk := false
		switch c.ENIOptions.ENIAttachType {
		case v1beta1.ENIOptionTypeENI:
			alloc.ENI.AttachmentOptions.Trunk = &trunk
		case v1beta1.ENIOptionTypeTrunk:
			trunk = true
			alloc.ENI.AttachmentOptions.Trunk = &trunk
		}

		if c.AllocationType != nil {
			alloc.AllocationType = *c.AllocationType
		}

		allocs = append(allocs, alloc)
	}

	return allocs, nil
}
