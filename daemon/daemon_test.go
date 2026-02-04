package daemon

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/eni"
	"github.com/AliyunContainerService/terway/pkg/k8s/mocks"
	k8smocks "github.com/AliyunContainerService/terway/pkg/k8s/mocks"
	"github.com/AliyunContainerService/terway/pkg/storage"
	storagemocks "github.com/AliyunContainerService/terway/pkg/storage/mocks"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
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
			scheme := k8sruntime.NewScheme()
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
				mockK8s.On("PodExist", mock.Anything, mock.Anything, mock.Anything).Return(
					func(ctx context.Context, namespace string, name string) (bool, error) {
						return tt.mockPodExist(namespace, name)
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
			name: "eniip resource with eniID not in attached - removed",
			podResources: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{
							Type:  "eniIp",
							ENIID: "eni-missing",
						},
					},
				},
			},
			attachedENIID: map[string]*daemon.ENI{
				"eni-1": {ID: "eni-1"},
			},
			expectedResult: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{},
				},
			},
		},
		{
			name: "eniip resource with empty eniID and mac not in attached - removed",
			podResources: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{
							Type:  "eniIp",
							ENIID: "",
							ID:    "mac-unknown.ip-1",
						},
					},
				},
			},
			attachedENIID: map[string]*daemon.ENI{
				"eni-1": {MAC: "mac-1"},
			},
			expectedResult: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{},
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

func TestExtractIPs(t *testing.T) {
	tests := []struct {
		name          string
		resourceItem  daemon.ResourceItem
		expectedIPv4  string
		expectedIPv6  string
		expectedENIID string
		expectValidV4 bool
		expectValidV6 bool
	}{
		{
			name: "valid ipv4 and ipv6",
			resourceItem: daemon.ResourceItem{
				IPv4:  "192.168.1.10",
				IPv6:  "2001:db8::1",
				ENIID: "eni-123",
			},
			expectedIPv4:  "192.168.1.10",
			expectedIPv6:  "2001:db8::1",
			expectedENIID: "eni-123",
			expectValidV4: true,
			expectValidV6: true,
		},
		{
			name: "only ipv4",
			resourceItem: daemon.ResourceItem{
				IPv4:  "10.0.0.100",
				IPv6:  "",
				ENIID: "eni-456",
			},
			expectedIPv4:  "10.0.0.100",
			expectedENIID: "eni-456",
			expectValidV4: true,
			expectValidV6: false,
		},
		{
			name: "only ipv6",
			resourceItem: daemon.ResourceItem{
				IPv4:  "",
				IPv6:  "fe80::1",
				ENIID: "eni-789",
			},
			expectedIPv6:  "fe80::1",
			expectedENIID: "eni-789",
			expectValidV4: false,
			expectValidV6: true,
		},
		{
			name: "empty ips",
			resourceItem: daemon.ResourceItem{
				IPv4:  "",
				IPv6:  "",
				ENIID: "eni-empty",
			},
			expectedENIID: "eni-empty",
			expectValidV4: false,
			expectValidV6: false,
		},
		{
			name: "invalid ip addresses",
			resourceItem: daemon.ResourceItem{
				IPv4:  "invalid-ipv4",
				IPv6:  "invalid-ipv6",
				ENIID: "eni-invalid",
			},
			expectedENIID: "eni-invalid",
			expectValidV4: false,
			expectValidV6: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ipv4, ipv6, eniID := extractIPs(tt.resourceItem)

			assert.Equal(t, tt.expectedENIID, eniID)

			if tt.expectValidV4 {
				assert.True(t, ipv4.IsValid())
				assert.Equal(t, tt.expectedIPv4, ipv4.String())
			} else {
				assert.False(t, ipv4.IsValid())
			}

			if tt.expectValidV6 {
				assert.True(t, ipv6.IsValid())
				assert.Equal(t, tt.expectedIPv6, ipv6.String())
			} else {
				assert.False(t, ipv6.IsValid())
			}
		})
	}
}

