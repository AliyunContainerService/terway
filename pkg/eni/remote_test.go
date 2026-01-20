package eni

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

func TestToRPC(t *testing.T) {
	t.Run("test with valid IPv4 and IPv6 allocations", func(t *testing.T) {
		l := &RemoteIPResource{
			podENI: networkv1beta1.PodENI{
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							IPv4:     "192.168.1.1",
							IPv4CIDR: "192.168.1.0/24",
							IPv6:     "fd00:db8::1",
							IPv6CIDR: "fd00:db8::/64",
							ENI: networkv1beta1.ENI{
								ID:  "eni-11",
								MAC: "00:00:00:00:00:00",
							},
							Interface:    "eth0",
							ExtraRoutes:  []networkv1beta1.Route{},
							DefaultRoute: true,
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					Phase:       "",
					InstanceID:  "i-123456",
					TrunkENIID:  "eni-12345678",
					Msg:         "",
					PodLastSeen: metav1.Time{},
					ENIInfos: map[string]networkv1beta1.ENIInfo{
						"eni-11": {},
					},
				},
			},
			trunkENI: daemon.ENI{
				ID:  "eni-12345678",
				MAC: "00:00:00:00:00:00",
				GatewayIP: types.IPSet{
					IPv4: net.ParseIP("192.168.1.253"),
					IPv6: net.ParseIP("fd00:db8::fffe"),
				},
			},
		}

		// Call ToRPC and check the result
		result := l.ToRPC()
		assert.NotNil(t, result)
		assert.Equal(t, 1, len(result))
		assert.Equal(t, "192.168.1.1", result[0].BasicInfo.PodIP.IPv4)
		assert.Equal(t, "fd00:db8::1", result[0].BasicInfo.PodIP.IPv6)
		assert.Equal(t, "192.168.1.0/24", result[0].BasicInfo.PodCIDR.IPv4)
		assert.Equal(t, "fd00:db8::/64", result[0].BasicInfo.PodCIDR.IPv6)
		assert.Equal(t, "00:00:00:00:00:00", result[0].ENIInfo.MAC)
		assert.Equal(t, "eth0", result[0].IfName)
		assert.Equal(t, true, result[0].DefaultRoute)
	})

	t.Run("test with VfId set in ENIInfo", func(t *testing.T) {
		vfID := uint32(5)
		l := &RemoteIPResource{
			podENI: networkv1beta1.PodENI{
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							IPv4:     "192.168.1.1",
							IPv4CIDR: "192.168.1.0/24",
							ENI: networkv1beta1.ENI{
								ID:  "eni-11",
								MAC: "00:00:00:00:00:00",
							},
							Interface:    "eth0",
							DefaultRoute: true,
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					ENIInfos: map[string]networkv1beta1.ENIInfo{
						"eni-11": {
							VfID: &vfID,
						},
					},
				},
			},
		}

		result := l.ToRPC()
		assert.NotNil(t, result)
		assert.Equal(t, 1, len(result))
		assert.NotNil(t, result[0].ENIInfo.VfId)
		assert.Equal(t, uint32(5), *result[0].ENIInfo.VfId)
	})

	t.Run("test with VfId nil in ENIInfo", func(t *testing.T) {
		l := &RemoteIPResource{
			podENI: networkv1beta1.PodENI{
				Spec: networkv1beta1.PodENISpec{
					Allocations: []networkv1beta1.Allocation{
						{
							IPv4:     "192.168.1.1",
							IPv4CIDR: "192.168.1.0/24",
							ENI: networkv1beta1.ENI{
								ID:  "eni-11",
								MAC: "00:00:00:00:00:00",
							},
							Interface:    "eth0",
							DefaultRoute: true,
						},
					},
				},
				Status: networkv1beta1.PodENIStatus{
					ENIInfos: map[string]networkv1beta1.ENIInfo{
						"eni-11": {
							VfID: nil,
						},
					},
				},
			},
		}

		result := l.ToRPC()
		assert.NotNil(t, result)
		assert.Equal(t, 1, len(result))
		assert.Nil(t, result[0].ENIInfo.VfId)
	})

}

