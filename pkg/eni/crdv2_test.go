package eni

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

func TestGetRuntimeNode(t *testing.T) {
	tests := []struct {
		name             string
		existingNodes    []corev1.Node
		existingRuntimes []networkv1beta1.NodeRuntime
		nodeName         string
		expectedError    bool
		expectedLabels   map[string]string
	}{
		{
			name:             "Node exists, Runtime does not",
			existingNodes:    []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "test-node"}}},
			existingRuntimes: []networkv1beta1.NodeRuntime{},
			nodeName:         "test-node",
			expectedError:    false,
			expectedLabels:   map[string]string{"name": "test-node"},
		},
		{
			name:             "Runtime exists",
			existingNodes:    []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "test-node"}}},
			existingRuntimes: []networkv1beta1.NodeRuntime{{ObjectMeta: metav1.ObjectMeta{Name: "test-node", Labels: map[string]string{"name": "test-node"}}}},
			nodeName:         "test-node",
			expectedError:    false,
			expectedLabels:   map[string]string{"name": "test-node"},
		},
		{
			name:             "Node does not exist",
			existingNodes:    []corev1.Node{},
			existingRuntimes: []networkv1beta1.NodeRuntime{},
			nodeName:         "test-node",
			expectedError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up the fake client with existing nodes and runtimes
			objs := []runtime.Object{}
			for _, n := range tt.existingNodes {
				objs = append(objs, &n)
			}
			for _, r := range tt.existingRuntimes {
				objs = append(objs, &r)
			}
			builder := fake.NewClientBuilder()
			fakeClient := builder.WithScheme(types.Scheme).WithRuntimeObjects(objs...).Build()
			r := &CRDV2{
				client:   fakeClient,
				nodeName: tt.nodeName,
				scheme:   types.Scheme,
			}
			ctx := context.Background()

			// Call the method under test
			nodeRuntime, err := r.getRuntimeNode(ctx)

			// Check for expected errors
			if (err != nil) != tt.expectedError {
				t.Errorf("expected error: %v, got: %v", tt.expectedError, err)
			}

			if !tt.expectedError {
				// Check expected labels
				assert.Equal(t, tt.expectedLabels, nodeRuntime.Labels)
			}
		})
	}
}

func TestInUsedPodUIDs(t *testing.T) {
	tests := []struct {
		name            string
		node            *networkv1beta1.Node
		expectedPodUIDs map[string]networkv1beta1.RuntimePodStatus
		expectedError   bool
	}{
		{
			name: "No network interfaces",
			node: &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: networkv1beta1.NodeStatus{}},
			expectedPodUIDs: map[string]networkv1beta1.RuntimePodStatus{},
			expectedError:   false,
		},
		{
			name: "IPv4 and IPv6 IPAM records present",
			node: &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: networkv1beta1.NodeStatus{
					NetworkInterfaces: map[string]*networkv1beta1.NetworkInterface{
						"eni-1": {
							ID: "eni-1",
							IPv4: map[string]*networkv1beta1.IP{
								"127.0.0.1": {
									IP:      "",
									Primary: false,
									Status:  "",
									PodID:   "pod-id-1",
									PodUID:  "pod-uid-1",
								},
								"127.0.0.2": {
									IP:      "",
									Primary: false,
									Status:  "",
									PodID:   "pod-id-2",
									PodUID:  "pod-uid-2",
								},
							},
						},
					},
				},
			},
			expectedPodUIDs: map[string]networkv1beta1.RuntimePodStatus{
				"pod-uid-1": {PodID: "pod-id-1", Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
					networkv1beta1.CNIStatusInitial: &networkv1beta1.CNIStatusInfo{
						LastUpdateTime: metav1.Time{},
					},
				}},
				"pod-uid-2": {PodID: "pod-id-2", Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
					networkv1beta1.CNIStatusInitial: &networkv1beta1.CNIStatusInfo{
						LastUpdateTime: metav1.Time{},
					},
				}},
			},
			expectedError: false,
		},
		{
			name:            "Error getting node",
			node:            nil,
			expectedPodUIDs: map[string]networkv1beta1.RuntimePodStatus{},
			expectedError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake client with the test node
			objs := []runtime.Object{}
			if tt.node != nil {
				objs = append(objs, tt.node)
			}
			builder := fake.NewClientBuilder()
			fakeClient := builder.WithScheme(types.Scheme).WithRuntimeObjects(objs...).Build()
			r := &CRDV2{
				client:   fakeClient,
				nodeName: "test-node",
			}
			ctx := context.Background()

			// Call the method under test
			podUIDs, err := r.inUsedPodUIDs(ctx)

			for _, podUID := range podUIDs {
				for _, v := range podUID.Status {
					v.LastUpdateTime = metav1.Time{}
				}
			}
			// Check for expected errors
			if (err != nil) != tt.expectedError {
				t.Errorf("expected error: %v, got: %v", tt.expectedError, err)
			}

			if !tt.expectedError {
				assert.Equal(t, tt.expectedPodUIDs, podUIDs)
			}
		})
	}
}

