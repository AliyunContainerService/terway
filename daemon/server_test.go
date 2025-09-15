package daemon

import (
	"context"
	"net/netip"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"

	"github.com/AliyunContainerService/terway/pkg/eni"
	"github.com/AliyunContainerService/terway/pkg/k8s/mocks"
	"github.com/AliyunContainerService/terway/pkg/storage"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

func Test_registerPrometheus(t *testing.T) {
	registerPrometheus()
}

func Test_ensureCNIConfig(t *testing.T) {
	_ = ensureCNIConfig()
}

// TestAllocIP tests the AllocIP function with various scenarios
func TestAllocIP(t *testing.T) {
	tests := []struct {
		name           string
		request        *rpc.AllocIPRequest
		podInfo        *daemon.PodInfo
		oldResource    daemon.PodResources
		expectedError  bool
		expectedIPType rpc.IPType
		setupMocks     func(*gomonkey.Patches, *networkService)
	}{
		{
			name: "successful allocation for ENIMultiIP pod",
			request: &rpc.AllocIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				Netns:                  "/proc/1234/ns/net",
				K8SPodInfraContainerId: "container-123",
			},
			podInfo: &daemon.PodInfo{
				Namespace:       "default",
				Name:            "test-pod",
				PodUID:          "pod-uid-123",
				PodNetworkType:  daemon.PodNetworkTypeENIMultiIP,
				PodENI:          false,
				ERdma:           false,
				TcIngress:       1000,
				TcEgress:        1000,
				NetworkPriority: "1",
			},
			oldResource:    daemon.PodResources{},
			expectedError:  false,
			expectedIPType: rpc.IPType_TypeENIMultiIP,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetPod using testify mock
				mockK8s := ns.k8s.(*mocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", false).Return(&daemon.PodInfo{
					Namespace:       "default",
					Name:            "test-pod",
					PodUID:          "pod-uid-123",
					PodNetworkType:  daemon.PodNetworkTypeENIMultiIP,
					PodENI:          false,
					ERdma:           false,
					TcIngress:       1000,
					TcEgress:        1000,
					NetworkPriority: "1",
				}, nil)

				// Mock eniMgr.Allocate
				patches.ApplyMethodFunc(ns.eniMgr, "Allocate", func(ctx context.Context, cni *daemon.CNI, req *eni.AllocRequest) (eni.NetworkResources, error) {
					// Create mock network resource
					mockResource := &eni.LocalIPResource{
						ENI: daemon.ENI{
							ID:  "eni-123",
							MAC: "aa:bb:cc:dd:ee:ff",
						},
						IP: types.IPSet2{
							IPv4: netip.MustParseAddr("192.168.1.100"),
							IPv6: netip.MustParseAddr("2001:db8::1"),
						},
					}
					return []eni.NetworkResource{mockResource}, nil
				})

				// Mock resourceDB.Put
				patches.ApplyMethodFunc(ns.resourceDB, "Put", func(key string, value interface{}) error {
					return nil
				})

				// Mock k8s.GetServiceCIDR
				mockK8s.On("GetServiceCIDR").Return(&types.IPNetSet{})

				// Mock resourceDB.Get to return empty resource (simulating no existing resource)
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return nil, storage.ErrNotFound
				})
			},
		},
		{
			name: "successful allocation for VPCENI pod",
			request: &rpc.AllocIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				Netns:                  "/proc/1234/ns/net",
				K8SPodInfraContainerId: "container-123",
			},
			podInfo: &daemon.PodInfo{
				Namespace:      "default",
				Name:           "test-pod",
				PodUID:         "pod-uid-123",
				PodNetworkType: daemon.PodNetworkTypeVPCENI,
				PodENI:         false,
			},
			oldResource:    daemon.PodResources{},
			expectedError:  false,
			expectedIPType: rpc.IPType_TypeVPCENI,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Set daemon mode to ENIOnly for VPCENI
				ns.daemonMode = daemon.ModeENIOnly

				// Mock k8s.GetPod using testify mock
				mockK8s := ns.k8s.(*mocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", false).Return(&daemon.PodInfo{
					Namespace:      "default",
					Name:           "test-pod",
					PodUID:         "pod-uid-123",
					PodNetworkType: daemon.PodNetworkTypeVPCENI,
					PodENI:         false,
				}, nil)

				// Mock eniMgr.Allocate
				patches.ApplyMethodFunc(ns.eniMgr, "Allocate", func(ctx context.Context, cni *daemon.CNI, req *eni.AllocRequest) (eni.NetworkResources, error) {
					mockResource := &eni.LocalIPResource{
						ENI: daemon.ENI{
							ID:  "eni-123",
							MAC: "aa:bb:cc:dd:ee:ff",
						},
						IP: types.IPSet2{
							IPv4: netip.MustParseAddr("192.168.1.100"),
						},
					}
					return []eni.NetworkResource{mockResource}, nil
				})

				// Mock resourceDB.Put
				patches.ApplyMethodFunc(ns.resourceDB, "Put", func(key string, value interface{}) error {
					return nil
				})

				// Mock k8s.GetServiceCIDR
				mockK8s.On("GetServiceCIDR").Return(&types.IPNetSet{})

				// Mock resourceDB.Get to return empty resource (simulating no existing resource)
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return nil, storage.ErrNotFound
				})
			},
		},
		{
			name: "error when pod not found",
			request: &rpc.AllocIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				Netns:                  "/proc/1234/ns/net",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: true,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetPod to return error
				mockK8s := ns.k8s.(*mocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", false).Return((*daemon.PodInfo)(nil), assert.AnError)
			},
		},
		{
			name: "error when eniMgr.Allocate fails",
			request: &rpc.AllocIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				Netns:                  "/proc/1234/ns/net",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: true,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetPod
				mockK8s := ns.k8s.(*mocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", false).Return(&daemon.PodInfo{
					Namespace:      "default",
					Name:           "test-pod",
					PodUID:         "pod-uid-123",
					PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
					PodENI:         false,
				}, nil)

				// Mock eniMgr.Allocate to return error
				patches.ApplyMethodFunc(ns.eniMgr, "Allocate", func(ctx context.Context, cni *daemon.CNI, req *eni.AllocRequest) (eni.NetworkResources, error) {
					return nil, assert.AnError
				})

				// Mock eniMgr.Release
				patches.ApplyMethodFunc(ns.eniMgr, "Release", func(ctx context.Context, cni *daemon.CNI, req *eni.ReleaseRequest) error {
					return nil
				})

				// Mock resourceDB.Get to return empty resource (simulating no existing resource)
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return nil, storage.ErrNotFound
				})
			},
		},
		{
			name: "error when resourceDB.Put fails",
			request: &rpc.AllocIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				Netns:                  "/proc/1234/ns/net",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: true,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetPod
				mockK8s := ns.k8s.(*mocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", false).Return(&daemon.PodInfo{
					Namespace:      "default",
					Name:           "test-pod",
					PodUID:         "pod-uid-123",
					PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
					PodENI:         false,
				}, nil)

				// Mock eniMgr.Allocate
				patches.ApplyMethodFunc(ns.eniMgr, "Allocate", func(ctx context.Context, cni *daemon.CNI, req *eni.AllocRequest) (eni.NetworkResources, error) {
					mockResource := &eni.LocalIPResource{
						ENI: daemon.ENI{
							ID:  "eni-123",
							MAC: "aa:bb:cc:dd:ee:ff",
						},
						IP: types.IPSet2{
							IPv4: netip.MustParseAddr("192.168.1.100"),
						},
					}
					return []eni.NetworkResource{mockResource}, nil
				})

				// Mock resourceDB.Put to return error
				patches.ApplyMethodFunc(ns.resourceDB, "Put", func(key string, value interface{}) error {
					return assert.AnError
				})

				// Mock k8s.GetServiceCIDR
				mockK8s.On("GetServiceCIDR").Return(&types.IPNetSet{})

				// Mock resourceDB.Get to return empty resource (simulating no existing resource)
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return nil, storage.ErrNotFound
				})
			},
		},
		{
			name: "pod already processing",
			request: &rpc.AllocIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				Netns:                  "/proc/1234/ns/net",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: true,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Pre-populate pendingPods to simulate already processing
				ns.pendingPods.Store("default/test-pod", struct{}{})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create patches for mocking
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			// Create mock k8s client
			mockK8s := &mocks.Kubernetes{}

			// Create mock network service
			ns := &networkService{
				daemonMode:        daemon.ModeENIMultiIP,
				enableIPv4:        true,
				enableIPv6:        false,
				ipamType:          types.IPAMTypeDefault,
				eniMgr:            &eni.Manager{},
				resourceDB:        &mockStorage{},
				k8s:               mockK8s,
				pendingPods:       sync.Map{},
				enablePatchPodIPs: false,
			}

			// Setup mocks
			if tt.setupMocks != nil {
				tt.setupMocks(patches, ns)
			}

			// Call AllocIP
			ctx := context.Background()
			reply, err := ns.AllocIP(ctx, tt.request)

			// Verify results
			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, reply)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, reply)
				assert.True(t, reply.Success)
				assert.Equal(t, tt.expectedIPType, reply.IPType)
				assert.Equal(t, ns.enableIPv4, reply.IPv4)
				assert.Equal(t, ns.enableIPv6, reply.IPv6)
			}
		})
	}
}

