package daemon

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	clientmocks "github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
	aliyuneni "github.com/AliyunContainerService/terway/pkg/aliyun/eni"
	"github.com/AliyunContainerService/terway/pkg/aliyun/instance"
	instancemocks "github.com/AliyunContainerService/terway/pkg/aliyun/instance/mocks"
	"github.com/AliyunContainerService/terway/pkg/eni"
	"github.com/AliyunContainerService/terway/pkg/factory"
	"github.com/AliyunContainerService/terway/pkg/factory/aliyun"
	k8smocks "github.com/AliyunContainerService/terway/pkg/k8s/mocks"
	"github.com/AliyunContainerService/terway/pkg/storage"
	storagemocks "github.com/AliyunContainerService/terway/pkg/storage/mocks"
	vswpool "github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
	daemon_types "github.com/AliyunContainerService/terway/types/daemon"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/alexflint/go-filemutex"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/featuregate"
)

func Test_registerPrometheus(t *testing.T) {
	registerPrometheus()
}

func Test_ensureCNIConfig(t *testing.T) {
	_ = ensureCNIConfig()
}

// newMockStorage creates a new mock storage with default expectations
func newMockStorage(t *testing.T) *storagemocks.Storage {
	mockStorage := storagemocks.NewStorage(t)
	mockStorage.On("Get", mock.Anything).Return(nil, storage.ErrNotFound).Maybe()
	mockStorage.On("Put", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStorage.On("Delete", mock.Anything).Return(nil).Maybe()
	mockStorage.On("List").Return([]interface{}{}, nil).Maybe()
	return mockStorage
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
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
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
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
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
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
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
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
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
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
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
			runtime.GC()
			// Create patches for mocking
			patches := gomonkey.NewPatches()
			defer func() {
				patches.Reset()
				runtime.GC()
			}()

			// Create mock k8s client
			mockK8s := &k8smocks.Kubernetes{}

			// Create mock storage
			mockStorage := newMockStorage(t)

			// Create mock network service
			ns := &networkService{
				daemonMode:        daemon.ModeENIMultiIP,
				enableIPv4:        true,
				enableIPv6:        false,
				ipamType:          types.IPAMTypeDefault,
				eniMgr:            &eni.Manager{},
				resourceDB:        mockStorage,
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
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
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
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
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
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", true).Return((*daemon.PodInfo)(nil), k8sErr.NewNotFound(corev1.Resource("pod"), "test-pod"))
			},
		},
		{
			name: "non-NotFound error when getting pod - should continue release",
			request: &rpc.ReleaseIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: false, // ReleaseIP ignores non-NotFound errors to not block delete operations
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetPod to return non-NotFound error
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
				mockK8s.On("GetPod", mock.Anything, "default", "test-pod", true).Return((*daemon.PodInfo)(nil), assert.AnError)

				// Mock resourceDB.Get to return ErrNotFound (no existing resource)
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return nil, storage.ErrNotFound
				})
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
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
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
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
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
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
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
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
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
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
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
			runtime.GC()
			// Create patches for mocking
			patches := gomonkey.NewPatches()
			defer func() {
				patches.Reset()
				runtime.GC()
			}()

			// Create mock k8s client
			mockK8s := &k8smocks.Kubernetes{}

			// Create mock storage
			mockStorage := newMockStorage(t)

			// Create mock network service
			ns := &networkService{
				daemonMode:        daemon.ModeENIMultiIP,
				enableIPv4:        true,
				enableIPv6:        false,
				ipamType:          types.IPAMTypeDefault,
				eniMgr:            &eni.Manager{},
				resourceDB:        mockStorage,
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

// TestGetIPInfo tests the GetIPInfo function with various scenarios
func TestGetIPInfo(t *testing.T) {
	tests := []struct {
		name           string
		request        *rpc.GetInfoRequest
		podInfo        *daemon.PodInfo
		oldResource    daemon.PodResources
		expectedError  bool
		expectedIPType rpc.IPType
		setupMocks     func(*gomonkey.Patches, *networkService)
	}{
		{
			name: "successful get info for ENIMultiIP pod",
			request: &rpc.GetInfoRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			podInfo: &daemon.PodInfo{
				Namespace:      "default",
				Name:           "test-pod",
				PodUID:         "pod-uid-123",
				PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
			},
			oldResource: daemon.PodResources{
				PodInfo: &daemon.PodInfo{
					Namespace: "default",
					Name:      "test-pod",
					PodUID:    "pod-uid-123",
				},
				ContainerID: stringPtr("container-123"),
				NetConf:     `[{"BasicInfo":{"PodIP":{"IPv4":"192.168.1.100"}}}]`,
			},
			expectedError:  false,
			expectedIPType: rpc.IPType_TypeENIMultiIP,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// GetIPInfo does NOT call GetPod - it only uses resourceDB

				// Mock resourceDB.Get to return existing resource
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return daemon.PodResources{
						PodInfo: &daemon.PodInfo{
							Namespace: "default",
							Name:      "test-pod",
							PodUID:    "pod-uid-123",
						},
						ContainerID: stringPtr("container-123"),
						NetConf:     `[{"BasicInfo":{"PodIP":{"IPv4":"192.168.1.100"}}}]`,
					}, nil
				})
			},
		},
		{
			name: "successful get info for VPCENI pod",
			request: &rpc.GetInfoRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			podInfo: &daemon.PodInfo{
				Namespace:      "default",
				Name:           "test-pod",
				PodUID:         "pod-uid-123",
				PodNetworkType: daemon.PodNetworkTypeVPCENI,
			},
			oldResource: daemon.PodResources{
				PodInfo: &daemon.PodInfo{
					Namespace: "default",
					Name:      "test-pod",
					PodUID:    "pod-uid-123",
				},
				ContainerID: stringPtr("container-123"),
				NetConf:     `[{"BasicInfo":{"PodIP":{"IPv4":"192.168.1.100"}}}]`,
			},
			expectedError:  false,
			expectedIPType: rpc.IPType_TypeVPCENI,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Set daemon mode to ENIOnly for VPCENI
				ns.daemonMode = daemon.ModeENIOnly

				// GetIPInfo does NOT call GetPod - it only uses resourceDB

				// Mock resourceDB.Get to return existing resource
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return daemon.PodResources{
						PodInfo: &daemon.PodInfo{
							Namespace: "default",
							Name:      "test-pod",
							PodUID:    "pod-uid-123",
						},
						ContainerID: stringPtr("container-123"),
						NetConf:     `[{"BasicInfo":{"PodIP":{"IPv4":"192.168.1.100"}}}]`,
					}, nil
				})
			},
		},
		{
			name: "success when resource not in DB - return empty netconf",
			request: &rpc.GetInfoRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError:  false, // GetIPInfo returns success with empty netconf when resource not found
			expectedIPType: rpc.IPType_TypeENIMultiIP,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// GetIPInfo does NOT call GetPod

				// Mock resourceDB.Get to return ErrNotFound
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return nil, storage.ErrNotFound
				})
			},
		},
		{
			name: "error when getPodResource fails",
			request: &rpc.GetInfoRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: true,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// GetIPInfo does NOT call GetPod

				// Mock resourceDB.Get to return error (not ErrNotFound)
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return nil, assert.AnError
				})
			},
		},
		{
			name: "error when daemon mode is unknown",
			request: &rpc.GetInfoRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: true,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Set an invalid daemon mode
				ns.daemonMode = "UnknownMode"

				// GetIPInfo does NOT call GetPod
			},
		},
		{
			name: "success with ENIOnly daemon mode",
			request: &rpc.GetInfoRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError:  false,
			expectedIPType: rpc.IPType_TypeVPCENI,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Set daemon mode to ENIOnly
				ns.daemonMode = daemon.ModeENIOnly

				// GetIPInfo does NOT call GetPod

				// Mock resourceDB.Get to return existing resource
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return daemon.PodResources{
						PodInfo: &daemon.PodInfo{
							Namespace: "default",
							Name:      "test-pod",
							PodUID:    "pod-uid-123",
						},
						ContainerID: stringPtr("container-123"),
						NetConf:     `[{"BasicInfo":{"PodIP":{"IPv4":"192.168.1.100"}}}]`,
					}, nil
				})
			},
		},
		{
			name: "pod already processing",
			request: &rpc.GetInfoRequest{
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
			name: "container ID mismatch - should return success without netconf",
			request: &rpc.GetInfoRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "different-container-123",
			},
			expectedError:  false,
			expectedIPType: rpc.IPType_TypeENIMultiIP,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// GetIPInfo does NOT call GetPod

				// Mock resourceDB.Get with different container ID
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return daemon.PodResources{
						PodInfo: &daemon.PodInfo{
							Namespace: "default",
							Name:      "test-pod",
							PodUID:    "pod-uid-123",
						},
						ContainerID: stringPtr("old-container-123"), // Different container ID
						NetConf:     `[{"BasicInfo":{"PodIP":{"IPv4":"192.168.1.100"}}}]`,
					}, nil
				})
			},
		},
		{
			name: "successful get info with invalid JSON netconf - should ignore error",
			request: &rpc.GetInfoRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError:  false,
			expectedIPType: rpc.IPType_TypeENIMultiIP,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// GetIPInfo does NOT call GetPod

				// Mock resourceDB.Get with invalid JSON
				patches.ApplyMethodFunc(ns.resourceDB, "Get", func(key string) (interface{}, error) {
					return daemon.PodResources{
						PodInfo: &daemon.PodInfo{
							Namespace: "default",
							Name:      "test-pod",
							PodUID:    "pod-uid-123",
						},
						ContainerID: stringPtr("container-123"),
						NetConf:     `invalid json`, // Invalid JSON
					}, nil
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime.GC()
			// Create patches for mocking
			patches := gomonkey.NewPatches()
			defer func() {
				patches.Reset()
				runtime.GC()
			}()

			// Create mock k8s client
			mockK8s := &k8smocks.Kubernetes{}

			// Create mock storage
			mockStorage := newMockStorage(t)

			// Create mock network service
			ns := &networkService{
				daemonMode:        daemon.ModeENIMultiIP,
				enableIPv4:        true,
				enableIPv6:        false,
				ipamType:          types.IPAMTypeDefault,
				eniMgr:            &eni.Manager{},
				resourceDB:        mockStorage,
				k8s:               mockK8s,
				pendingPods:       sync.Map{},
				enablePatchPodIPs: false,
			}

			// Setup mocks
			if tt.setupMocks != nil {
				tt.setupMocks(patches, ns)
			}

			// Call GetIPInfo
			ctx := context.Background()
			reply, err := ns.GetIPInfo(ctx, tt.request)

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

			// Verify mock expectations
			mockK8s.AssertExpectations(t)
		})
	}
}