func TestSetRequest(t *testing.T) {
	tests := []struct {
		name         string
		resourceItem daemon.ResourceItem
	}{
		{
			name: "set request with ipv4 and ipv6",
			resourceItem: daemon.ResourceItem{
				IPv4:  "192.168.1.10",
				IPv6:  "2001:db8::1",
				ENIID: "eni-123",
			},
		},
		{
			name: "set request with only ipv4",
			resourceItem: daemon.ResourceItem{
				IPv4:  "10.0.0.100",
				IPv6:  "",
				ENIID: "eni-456",
			},
		},
		{
			name: "set request with only ipv6",
			resourceItem: daemon.ResourceItem{
				IPv4:  "",
				IPv6:  "fe80::1",
				ENIID: "eni-789",
			},
		},
		{
			name: "set request with empty ips",
			resourceItem: daemon.ResourceItem{
				IPv4:  "",
				IPv6:  "",
				ENIID: "eni-empty",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &eni.LocalIPRequest{}
			setRequest(req, tt.resourceItem)

			// Verify that the request fields match the extracted values
			expectedIPv4, expectedIPv6, expectedENIID := extractIPs(tt.resourceItem)
			assert.Equal(t, expectedIPv4, req.IPv4)
			assert.Equal(t, expectedIPv6, req.IPv6)
			assert.Equal(t, expectedENIID, req.NetworkInterfaceID)
		})
	}
}

