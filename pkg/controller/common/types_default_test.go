package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
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

func TestNodeInfoWithLabelZoneFailureDomain(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Labels: map[string]string{
				corev1.LabelTopologyRegion:     "test-region",
				corev1.LabelInstanceTypeStable: "test-instance-type",
				corev1.LabelZoneFailureDomain:  "test-zone-fd",
			},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "provider.test-instance-id",
		},
	}

	nodeInfo, err := NewNodeInfo(node)
	assert.Nil(t, err)
	assert.Equal(t, "test-zone-fd", nodeInfo.ZoneID)
}

func TestNodeInfoWithLingJunNode(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-lingjun-node",
			Labels: map[string]string{
				corev1.LabelTopologyRegion:     "test-region",
				corev1.LabelInstanceTypeStable: "test-instance-type",
				corev1.LabelTopologyZone:       "test-zone",
				types.LingJunNodeLabelKey:      "true",
			},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "lingjun-provider-id-full",
		},
	}

	nodeInfo, err := NewNodeInfo(node)
	assert.Nil(t, err)
	assert.Equal(t, "lingjun-provider-id-full", nodeInfo.InstanceID)
}

func TestNodeInfoReturnsErrorWhenInstanceIDEmpty(t *testing.T) {
	// ProviderID "provider." splits to ["provider",""] so ids[1] is empty and we hit "can not found instanceID"
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Labels: map[string]string{
				corev1.LabelTopologyRegion:     "test-region",
				corev1.LabelInstanceTypeStable: "test-instance-type",
				corev1.LabelTopologyZone:       "test-zone",
			},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "provider.",
		},
	}

	nodeInfo, err := NewNodeInfo(node)
	assert.NotNil(t, err)
	assert.Nil(t, nodeInfo)
	assert.Contains(t, err.Error(), "instanceID")
}

func TestToNetworkInterfaceCR(t *testing.T) {
	t.Run("basic conversion without IPv6", func(t *testing.T) {
		eni := &aliyunClient.NetworkInterface{
			NetworkInterfaceID: "eni-123",
			MacAddress:         "00:11:22:33:44:55",
			VPCID:              "vpc-123",
			ZoneID:             "cn-hangzhou-a",
			VSwitchID:          "vsw-123",
			ResourceGroupID:    "rg-123",
			SecurityGroupIDs:   []string{"sg-1", "sg-2"},
			PrivateIPAddress:   "192.168.1.1",
		}

		cr := ToNetworkInterfaceCR(eni)
		assert.Equal(t, "eni-123", cr.Name)
		assert.Equal(t, "eni-123", cr.Spec.ENI.ID)
		assert.Equal(t, "00:11:22:33:44:55", cr.Spec.ENI.MAC)
		assert.Equal(t, "vpc-123", cr.Spec.ENI.VPCID)
		assert.Equal(t, "cn-hangzhou-a", cr.Spec.ENI.Zone)
		assert.Equal(t, "vsw-123", cr.Spec.ENI.VSwitchID)
		assert.Equal(t, "rg-123", cr.Spec.ENI.ResourceGroupID)
		assert.Equal(t, []string{"sg-1", "sg-2"}, cr.Spec.ENI.SecurityGroupIDs)
		assert.Equal(t, "192.168.1.1", cr.Spec.IPv4)
		assert.Empty(t, cr.Spec.IPv6)
		assert.NotNil(t, cr.Spec.ExtraConfig)
	})

	t.Run("conversion with IPv6", func(t *testing.T) {
		eni := &aliyunClient.NetworkInterface{
			NetworkInterfaceID: "eni-456",
			IPv6Set: []aliyunClient.IPSet{
				{IPAddress: "fd00::1"},
				{IPAddress: "fd00::2"},
			},
		}

		cr := ToNetworkInterfaceCR(eni)
		assert.Equal(t, "fd00::1", cr.Spec.IPv6)
	})

	t.Run("empty IPv6 set", func(t *testing.T) {
		eni := &aliyunClient.NetworkInterface{
			NetworkInterfaceID: "eni-789",
		}

		cr := ToNetworkInterfaceCR(eni)
		assert.Empty(t, cr.Spec.IPv6)
	})
}