// TestGcPods tests the gcPods function with various scenarios
func TestGcPods(t *testing.T) {
	tests := []struct {
		name          string
		expectedError bool
		setupMocks    func(*gomonkey.Patches, *networkService)
	}{
		{
			name:          "successful gc with no pods to clean",
			expectedError: false,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetLocalPods to return empty list
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
				mockK8s.On("GetLocalPods", mock.Anything).Return([]*daemon.PodInfo{}, nil)

				// Mock resourceDB.List to return empty list
				patches.ApplyMethodFunc(ns.resourceDB, "List", func() ([]interface{}, error) {
					return []interface{}{}, nil
				})

				// Mock cleanRuntimeNode private method
				patches.ApplyPrivateMethod(ns, "cleanRuntimeNode", func(ctx context.Context, localUIDs sets.Set[string]) error {
					return nil
				})
			},
		},
		{
			name:          "successful gc with existing pods",
			expectedError: false,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetLocalPods to return existing pods
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
				mockK8s.On("GetLocalPods", mock.Anything).Return([]*daemon.PodInfo{
					{
						Namespace:     "default",
						Name:          "test-pod-1",
						PodUID:        "pod-uid-1",
						SandboxExited: false,
						PodIPs: types.IPSet{
							IPv4: net.ParseIP("192.168.1.100"),
						},
					},
				}, nil)

				// Mock resourceDB.List to return pod resources
				patches.ApplyMethodFunc(ns.resourceDB, "List", func() ([]interface{}, error) {
					return []interface{}{
						daemon.PodResources{
							PodInfo: &daemon.PodInfo{
								Namespace: "default",
								Name:      "test-pod-1",
								PodUID:    "pod-uid-1",
								PodIPs: types.IPSet{
									IPv4: net.ParseIP("192.168.1.100"),
								},
							},
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
					}, nil
				})

				// Mock ruleSync
				patches.ApplyFunc(ruleSync, func(ctx context.Context, res daemon.PodResources) error {
					return nil
				})

				// Mock cleanRuntimeNode private method
				patches.ApplyPrivateMethod(ns, "cleanRuntimeNode", func(ctx context.Context, localUIDs sets.Set[string]) error {
					return nil
				})
			},
		},
		{
			name:          "successful gc with pods to clean up",
			expectedError: false,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetLocalPods to return empty list (no existing pods)
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
				mockK8s.On("GetLocalPods", mock.Anything).Return([]*daemon.PodInfo{}, nil)

				// Mock resourceDB.List to return pod resources that need cleanup
				patches.ApplyMethodFunc(ns.resourceDB, "List", func() ([]interface{}, error) {
					return []interface{}{
						daemon.PodResources{
							PodInfo: &daemon.PodInfo{
								Namespace:   "default",
								Name:        "test-pod-1",
								PodUID:      "pod-uid-1",
								IPStickTime: 0, // No IP stick time
								PodIPs: types.IPSet{
									IPv4: net.ParseIP("192.168.1.100"),
								},
							},
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
					}, nil
				})

				// Mock k8s.PodExist to return false (pod doesn't exist)
				mockK8s.On("PodExist", mock.Anything, "default", "test-pod-1").Return(false, nil)

				// Mock gcPolicyRoutes
				patches.ApplyFunc(gcPolicyRoutes, func(ctx context.Context, mac string, containerIPNet *types.IPNetSet, namespace, name string) error {
					return nil
				})

				// Mock eniMgr.Release
				patches.ApplyMethodFunc(ns.eniMgr, "Release", func(ctx context.Context, cni *daemon.CNI, req *eni.ReleaseRequest) error {
					return nil
				})

				// Mock deletePodResource private method
				patches.ApplyPrivateMethod(ns, "deletePodResource", func(info *daemon.PodInfo) error {
					return nil
				})

				// Mock cleanRuntimeNode private method
				patches.ApplyPrivateMethod(ns, "cleanRuntimeNode", func(ctx context.Context, localUIDs sets.Set[string]) error {
					return nil
				})
			},
		},
		{
			name:          "error when GetLocalPods fails",
			expectedError: true,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetLocalPods to return error
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
				mockK8s.On("GetLocalPods", mock.Anything).Return(([]*daemon.PodInfo)(nil), assert.AnError)
			},
		},
		{
			name:          "error when resourceDB.List fails",
			expectedError: true,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetLocalPods
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
				mockK8s.On("GetLocalPods", mock.Anything).Return([]*daemon.PodInfo{}, nil)

				// Mock resourceDB.List to return error
				patches.ApplyMethodFunc(ns.resourceDB, "List", func() ([]interface{}, error) {
					return nil, assert.AnError
				})
			},
		},
		{
			name:          "error when eniMgr.Release fails",
			expectedError: true,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetLocalPods to return empty list
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
				mockK8s.On("GetLocalPods", mock.Anything).Return([]*daemon.PodInfo{}, nil)

				// Mock resourceDB.List to return pod resources
				patches.ApplyMethodFunc(ns.resourceDB, "List", func() ([]interface{}, error) {
					return []interface{}{
						daemon.PodResources{
							PodInfo: &daemon.PodInfo{
								Namespace:   "default",
								Name:        "test-pod-1",
								PodUID:      "pod-uid-1",
								IPStickTime: 0,
								PodIPs: types.IPSet{
									IPv4: net.ParseIP("192.168.1.100"),
								},
							},
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
					}, nil
				})

				// Mock k8s.PodExist to return false
				mockK8s.On("PodExist", mock.Anything, "default", "test-pod-1").Return(false, nil)

				// Mock gcPolicyRoutes
				patches.ApplyFunc(gcPolicyRoutes, func(ctx context.Context, mac string, containerIPNet *types.IPNetSet, namespace, name string) error {
					return nil
				})

				// Mock eniMgr.Release to return error
				patches.ApplyMethodFunc(ns.eniMgr, "Release", func(ctx context.Context, cni *daemon.CNI, req *eni.ReleaseRequest) error {
					return assert.AnError
				})
			},
		},
		{
			name:          "error when deletePodResource fails",
			expectedError: true,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Mock k8s.GetLocalPods to return empty list
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
				mockK8s.On("GetLocalPods", mock.Anything).Return([]*daemon.PodInfo{}, nil)

				// Mock resourceDB.List to return pod resources
				patches.ApplyMethodFunc(ns.resourceDB, "List", func() ([]interface{}, error) {
					return []interface{}{
						daemon.PodResources{
							PodInfo: &daemon.PodInfo{
								Namespace:   "default",
								Name:        "test-pod-1",
								PodUID:      "pod-uid-1",
								IPStickTime: 0,
								PodIPs: types.IPSet{
									IPv4: net.ParseIP("192.168.1.100"),
								},
							},
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
					}, nil
				})

				// Mock k8s.PodExist to return false
				mockK8s.On("PodExist", mock.Anything, "default", "test-pod-1").Return(false, nil)

				// Mock gcPolicyRoutes
				patches.ApplyFunc(gcPolicyRoutes, func(ctx context.Context, mac string, containerIPNet *types.IPNetSet, namespace, name string) error {
					return nil
				})

				// Mock eniMgr.Release
				patches.ApplyMethodFunc(ns.eniMgr, "Release", func(ctx context.Context, cni *daemon.CNI, req *eni.ReleaseRequest) error {
					return nil
				})

				// Mock deletePodResource private method to return error
				patches.ApplyPrivateMethod(ns, "deletePodResource", func(info *daemon.PodInfo) error {
					return assert.AnError
				})
			},
		},
		{
			name:          "successful gc with IP stick time logic",
			expectedError: false,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Set IPAM type to non-CRD
				ns.ipamType = types.IPAMTypeDefault

				// Mock k8s.GetLocalPods to return empty list
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
				mockK8s.On("GetLocalPods", mock.Anything).Return([]*daemon.PodInfo{}, nil)

				// Mock resourceDB.List to return pod resources with IP stick time
				patches.ApplyMethodFunc(ns.resourceDB, "List", func() ([]interface{}, error) {
					return []interface{}{
						daemon.PodResources{
							PodInfo: &daemon.PodInfo{
								Namespace:   "default",
								Name:        "test-pod-1",
								PodUID:      "pod-uid-1",
								IPStickTime: 3600, // 1 hour stick time
								PodIPs: types.IPSet{
									IPv4: net.ParseIP("192.168.1.100"),
								},
							},
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
					}, nil
				})

				// Mock k8s.PodExist to return false
				mockK8s.On("PodExist", mock.Anything, "default", "test-pod-1").Return(false, nil)

				// Mock resourceDB.Put to update IP stick time
				patches.ApplyMethodFunc(ns.resourceDB, "Put", func(key string, value interface{}) error {
					return nil
				})

				// Mock cleanRuntimeNode private method
				patches.ApplyPrivateMethod(ns, "cleanRuntimeNode", func(ctx context.Context, localUIDs sets.Set[string]) error {
					return nil
				})
			},
		},
		{
			name:          "successful gc with CRD mode",
			expectedError: false,
			setupMocks: func(patches *gomonkey.Patches, ns *networkService) {
				// Set IPAM type to CRD and daemon mode to ENIMultiIP for cleanRuntimeNode
				ns.ipamType = types.IPAMTypeCRD
				ns.daemonMode = daemon.ModeENIMultiIP

				// Mock k8s.GetLocalPods to return empty list
				mockK8s := ns.k8s.(*k8smocks.Kubernetes)
				mockK8s.On("GetLocalPods", mock.Anything).Return([]*daemon.PodInfo{}, nil)

				// Mock resourceDB.List to return pod resources
				patches.ApplyMethodFunc(ns.resourceDB, "List", func() ([]interface{}, error) {
					return []interface{}{
						daemon.PodResources{
							PodInfo: &daemon.PodInfo{
								Namespace:   "default",
								Name:        "test-pod-1",
								PodUID:      "pod-uid-1",
								IPStickTime: 3600, // IP stick time should be ignored in CRD mode
								PodIPs: types.IPSet{
									IPv4: net.ParseIP("192.168.1.100"),
								},
							},
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
					}, nil
				})

				// Mock k8s.PodExist to return false
				mockK8s.On("PodExist", mock.Anything, "default", "test-pod-1").Return(false, nil)

				// Mock gcPolicyRoutes
				patches.ApplyFunc(gcPolicyRoutes, func(ctx context.Context, mac string, containerIPNet *types.IPNetSet, namespace, name string) error {
					return nil
				})

				// Mock eniMgr.Release
				patches.ApplyMethodFunc(ns.eniMgr, "Release", func(ctx context.Context, cni *daemon.CNI, req *eni.ReleaseRequest) error {
					return nil
				})

				// Mock deletePodResource private method
				patches.ApplyPrivateMethod(ns, "deletePodResource", func(info *daemon.PodInfo) error {
					return nil
				})

				// Mock cleanRuntimeNode private method to avoid complex k8s client mocking
				patches.ApplyPrivateMethod(ns, "cleanRuntimeNode", func(ctx context.Context, localUIDs sets.Set[string]) error {
					return nil
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime.GC()
			// Create patches for mocking
			patches := gomonkey.NewPatches()
			defer func() {
				patches.Reset()
				runtime.GC()
			}()

			// Create mock k8s client
			mockK8s := &k8smocks.Kubernetes{}

			// Create mock storage
			mockStorage := newMockStorage(t)

			// Create mock network service
			ns := &networkService{
				daemonMode:        daemon.ModeENIMultiIP,
				enableIPv4:        true,
				enableIPv6:        false,
				ipamType:          types.IPAMTypeDefault,
				eniMgr:            &eni.Manager{},
				resourceDB:        mockStorage,
				k8s:               mockK8s,
				pendingPods:       sync.Map{},
				enablePatchPodIPs: false,
			}

			// Setup mocks
			if tt.setupMocks != nil {
				tt.setupMocks(patches, ns)
			}

			// Call gcPods
			ctx := context.Background()
			err := ns.gcPods(ctx)

			// Verify results
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
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

// TestNewUnixListener tests the newUnixListener function
func TestNewUnixListener(t *testing.T) {
	tests := []struct {
		name          string
		addr          string
		setupFunc     func(string)
		cleanupFunc   func(string)
		expectedError bool
	}{
		{
			name: "successful creation",
			addr: "/tmp/test-terway-socket.sock",
			setupFunc: func(addr string) {
				// Ensure directory exists
				os.MkdirAll(filepath.Dir(addr), 0700)
			},
			cleanupFunc: func(addr string) {
				os.Remove(addr)
			},
			expectedError: false,
		},
		{
			name: "create in non-existent directory",
			addr: "/tmp/test-terway-dir/test-socket.sock",
			setupFunc: func(addr string) {
				// Don't create directory - let function create it
			},
			cleanupFunc: func(addr string) {
				os.Remove(addr)
				os.RemoveAll(filepath.Dir(addr))
			},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupFunc != nil {
				tt.setupFunc(tt.addr)
			}
			if tt.cleanupFunc != nil {
				defer tt.cleanupFunc(tt.addr)
			}

			l, err := newUnixListener(tt.addr)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, l)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, l)
				if l != nil {
					l.Close()
				}
			}
		})
	}
}

// TestRunDebugServer tests the runDebugServer function
func TestRunDebugServer(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(prometheus.MustRegister, func() {})

	patches.ApplyMethod(http.DefaultServeMux, "Handle", func() {})

	tests := []struct {
		name              string
		debugSocketListen string
		expectedError     bool
		setupFunc         func()
		cleanupFunc       func()
	}{
		{
			name:              "tcp listener",
			debugSocketListen: "127.0.0.1:0", // Use port 0 to let OS assign a free port
			expectedError:     false,
		},
		{
			name:              "unix socket listener",
			debugSocketListen: "unix:///tmp/test-debug.sock",
			expectedError:     false,
			cleanupFunc: func() {
				os.Remove("/tmp/test-debug.sock")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupFunc != nil {
				tt.setupFunc()
			}
			if tt.cleanupFunc != nil {
				defer tt.cleanupFunc()
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			wg := &sync.WaitGroup{}

			err := runDebugServer(ctx, wg, tt.debugSocketListen)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Wait for goroutines to finish
			cancel()
			wg.Wait()
		})
	}
}

// TestCniInterceptor tests the cniInterceptor function
func TestCniInterceptor(t *testing.T) {
	tests := []struct {
		name          string
		req           interface{}
		expectedError bool
	}{
		{
			name: "AllocIPRequest",
			req: &rpc.AllocIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: false,
		},
		{
			name: "ReleaseIPRequest",
			req: &rpc.ReleaseIPRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: false,
		},
		{
			name: "GetInfoRequest",
			req: &rpc.GetInfoRequest{
				K8SPodNamespace:        "default",
				K8SPodName:             "test-pod",
				K8SPodInfraContainerId: "container-123",
			},
			expectedError: false,
		},
		{
			name:          "Unknown request type",
			req:           &struct{}{},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			handlerCalled := false
			handler := func(ctx context.Context, req interface{}) (interface{}, error) {
				handlerCalled = true
				return nil, nil
			}

			info := &grpc.UnaryServerInfo{
				FullMethod: "/test.method",
			}

			_, err := cniInterceptor(ctx, tt.req, info, handler)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.True(t, handlerCalled, "handler should be called")
			}
		})
	}
}