func TestAllocateReturnsErrorWhenResourceTypeMismatch(t *testing.T) {
	r := &Remote{}
	resp, traces := r.Allocate(context.Background(), &daemon.CNI{}, &LocalIPResource{})
	assert.Nil(t, resp)
	assert.Equal(t, ResourceTypeMismatch, traces[0].Condition)
}

func TestAllocateReturnsNetworkResourcesWhenPodENIReady(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = networkv1beta1.AddToScheme(scheme)
	// Build the fake client with scheme and objects
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&networkv1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod-1",
				Namespace: "default",
			},
			Spec: networkv1beta1.PodENISpec{
				Allocations: []networkv1beta1.Allocation{
					{
						IPv4:     "192.168.1.1",
						IPv4CIDR: "192.168.1.0/24",
						ENI: networkv1beta1.ENI{
							ID:  "eni-1",
							MAC: "00:00:00:00:00:00",
						},
						Interface:    "eth0",
						DefaultRoute: true,
					},
				},
			},
			Status: networkv1beta1.PodENIStatus{
				Phase:      networkv1beta1.ENIPhaseBind,
				InstanceID: "i-123456",
				ENIInfos: map[string]networkv1beta1.ENIInfo{
					"eni-1": {
						ID:               "eni-1",
						Type:             "",
						Vid:              0,
						Status:           "",
						NetworkCardIndex: nil,
						VfID:             nil,
					},
				},
			},
		}).
		Build()

	r := NewRemote(client, nil, nil)
	cni := &daemon.CNI{PodNamespace: "default", PodName: "pod-1"}
	resp, _ := r.Allocate(context.Background(), cni, &RemoteIPRequest{})
	result := <-resp
	assert.NoError(t, result.Err)
	assert.NotNil(t, result.NetworkConfigs)
	assert.Equal(t, "192.168.1.1", result.NetworkConfigs[0].ToRPC()[0].BasicInfo.PodIP.IPv4)
}

func TestAllocateWithNotifier_PodENIAlreadyReady(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = networkv1beta1.AddToScheme(scheme)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&networkv1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod-1",
				Namespace: "default",
			},
			Spec: networkv1beta1.PodENISpec{
				Allocations: []networkv1beta1.Allocation{
					{
						IPv4:     "192.168.1.1",
						IPv4CIDR: "192.168.1.0/24",
						ENI: networkv1beta1.ENI{
							ID:  "eni-1",
							MAC: "00:00:00:00:00:00",
						},
						Interface:    "eth0",
						DefaultRoute: true,
					},
				},
			},
			Status: networkv1beta1.PodENIStatus{
				Phase:      networkv1beta1.ENIPhaseBind,
				InstanceID: "i-123456",
				ENIInfos: map[string]networkv1beta1.ENIInfo{
					"eni-1": {},
				},
			},
		}).
		Build()

	notifier := NewNotifier()
	r := NewRemote(client, nil, notifier)
	cni := &daemon.CNI{PodNamespace: "default", PodName: "pod-1"}
	resp, _ := r.Allocate(context.Background(), cni, &RemoteIPRequest{})

	result := <-resp
	assert.NoError(t, result.Err)
	assert.NotNil(t, result.NetworkConfigs)
	assert.Equal(t, "192.168.1.1", result.NetworkConfigs[0].ToRPC()[0].BasicInfo.PodIP.IPv4)
}

