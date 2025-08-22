package eni

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/AliyunContainerService/terway/rpc"
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

	t.Run("test with VfTypeVPC when APIEcsHDeni annotation is set", func(t *testing.T) {
		l := &RemoteIPResource{
			podENI: networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						types.ENOApi: types.APIEcsHDeni,
					},
				},
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
						"eni-11": {},
					},
				},
			},
		}

		result := l.ToRPC()
		assert.NotNil(t, result)
		assert.Equal(t, 1, len(result))
		assert.Equal(t, rpc.VfType_VfTypeVPC, *result[0].ENIInfo.VfType)
	})

	t.Run("test without ENOApi annotation", func(t *testing.T) {
		l := &RemoteIPResource{
			podENI: networkv1beta1.PodENI{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
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
						"eni-11": {},
					},
				},
			},
		}

		result := l.ToRPC()
		assert.NotNil(t, result)
		assert.Equal(t, 1, len(result))
		assert.Nil(t, result[0].ENIInfo.VfType)
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

	r := NewRemote(client, nil)
	cni := &daemon.CNI{PodNamespace: "default", PodName: "pod-1"}
	resp, _ := r.Allocate(context.Background(), cni, &RemoteIPRequest{})
	result := <-resp
	assert.NoError(t, result.Err)
	assert.NotNil(t, result.NetworkConfigs)
	assert.Equal(t, "192.168.1.1", result.NetworkConfigs[0].ToRPC()[0].BasicInfo.PodIP.IPv4)
}