// TestStackTriger tests the stackTriger function
func TestStackTriger(t *testing.T) {
	stackTriger()

	// Test that stackTriggerSignals is defined (may be empty on unsupported platforms)
	assert.NotNil(t, stackTriggerSignals, "stackTriggerSignals should be defined")

	// Test that signal.Notify works with a channel (basic verification)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	signal.Stop(sigChan)
	close(sigChan)
}

// TestEnsureCNIConfig tests the ensureCNIConfig function with mocking
func TestEnsureCNIConfigWithMocking(t *testing.T) {
	tests := []struct {
		name          string
		setupMocks    func(*gomonkey.Patches)
		expectedError bool
	}{
		{
			name: "feature gate enabled - skip write",
			setupMocks: func(patches *gomonkey.Patches) {
				// Mock feature gate to return true using ApplyMethodFunc
				// Note: This may not work if DefaultFeatureGate is an interface
				// In that case, we need to mock the actual implementation
				patches.ApplyMethodFunc(utilfeature.DefaultFeatureGate, "Enabled", func(_ featuregate.Feature) bool {
					return true
				})
			},
			expectedError: false,
		},
		{
			name: "source file not found",
			setupMocks: func(patches *gomonkey.Patches) {
				// Mock feature gate to return false
				patches.ApplyMethodFunc(utilfeature.DefaultFeatureGate, "Enabled", func(_ featuregate.Feature) bool {
					return false
				})
				// Mock os.Open to return error
				patches.ApplyFunc(os.Open, func(name string) (*os.File, error) {
					return nil, os.ErrNotExist
				})
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			if tt.setupMocks != nil {
				tt.setupMocks(patches)
			}

			err := ensureCNIConfig()

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestRun tests the Run function with mocking
func TestRun(t *testing.T) {
	tests := []struct {
		name          string
		setupMocks    func(*gomonkey.Patches)
		expectedError bool
	}{
		{
			name: "lock acquisition failure",
			setupMocks: func(patches *gomonkey.Patches) {
				// Mock filemutex.New to return a mock lock
				patches.ApplyFunc(filemutex.New, func(filename string) (*filemutex.FileMutex, error) {
					return nil, assert.AnError
				})
			},
			expectedError: true,
		},
		{
			name: "lock timeout",
			setupMocks: func(patches *gomonkey.Patches) {
				mockLock := &filemutex.FileMutex{}
				patches.ApplyFunc(filemutex.New, func(filename string) (*filemutex.FileMutex, error) {
					return mockLock, nil
				})
				patches.ApplyMethodFunc(mockLock, "TryLock", func() error {
					return assert.AnError
				})
				patches.ApplyMethodFunc(mockLock, "Unlock", func() error {
					return nil
				})
				// Mock wait.PollUntilContextTimeout to return timeout error
				patches.ApplyFunc(wait.PollUntilContextTimeout, func(ctx context.Context, interval, timeout time.Duration, immediate bool, condition wait.ConditionWithContextFunc) error {
					return context.DeadlineExceeded
				})
			},
			expectedError: true,
		},
		{
			name: "newUnixListener failure",
			setupMocks: func(patches *gomonkey.Patches) {
				mockLock := &filemutex.FileMutex{}
				patches.ApplyFunc(filemutex.New, func(filename string) (*filemutex.FileMutex, error) {
					return mockLock, nil
				})
				patches.ApplyMethodFunc(mockLock, "TryLock", func() error {
					return nil
				})
				patches.ApplyMethodFunc(mockLock, "Unlock", func() error {
					return nil
				})
				patches.ApplyFunc(wait.PollUntilContextTimeout, func(ctx context.Context, interval, timeout time.Duration, immediate bool, condition wait.ConditionWithContextFunc) error {
					return nil
				})
				patches.ApplyFunc(newUnixListener, func(addr string) (net.Listener, error) {
					return nil, assert.AnError
				})
			},
			expectedError: true,
		},
		{
			name: "newNetworkService failure",
			setupMocks: func(patches *gomonkey.Patches) {
				mockLock := &filemutex.FileMutex{}
				patches.ApplyFunc(filemutex.New, func(filename string) (*filemutex.FileMutex, error) {
					return mockLock, nil
				})
				patches.ApplyMethodFunc(mockLock, "TryLock", func() error {
					return nil
				})
				patches.ApplyMethodFunc(mockLock, "Unlock", func() error {
					return nil
				})
				patches.ApplyFunc(wait.PollUntilContextTimeout, func(ctx context.Context, interval, timeout time.Duration, immediate bool, condition wait.ConditionWithContextFunc) error {
					return nil
				})
				mockListener := &mockNetListener{}
				patches.ApplyFunc(newUnixListener, func(addr string) (net.Listener, error) {
					return mockListener, nil
				})
				patches.ApplyFunc(newNetworkService, func(ctx context.Context, configFilePath, daemonMode string) (*networkService, error) {
					return nil, assert.AnError
				})
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			if tt.setupMocks != nil {
				tt.setupMocks(patches)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			err := Run(ctx, "/tmp/test.sock", "127.0.0.1:0", "/tmp/config.json", "ENIMultiIP")

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// mockNetListener is a mock implementation of net.Listener
type mockNetListener struct {
	acceptChan chan net.Conn
	closeChan  chan struct{}
}

func (m *mockNetListener) Accept() (net.Conn, error) {
	select {
	case <-m.closeChan:
		return nil, net.ErrClosed
	case conn := <-m.acceptChan:
		return conn, nil
	}
}

func (m *mockNetListener) Close() error {
	close(m.closeChan)
	return nil
}

func (m *mockNetListener) Addr() net.Addr {
	return &net.UnixAddr{Name: "/tmp/test.sock", Net: "unix"}
}

func TestNetworkServiceBuilder_setupENIManager_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Use context with cancel to ensure background goroutines stop when test finishes
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockECS := clientmocks.NewECS(t)
	mockMeta := instancemocks.NewInterface(t)

	instance.Init(mockMeta)

	aliyunClient := &client.APIFacade{}
	patches.ApplyMethodFunc(aliyunClient, "GetECS", func() client.ECS {
		return mockECS
	})

	// 1. Mock instance metadata
	mockMeta.On("GetZoneID").Return("cn-hangzhou-i", nil)
	mockMeta.On("GetInstanceID").Return("i-instance", nil)
	mockMeta.On("GetVSwitchID").Return("vsw-123", nil)

	// 2. Mock configuration functions
	patches.ApplyFunc(getENIConfig, func(cfg *daemon_types.Config, zoneID string) *daemon_types.ENIConfig {
		return &daemon_types.ENIConfig{
			SecurityGroupIDs: []string{"sg-1"},
		}
	})
	patches.ApplyFunc(getPoolConfig, func(cfg *daemon_types.Config, daemonMode string, limit *client.Limits) (*daemon_types.PoolConfig, error) {
		return &daemon_types.PoolConfig{
			Capacity: 10,
			MaxENI:   3,
		}, nil
	})

	// 3. Mock factory and vswitch pool
	patches.ApplyFunc(vswpool.NewSwitchPool, func(size int, ttl string) (*vswpool.SwitchPool, error) {
		return &vswpool.SwitchPool{}, nil
	})

	attachedENIs := []*daemon_types.ENI{
		{ID: "eni-1", MAC: "mac-1"},
	}

	patches.ApplyFunc(aliyun.NewAliyun, func(ctx context.Context, openAPI client.OpenAPI, getter aliyuneni.ENIInfoGetter, vsw *vswpool.SwitchPool, cfg *daemon_types.ENIConfig) *aliyun.Aliyun {
		return &aliyun.Aliyun{}
	})
	patches.ApplyMethodFunc(&aliyun.Aliyun{}, "GetAttachedNetworkInterface", func(trunkENIID string) ([]*daemon_types.ENI, error) {
		return attachedENIs, nil
	})

	// 5. Mock K8S and Storage
	mockK8sReal := k8smocks.NewKubernetes(t)
	mockK8sReal.On("PatchNodeAnnotations", mock.Anything, mock.Anything).Return(nil)
	// Mock GetLocalPods to handle the GC loop call. Using Maybe() as it happens asynchronously.
	mockK8sReal.On("GetLocalPods", mock.Anything).Return([]*daemon_types.PodInfo{}, nil).Maybe()

	mockStorage := &builderMockStorage{
		listResult: []interface{}{},
		listErr:    nil,
	}

	// 6. Mock other package functions
	patches.ApplyFunc(runDevicePlugin, func(daemonMode string, config *daemon_types.Config, poolConfig *daemon_types.PoolConfig) {})
	patches.ApplyFunc(preStartResourceManager, func(daemonMode string, k8s interface{}) error { return nil })

	// 7. Mock ENI package types/functions
	patches.ApplyFunc(eni.NewLocal, func(eni *daemon_types.ENI, name string, factory factory.Factory, poolConfig *daemon_types.PoolConfig) eni.NetworkInterface {
		return &mockNetworkInterface{}
	})
	patches.ApplyFunc(eni.NewManager, func(pool *daemon_types.PoolConfig, syncPeriod time.Duration, nis []eni.NetworkInterface, policy daemon_types.EniSelectionPolicy, k8s interface{}) *eni.Manager {
		return &eni.Manager{}
	})
	patches.ApplyMethodFunc(&eni.Manager{}, "Run", func(ctx context.Context, wg *sync.WaitGroup, podResources []daemon_types.PodResources) error {
		return nil
	})

	builder := &NetworkServiceBuilder{
		ctx:          ctx,
		daemonMode:   daemon_types.ModeENIMultiIP,
		config:       &daemon_types.Config{IPStack: "ipv4"},
		service:      &networkService{k8s: mockK8sReal, resourceDB: mockStorage, enableIPv4: true},
		aliyunClient: aliyunClient,
		limit:        &client.Limits{Adapters: 3, IPv4PerAdapter: 10, ERdmaAdapters: 0},
	}

	err := builder.setupENIManager()
	assert.NoError(t, err)
	assert.NotNil(t, builder.service.eniMgr)
}