func TestAllocateWithNotifier_WaitForNotification(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = networkv1beta1.AddToScheme(scheme)

	podENI := &networkv1beta1.PodENI{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
		},
		Spec: networkv1beta1.PodENISpec{
			Allocations: []networkv1beta1.Allocation{},
		},
		Status: networkv1beta1.PodENIStatus{
			Phase: networkv1beta1.ENIPhaseBinding,
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(podENI).
		Build()

	notifier := NewNotifier()
	r := NewRemote(client, nil, notifier)
	cni := &daemon.CNI{PodNamespace: "default", PodName: "pod-1"}

	resp, _ := r.Allocate(context.Background(), cni, &RemoteIPRequest{})

	go func() {
		time.Sleep(50 * time.Millisecond)
		podENI.Spec.Allocations = []networkv1beta1.Allocation{
			{
				IPv4:     "192.168.1.2",
				IPv4CIDR: "192.168.1.0/24",
				ENI: networkv1beta1.ENI{
					ID:  "eni-2",
					MAC: "00:00:00:00:00:01",
				},
				Interface:    "eth0",
				DefaultRoute: true,
			},
		}
		podENI.Status.Phase = networkv1beta1.ENIPhaseBind
		podENI.Status.ENIInfos = map[string]networkv1beta1.ENIInfo{
			"eni-2": {},
		}
		_ = client.Update(context.Background(), podENI)
		notifier.Notify()
	}()

	select {
	case result := <-resp:
		assert.NoError(t, result.Err)
		assert.NotNil(t, result.NetworkConfigs)
		assert.Equal(t, "192.168.1.2", result.NetworkConfigs[0].ToRPC()[0].BasicInfo.PodIP.IPv4)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for allocation")
	}
}

func TestAllocateWithNotifier_ContextCanceled(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = networkv1beta1.AddToScheme(scheme)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&networkv1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod-1",
				Namespace: "default",
			},
			Spec: networkv1beta1.PodENISpec{
				Allocations: []networkv1beta1.Allocation{},
			},
			Status: networkv1beta1.PodENIStatus{
				Phase: networkv1beta1.ENIPhaseBinding,
			},
		}).
		Build()

	notifier := NewNotifier()
	r := NewRemote(client, nil, notifier)
	cni := &daemon.CNI{PodNamespace: "default", PodName: "pod-1"}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	resp, _ := r.Allocate(ctx, cni, &RemoteIPRequest{})

	select {
	case result := <-resp:
		assert.Nil(t, result)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for allocation to be canceled")
	}
}

func TestAllocateWithNotifier_TrunkMismatch(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = networkv1beta1.AddToScheme(scheme)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&networkv1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod-1",
				Namespace: "default",
			},
			Spec: networkv1beta1.PodENISpec{
				Allocations: []networkv1beta1.Allocation{
					{
						IPv4:     "192.168.1.1",
						IPv4CIDR: "192.168.1.0/24",
						ENI: networkv1beta1.ENI{
							ID:  "eni-1",
							MAC: "00:00:00:00:00:00",
						},
						Interface:    "eth0",
						DefaultRoute: true,
					},
				},
			},
			Status: networkv1beta1.PodENIStatus{
				Phase:      networkv1beta1.ENIPhaseBind,
				TrunkENIID: "eni-trunk-wrong",
				ENIInfos: map[string]networkv1beta1.ENIInfo{
					"eni-1": {},
				},
			},
		}).
		Build()

	notifier := NewNotifier()
	trunkENI := &daemon.ENI{
		ID:  "eni-trunk-expected",
		MAC: "00:00:00:00:00:02",
	}
	r := NewRemote(client, trunkENI, notifier)
	cni := &daemon.CNI{PodNamespace: "default", PodName: "pod-1"}

	resp, _ := r.Allocate(context.Background(), cni, &RemoteIPRequest{})

	result := <-resp
	assert.Error(t, result.Err)
	assert.Contains(t, result.Err.Error(), "trunk")
}

