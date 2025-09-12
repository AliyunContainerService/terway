package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

func TestCleanRuntimeNode(t *testing.T) {
	tests := []struct {
		name          string
		ipamType      types.IPAMType
		daemonMode    string
		nodeRuntime   *networkv1beta1.NodeRuntime
		localUIDs     sets.Set[string]
		expectedError bool
		expectedPods  map[string]*networkv1beta1.RuntimePodStatus
		mockPodExist  func(namespace, name string) (bool, error)
	}{
		{
			name:          "Non-CRD IPAM type should return early",
			ipamType:      types.IPAMTypeDefault,
			daemonMode:    daemon.ModeENIMultiIP,
			localUIDs:     sets.New("pod-1"),
			expectedError: false,
		},
		{
			name:          "Non-ENIMultiIP mode should return early",
			ipamType:      types.IPAMTypeCRD,
			daemonMode:    daemon.ModeENIOnly,
			localUIDs:     sets.New("pod-1"),
			expectedError: false,
		},
		{
			name:       "Successfully clean runtime node",
			ipamType:   types.IPAMTypeCRD,
			daemonMode: daemon.ModeENIMultiIP,
			nodeRuntime: &networkv1beta1.NodeRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: networkv1beta1.NodeRuntimeStatus{
					Pods: map[string]*networkv1beta1.RuntimePodStatus{
						"pod-1": {
							PodID: "default/pod-1",
							Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
								networkv1beta1.CNIStatusInitial: {
									LastUpdateTime: metav1.NewTime(time.Now().Add(-35 * time.Second)), // 35 seconds ago
								},
							},
						},
						"pod-2": {
							PodID: "default/pod-2",
							Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
								networkv1beta1.CNIStatusInitial: {
									LastUpdateTime: metav1.NewTime(time.Now().Add(-35 * time.Second)), // 35 seconds ago
								},
							},
						},
					},
				},
			},
			localUIDs:     sets.New("pod-1"), // Only pod-1 is in local UIDs
			expectedError: false,
			expectedPods: map[string]*networkv1beta1.RuntimePodStatus{
				"pod-1": {
					PodID: "default/pod-1",
					Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
						networkv1beta1.CNIStatusInitial: {
							LastUpdateTime: metav1.NewTime(time.Now().Add(-35 * time.Second)),
						},
					},
				},
				"pod-2": {
					PodID: "default/pod-2",
					Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
						networkv1beta1.CNIStatusInitial: {
							LastUpdateTime: metav1.NewTime(time.Now().Add(-35 * time.Second)),
						},
						networkv1beta1.CNIStatusDeleted: {
							LastUpdateTime: metav1.NewTime(time.Now()),
						},
					},
				},
			},
			mockPodExist: func(namespace, name string) (bool, error) {
				// pod-2 doesn't exist in cluster
				if name == "pod-2" {
					return false, nil
				}
				return true, nil
			},
		},
		{
			name:       "Pod exists in cluster should not be marked as deleted",
			ipamType:   types.IPAMTypeCRD,
			daemonMode: daemon.ModeENIMultiIP,
			nodeRuntime: &networkv1beta1.NodeRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: networkv1beta1.NodeRuntimeStatus{
					Pods: map[string]*networkv1beta1.RuntimePodStatus{
						"pod-1": {
							PodID: "default/pod-1",
							Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
								networkv1beta1.CNIStatusInitial: {
									LastUpdateTime: metav1.NewTime(time.Now().Add(-35 * time.Second)),
								},
							},
						},
					},
				},
			},
			localUIDs:     sets.New[string](), // No local UIDs
			expectedError: false,
			expectedPods: map[string]*networkv1beta1.RuntimePodStatus{
				"pod-1": {
					PodID: "default/pod-1",
					Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
						networkv1beta1.CNIStatusInitial: {
							LastUpdateTime: metav1.NewTime(time.Now().Add(-35 * time.Second)),
						},
					},
				},
			},
			mockPodExist: func(namespace, name string) (bool, error) {
				// pod-1 exists in cluster
				return true, nil
			},
		},
		{
			name:       "Pod with recent update should not be marked as deleted",
			ipamType:   types.IPAMTypeCRD,
			daemonMode: daemon.ModeENIMultiIP,
			nodeRuntime: &networkv1beta1.NodeRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: networkv1beta1.NodeRuntimeStatus{
					Pods: map[string]*networkv1beta1.RuntimePodStatus{
						"pod-1": {
							PodID: "default/pod-1",
							Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
								networkv1beta1.CNIStatusInitial: {
									LastUpdateTime: metav1.NewTime(time.Now().Add(-10 * time.Second)), // 10 seconds ago
								},
							},
						},
					},
				},
			},
			localUIDs:     sets.New[string](), // No local UIDs
			expectedError: false,
			expectedPods: map[string]*networkv1beta1.RuntimePodStatus{
				"pod-1": {
					PodID: "default/pod-1",
					Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
						networkv1beta1.CNIStatusInitial: {
							LastUpdateTime: metav1.NewTime(time.Now().Add(-10 * time.Second)),
						},
					},
				},
			},
			mockPodExist: func(namespace, name string) (bool, error) {
				return false, nil
			},
		},
		{
			name:       "Invalid pod ID format should be skipped",
			ipamType:   types.IPAMTypeCRD,
			daemonMode: daemon.ModeENIMultiIP,
			nodeRuntime: &networkv1beta1.NodeRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: networkv1beta1.NodeRuntimeStatus{
					Pods: map[string]*networkv1beta1.RuntimePodStatus{
						"pod-1": {
							PodID: "invalid-pod-id", // Invalid format
							Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
								networkv1beta1.CNIStatusInitial: {
									LastUpdateTime: metav1.NewTime(time.Now().Add(-35 * time.Second)),
								},
							},
						},
					},
				},
			},
			localUIDs:     sets.New[string](), // No local UIDs
			expectedError: false,
			expectedPods: map[string]*networkv1beta1.RuntimePodStatus{
				"pod-1": {
					PodID: "invalid-pod-id",
					Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
						networkv1beta1.CNIStatusInitial: {
							LastUpdateTime: metav1.NewTime(time.Now().Add(-35 * time.Second)),
						},
					},
				},
			},
			mockPodExist: func(namespace, name string) (bool, error) {
				return false, nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup fake client
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			_ = networkv1beta1.AddToScheme(scheme)

			var fakeClient client.Client
			if tt.nodeRuntime != nil {
				fakeClient = fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(tt.nodeRuntime).
					WithStatusSubresource(tt.nodeRuntime).
					Build()
			} else {
				fakeClient = fake.NewClientBuilder().
					WithScheme(scheme).
					Build()
			}

			// Create mock k8s interface
			mockK8s := &mockKubernetes{
				client:   fakeClient,
				nodeName: "test-node",
			}

			// Create networkService instance
			ns := &networkService{
				ipamType:   tt.ipamType,
				daemonMode: tt.daemonMode,
				k8s:        mockK8s,
			}

			// Mock PodExist method if provided
			if tt.mockPodExist != nil {
				patches := gomonkey.ApplyMethod(mockK8s, "PodExist", func(m *mockKubernetes, namespace, name string) (bool, error) {
					return tt.mockPodExist(namespace, name)
				})
				defer patches.Reset()
			}

			// Execute the function
			err := ns.cleanRuntimeNode(context.Background(), tt.localUIDs)

			// Assertions
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Verify the result if we have expected pods
			if tt.expectedPods != nil {
				var nodeRuntime networkv1beta1.NodeRuntime
				err = fakeClient.Get(context.Background(), client.ObjectKey{Name: "test-node"}, &nodeRuntime)
				assert.NoError(t, err)

				// Compare pod statuses
				for uid, expectedStatus := range tt.expectedPods {
					actualStatus, exists := nodeRuntime.Status.Pods[uid]
					assert.True(t, exists, "Pod %s should exist", uid)
					assert.Equal(t, expectedStatus.PodID, actualStatus.PodID)

					// Compare status maps
					for statusType, expectedInfo := range expectedStatus.Status {
						actualInfo, exists := actualStatus.Status[statusType]
						if expectedInfo != nil {
							assert.True(t, exists, "Status %s should exist for pod %s", statusType, uid)
							assert.Equal(t, expectedInfo.LastUpdateTime.Time.Truncate(time.Second),
								actualInfo.LastUpdateTime.Time.Truncate(time.Second))
						}
					}
				}
			}
		})
	}
}

