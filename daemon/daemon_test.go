package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/pkg/k8s/mocks"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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
			mockK8s := &mocks.Kubernetes{}
			mockK8s.On("GetClient").Return(fakeClient).Maybe()
			mockK8s.On("NodeName").Return("test-node").Maybe()
			if tt.mockPodExist != nil {
				mockK8s.On("PodExist", mock.Anything, mock.Anything).Return(
					func(namespace string, name string) bool {
						result, _ := tt.mockPodExist(namespace, name)
						return result
					},
					func(namespace string, name string) error {
						_, err := tt.mockPodExist(namespace, name)
						return err
					}).Maybe()
			}

			// Create networkService instance
			ns := &networkService{
				ipamType:   tt.ipamType,
				daemonMode: tt.daemonMode,
				k8s:        mockK8s,
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

			mockK8s.AssertExpectations(t)
		})
	}
}

func TestGetPodIPs(t *testing.T) {
	tests := []struct {
		name     string
		netConfs []*rpc.NetConf
		want     []string
	}{
		{
			name:     "empty net confs",
			netConfs: []*rpc.NetConf{},
			want:     []string{},
		},
		{
			name: "nil basic info",
			netConfs: []*rpc.NetConf{
				{
					IfName:    "eth0",
					BasicInfo: nil,
				},
			},
			want: []string{},
		},
		{
			name: "nil pod ip",
			netConfs: []*rpc.NetConf{
				{
					IfName: "eth0",
					BasicInfo: &rpc.BasicInfo{
						PodIP: nil,
					},
				},
			},
			want: []string{},
		},
		{
			name: "only ipv4",
			netConfs: []*rpc.NetConf{
				{
					IfName: "eth0",
					BasicInfo: &rpc.BasicInfo{
						PodIP: &rpc.IPSet{
							IPv4: "192.168.1.10",
							IPv6: "",
						},
					},
				},
			},
			want: []string{"192.168.1.10"},
		},
		{
			name: "only ipv6",
			netConfs: []*rpc.NetConf{
				{
					IfName: "eth0",
					BasicInfo: &rpc.BasicInfo{
						PodIP: &rpc.IPSet{
							IPv4: "",
							IPv6: "2001:db8::1",
						},
					},
				},
			},
			want: []string{"2001:db8::1"},
		},
		{
			name: "both ipv4 and ipv6",
			netConfs: []*rpc.NetConf{
				{
					IfName: "eth0",
					BasicInfo: &rpc.BasicInfo{
						PodIP: &rpc.IPSet{
							IPv4: "192.168.1.10",
							IPv6: "2001:db8::1",
						},
					},
				},
			},
			want: []string{"192.168.1.10", "2001:db8::1"},
		},
		{
			name: "multiple interfaces with eth0 only",
			netConfs: []*rpc.NetConf{
				{
					IfName: "eth1",
					BasicInfo: &rpc.BasicInfo{
						PodIP: &rpc.IPSet{
							IPv4: "192.168.2.10",
						},
					},
				},
				{
					IfName: "eth0",
					BasicInfo: &rpc.BasicInfo{
						PodIP: &rpc.IPSet{
							IPv4: "192.168.1.10",
						},
					},
				},
			},
			want: []string{"192.168.1.10"},
		},
		{
			name: "multiple interfaces with empty ifname",
			netConfs: []*rpc.NetConf{
				{
					IfName: "",
					BasicInfo: &rpc.BasicInfo{
						PodIP: &rpc.IPSet{
							IPv6: "2001:db8::1",
						},
					},
				},
				{
					IfName: "eth1",
					BasicInfo: &rpc.BasicInfo{
						PodIP: &rpc.IPSet{
							IPv4: "192.168.2.10",
						},
					},
				},
			},
			want: []string{"2001:db8::1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getPodIPs(tt.netConfs)
			require.ElementsMatch(t, tt.want, got)
		})
	}
}

func TestFilterENINotFound(t *testing.T) {
	tests := []struct {
		name           string
		podResources   []daemon.PodResources
		attachedENIID  map[string]*daemon.ENI
		expectedResult []daemon.PodResources
	}{
		{
			name:           "empty pod resources",
			podResources:   []daemon.PodResources{},
			attachedENIID:  map[string]*daemon.ENI{},
			expectedResult: []daemon.PodResources{},
		},
		{
			name: "pod resources with no ENIIP type",
			podResources: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{
							Type: "eni",
							ID:   "eni-1",
						},
					},
				},
			},
			attachedENIID: map[string]*daemon.ENI{},
			expectedResult: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{
							Type: "eni",
							ID:   "eni-1",
						},
					},
				},
			},
		},
		{
			name: "eniip resource with eniID found",
			podResources: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{
							Type:  "eniIp",
							ENIID: "eni-1",
						},
					},
				},
			},
			attachedENIID: map[string]*daemon.ENI{
				"eni-1": {
					ID: "eni-1",
				},
			},
			expectedResult: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{
							Type:  "eniIp",
							ENIID: "eni-1",
						},
					},
				},
			},
		},

		{
			name: "eniip resource with empty eniID and mac found",
			podResources: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{
							Type:  "eniIp",
							ENIID: "",
							ID:    "mac-1.ip-1",
						},
					},
				},
			},
			attachedENIID: map[string]*daemon.ENI{
				"eni-1": {
					MAC: "mac-1",
				},
			},
			expectedResult: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{
							Type:  "eniIp",
							ENIID: "",
							ID:    "mac-1.ip-1",
						},
					},
				},
			},
		},

		{
			name: "multiple resources with mixed conditions",
			podResources: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{
							Type:  "eniIp",
							ENIID: "eni-1",
						},
						{
							Type:  "eniIp",
							ENIID: "",
							ID:    "mac-2.ip-2",
						},
						{
							Type: "eni",
							ID:   "eni-3",
						},
					},
				},
			},
			attachedENIID: map[string]*daemon.ENI{
				"eni-1": {
					ID: "eni-1",
				},
				"eni-2": {
					MAC: "mac-2",
				},
			},
			expectedResult: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{
							Type:  "eniIp",
							ENIID: "eni-1",
						},
						{
							Type:  "eniIp",
							ENIID: "",
							ID:    "mac-2.ip-2",
						},
						{
							Type: "eni",
							ID:   "eni-3",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterENINotFound(tt.podResources, tt.attachedENIID)
			require.ElementsMatch(t, tt.expectedResult, result)
		})
	}
}