func TestAllocateWithNotifier_PodUIDMismatch(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = networkv1beta1.AddToScheme(scheme)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&networkv1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod-1",
				Namespace: "default",
				Annotations: map[string]string{
					types.PodUID: "wrong-uid",
				},
			},
			Spec: networkv1beta1.PodENISpec{
				Allocations: []networkv1beta1.Allocation{
					{
						IPv4:     "192.168.1.1",
						IPv4CIDR: "192.168.1.0/24",
						ENI: networkv1beta1.ENI{
							ID:  "eni-1",
							MAC: "00:00:00:00:00:00",
						},
						Interface:    "eth0",
						DefaultRoute: true,
					},
				},
			},
			Status: networkv1beta1.PodENIStatus{
				Phase: networkv1beta1.ENIPhaseBind,
				ENIInfos: map[string]networkv1beta1.ENIInfo{
					"eni-1": {},
				},
			},
		}).
		Build()

	notifier := NewNotifier()
	r := NewRemote(client, nil, notifier)
	cni := &daemon.CNI{
		PodNamespace: "default",
		PodName:      "pod-1",
		PodUID:       "expected-uid",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	resp, _ := r.Allocate(ctx, cni, &RemoteIPRequest{})

	select {
	case result := <-resp:
		assert.Nil(t, result)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for allocation to fail on UID mismatch")
	}
}

func TestTryAllocatePodENI_PodENINotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = networkv1beta1.AddToScheme(scheme)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	r := NewRemote(client, nil, NewNotifier())
	cni := &daemon.CNI{PodNamespace: "default", PodName: "pod-not-found"}

	allocResp, success := r.tryAllocatePodENI(context.Background(), cni, logr.Discard())
	assert.False(t, success)
	assert.Nil(t, allocResp)
}

func TestTryAllocatePodENI_PodENIBeingDeleted(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = networkv1beta1.AddToScheme(scheme)

	now := metav1.Now()
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&networkv1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "pod-1",
				Namespace:         "default",
				DeletionTimestamp: &now,
				Finalizers:        []string{"test-finalizer"},
			},
			Status: networkv1beta1.PodENIStatus{
				Phase: networkv1beta1.ENIPhaseBind,
			},
		}).
		Build()

	r := NewRemote(client, nil, NewNotifier())
	cni := &daemon.CNI{PodNamespace: "default", PodName: "pod-1"}

	allocResp, success := r.tryAllocatePodENI(context.Background(), cni, logr.Discard())
	assert.False(t, success)
	assert.Nil(t, allocResp)
}

func TestTryAllocatePodENI_PodENINotInBindPhase(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = networkv1beta1.AddToScheme(scheme)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&networkv1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod-1",
				Namespace: "default",
			},
			Status: networkv1beta1.PodENIStatus{
				Phase: networkv1beta1.ENIPhaseBinding,
			},
		}).
		Build()

	r := NewRemote(client, nil, NewNotifier())
	cni := &daemon.CNI{PodNamespace: "default", PodName: "pod-1"}

	allocResp, success := r.tryAllocatePodENI(context.Background(), cni, logr.Discard())
	assert.False(t, success)
	assert.Nil(t, allocResp)
}

func TestRemote_Release(t *testing.T) {
	r := &Remote{}
	ok, err := r.Release(context.Background(), &daemon.CNI{}, nil)
	assert.False(t, ok)
	assert.NoError(t, err)
}

func TestRemote_Dispose(t *testing.T) {
	r := &Remote{}
	assert.Equal(t, 0, r.Dispose(10))
}

func TestRemote_Run(t *testing.T) {
	r := &Remote{}
	assert.NoError(t, r.Run(context.Background(), nil, nil))
}

func TestRemote_Priority(t *testing.T) {
	r := &Remote{}
	assert.Equal(t, 100, r.Priority())
}

func TestRemoteIPResource_ToStore(t *testing.T) {
	res := &RemoteIPResource{}
	assert.Nil(t, res.ToStore())
}

