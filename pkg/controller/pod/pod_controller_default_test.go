package pod

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/AliyunContainerService/terway/types/controlplane"
)

func TestParsePodNetworksFromAnnotation_EmptyZoneID(t *testing.T) {
	r := &ReconcilePod{}
	anno := &controlplane.PodNetworksAnnotation{
		PodNetworks: []controlplane.PodNetworks{
			{
				VSwitchOptions:   []string{"vsw-1"},
				SecurityGroupIDs: []string{"sg-1"},
			},
		},
	}
	_, err := r.ParsePodNetworksFromAnnotation(context.Background(), "", anno)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "zoneID is empty")
}

func TestParsePodNetworksFromAnnotation_MissingVSwitchOrSecurityGroup(t *testing.T) {
	r := &ReconcilePod{}

	t.Run("missing vSwitchOptions", func(t *testing.T) {
		anno := &controlplane.PodNetworksAnnotation{
			PodNetworks: []controlplane.PodNetworks{
				{
					VSwitchOptions:   nil,
					SecurityGroupIDs: []string{"sg-1"},
				},
			},
		}
		_, err := r.ParsePodNetworksFromAnnotation(context.Background(), "zone-a", anno)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "vSwitchOptions or securityGroupIDs")
	})

	t.Run("missing securityGroupIDs", func(t *testing.T) {
		anno := &controlplane.PodNetworksAnnotation{
			PodNetworks: []controlplane.PodNetworks{
				{
					VSwitchOptions:   []string{"vsw-1"},
					SecurityGroupIDs: nil,
				},
			},
		}
		_, err := r.ParsePodNetworksFromAnnotation(context.Background(), "zone-a", anno)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "vSwitchOptions or securityGroupIDs")
	})
}