func TestDefaultForNetConf(t *testing.T) {
	tests := []struct {
		name        string
		netConf     []*rpc.NetConf
		expectedErr bool
		errContains string
	}{
		{
			name:        "empty netConf returns nil",
			netConf:     []*rpc.NetConf{},
			expectedErr: false,
		},
		{
			name: "duplicate default route returns error",
			netConf: []*rpc.NetConf{
				{DefaultRoute: true, IfName: "eth0"},
				{DefaultRoute: true, IfName: "eth1"},
			},
			expectedErr: true,
			errContains: "dumplicated",
		},
		{
			name: "default interface not set returns error",
			netConf: []*rpc.NetConf{
				{IfName: "eth1", BasicInfo: &rpc.BasicInfo{}},
			},
			expectedErr: true,
			errContains: "default interface is not set",
		},
		{
			name: "no default route sets first eth0 to default",
			netConf: []*rpc.NetConf{
				{IfName: IfEth0, DefaultRoute: false},
			},
			expectedErr: false,
		},
		{
			name: "no default route sets first empty ifname to default",
			netConf: []*rpc.NetConf{
				{IfName: "", DefaultRoute: false},
			},
			expectedErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := defaultForNetConf(tt.netConf)
			if tt.expectedErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestDefaultIf(t *testing.T) {
	assert.True(t, defaultIf(""))
	assert.True(t, defaultIf(IfEth0))
	assert.False(t, defaultIf("eth1"))
	assert.False(t, defaultIf("cali0"))
}

func TestParseNetworkResource_UnknownType(t *testing.T) {
	// Unknown type returns nil
	item := daemon.ResourceItem{Type: "unknown"}
	res := parseNetworkResource(item)
	assert.Nil(t, res)
}

func TestToRPCMapping(t *testing.T) {
	tests := []struct {
		name     string
		status   eni.Status
		expected *rpc.ResourceMapping
	}{
		{
			name: "basic status",
			status: eni.Status{
				NetworkInterfaceID:   "eni-123",
				MAC:                  "aa:bb:cc:dd:ee:ff",
				Type:                 "primary",
				AllocInhibitExpireAt: "2024-01-01T00:00:00Z",
				Status:               "InUse",
				Usage:                [][]string{},
			},
			expected: &rpc.ResourceMapping{
				NetworkInterfaceID:   "eni-123",
				MAC:                  "aa:bb:cc:dd:ee:ff",
				Type:                 "primary",
				AllocInhibitExpireAt: "2024-01-01T00:00:00Z",
				Status:               "InUse",
				Info:                 []string{},
			},
		},
		{
			name: "status with usage",
			status: eni.Status{
				NetworkInterfaceID:   "eni-456",
				MAC:                  "11:22:33:44:55:66",
				Type:                 "secondary",
				AllocInhibitExpireAt: "",
				Status:               "Available",
				Usage: [][]string{
					{"pod1", "namespace1", "192.168.1.10"},
					{"pod2", "namespace2", "192.168.1.11"},
				},
			},
			expected: &rpc.ResourceMapping{
				NetworkInterfaceID:   "eni-456",
				MAC:                  "11:22:33:44:55:66",
				Type:                 "secondary",
				AllocInhibitExpireAt: "",
				Status:               "Available",
				Info: []string{
					"pod1  namespace1  192.168.1.10",
					"pod2  namespace2  192.168.1.11",
				},
			},
		},
		{
			name: "status with empty fields",
			status: eni.Status{
				NetworkInterfaceID:   "",
				MAC:                  "",
				Type:                 "",
				AllocInhibitExpireAt: "",
				Status:               "",
				Usage:                nil,
			},
			expected: &rpc.ResourceMapping{
				NetworkInterfaceID:   "",
				MAC:                  "",
				Type:                 "",
				AllocInhibitExpireAt: "",
				Status:               "",
				Info:                 []string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toRPCMapping(tt.status)
			assert.NotNil(t, result)
			assert.Equal(t, tt.expected.NetworkInterfaceID, result.NetworkInterfaceID)
			assert.Equal(t, tt.expected.MAC, result.MAC)
			assert.Equal(t, tt.expected.Type, result.Type)
			assert.Equal(t, tt.expected.AllocInhibitExpireAt, result.AllocInhibitExpireAt)
			assert.Equal(t, tt.expected.Status, result.Status)
			// Info can be nil or empty slice, both are acceptable
			if len(tt.expected.Info) == 0 {
				assert.Empty(t, result.Info)
			} else {
				assert.Equal(t, tt.expected.Info, result.Info)
			}
		})
	}
}

func TestNetworkService_Config(t *testing.T) {
	tests := []struct {
		name           string
		daemonMode     string
		configFilePath string
	}{
		{
			name:           "basic config",
			daemonMode:     daemon.ModeENIMultiIP,
			configFilePath: "/etc/eni/eni.json",
		},
		{
			name:           "eni only mode",
			daemonMode:     daemon.ModeENIOnly,
			configFilePath: "/etc/eni/config.json",
		},
		{
			name:           "vpc mode",
			daemonMode:     daemon.ModeVPC,
			configFilePath: "/etc/eni/vpc.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns := &networkService{
				daemonMode:     tt.daemonMode,
				configFilePath: tt.configFilePath,
			}

			config := ns.Config()

			assert.NotNil(t, config)
			assert.GreaterOrEqual(t, len(config), 3)

			// Check specific keys
			foundName := false
			foundDaemonMode := false
			foundConfigPath := false

			for _, entry := range config {
				if entry.Key == tracingKeyName && entry.Value == networkServiceName {
					foundName = true
				}
				if entry.Key == tracingKeyDaemonMode && entry.Value == tt.daemonMode {
					foundDaemonMode = true
				}
				if entry.Key == tracingKeyConfigFilePath && entry.Value == tt.configFilePath {
					foundConfigPath = true
				}
			}

			assert.True(t, foundName, "should have name entry")
			assert.True(t, foundDaemonMode, "should have daemon mode entry")
			assert.True(t, foundConfigPath, "should have config path entry")
		})
	}
}

func TestNetworkService_Trace(t *testing.T) {
	tests := []struct {
		name          string
		setupStorage  func(t *testing.T) storage.Storage
		setupPending  func(ns *networkService)
		expectedError bool
	}{
		{
			name: "trace with no pending pods and no resources",
			setupStorage: func(t *testing.T) storage.Storage {
				mockStorage := storagemocks.NewStorage(t)
				mockStorage.On("List").Return([]interface{}{}, nil)
				return mockStorage
			},
			expectedError: false,
		},
		{
			name: "trace with pending pods",
			setupStorage: func(t *testing.T) storage.Storage {
				mockStorage := storagemocks.NewStorage(t)
				mockStorage.On("List").Return([]interface{}{}, nil)
				return mockStorage
			},
			setupPending: func(ns *networkService) {
				ns.pendingPods.Store("default/pod1", struct{}{})
				ns.pendingPods.Store("default/pod2", struct{}{})
			},
			expectedError: false,
		},
		{
			name: "trace with resources",
			setupStorage: func(t *testing.T) storage.Storage {
				mockStorage := storagemocks.NewStorage(t)
				mockStorage.On("List").Return([]interface{}{
					daemon.PodResources{
						PodInfo: &daemon.PodInfo{
							Namespace: "default",
							Name:      "test-pod",
						},
						Resources: []daemon.ResourceItem{
							{
								Type: daemon.ResourceTypeENIIP,
								ID:   "eni-123.192.168.1.10",
							},
						},
					},
				}, nil)
				return mockStorage
			},
			expectedError: false,
		},
		{
			name: "trace with resourceDB error",
			setupStorage: func(t *testing.T) storage.Storage {
				mockStorage := storagemocks.NewStorage(t)
				mockStorage.On("List").Return(nil, assert.AnError)
				return mockStorage
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns := &networkService{
				resourceDB: tt.setupStorage(t),
			}

			if tt.setupPending != nil {
				tt.setupPending(ns)
			}

			trace := ns.Trace()

			assert.NotNil(t, trace)
			assert.GreaterOrEqual(t, len(trace), 1, "should at least have pending pods count")

			// Check for pending pods count entry
			foundPendingCount := false
			for _, entry := range trace {
				if entry.Key == tracingKeyPendingPodsCount {
					foundPendingCount = true
				}
				if tt.expectedError && entry.Key == "error" {
					assert.NotEmpty(t, entry.Value)
				}
			}
			assert.True(t, foundPendingCount, "should have pending pods count")
		})
	}
}

func TestNetworkService_Execute(t *testing.T) {
	tests := []struct {
		name          string
		command       string
		setupMocks    func(*gomonkey.Patches, *networkService)
		expectMessage bool
	}{
		{
			name:    "execute mapping command",
			command: commandMapping,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock eniMgr.Status
				patches.ApplyMethodFunc(ns.eniMgr, "Status", func() []eni.Status {
					return []eni.Status{
						{
							NetworkInterfaceID: "eni-123",
							MAC:                "aa:bb:cc:dd:ee:ff",
							Type:               "primary",
							Status:             "InUse",
						},
					}
				})
			},
			expectMessage: true,
		},
		{
			name:    "execute resdb command",
			command: commandResDB,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock resourceDB.List
				patches.ApplyMethodFunc(ns.resourceDB, "List", func() ([]interface{}, error) {
					return []interface{}{
						daemon.PodResources{
							PodInfo: &daemon.PodInfo{
								Namespace: "default",
								Name:      "test-pod",
							},
						},
					}, nil
				})
			},
			expectMessage: true,
		},
		{
			name:    "execute resdb command with error",
			command: commandResDB,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock resourceDB.List to return error
				patches.ApplyMethodFunc(ns.resourceDB, "List", func() ([]interface{}, error) {
					return nil, assert.AnError
				})
			},
			expectMessage: true,
		},
		{
			name:          "execute unknown command",
			command:       "unknown",
			setupMocks:    func(patches *gomonkey.Patches, ns *networkService) {},
			expectMessage: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			ns := &networkService{
				eniMgr:     &eni.Manager{},
				resourceDB: newMockStorage(t),
			}

			if tt.setupMocks != nil {
				tt.setupMocks(patches, ns)
			}

			message := make(chan string, 1)
			ns.Execute(tt.command, nil, message)

			if tt.expectMessage {
				select {
				case msg := <-message:
					assert.NotEmpty(t, msg)
				case <-time.After(1 * time.Second):
					t.Fatal("timeout waiting for message")
				}
			}

			// Channel should be closed
			select {
			case _, ok := <-message:
				assert.False(t, ok, "message channel should be closed")
			case <-time.After(1 * time.Second):
				t.Fatal("timeout waiting for channel close")
			}
		})
	}
}

