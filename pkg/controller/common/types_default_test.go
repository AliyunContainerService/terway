package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/AliyunContainerService/terway/types"
)

func TestNodeInfoIsCreatedSuccessfully(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Labels: map[string]string{
				corev1.LabelTopologyRegion:     "test-region",
				corev1.LabelInstanceTypeStable: "test-instance-type",
				corev1.LabelTopologyZone:       "test-zone",
			},
			Annotations: map[string]string{
				types.TrunkOn: "test-trunk-eni-id",
			},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "provider.test-instance-id",
		},
	}

	nodeInfo, err := NewNodeInfo(node)
	assert.Nil(t, err)
	assert.Equal(t, "test-node", nodeInfo.NodeName)
	assert.Equal(t, "test-instance-id", nodeInfo.InstanceID)
	assert.Equal(t, "test-trunk-eni-id", nodeInfo.TrunkENIID)
	assert.Equal(t, "test-region", nodeInfo.RegionID)
	assert.Equal(t, "test-instance-type", nodeInfo.InstanceType)
	assert.Equal(t, "test-zone", nodeInfo.ZoneID)
}

func TestNodeInfoReturnsErrorWhenProviderIDIsInvalid(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Spec: corev1.NodeSpec{
			ProviderID: "invalid-provider-id",
		},
	}

	nodeInfo, err := NewNodeInfo(node)
	assert.NotNil(t, err)
	assert.Nil(t, nodeInfo)
}

func TestNodeInfoReturnsErrorWhenRegionIDIsMissing(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Labels: map[string]string{
				corev1.LabelInstanceTypeStable: "test-instance-type",
				corev1.LabelTopologyZone:       "test-zone",
			},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "provider.test-instance-id",
		},
	}

	nodeInfo, err := NewNodeInfo(node)
	assert.NotNil(t, err)
	assert.Nil(t, nodeInfo)
}

func TestNodeInfoReturnsErrorWhenInstanceTypeIsMissing(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Labels: map[string]string{
				corev1.LabelTopologyRegion: "test-region",
				corev1.LabelTopologyZone:   "test-zone",
			},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "provider.test-instance-id",
		},
	}

	nodeInfo, err := NewNodeInfo(node)
	assert.NotNil(t, err)
	assert.Nil(t, nodeInfo)
}

func TestNodeInfoReturnsErrorWhenZoneLabelIsMissing(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Labels: map[string]string{
				corev1.LabelTopologyRegion:     "test-region",
				corev1.LabelInstanceTypeStable: "test-instance-type",
			},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "provider.test-instance-id",
		},
	}

	nodeInfo, err := NewNodeInfo(node)
	assert.NotNil(t, err)
	assert.Nil(t, nodeInfo)
}
