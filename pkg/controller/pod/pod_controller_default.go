//go:build default_build

package pod

import (
	"context"
	"fmt"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
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

		ctx := common.WithCtx(ctx, alloc)

		// allow config route without
		realClient, _, err := common.Became(ctx, m.aliyun)
		if err != nil {
			return nil, err
		}

		sw, err := m.swPool.GetOne(ctx, realClient, zoneID, c.VSwitchOptions, &vswitch.SelectOptions{
			IgnoreZone: false,
		})
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

		allocs = append(allocs, alloc)
	}

	return allocs, nil
}

func (m *ReconcilePod) PostENICreate(ctx context.Context, alloc *v1beta1.Allocation) error {
	return nil
}