func TestNetworkService_GetResourceMapping(t *testing.T) {
	tests := []struct {
		name          string
		setupMocks    func(*gomonkey.Patches, *networkService)
		expectedCount int
	}{
		{
			name: "get empty mapping",
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock eniMgr.Status to return empty list
				patches.ApplyMethodFunc(ns.eniMgr, "Status", func() []eni.Status {
					return []eni.Status{}
				})
			},
			expectedCount: 0,
		},
		{
			name: "get single ENI mapping",
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock eniMgr.Status
				patches.ApplyMethodFunc(ns.eniMgr, "Status", func() []eni.Status {
					return []eni.Status{
						{
							NetworkInterfaceID:   "eni-123",
							MAC:                  "aa:bb:cc:dd:ee:ff",
							Type:                 "primary",
							AllocInhibitExpireAt: "2024-01-01T00:00:00Z",
							Status:               "InUse",
							Usage:                [][]string{},
						},
					}
				})
			},
			expectedCount: 1,
		},
		{
			name: "get multiple ENI mappings",
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock eniMgr.Status
				patches.ApplyMethodFunc(ns.eniMgr, "Status", func() []eni.Status {
					return []eni.Status{
						{
							NetworkInterfaceID: "eni-123",
							MAC:                "aa:bb:cc:dd:ee:ff",
							Type:               "primary",
							Status:             "InUse",
							Usage: [][]string{
								{"pod1", "namespace1", "192.168.1.10"},
							},
						},
						{
							NetworkInterfaceID: "eni-456",
							MAC:                "11:22:33:44:55:66",
							Type:               "secondary",
							Status:             "Available",
							Usage:              [][]string{},
						},
					}
				})
			},
			expectedCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime.GC()
			patches := gomonkey.NewPatches()
			defer func() {
				patches.Reset()
				runtime.GC()
			}()

			ns := &networkService{
				eniMgr: &eni.Manager{},
			}

			if tt.setupMocks != nil {
				tt.setupMocks(patches, ns)
			}

			mapping, err := ns.GetResourceMapping()

			assert.NoError(t, err)
			// mapping can be nil when there are no resources
			if tt.expectedCount == 0 {
				assert.Empty(t, mapping)
			} else {
				assert.NotNil(t, mapping)
				assert.Equal(t, tt.expectedCount, len(mapping))
			}

			for _, m := range mapping {
				assert.NotNil(t, m)
				// Info field can be nil or empty slice
			}
		})
	}
}

