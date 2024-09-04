package eni

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	"github.com/AliyunContainerService/terway/types"
)

func TestGetTrunkENIReturnsErrorWhenNodeNotInitialized(t *testing.T) {
	backoff.Backoff(backoff.WaitPodENIStatus)
	backoff.OverrideBackoff(map[string]wait.Backoff{
		backoff.WaitPodENIStatus: {
			Duration: 0,
			Factor:   0,
			Jitter:   0,
			Steps:    1,
			Cap:      0,
		},
	})

	crd := &CRDV2{
		nodeName: "node1",
	}
	ctx := context.Background()

	fakeClient := fake.NewClientBuilder().WithScheme(types.Scheme).WithObjects(&networkv1beta1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node1"},
		Spec:       networkv1beta1.NodeSpec{ENISpec: nil},
	}).Build()
	crd.client = fakeClient

	eni, err := crd.getTrunkENI(ctx)
	assert.Nil(t, eni)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "has not been initialized")
}

func TestGetTrunkENIReturnsErrorWhenTrunkNotEnabled(t *testing.T) {
	backoff.Backoff(backoff.WaitPodENIStatus)
	backoff.OverrideBackoff(map[string]wait.Backoff{
		backoff.WaitPodENIStatus: {
			Duration: 0,
			Factor:   0,
			Jitter:   0,
			Steps:    1,
			Cap:      0,
		},
	})

	crd := &CRDV2{
		nodeName: "node1",
	}
	ctx := context.Background()

	fakeClient := fake.NewClientBuilder().WithScheme(types.Scheme).WithObjects(&networkv1beta1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node1"},
		Spec:       networkv1beta1.NodeSpec{ENISpec: &networkv1beta1.ENISpec{EnableTrunk: false}},
	}).Build()
	crd.client = fakeClient

	eni, err := crd.getTrunkENI(ctx)
	assert.Nil(t, eni)
	assert.NoError(t, err)
}

func TestGetTrunkENIReturnsErrorWhenTrunkIDNotFound(t *testing.T) {
	backoff.Backoff(backoff.WaitPodENIStatus)
	backoff.OverrideBackoff(map[string]wait.Backoff{
		backoff.WaitPodENIStatus: {
			Duration: 0,
			Factor:   0,
			Jitter:   0,
			Steps:    1,
			Cap:      0,
		},
	})

	crd := &CRDV2{
		nodeName: "node1",
	}
	ctx := context.Background()

	// Create a fake client with the initial state
	fakeClient := fake.NewClientBuilder().WithScheme(types.Scheme).WithObjects(
		&networkv1beta1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node1"},
			Spec:       networkv1beta1.NodeSpec{ENISpec: &networkv1beta1.ENISpec{EnableTrunk: true}},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node1", Annotations: map[string]string{}},
		},
	).Build()
	crd.client = fakeClient

	eni, err := crd.getTrunkENI(ctx)
	assert.Nil(t, eni)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "has not been initialized")
}

func TestGetTrunkENIReturnsValidENI(t *testing.T) {
	backoff.Backoff(backoff.WaitPodENIStatus)
	backoff.OverrideBackoff(map[string]wait.Backoff{
		backoff.WaitPodENIStatus: {
			Duration: 0,
			Factor:   0,
			Jitter:   0,
			Steps:    1,
			Cap:      0,
		},
	})

	crd := &CRDV2{
		nodeName: "node1",
	}
	ctx := context.Background()

	// Create a fake client with the initial state
	fakeClient := fake.NewClientBuilder().WithScheme(types.Scheme).WithObjects(
		&networkv1beta1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node1"},
			Spec: networkv1beta1.NodeSpec{
				ENISpec: &networkv1beta1.ENISpec{
					EnableTrunk: true,
					EnableIPv4:  true,
					EnableIPv6:  true,
				}},
			Status: networkv1beta1.NodeStatus{
				NetworkInterfaces: map[string]*networkv1beta1.NetworkInterface{
					"eni-1": {
						ID:                          "eni-1",
						MacAddress:                  "00:00:00:00:00:01",
						SecurityGroupIDs:            []string{"sg-1"},
						IPv4CIDR:                    "192.168.1.0/24",
						IPv6CIDR:                    "fd00::/64",
						PrimaryIPAddress:            "192.168.1.10",
						NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
						NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
					},
				},
			},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node1", Annotations: map[string]string{types.TrunkOn: "eni-1"}},
		},
	).Build()
	crd.client = fakeClient

	eni, err := crd.getTrunkENI(ctx)
	assert.NotNil(t, eni)
	assert.NoError(t, err)
	assert.Equal(t, "eni-1", eni.ID)
	assert.Equal(t, "00:00:00:00:00:01", eni.MAC)
	assert.Equal(t, []string{"sg-1"}, eni.SecurityGroupIDs)
	assert.Equal(t, "192.168.1.10", eni.PrimaryIP.IPv4.String())
	assert.Equal(t, "192.168.1.253", eni.GatewayIP.IPv4.String())
	assert.Equal(t, "192.168.1.0/24", eni.VSwitchCIDR.IPv4.String())
	assert.Equal(t, "fd00::/64", eni.VSwitchCIDR.IPv6.String())
}