func Test_syncBack(t *testing.T) {
	type args struct {
		l           logr.Logger
		nodeRuntime *networkv1beta1.NodeRuntime
		inUsed      map[string]networkv1beta1.RuntimePodStatus
	}
	tests := []struct {
		name string
		args args

		expect *networkv1beta1.NodeRuntime
	}{
		{
			name: "add pod back",
			args: args{
				l:           logr.Discard(),
				nodeRuntime: &networkv1beta1.NodeRuntime{},
				inUsed: map[string]networkv1beta1.RuntimePodStatus{
					"uid1": networkv1beta1.RuntimePodStatus{
						PodID: "pod-id-1",
						Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
							networkv1beta1.CNIStatusInitial: &networkv1beta1.CNIStatusInfo{
								LastUpdateTime: metav1.Time{},
							},
						},
					},
					"uid2": networkv1beta1.RuntimePodStatus{
						PodID: "pod-id-2",
						Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
							networkv1beta1.CNIStatusInitial: &networkv1beta1.CNIStatusInfo{
								LastUpdateTime: metav1.Time{},
							},
						},
					},
				},
			},
			expect: &networkv1beta1.NodeRuntime{
				Status: networkv1beta1.NodeRuntimeStatus{Pods: map[string]*networkv1beta1.RuntimePodStatus{
					"uid1": {
						PodID: "pod-id-1",
						Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
							networkv1beta1.CNIStatusInitial: &networkv1beta1.CNIStatusInfo{
								LastUpdateTime: metav1.Time{},
							},
						},
					},
					"uid2": {
						PodID: "pod-id-2",
						Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
							networkv1beta1.CNIStatusInitial: &networkv1beta1.CNIStatusInfo{
								LastUpdateTime: metav1.Time{},
							},
						},
					},
				}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncBack(tt.args.l, tt.args.nodeRuntime, tt.args.inUsed)

			assert.Equal(t, tt.expect, tt.args.nodeRuntime)
		})
	}
}

func Test_removeDeleted(t *testing.T) {
	type args struct {
		l           logr.Logger
		nodeRuntime *networkv1beta1.NodeRuntime
		inUsed      map[string]networkv1beta1.RuntimePodStatus
	}
	tests := []struct {
		name   string
		args   args
		expect *networkv1beta1.NodeRuntime
	}{
		{
			name: "remove unused",
			args: args{
				l: logr.Discard(),
				nodeRuntime: &networkv1beta1.NodeRuntime{
					Status: networkv1beta1.NodeRuntimeStatus{
						Pods: map[string]*networkv1beta1.RuntimePodStatus{
							"uid1": {
								PodID: "pod-id-1",
								Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
									networkv1beta1.CNIStatusInitial: &networkv1beta1.CNIStatusInfo{
										LastUpdateTime: metav1.Time{},
									},
								},
							},
							"uid2": {
								PodID: "pod-id-2",
								Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
									networkv1beta1.CNIStatusDeleted: &networkv1beta1.CNIStatusInfo{
										LastUpdateTime: metav1.Time{},
									},
								},
							},
						},
					},
				},
				inUsed: map[string]networkv1beta1.RuntimePodStatus{},
			},
			expect: &networkv1beta1.NodeRuntime{
				Status: networkv1beta1.NodeRuntimeStatus{Pods: map[string]*networkv1beta1.RuntimePodStatus{
					"uid1": {
						PodID: "pod-id-1",
						Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
							networkv1beta1.CNIStatusInitial: &networkv1beta1.CNIStatusInfo{
								LastUpdateTime: metav1.Time{},
							},
						},
					},
				}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			removeDeleted(tt.args.l, tt.args.nodeRuntime, tt.args.inUsed)

			assert.Equal(t, tt.expect, tt.args.nodeRuntime)
		})
	}
}