func TestAllocateWithBackoff_VariousScenarios(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = networkv1beta1.AddToScheme(scheme)

	t.Run("PodENI Not Found (Retry and Timeout)", func(t *testing.T) {
		client := fake.NewClientBuilder().WithScheme(scheme).Build()
		r := NewRemote(client, nil, nil)
		cni := &daemon.CNI{PodNamespace: "default", PodName: "pod-1"}

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		resp, _ := r.Allocate(ctx, cni, &RemoteIPRequest{})
		select {
		case result := <-resp:
			if result != nil {
				assert.Error(t, result.Err)
			}
		case <-time.After(200 * time.Millisecond):
			// Timeout is fine too, as long as it doesn't panic
		}
	})

	t.Run("PodENI Being Deleted (Retry and Timeout)", func(t *testing.T) {
		now := metav1.Now()
		podENI := &networkv1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "pod-1",
				Namespace:         "default",
				DeletionTimestamp: &now,
				Finalizers:        []string{"test"},
			},
		}
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(podENI).Build()
		r := NewRemote(client, nil, nil)
		cni := &daemon.CNI{PodNamespace: "default", PodName: "pod-1"}

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		resp, _ := r.Allocate(ctx, cni, &RemoteIPRequest{})
		select {
		case result := <-resp:
			if result != nil {
				assert.Error(t, result.Err)
			}
		case <-time.After(200 * time.Millisecond):
		}
	})

	t.Run("PodENI Phase Not Bind (Retry and Timeout)", func(t *testing.T) {
		podENI := &networkv1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod-1",
				Namespace: "default",
			},
			Status: networkv1beta1.PodENIStatus{
				Phase: networkv1beta1.ENIPhaseBinding,
			},
		}
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(podENI).Build()
		r := NewRemote(client, nil, nil)
		cni := &daemon.CNI{PodNamespace: "default", PodName: "pod-1"}

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		resp, _ := r.Allocate(ctx, cni, &RemoteIPRequest{})
		select {
		case result := <-resp:
			if result != nil {
				assert.Error(t, result.Err)
			}
		case <-time.After(200 * time.Millisecond):
		}
	})

	t.Run("Pod UID Mismatch (Retry and Timeout)", func(t *testing.T) {
		podENI := &networkv1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod-1",
				Namespace: "default",
				Annotations: map[string]string{
					types.PodUID: "wrong-uid",
				},
			},
			Status: networkv1beta1.PodENIStatus{
				Phase: networkv1beta1.ENIPhaseBind,
			},
		}
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(podENI).Build()
		r := NewRemote(client, nil, nil)
		cni := &daemon.CNI{PodNamespace: "default", PodName: "pod-1", PodUID: "expected-uid"}

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		resp, _ := r.Allocate(ctx, cni, &RemoteIPRequest{})
		select {
		case result := <-resp:
			if result != nil {
				assert.Error(t, result.Err)
			}
		case <-time.After(200 * time.Millisecond):
		}
	})

	t.Run("Trunk Mismatch (Fatal Error)", func(t *testing.T) {
		podENI := &networkv1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod-1",
				Namespace: "default",
			},
			Status: networkv1beta1.PodENIStatus{
				Phase:      networkv1beta1.ENIPhaseBind,
				TrunkENIID: "eni-trunk-wrong",
			},
		}
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(podENI).Build()
		r := NewRemote(client, &daemon.ENI{ID: "eni-trunk-expected"}, nil)
		cni := &daemon.CNI{PodNamespace: "default", PodName: "pod-1"}

		resp, _ := r.Allocate(context.Background(), cni, &RemoteIPRequest{})
		result := <-resp
		assert.NotNil(t, result)
		assert.Error(t, result.Err)
		assert.Contains(t, result.Err.Error(), "used a different trunk")
	})

	t.Run("Empty Allocations (Fatal Error)", func(t *testing.T) {
		podENI := &networkv1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod-1",
				Namespace: "default",
			},
			Spec: networkv1beta1.PodENISpec{
				Allocations: []networkv1beta1.Allocation{},
			},
			Status: networkv1beta1.PodENIStatus{
				Phase: networkv1beta1.ENIPhaseBind,
			},
		}
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(podENI).Build()
		r := NewRemote(client, nil, nil)
		cni := &daemon.CNI{PodNamespace: "default", PodName: "pod-1"}

		resp, _ := r.Allocate(context.Background(), cni, &RemoteIPRequest{})
		result := <-resp
		assert.NotNil(t, result)
		assert.Error(t, result.Err)
		assert.Contains(t, result.Err.Error(), "empty allocations")
	})
}