// TestReleaseIP tests the ReleaseIP function with various scenarios
func TestReleaseIP(t *testing.T) {
	tests := []struct {
		name          string
		request       *rpc.ReleaseIPRequest
		podInfo       *daemon.PodInfo
		oldResource   daemon.PodResources
		expectedError bool
		setupMocks    func(*gomonkey.Patches, *networkService)
	}{
		{
			name: "successful release for ENIMultiIP pod",
			request: &rpc.ReleaseIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			podInfo: &daemon.PodInfo{
				Namespace:      "default",
				Name:           "test-pod",
				PodUID:         "pod-uid-123",
				PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
				IPStickTime:    0, // No IP stick time
			},
			oldResource: daemon.PodResources{
				PodInfo: &daemon.PodInfo{
					Namespace: "default",
					Name:      "test-pod",
					PodUID:    "pod-uid-123",
				},
				ContainerID: stringPtr("container-123"),
				Resources: []daemon.ResourceItem{
					{
						Type:   daemon.ResourceTypeENIIP,
						ID:     "eni-123.192.168.1.100",
						ENIID:  "eni-123",
						ENIMAC: "aa:bb:cc:dd:ee:ff",
						IPv4:   "192.168.1.100",
					},
				},
			},
			expectedError: false,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetPod
				mockK8s := ns.k8s.(*mocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", true).Return(&daemon.PodInfo{
					Namespace:      "default",
					Name:           "test-pod",
					PodUID:         "pod-uid-123",
					PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
					IPStickTime:    0,
				}, nil)

				// Mock resourceDB.Get to return existing resource
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return daemon.PodResources{
						PodInfo: &daemon.PodInfo{
							Namespace: "default",
							Name:      "test-pod",
							PodUID:    "pod-uid-123",
						},
						ContainerID: stringPtr("container-123"),
						Resources: []daemon.ResourceItem{
							{
								Type:   daemon.ResourceTypeENIIP,
								ID:     "eni-123.192.168.1.100",
								ENIID:  "eni-123",
								ENIMAC: "aa:bb:cc:dd:ee:ff",
								IPv4:   "192.168.1.100",
							},
						},
					}, nil
				})

				// Mock eniMgr.Release
				patches.ApplyMethodFunc(ns.eniMgr, "Release", func(ctx context.Context, cni *daemon.CNI, req *eni.ReleaseRequest) error {
					return nil
				})

				// Mock resourceDB.Delete
				patches.ApplyMethodFunc(ns.resourceDB, "Delete", func(key string) error {
					return nil
				})
			},
		},
		{
			name: "successful release for VPCENI pod",
			request: &rpc.ReleaseIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			podInfo: &daemon.PodInfo{
				Namespace:      "default",
				Name:           "test-pod",
				PodUID:         "pod-uid-123",
				PodNetworkType: daemon.PodNetworkTypeVPCENI,
				IPStickTime:    0,
			},
			oldResource: daemon.PodResources{
				PodInfo: &daemon.PodInfo{
					Namespace: "default",
					Name:      "test-pod",
					PodUID:    "pod-uid-123",
				},
				ContainerID: stringPtr("container-123"),
				Resources: []daemon.ResourceItem{
					{
						Type:   daemon.ResourceTypeENI,
						ID:     "eni-123",
						ENIID:  "eni-123",
						ENIMAC: "aa:bb:cc:dd:ee:ff",
						IPv4:   "192.168.1.100",
					},
				},
			},
			expectedError: false,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Set daemon mode to ENIOnly for VPCENI
				ns.daemonMode = daemon.ModeENIOnly

				// Mock k8s.GetPod
				mockK8s := ns.k8s.(*mocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", true).Return(&daemon.PodInfo{
					Namespace:      "default",
					Name:           "test-pod",
					PodUID:         "pod-uid-123",
					PodNetworkType: daemon.PodNetworkTypeVPCENI,
					IPStickTime:    0,
				}, nil)

				// Mock resourceDB.Get to return existing resource
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return daemon.PodResources{
						PodInfo: &daemon.PodInfo{
							Namespace: "default",
							Name:      "test-pod",
							PodUID:    "pod-uid-123",
						},
						ContainerID: stringPtr("container-123"),
						Resources: []daemon.ResourceItem{
							{
								Type:   daemon.ResourceTypeENI,
								ID:     "eni-123",
								ENIID:  "eni-123",
								ENIMAC: "aa:bb:cc:dd:ee:ff",
								IPv4:   "192.168.1.100",
							},
						},
					}, nil
				})

				// Mock eniMgr.Release
				patches.ApplyMethodFunc(ns.eniMgr, "Release", func(ctx context.Context, cni *daemon.CNI, req *eni.ReleaseRequest) error {
					return nil
				})

				// Mock resourceDB.Delete
				patches.ApplyMethodFunc(ns.resourceDB, "Delete", func(key string) error {
					return nil
				})
			},
		},
		{
			name: "pod not found - should return success",
			request: &rpc.ReleaseIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: false,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetPod to return NotFound error
				mockK8s := ns.k8s.(*mocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", true).Return((*daemon.PodInfo)(nil), k8sErr.NewNotFound(corev1.Resource("pod"), "test-pod"))
			},
		},
		{
			name: "error when pod not found but not NotFound error",
			request: &rpc.ReleaseIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: true,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetPod to return non-NotFound error
				mockK8s := ns.k8s.(*mocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", true).Return((*daemon.PodInfo)(nil), assert.AnError)
			},
		},
		{
			name: "error when getPodResource fails",
			request: &rpc.ReleaseIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: true,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetPod
				mockK8s := ns.k8s.(*mocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", true).Return(&daemon.PodInfo{
					Namespace:      "default",
					Name:           "test-pod",
					PodUID:         "pod-uid-123",
					PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
				}, nil)

				// Mock resourceDB.Get to return error
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return nil, assert.AnError
				})
			},
		},
		{
			name: "error when eniMgr.Release fails",
			request: &rpc.ReleaseIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: true,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetPod
				mockK8s := ns.k8s.(*mocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", true).Return(&daemon.PodInfo{
					Namespace:      "default",
					Name:           "test-pod",
					PodUID:         "pod-uid-123",
					PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
					IPStickTime:    0,
				}, nil)

				// Mock resourceDB.Get to return existing resource
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return daemon.PodResources{
						PodInfo: &daemon.PodInfo{
							Namespace: "default",
							Name:      "test-pod",
							PodUID:    "pod-uid-123",
						},
						ContainerID: stringPtr("container-123"),
						Resources: []daemon.ResourceItem{
							{
								Type:   daemon.ResourceTypeENIIP,
								ID:     "eni-123.192.168.1.100",
								ENIID:  "eni-123",
								ENIMAC: "aa:bb:cc:dd:ee:ff",
								IPv4:   "192.168.1.100",
							},
						},
					}, nil
				})

				// Mock eniMgr.Release to return error
				patches.ApplyMethodFunc(ns.eniMgr, "Release", func(ctx context.Context, cni *daemon.CNI, req *eni.ReleaseRequest) error {
					return assert.AnError
				})
			},
		},
		{
			name: "error when deletePodResource fails",
			request: &rpc.ReleaseIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: true,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetPod
				mockK8s := ns.k8s.(*mocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", true).Return(&daemon.PodInfo{
					Namespace:      "default",
					Name:           "test-pod",
					PodUID:         "pod-uid-123",
					PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
					IPStickTime:    0,
				}, nil)

				// Mock resourceDB.Get to return existing resource
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return daemon.PodResources{
						PodInfo: &daemon.PodInfo{
							Namespace: "default",
							Name:      "test-pod",
							PodUID:    "pod-uid-123",
						},
						ContainerID: stringPtr("container-123"),
						Resources: []daemon.ResourceItem{
							{
								Type:   daemon.ResourceTypeENIIP,
								ID:     "eni-123.192.168.1.100",
								ENIID:  "eni-123",
								ENIMAC: "aa:bb:cc:dd:ee:ff",
								IPv4:   "192.168.1.100",
							},
						},
					}, nil
				})

				// Mock eniMgr.Release
				patches.ApplyMethodFunc(ns.eniMgr, "Release", func(ctx context.Context, cni *daemon.CNI, req *eni.ReleaseRequest) error {
					return nil
				})

				// Mock resourceDB.Delete to return error
				patches.ApplyMethodFunc(ns.resourceDB, "Delete", func(key string) error {
					return assert.AnError
				})
			},
		},
		{
			name: "pod already processing",
			request: &rpc.ReleaseIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: true,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Pre-populate pendingPods to simulate already processing
				ns.pendingPods.Store("default/test-pod", struct{}{})
			},
		},
		{
			name: "container ID mismatch - should return success without releasing",
			request: &rpc.ReleaseIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "different-container-123",
			},
			expectedError: false,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetPod
				mockK8s := ns.k8s.(*mocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", true).Return(&daemon.PodInfo{
					Namespace:      "default",
					Name:           "test-pod",
					PodUID:         "pod-uid-123",
					PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
				}, nil)

				// Mock resourceDB.Get with different container ID
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return daemon.PodResources{
						PodInfo: &daemon.PodInfo{
							Namespace: "default",
							Name:      "test-pod",
							PodUID:    "pod-uid-123",
						},
						ContainerID: stringPtr("old-container-123"), // Different container ID
						Resources: []daemon.ResourceItem{
							{
								Type:   daemon.ResourceTypeENIIP,
								ID:     "eni-123.192.168.1.100",
								ENIID:  "eni-123",
								ENIMAC: "aa:bb:cc:dd:ee:ff",
								IPv4:   "192.168.1.100",
							},
						},
					}, nil
				})
			},
		},
		{
			name: "IP stick time > 0 and not CRD mode - should not release",
			request: &rpc.ReleaseIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: false,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Set IPAM type to non-CRD
				ns.ipamType = types.IPAMTypeDefault

				// Mock k8s.GetPod with IPStickTime > 0
				mockK8s := ns.k8s.(*mocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", true).Return(&daemon.PodInfo{
					Namespace:      "default",
					Name:           "test-pod",
					PodUID:         "pod-uid-123",
					PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
					IPStickTime:    3600, // 1 hour stick time
				}, nil)

				// Mock resourceDB.Get to return existing resource
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return daemon.PodResources{
						PodInfo: &daemon.PodInfo{
							Namespace: "default",
							Name:      "test-pod",
							PodUID:    "pod-uid-123",
						},
						ContainerID: stringPtr("container-123"),
						Resources: []daemon.ResourceItem{
							{
								Type:   daemon.ResourceTypeENIIP,
								ID:     "eni-123.192.168.1.100",
								ENIID:  "eni-123",
								ENIMAC: "aa:bb:cc:dd:ee:ff",
								IPv4:   "192.168.1.100",
							},
						},
					}, nil
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create patches for mocking
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			// Create mock k8s client
			mockK8s := &mocks.Kubernetes{}

			// Create mock network service
			ns := &networkService{
				daemonMode:        daemon.ModeENIMultiIP,
				enableIPv4:        true,
				enableIPv6:        false,
				ipamType:          types.IPAMTypeDefault,
				eniMgr:            &eni.Manager{},
				resourceDB:        &mockStorage{},
				k8s:               mockK8s,
				pendingPods:       sync.Map{},
				enablePatchPodIPs: false,
			}

			// Setup mocks
			if tt.setupMocks != nil {
				tt.setupMocks(patches, ns)
			}

			// Call ReleaseIP
			ctx := context.Background()
			reply, err := ns.ReleaseIP(ctx, tt.request)

			// Verify results
			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, reply)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, reply)
				assert.True(t, reply.Success)
				assert.Equal(t, ns.enableIPv4, reply.IPv4)
				assert.Equal(t, ns.enableIPv6, reply.IPv6)
			}

			// Verify mock expectations
			mockK8s.AssertExpectations(t)
		})
	}
}

// Helper function to create string pointer
func stringPtr(s string) *string {
	return &s
}

// Mock implementations for testing

type mockStorage struct{}

func (m *mockStorage) Get(key string) (interface{}, error) {
	return nil, storage.ErrNotFound
}

func (m *mockStorage) Put(key string, value interface{}) error {
	return nil
}

func (m *mockStorage) Delete(key string) error {
	return nil
}

func (m *mockStorage) List() ([]interface{}, error) {
	return nil, nil
}