// Mock implementation of k8s.Kubernetes interface
type mockKubernetes struct {
	client   client.Client
	nodeName string
}

func (m *mockKubernetes) GetClient() client.Client {
	return m.client
}

func (m *mockKubernetes) NodeName() string {
	return m.nodeName
}

func (m *mockKubernetes) PodExist(namespace, name string) (bool, error) {
	// This will be mocked in tests
	return false, nil
}

// Implement other required methods with dummy implementations
func (m *mockKubernetes) GetLocalPods() ([]*daemon.PodInfo, error) { return nil, nil }
func (m *mockKubernetes) GetPod(ctx context.Context, namespace, name string, cache bool) (*daemon.PodInfo, error) {
	return nil, nil
}
func (m *mockKubernetes) GetServiceCIDR() *types.IPNetSet                       { return nil }
func (m *mockKubernetes) SetNodeAllocatablePod(count int) error                 { return nil }
func (m *mockKubernetes) PatchNodeAnnotations(anno map[string]string) error     { return nil }
func (m *mockKubernetes) PatchPodIPInfo(info *daemon.PodInfo, ips string) error { return nil }
func (m *mockKubernetes) PatchNodeIPResCondition(status corev1.ConditionStatus, reason, message string) error {
	return nil
}
func (m *mockKubernetes) RecordNodeEvent(eventType, reason, message string) {}
func (m *mockKubernetes) RecordPodEvent(podName, podNamespace, eventType, reason, message string) error {
	return nil
}
func (m *mockKubernetes) GetNodeDynamicConfigLabel() string { return "" }
func (m *mockKubernetes) GetDynamicConfigWithName(ctx context.Context, name string) (string, error) {
	return "", nil
}
func (m *mockKubernetes) SetCustomStatefulWorkloadKinds(kinds []string) error { return nil }
func (m *mockKubernetes) GetTrunkID() string                                  { return "" }
func (m *mockKubernetes) Node() *corev1.Node                                  { return &corev1.Node{} }
