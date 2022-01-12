//go:build default_build
// +build default_build

package pod

import (
	"context"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/types/controlplane"
)

// ParsePodNetworksFromAnnotation parse alloc
func (m *ReconcilePod) ParsePodNetworksFromAnnotation(ctx context.Context, zoneID string, anno *controlplane.PodNetworksAnnotation) ([]*v1beta1.Allocation, error) {
	return nil, nil
}

func (m *ReconcilePod) PostENICreate(ctx context.Context, client register.Interface, alloc *v1beta1.Allocation) error {
	return nil
}