func TestRecordEvent(t *testing.T) {

	k8s := k8smocks.NewKubernetes(t)
	n := &networkService{
		k8s: k8s,
	}
	k8s.On("RecordNodeEvent", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	k8s.On("RecordPodEvent", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	_, err := n.RecordEvent(nil, &rpc.EventRequest{
		EventTarget:     rpc.EventTarget_EventTargetNode,
		K8SPodName:      "",
		K8SPodNamespace: "",
		EventType:       rpc.EventType_EventTypeWarning,
		Reason:          "",
		Message:         "",
	})

	require.NoError(t, err)
	_, err = n.RecordEvent(nil, &rpc.EventRequest{
		EventTarget:     rpc.EventTarget_EventTargetPod,
		K8SPodName:      "",
		K8SPodNamespace: "",
		EventType:       rpc.EventType_EventTypeNormal,
		Reason:          "",
		Message:         "",
	})
	require.NoError(t, err)
}

func TestRunDevicePlugin(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	dp := deviceplugin.NewENIDevicePlugin(0, deviceplugin.ENITypeMember)

	serveCalled := make(chan struct{}, 1)

	patches.ApplyFunc(deviceplugin.NewENIDevicePlugin, func() *deviceplugin.ENIDevicePlugin {
		return dp
	})
	patches.ApplyMethod(dp, "Serve", func() {
		serveCalled <- struct{}{}
	})

	runDevicePlugin("ENIMultiIP", &daemon.Config{
		EnableENITrunking: true,
		EnableERDMA:       true,
	}, &daemon.PoolConfig{})

	// 等待 goroutine 执行并验证 patch 被调用
	select {
	case <-serveCalled:
		// Patch 被成功调用
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for Serve to be called")
	}
}

func TestNewServer(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(NewNetworkServiceBuilder, func() *NetworkServiceBuilder {
		return &NetworkServiceBuilder{
			err: fmt.Errorf("some error"),
		}
	})
	_, err := newCRDV2Service(context.Background(), "", "")
	require.Error(t, err)

	_, err = newLegacyService(context.Background(), "", "")
	require.Error(t, err)
}
