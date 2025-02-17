package node

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/mocks"
	vswpool "github.com/AliyunContainerService/terway/pkg/vswitch"
)

func MetaIntoCtx(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxMetaKey{}, &NodeStatus{
		NeedSyncOpenAPI: &atomic.Bool{},
		StatusChanged:   &atomic.Bool{},
	})
}

// TestReleasePodNotFound tests the releasePodNotFound function
func TestReleasePodNotFound(t *testing.T) {

	now := metav1.Now()
	last := metav1.NewTime(now.Add(-time.Minute))

	tests := []struct {
		name        string
		nodeName    string
		podsMapper  map[string]*PodRequest
		ipMapper    map[string]*EniIP
		nodeRuntime *networkv1beta1.NodeRuntime
		expectPods  map[string]*EniIP
	}{
		{
			name:       "Node not found, will not release ipam",
			nodeName:   "test-node",
			podsMapper: map[string]*PodRequest{},
			ipMapper: map[string]*EniIP{
				"ip1": {
					NetworkInterface: &networkv1beta1.NetworkInterface{},
					IP: &networkv1beta1.IP{
						PodID: "pod-id",
					},
				},
			},
			nodeRuntime: &networkv1beta1.NodeRuntime{
				ObjectMeta: metav1.ObjectMeta{Name: "other-node"},
				Status: networkv1beta1.NodeRuntimeStatus{
					Pods: map[string]*networkv1beta1.RuntimePodStatus{},
				},
			},
			expectPods: map[string]*EniIP{
				"ip1": {
					NetworkInterface: &networkv1beta1.NetworkInterface{},
					IP: &networkv1beta1.IP{
						PodID: "pod-id",
					},
				},
			},
		},
		{
			name:     "Allow update pod uid",
			nodeName: "test-node",
			podsMapper: map[string]*PodRequest{
				"pod-id": {PodUID: "pod-uid-1"},
			},
			ipMapper: map[string]*EniIP{
				"ip1": {
					NetworkInterface: &networkv1beta1.NetworkInterface{},
					IP: &networkv1beta1.IP{
						PodID:  "pod-id",
						PodUID: "old",
					},
				},
			},
			nodeRuntime: &networkv1beta1.NodeRuntime{
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Spec:       networkv1beta1.NodeRuntimeSpec{},
				Status: networkv1beta1.NodeRuntimeStatus{
					Pods: map[string]*networkv1beta1.RuntimePodStatus{},
				},
			},
			expectPods: map[string]*EniIP{
				"ip1": {
					NetworkInterface: &networkv1beta1.NetworkInterface{},
					IP: &networkv1beta1.IP{
						PodID:  "pod-id",
						PodUID: "pod-uid-1",
					},
				},
			},
		},
		{
			name:       "Valid IP release",
			nodeName:   "test-node",
			podsMapper: map[string]*PodRequest{},
			ipMapper: map[string]*EniIP{
				"ip1": {
					NetworkInterface: &networkv1beta1.NetworkInterface{},
					IP: &networkv1beta1.IP{
						PodID:  "pod-id",
						PodUID: "pod-uid-1",
					},
				},
			},
			nodeRuntime: &networkv1beta1.NodeRuntime{
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Status: networkv1beta1.NodeRuntimeStatus{
					Pods: map[string]*networkv1beta1.RuntimePodStatus{
						"pod-uid-1": {Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
							networkv1beta1.CNIStatusDeleted: {LastUpdateTime: last},
						}},
					},
				},
			},
			expectPods: map[string]*EniIP{
				"ip1": {
					NetworkInterface: &networkv1beta1.NetworkInterface{},
					IP:               &networkv1beta1.IP{},
				},
			},
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			// Add networkv1beta1 scheme
			_ = networkv1beta1.AddToScheme(scheme)

			// Build the fake client with scheme and objects
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tests[i].nodeRuntime).
				Build()

			// Call the function we're testing
			releasePodNotFound(ctx, fakeClient, tests[i].nodeName, tests[i].podsMapper, tests[i].ipMapper)

			// Assertions
			assert.Equal(t, tests[i].expectPods, tests[i].ipMapper)
		})
	}
}

func Test_getEniOptions(t *testing.T) {
	type args struct {
		node *networkv1beta1.Node
	}
	tests := []struct {
		name string
		args args
		want []*eniOptions
	}{
		{
			name: "empty node",
			args: args{
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{},
						Flavor: []networkv1beta1.Flavor{
							{
								NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								Count:                       2,
							},
						},
					},
				},
			},
			want: []*eniOptions{
				{
					eniTypeKey: secondaryKey,
					eniRef:     nil,
				},
				{
					eniTypeKey: secondaryKey,
					eniRef:     nil,
				},
			},
		},
		{
			name: "empty node require trunk eni, do not add trunk if flavor tell not",
			args: args{
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{
							EnableTrunk: true,
						},
						Flavor: []networkv1beta1.Flavor{
							{
								NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								Count:                       2,
							},
						},
					},
				},
			},
			want: []*eniOptions{
				{
					eniTypeKey: secondaryKey,
					eniRef:     nil,
				},
				{
					eniTypeKey: secondaryKey,
					eniRef:     nil,
				},
			},
		},
		{
			name: "add trunk",
			args: args{
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{
							EnableTrunk: true,
						},
						Flavor: []networkv1beta1.Flavor{
							{
								NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								Count:                       2,
							},
							{
								NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								Count:                       1,
							},
						},
					},
				},
			},
			want: []*eniOptions{
				{
					eniTypeKey: trunkKey,
					eniRef:     nil,
				},
				{
					eniTypeKey: secondaryKey,
					eniRef:     nil,
				},
				{
					eniTypeKey: secondaryKey,
					eniRef:     nil,
				},
			},
		},
		{
			name: "multi trunk support",
			args: args{
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{
							EnableTrunk: true,
						},
						Flavor: []networkv1beta1.Flavor{
							{
								NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								Count:                       2,
							},
							{
								NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								Count:                       2,
							},
						},
					},
				},
			},
			want: []*eniOptions{
				{
					eniTypeKey: trunkKey,
				},
				{
					eniTypeKey: trunkKey,
				},
				{
					eniTypeKey: secondaryKey,
					eniRef:     nil,
				},
				{
					eniTypeKey: secondaryKey,
					eniRef:     nil,
				},
			},
		},
		{
			name: "do not modify current eni , if eni is not expected as flavor describe",
			args: args{
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{
							EnableTrunk: true,
						},
						Flavor: []networkv1beta1.Flavor{
							{
								NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								Count:                       3,
							},
							{
								NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								Count:                       1,
							},
						},
					},
					Status: networkv1beta1.NodeStatus{
						NetworkInterfaces: map[string]*networkv1beta1.NetworkInterface{
							"eni-1": {
								NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							},
							"eni-2": {
								NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							},
							"eni-3": {
								NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeHighPerformance,
							},
						},
					},
				},
			},
			want: []*eniOptions{
				{
					eniRef: &networkv1beta1.NetworkInterface{
						NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
						NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
					},
					eniTypeKey: trunkKey,
				},
				{
					eniRef: &networkv1beta1.NetworkInterface{
						NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
						NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
					},
					eniTypeKey: trunkKey,
				},
				{
					eniRef: &networkv1beta1.NetworkInterface{
						NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
						NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeHighPerformance,
					},
					eniTypeKey: rdmaKey,
				},
				{
					eniTypeKey: secondaryKey,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, getEniOptions(tt.args.node), "getEniOptions(%v)", tt.args.node)
		})
	}
}

func TestReconcileNodeSyncWithAPI(t *testing.T) {
	ctx := context.TODO()
	ctx = MetaIntoCtx(ctx)
	MetaCtx(ctx).NeedSyncOpenAPI.Store(true)

	openAPI := mocks.NewInterface(t)
	openAPI.On("DescribeVSwitchByID", mock.Anything, "vsw-1").Return(&vpc.VSwitch{
		VSwitchId:               "vsw-1",
		ZoneId:                  "zone-1",
		AvailableIpAddressCount: 10,
		CidrBlock:               "192.168.0.0/16",
		Ipv6CidrBlock:           "fd00::/64",
	}, nil).Maybe()
	openAPI.On("DescribeNetworkInterface", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
		{
			Status:                      "InUse",
			MacAddress:                  "",
			NetworkInterfaceID:          "eni-1",
			VSwitchID:                   "",
			PrivateIPAddress:            "",
			PrivateIPSets:               nil,
			ZoneID:                      "",
			SecurityGroupIDs:            nil,
			ResourceGroupID:             "",
			IPv6Set:                     nil,
			Tags:                        nil,
			Type:                        "Primary",
			InstanceID:                  "",
			TrunkNetworkInterfaceID:     "",
			NetworkInterfaceTrafficMode: "",
			DeviceIndex:                 0,
			CreationTime:                "",
		},
		{
			Status:                      "InUse",
			MacAddress:                  "",
			NetworkInterfaceID:          "eni-2",
			VSwitchID:                   "vsw-1",
			PrivateIPAddress:            "",
			PrivateIPSets:               nil,
			ZoneID:                      "zone-1",
			SecurityGroupIDs:            nil,
			ResourceGroupID:             "",
			IPv6Set:                     nil,
			Tags:                        nil,
			Type:                        "Secondary",
			InstanceID:                  "",
			TrunkNetworkInterfaceID:     "",
			NetworkInterfaceTrafficMode: "",
			DeviceIndex:                 0,
			CreationTime:                "",
		},
		{
			Status:             "InUse",
			MacAddress:         "",
			NetworkInterfaceID: "eni-3",
			VSwitchID:          "vsw-1",
			PrivateIPAddress:   "",
			PrivateIPSets: []aliyunClient.IPSet{
				{
					IPAddress: "192.168.0.1",
					Primary:   true,
				},
				{
					IPAddress: "192.168.0.2",
					Primary:   false,
				},
			},
			ZoneID:                      "zone-1",
			SecurityGroupIDs:            nil,
			ResourceGroupID:             "",
			IPv6Set:                     nil,
			Tags:                        nil,
			Type:                        "Secondary",
			InstanceID:                  "",
			TrunkNetworkInterfaceID:     "",
			NetworkInterfaceTrafficMode: "",
			DeviceIndex:                 0,
			CreationTime:                "",
		},
	}, nil).Maybe()

	vsw, err := vswpool.NewSwitchPool(100, "10m")
	assert.NoError(t, err)

	node := &networkv1beta1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo",
		},
		Spec: networkv1beta1.NodeSpec{
			NodeMetadata: networkv1beta1.NodeMetadata{
				InstanceID: "test-instance",
			},
			ENISpec: &networkv1beta1.ENISpec{},
		},
		Status: networkv1beta1.NodeStatus{
			NetworkInterfaces: map[string]*networkv1beta1.NetworkInterface{
				"eni-3": {
					ID: "eni-3",
					IPv4: map[string]*networkv1beta1.IP{
						"192.168.0.1": {
							IP:      "192.168.0.1",
							Primary: true,
						},
					},
					IPv6: map[string]*networkv1beta1.IP{
						"fd00::1": {
							IP: "fd00::1",
						},
					},
				},
				"eni-4": {
					ID: "eni-4",
					IPv4: map[string]*networkv1beta1.IP{
						"192.168.0.1": {
							IP:      "192.168.0.1",
							Primary: true,
						},
					},
					IPv6: map[string]*networkv1beta1.IP{
						"fd00::1": {
							IP: "fd00::1",
						},
					},
				},
			},
		},
	}

	reconciler := &ReconcileNode{
		aliyun:             openAPI,
		vswpool:            vsw,
		fullSyncNodePeriod: time.Hour,
		tracer:             trace.NewNoopTracerProvider().Tracer(""),
	}

	err = reconciler.syncWithAPI(ctx, node)
	assert.NoError(t, err)

	assert.Equal(t, 2, len(node.Status.NetworkInterfaces))
	assert.Equal(t, "192.168.0.0/16", node.Status.NetworkInterfaces["eni-2"].IPv4CIDR)
	assert.Len(t, node.Status.NetworkInterfaces["eni-3"].IPv4, 2)
	assert.Nil(t, node.Status.NetworkInterfaces["eni-4"])
}

func Test_assignIPFromLocalPool(t *testing.T) {
	type args struct {
		log        logr.Logger
		podsMapper map[string]*PodRequest
		ipv4Map    map[string]*EniIP
		ipv6Map    map[string]*EniIP
	}
	tests := []struct {
		name             string
		args             args
		checkResultFunc  func(t *testing.T, got map[string]*PodRequest)
		checkPodsMapFunc func(t *testing.T, got map[string]*PodRequest)
	}{
		{
			name: "handle prev, should ignore ip status",
			args: args{
				log: logr.Discard(),
				podsMapper: map[string]*PodRequest{
					"pod-1": {
						RequireIPv4: true,
						RequireIPv6: true,
						IPv4:        "192.168.0.1",
						IPv6:        "fd00::1",
					},
					"pod-2": {
						RequireIPv4: true,
						RequireIPv6: true,
						IPv4:        "192.168.0.2",
						IPv6:        "fd00::2",
					},
				},
				ipv4Map: map[string]*EniIP{
					"192.168.0.1": {
						IP: &networkv1beta1.IP{
							IP:     "192.168.0.1",
							Status: networkv1beta1.IPStatusDeleting,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"192.168.0.2": {
						IP: &networkv1beta1.IP{
							IP:     "192.168.0.2",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-2",
							Status: "InUse",
						},
					},
				},
				ipv6Map: map[string]*EniIP{
					"fd00::1": {
						IP: &networkv1beta1.IP{
							IP:     "fd00::1",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"fd00::2": {
						IP: &networkv1beta1.IP{
							IP:     "fd00::2",
							Status: networkv1beta1.IPStatusDeleting,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-2",
							Status: "InUse",
						},
					},
				},
			},
			checkResultFunc: func(t *testing.T, got map[string]*PodRequest) {
				assert.Len(t, got, 0)
			},
			checkPodsMapFunc: func(t *testing.T, got map[string]*PodRequest) {
				assert.NotNil(t, got["pod-1"].ipv4Ref)
				assert.NotNil(t, got["pod-1"].ipv6Ref)
				assert.NotNil(t, got["pod-2"].ipv4Ref)
				assert.NotNil(t, got["pod-2"].ipv6Ref)
			},
		},
		{
			name: "handle new pod",
			args: args{
				log: logr.Discard(),
				podsMapper: map[string]*PodRequest{
					"pod-1": {
						RequireIPv4: true,
						RequireIPv6: true,
					},
					"pod-2": {
						RequireIPv4: true,
						RequireIPv6: true,
					},
				},
				ipv4Map: map[string]*EniIP{
					"192.168.0.1": {
						IP: &networkv1beta1.IP{
							IP:     "192.168.0.1",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"192.168.0.2": {
						IP: &networkv1beta1.IP{
							IP:     "192.168.0.2",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-2",
							Status: "InUse",
						},
					},
				},
				ipv6Map: map[string]*EniIP{
					"fd00::1": {
						IP: &networkv1beta1.IP{
							IP:     "fd00::1",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"fd00::2": {
						IP: &networkv1beta1.IP{
							IP:     "fd00::2",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-2",
							Status: "InUse",
						},
					},
				},
			},
			checkResultFunc: func(t *testing.T, got map[string]*PodRequest) {
				assert.Len(t, got, 0)
			},
			checkPodsMapFunc: func(t *testing.T, got map[string]*PodRequest) {
				assert.NotNil(t, got["pod-1"].ipv4Ref)
				assert.NotNil(t, got["pod-1"].ipv6Ref)
				assert.NotNil(t, got["pod-2"].ipv4Ref)
				assert.NotNil(t, got["pod-2"].ipv6Ref)

				assert.Equal(t, "pod-1", got["pod-1"].ipv4Ref.IP.PodID)
				assert.Equal(t, "pod-1", got["pod-1"].ipv6Ref.IP.PodID)
				assert.Equal(t, "pod-2", got["pod-2"].ipv4Ref.IP.PodID)
				assert.Equal(t, "pod-2", got["pod-2"].ipv6Ref.IP.PodID)

				assert.True(t, got["pod-1"].ipv4Ref.NetworkInterface.ID == got["pod-1"].ipv6Ref.NetworkInterface.ID)
				assert.True(t, got["pod-2"].ipv4Ref.NetworkInterface.ID == got["pod-2"].ipv6Ref.NetworkInterface.ID)
			},
		},
		{
			name: "failed to handle if ipv6 is not enough",
			args: args{
				log: logr.Discard(),
				podsMapper: map[string]*PodRequest{
					"pod-1": {
						RequireIPv4: true,
						RequireIPv6: true,
					},
					"pod-2": {
						RequireIPv4: true,
						RequireIPv6: true,
					},
				},
				ipv4Map: map[string]*EniIP{
					"192.168.0.1": {
						IP: &networkv1beta1.IP{
							IP:     "192.168.0.1",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"192.168.0.2": {
						IP: &networkv1beta1.IP{
							IP:     "192.168.0.2",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-2",
							Status: "InUse",
						},
					},
				},
				ipv6Map: map[string]*EniIP{
					"fd00::1": {
						IP: &networkv1beta1.IP{
							IP:     "fd00::1",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"fd00::2": {
						IP: &networkv1beta1.IP{
							IP:     "fd00::2",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
				},
			},
			checkResultFunc: func(t *testing.T, got map[string]*PodRequest) {
				assert.Len(t, got, 1)
			},
			checkPodsMapFunc: func(t *testing.T, got map[string]*PodRequest) {
				if got["pod-1"].ipv4Ref == nil {
					assert.Nil(t, got["pod-1"].ipv4Ref)
					assert.Nil(t, got["pod-1"].ipv6Ref)
					assert.NotNil(t, got["pod-2"].ipv4Ref)
					assert.NotNil(t, got["pod-2"].ipv6Ref)
				} else {
					assert.NotNil(t, got["pod-1"].ipv4Ref)
					assert.NotNil(t, got["pod-1"].ipv6Ref)
					assert.Nil(t, got["pod-2"].ipv4Ref)
					assert.Nil(t, got["pod-2"].ipv6Ref)
				}
			},
		},
		{
			name: "take over exists pods",
			args: args{
				log: logr.Discard(),
				podsMapper: map[string]*PodRequest{

					"pod-2": {
						RequireIPv4: true,
						RequireIPv6: true,
					},

					"pod-1": {
						RequireIPv4: true,
						RequireIPv6: true,
						IPv4:        "192.168.0.1",
						IPv6:        "fd00::1",
					},
				},
				ipv4Map: map[string]*EniIP{
					"192.168.0.1": {
						IP: &networkv1beta1.IP{
							IP:     "192.168.0.1",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"192.168.0.2": {
						IP: &networkv1beta1.IP{
							IP:     "192.168.0.2",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-2",
							Status: "InUse",
						},
					},
				},
				ipv6Map: map[string]*EniIP{
					"fd00::1": {
						IP: &networkv1beta1.IP{
							IP:     "fd00::1",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"fd00::2": {
						IP: &networkv1beta1.IP{
							IP:     "fd00::2",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.NetworkInterface{
							ID:     "eni-2",
							Status: "InUse",
						},
					},
				},
			},
			checkResultFunc: func(t *testing.T, got map[string]*PodRequest) {
				assert.Len(t, got, 0)
			},
			checkPodsMapFunc: func(t *testing.T, got map[string]*PodRequest) {
				assert.NotNil(t, got["pod-1"].ipv4Ref)
				assert.NotNil(t, got["pod-1"].ipv6Ref)
				assert.NotNil(t, got["pod-2"].ipv4Ref)
				assert.NotNil(t, got["pod-2"].ipv6Ref)
				assert.Equal(t, "pod-1", got["pod-1"].ipv4Ref.IP.PodID)
				assert.Equal(t, "192.168.0.1", got["pod-1"].ipv4Ref.IP.IP)
				assert.Equal(t, "pod-2", got["pod-2"].ipv4Ref.IP.PodID)
				assert.Equal(t, "pod-1", got["pod-1"].ipv6Ref.IP.PodID)
				assert.Equal(t, "pod-2", got["pod-2"].ipv6Ref.IP.PodID)
				assert.Equal(t, "192.168.0.2", got["pod-2"].ipv4Ref.IP.IP)

			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resul := assignIPFromLocalPool(tt.args.log, tt.args.podsMapper, tt.args.ipv4Map, tt.args.ipv6Map, false)
			tt.checkResultFunc(t, resul)
			tt.checkPodsMapFunc(t, tt.args.podsMapper)
		})
	}
}

func TestReconcileNode_assignIP(t *testing.T) {

	type fields struct {
		aliyun register.Interface
	}
	type args struct {
		ctx context.Context
		opt *eniOptions
	}
	tests := []struct {
		name      string
		fields    fields
		args      args
		wantErr   assert.ErrorAssertionFunc
		checkFunc func(*testing.T, *eniOptions)
	}{
		{
			name: "alloc ip success",
			fields: fields{
				aliyun: func() register.Interface {
					openAPI := mocks.NewInterface(t)
					openAPI.On("AssignPrivateIPAddress", mock.Anything, mock.Anything).Return([]netip.Addr{
						netip.MustParseAddr("192.168.0.1"),
						netip.MustParseAddr("192.168.0.2"),
					}, nil)
					openAPI.On("AssignIpv6Addresses", mock.Anything, mock.Anything).Return([]netip.Addr{
						netip.MustParseAddr("fd00::1"),
						netip.MustParseAddr("fd00::2"),
					}, nil)
					return openAPI
				}(),
			},
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				opt: &eniOptions{
					eniRef: &networkv1beta1.NetworkInterface{
						ID: "eni-1",
					},
					addIPv4N: 2,
					addIPv6N: 2,
				},
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return err == nil
			},
			checkFunc: func(t *testing.T, opt *eniOptions) {
				ip1 := opt.eniRef.IPv4["192.168.0.1"]
				assert.NotNil(t, ip1)
				assert.Equal(t, networkv1beta1.IPStatusValid, ip1.Status)

				ip2 := opt.eniRef.IPv4["192.168.0.2"]
				assert.NotNil(t, ip2)
				assert.Equal(t, networkv1beta1.IPStatusValid, ip2.Status)

				ip3 := opt.eniRef.IPv6["fd00::1"]
				assert.NotNil(t, ip3)
				assert.Equal(t, networkv1beta1.IPStatusValid, ip3.Status)

				ip4 := opt.eniRef.IPv6["fd00::2"]
				assert.NotNil(t, ip4)
				assert.Equal(t, networkv1beta1.IPStatusValid, ip4.Status)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &ReconcileNode{
				aliyun: tt.fields.aliyun,
				tracer: trace.NewNoopTracerProvider().Tracer(""),
			}
			tt.wantErr(t, n.assignIP(tt.args.ctx, tt.args.opt), fmt.Sprintf("assignIP(%v, %v)", tt.args.ctx, tt.args.opt))

			tt.checkFunc(t, tt.args.opt)
		})
	}
}

func Test_addIPToMap(t *testing.T) {
	type args struct {
		in map[string]*networkv1beta1.IP
		ip *networkv1beta1.IP
	}
	tests := []struct {
		name   string
		args   args
		expect map[string]*networkv1beta1.IP
	}{
		{
			name: "exist ip",
			args: args{
				in: map[string]*networkv1beta1.IP{},
				ip: &networkv1beta1.IP{
					IP:     "192.168.0.1",
					Status: networkv1beta1.IPStatusValid,
					PodID:  "pod-1",
				},
			},
			expect: map[string]*networkv1beta1.IP{
				"192.168.0.1": {
					IP:     "192.168.0.1",
					Status: networkv1beta1.IPStatusValid,
					PodID:  "pod-1",
				},
			},
		},
		{
			name: "new",
			args: args{
				in: map[string]*networkv1beta1.IP{},
				ip: &networkv1beta1.IP{
					IP:     "192.168.0.1",
					Status: networkv1beta1.IPStatusValid,
				},
			},
			expect: map[string]*networkv1beta1.IP{
				"192.168.0.1": {
					IP:     "192.168.0.1",
					Status: networkv1beta1.IPStatusValid,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addIPToMap(tt.args.in, tt.args.ip)
			assert.Equal(t, tt.expect, tt.args.in)
		})
	}
}

func TestReconcileNode_createENI(t *testing.T) {
	type fields struct {
		aliyun  register.Interface
		vswpool *vswpool.SwitchPool
	}
	type args struct {
		ctx  context.Context
		node *networkv1beta1.Node
		opt  *eniOptions
	}
	tests := []struct {
		name        string
		fields      fields
		args        args
		wantErr     assert.ErrorAssertionFunc
		checkResult func(t *testing.T, node *networkv1beta1.Node)
	}{
		{
			name: "create eni success",
			fields: fields{
				aliyun: func() register.Interface {
					openAPI := mocks.NewInterface(t)
					openAPI.On("CreateNetworkInterface", mock.Anything, mock.Anything, mock.Anything).Return(&aliyunClient.NetworkInterface{
						Status:             "Available",
						MacAddress:         "",
						NetworkInterfaceID: "eni-1",
						VSwitchID:          "vsw-1",
						PrivateIPAddress:   "127.0.0.1",
						PrivateIPSets: []aliyunClient.IPSet{
							{
								IPAddress: "127.0.0.1",
								Primary:   true,
							},
							{
								IPAddress: "127.0.0.2",
								Primary:   false,
							},
						},
						ZoneID:           "zone-1",
						SecurityGroupIDs: nil,
						ResourceGroupID:  "",
						IPv6Set: []aliyunClient.IPSet{
							{
								IPAddress: "fd00::1",
							},
							{
								IPAddress: "fd00::2",
							},
						},
						Tags:                        nil,
						Type:                        "Secondary",
						InstanceID:                  "",
						NetworkInterfaceTrafficMode: "",
					}, nil)
					openAPI.On("AttachNetworkInterface", mock.Anything, "eni-1", mock.Anything, "").Return(nil)
					openAPI.On("WaitForNetworkInterface", mock.Anything, "eni-1", mock.Anything, mock.Anything, mock.Anything).Return(&aliyunClient.NetworkInterface{
						Status:             "InUse",
						MacAddress:         "",
						NetworkInterfaceID: "eni-1",
						VSwitchID:          "vsw-1",
						PrivateIPAddress:   "127.0.0.1",
						PrivateIPSets: []aliyunClient.IPSet{
							{
								IPAddress: "127.0.0.1",
								Primary:   true,
							},
							{
								IPAddress: "127.0.0.2",
								Primary:   false,
							},
						},
						ZoneID:           "zone-1",
						SecurityGroupIDs: nil,
						ResourceGroupID:  "",
						IPv6Set: []aliyunClient.IPSet{
							{
								IPAddress: "fd00::1",
							},
							{
								IPAddress: "fd00::2",
							},
						},
						Tags:                        nil,
						Type:                        "Secondary",
						InstanceID:                  "",
						TrunkNetworkInterfaceID:     "",
						NetworkInterfaceTrafficMode: "",
						DeviceIndex:                 0,
						CreationTime:                "",
					}, nil)
					return openAPI
				}(),
				vswpool: func() *vswpool.SwitchPool {
					v, _ := vswpool.NewSwitchPool(100, "10m")
					v.Add(&vswpool.Switch{
						ID:               "vsw-1",
						Zone:             "zone-1",
						AvailableIPCount: 100,
						IPv4CIDR:         "127.0.0.0/24",
						IPv6CIDR:         "fd00::/64",
					})
					return v
				}(),
			},
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
					},
					Spec: networkv1beta1.NodeSpec{
						NodeMetadata: networkv1beta1.NodeMetadata{
							ZoneID: "zone-1",
						},
						ENISpec: &networkv1beta1.ENISpec{
							VSwitchOptions: []string{"vsw-1"},
						},
					},
				},
				opt: &eniOptions{
					addIPv4N:   2,
					addIPv6N:   2,
					eniTypeKey: secondaryKey,
				},
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return err == nil
			},
			checkResult: func(t *testing.T, node *networkv1beta1.Node) {
				assert.NotNil(t, node.Status.NetworkInterfaces["eni-1"])
				assert.Equal(t, "InUse", node.Status.NetworkInterfaces["eni-1"].Status)
				assert.Equal(t, "eni-1", node.Status.NetworkInterfaces["eni-1"].ID)
				assert.Equal(t, "vsw-1", node.Status.NetworkInterfaces["eni-1"].VSwitchID)
				assert.Equal(t, "127.0.0.0/24", node.Status.NetworkInterfaces["eni-1"].IPv4CIDR)
				assert.Equal(t, "fd00::/64", node.Status.NetworkInterfaces["eni-1"].IPv6CIDR)
				assert.Equal(t, &networkv1beta1.IP{
					IP:      "127.0.0.1",
					Primary: true,
					Status:  networkv1beta1.IPStatusValid,
					PodID:   "",
				}, node.Status.NetworkInterfaces["eni-1"].IPv4["127.0.0.1"])
			},
		},
		{
			name: "attach eni failed, should roll back",
			fields: fields{
				aliyun: func() register.Interface {
					openAPI := mocks.NewInterface(t)
					openAPI.On("CreateNetworkInterface", mock.Anything, mock.Anything, mock.Anything).Return(&aliyunClient.NetworkInterface{
						Status:             "Available",
						MacAddress:         "",
						NetworkInterfaceID: "eni-1",
						VSwitchID:          "vsw-1",
						PrivateIPAddress:   "127.0.0.1",
						PrivateIPSets: []aliyunClient.IPSet{
							{
								IPAddress: "127.0.0.1",
								Primary:   true,
							},
							{
								IPAddress: "127.0.0.2",
								Primary:   false,
							},
						},
						ZoneID:           "zone-1",
						SecurityGroupIDs: nil,
						ResourceGroupID:  "",
						IPv6Set: []aliyunClient.IPSet{
							{
								IPAddress: "fd00::1",
							},
							{
								IPAddress: "fd00::2",
							},
						},
						Tags:                        nil,
						Type:                        "Secondary",
						InstanceID:                  "",
						NetworkInterfaceTrafficMode: "",
					}, nil)
					openAPI.On("AttachNetworkInterface", mock.Anything, "eni-1", mock.Anything, "").Return(nil)
					openAPI.On("WaitForNetworkInterface", mock.Anything, "eni-1", mock.Anything, mock.Anything, mock.Anything).Return(nil, fmt.Errorf("time out"))
					openAPI.On("DeleteNetworkInterface", mock.Anything, "eni-1").Return(fmt.Errorf("eni already attached"))
					return openAPI
				}(),
				vswpool: func() *vswpool.SwitchPool {
					v, _ := vswpool.NewSwitchPool(100, "10m")
					v.Add(&vswpool.Switch{
						ID:               "vsw-1",
						Zone:             "zone-1",
						AvailableIPCount: 100,
						IPv4CIDR:         "127.0.0.0/24",
						IPv6CIDR:         "fd00::/64",
					})
					return v
				}(),
			},
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
					},
					Spec: networkv1beta1.NodeSpec{
						NodeMetadata: networkv1beta1.NodeMetadata{
							ZoneID: "zone-1",
						},
						ENISpec: &networkv1beta1.ENISpec{
							VSwitchOptions: []string{"vsw-1"},
						},
					},
				},
				opt: &eniOptions{
					addIPv4N:   2,
					addIPv6N:   2,
					eniTypeKey: secondaryKey,
				},
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return err == nil
			},
			checkResult: func(t *testing.T, node *networkv1beta1.Node) {
				assert.NotNil(t, node.Status.NetworkInterfaces["eni-1"])
				assert.Equal(t, "Deleting", node.Status.NetworkInterfaces["eni-1"].Status)
				assert.Equal(t, "eni-1", node.Status.NetworkInterfaces["eni-1"].ID)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &ReconcileNode{
				aliyun:  tt.fields.aliyun,
				vswpool: tt.fields.vswpool,
				tracer:  trace.NewNoopTracerProvider().Tracer(""),
			}
			tt.wantErr(t, n.createENI(tt.args.ctx, tt.args.node, tt.args.opt), fmt.Sprintf("createENI(%v, %v, %v)", tt.args.ctx, tt.args.node, tt.args.opt))

			tt.checkResult(t, tt.args.node)
		})
	}
}

func Test_assignEniWithOptions(t *testing.T) {
	type args struct {
		node       *networkv1beta1.Node
		toAdd      int
		options    []*eniOptions
		filterFunc func(option *eniOptions) bool
	}
	tests := []struct {
		name        string
		args        args
		checkResult func(t *testing.T, options []*eniOptions)
	}{
		{
			name: "test new eni",
			args: args{
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						NodeCap: networkv1beta1.NodeCap{
							IPv4PerAdapter: 10,
							IPv6PerAdapter: 10,
						},
						ENISpec: &networkv1beta1.ENISpec{
							VSwitchOptions: []string{"vsw-1"},
							EnableIPv4:     true,
							EnableIPv6:     true,
						},
					},
				},
				toAdd: 2,
				options: []*eniOptions{
					{
						eniTypeKey: trunkKey,
					},
					{
						eniTypeKey: secondaryKey,
					},
				},
				filterFunc: func(option *eniOptions) bool {
					return option.eniTypeKey == secondaryKey
				},
			},
			checkResult: func(t *testing.T, options []*eniOptions) {
				assert.Equal(t, []*eniOptions{
					{
						eniTypeKey: trunkKey,
						addIPv4N:   0,
						addIPv6N:   0,
					},
					{
						eniTypeKey: secondaryKey,
						addIPv4N:   2,
						addIPv6N:   2,
					},
				}, options)
			},
		},
		{
			name: "test new trunk",
			args: args{
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						NodeCap: networkv1beta1.NodeCap{
							IPv4PerAdapter: 10,
							IPv6PerAdapter: 10,
						},
						ENISpec: &networkv1beta1.ENISpec{
							VSwitchOptions: []string{"vsw-1"},
							EnableIPv4:     true,
							EnableIPv6:     true,
						},
					},
				},
				toAdd: 2,
				options: []*eniOptions{
					{
						eniTypeKey: trunkKey,
					},
					{
						eniTypeKey: secondaryKey,
					},
				},
				filterFunc: func(option *eniOptions) bool {
					return option.eniTypeKey == secondaryKey || option.eniTypeKey == trunkKey
				},
			},
			checkResult: func(t *testing.T, options []*eniOptions) {
				assert.Equal(t, []*eniOptions{
					{
						eniTypeKey: trunkKey,
						addIPv4N:   2,
						addIPv6N:   2,
					},
					{
						eniTypeKey: secondaryKey,
						addIPv4N:   0,
						addIPv6N:   0,
					},
				}, options)
			},
		},
		{
			name: "test new node with trunk enabled",
			args: args{
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						NodeCap: networkv1beta1.NodeCap{
							IPv4PerAdapter: 10,
							IPv6PerAdapter: 10,
						},
						ENISpec: &networkv1beta1.ENISpec{
							VSwitchOptions: []string{"vsw-1"},
							EnableIPv4:     true,
							EnableIPv6:     true,
							EnableTrunk:    true,
						},
					},
				},
				toAdd: 0,
				options: []*eniOptions{
					{
						eniTypeKey: trunkKey,
					},
					{
						eniTypeKey: secondaryKey,
					},
				},
				filterFunc: func(option *eniOptions) bool {
					return option.eniTypeKey == secondaryKey || option.eniTypeKey == trunkKey
				},
			},
			checkResult: func(t *testing.T, options []*eniOptions) {
				assert.Equal(t, []*eniOptions{
					{
						eniTypeKey: trunkKey,
						addIPv4N:   1,
						addIPv6N:   1,
					},
					{
						eniTypeKey: secondaryKey,
						addIPv4N:   0,
						addIPv6N:   0,
					},
				}, options)
			},
		},
		{
			name: "test exist eni, ipv4 and ipv6 is not equal",
			args: args{
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						NodeCap: networkv1beta1.NodeCap{
							IPv4PerAdapter: 4,
							IPv6PerAdapter: 4,
						},
						ENISpec: &networkv1beta1.ENISpec{
							VSwitchOptions: []string{"vsw-1"},
							EnableIPv4:     true,
							EnableIPv6:     true,
							EnableTrunk:    true,
						},
					},
				},
				toAdd: 1,
				options: []*eniOptions{
					{
						eniTypeKey: trunkKey,
						eniRef: &networkv1beta1.NetworkInterface{
							ID:     "eni-1",
							Status: "Available",
							IPv4: map[string]*networkv1beta1.IP{
								"127.0.0.1": {
									IP:     "127.0.0.1",
									Status: networkv1beta1.IPStatusValid,
									PodID:  "foo",
								},
							},
							IPv6: map[string]*networkv1beta1.IP{
								"fd00::1": {
									IP:     "fd00::1",
									Status: networkv1beta1.IPStatusValid,
								},
							},
						},
					},
					{
						eniTypeKey: secondaryKey,
					},
				},
				filterFunc: func(option *eniOptions) bool {
					return option.eniTypeKey == secondaryKey || option.eniTypeKey == trunkKey
				},
			},
			checkResult: func(t *testing.T, options []*eniOptions) {
				assert.Equal(t, 1, options[0].addIPv4N)
				assert.Equal(t, 0, options[0].addIPv6N)
			},
		},
		{
			name: "test exist eni, one eni is full",
			args: args{
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						NodeCap: networkv1beta1.NodeCap{
							IPv4PerAdapter: 4,
							IPv6PerAdapter: 4,
						},
						ENISpec: &networkv1beta1.ENISpec{
							VSwitchOptions: []string{"vsw-1"},
							EnableIPv4:     true,
							EnableIPv6:     true,
							EnableTrunk:    true,
						},
					},
				},
				toAdd: 3,
				options: []*eniOptions{
					{
						eniTypeKey: trunkKey,
						eniRef: &networkv1beta1.NetworkInterface{
							ID:     "eni-1",
							Status: "Available",
							IPv4: map[string]*networkv1beta1.IP{
								"127.0.0.1": {
									IP:     "127.0.0.1",
									Status: networkv1beta1.IPStatusValid,
									PodID:  "foo",
								},
								"127.0.0.2": {
									IP:     "127.0.0.2",
									Status: networkv1beta1.IPStatusDeleting,
								},
								"127.0.0.3": {
									IP:     "127.0.0.3",
									Status: networkv1beta1.IPStatusDeleting,
								},
								"127.0.0.4": {
									IP:     "127.0.0.4",
									Status: networkv1beta1.IPStatusValid,
								},
							},
							IPv6: map[string]*networkv1beta1.IP{
								"fd00::1": {
									IP:     "fd00::1",
									Status: networkv1beta1.IPStatusValid,
									PodID:  "foo",
								},
								"fd00::2": {
									IP:     "fd00::2",
									Status: networkv1beta1.IPStatusDeleting,
								},
								"fd00::3": {
									IP:     "fd00::3",
									Status: networkv1beta1.IPStatusDeleting,
								},
								"fd00::4": {
									IP:     "fd00::4",
									Status: networkv1beta1.IPStatusValid,
								},
							},
						},
					},
					{
						eniTypeKey: secondaryKey,
					},
				},
				filterFunc: func(option *eniOptions) bool {
					return option.eniTypeKey == secondaryKey || option.eniTypeKey == trunkKey
				},
			},
			checkResult: func(t *testing.T, options []*eniOptions) {
				assert.Equal(t, 0, options[0].addIPv4N, "should reuse prev ip")
				assert.Equal(t, 0, options[0].addIPv6N, "should reuse prev ip")
				assert.Equal(t, 2, options[1].addIPv4N, "should reuse prev ip")
				assert.Equal(t, 2, options[1].addIPv6N, "should reuse prev ip")
			},
		},
		{
			name: "test exist eni, eni still has space",
			args: args{
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						NodeCap: networkv1beta1.NodeCap{
							IPv4PerAdapter: 4,
							IPv6PerAdapter: 4,
						},
						ENISpec: &networkv1beta1.ENISpec{
							VSwitchOptions: []string{"vsw-1"},
							EnableIPv4:     true,
							EnableIPv6:     true,
							EnableTrunk:    true,
						},
					},
				},
				toAdd: 3,
				options: []*eniOptions{
					{
						eniTypeKey: trunkKey,
						eniRef: &networkv1beta1.NetworkInterface{
							ID:     "eni-1",
							Status: "Available",
							IPv4: map[string]*networkv1beta1.IP{
								"127.0.0.1": {
									IP:     "127.0.0.1",
									Status: networkv1beta1.IPStatusValid,
									PodID:  "foo",
								},
								"127.0.0.2": {
									IP:     "127.0.0.2",
									Status: networkv1beta1.IPStatusDeleting,
								},
								"127.0.0.3": {
									IP:     "127.0.0.3",
									Status: networkv1beta1.IPStatusDeleting,
								},
							},
							IPv6: map[string]*networkv1beta1.IP{
								"fd00::1": {
									IP:     "fd00::1",
									Status: networkv1beta1.IPStatusValid,
									PodID:  "foo",
								},
								"fd00::2": {
									IP:     "fd00::2",
									Status: networkv1beta1.IPStatusDeleting,
								},
								"fd00::3": {
									IP:     "fd00::3",
									Status: networkv1beta1.IPStatusDeleting,
								},
							},
						},
					},
					{
						eniTypeKey: secondaryKey,
					},
				},
				filterFunc: func(option *eniOptions) bool {
					return option.eniTypeKey == secondaryKey || option.eniTypeKey == trunkKey
				},
			},
			checkResult: func(t *testing.T, options []*eniOptions) {
				assert.Equal(t, 1, options[0].addIPv4N)
				assert.Equal(t, 1, options[0].addIPv6N)
				assert.Equal(t, 2, options[1].addIPv4N)
				assert.Equal(t, 2, options[1].addIPv6N)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assignEniWithOptions(tt.args.node, tt.args.toAdd, tt.args.options, tt.args.filterFunc)

			tt.checkResult(t, tt.args.options)
		})
	}
}

func TestReconcileNode_handleStatus(t *testing.T) {
	type fields struct {
		aliyun register.Interface
	}
	type args struct {
		ctx  context.Context
		node *networkv1beta1.Node
	}
	tests := []struct {
		name        string
		fields      fields
		args        args
		wantErr     assert.ErrorAssertionFunc
		checkResult func(t *testing.T, node *networkv1beta1.Node)
	}{
		{
			name: "test eni inUse",
			fields: fields{
				aliyun: func() register.Interface {
					openAPI := mocks.NewInterface(t)
					openAPI.On("UnAssignPrivateIPAddresses", mock.Anything, "eni-1", mock.Anything).Return(nil)
					openAPI.On("UnAssignIpv6Addresses", mock.Anything, "eni-1", mock.Anything).Return(nil)
					return openAPI
				}(),
			},
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					Status: networkv1beta1.NodeStatus{
						NetworkInterfaces: map[string]*networkv1beta1.NetworkInterface{
							"eni-1": {
								ID:     "eni-1",
								Status: "InUse",
								IPv4: map[string]*networkv1beta1.IP{
									"127.0.0.1": {
										IP:     "127.0.0.1",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "foo",
									},
									"127.0.0.2": {
										IP:     "127.0.0.2",
										Status: networkv1beta1.IPStatusDeleting,
										PodID:  "bar",
									},
								},
								IPv6: map[string]*networkv1beta1.IP{
									"fd00::1": {
										IP:     "fd00::1",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "foo",
									},
									"fd00::2": {
										IP:     "fd00::2",
										Status: networkv1beta1.IPStatusDeleting,
										PodID:  "bar",
									},
								},
							},
						},
					},
				},
			},
			wantErr: assert.NoError,
			checkResult: func(t *testing.T, node *networkv1beta1.Node) {
				assert.Equal(t, 1, len(node.Status.NetworkInterfaces["eni-1"].IPv4))
				assert.Nil(t, node.Status.NetworkInterfaces["eni-1"].IPv4["127.0.0.2"])
				assert.Nil(t, node.Status.NetworkInterfaces["eni-1"].IPv6["fd00::2"])
			},
		},
		{
			name: "test eni deleting",
			fields: fields{
				aliyun: func() register.Interface {
					openAPI := mocks.NewInterface(t)
					openAPI.On("DetachNetworkInterface", mock.Anything, "eni-1", mock.Anything, mock.Anything).Return(nil)
					openAPI.On("WaitForNetworkInterface", mock.Anything, "eni-1", mock.Anything, mock.Anything, mock.Anything).Return(&aliyunClient.NetworkInterface{
						Status:             "Available",
						MacAddress:         "",
						NetworkInterfaceID: "eni-1",
						VSwitchID:          "vsw-1",
						PrivateIPAddress:   "127.0.0.1",
						PrivateIPSets: []aliyunClient.IPSet{
							{
								IPAddress: "127.0.0.1",
								Primary:   true,
							},
							{
								IPAddress: "127.0.0.2",
								Primary:   false,
							},
						},
						ZoneID:           "zone-1",
						SecurityGroupIDs: nil,
						ResourceGroupID:  "",
						IPv6Set: []aliyunClient.IPSet{
							{
								IPAddress: "fd00::1",
							},
							{
								IPAddress: "fd00::2",
							},
						},
						Tags:                        nil,
						Type:                        "Secondary",
						InstanceID:                  "",
						TrunkNetworkInterfaceID:     "",
						NetworkInterfaceTrafficMode: "",
						DeviceIndex:                 0,
						CreationTime:                "",
					}, nil)
					openAPI.On("DeleteNetworkInterface", mock.Anything, "eni-1").Return(nil)
					return openAPI
				}(),
			},
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					Status: networkv1beta1.NodeStatus{
						NetworkInterfaces: map[string]*networkv1beta1.NetworkInterface{
							"eni-1": {
								ID:     "eni-1",
								Status: "Deleting",
								IPv4: map[string]*networkv1beta1.IP{
									"127.0.0.1": {
										IP:     "127.0.0.1",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "foo",
									},
									"127.0.0.2": {
										IP:     "127.0.0.2",
										Status: networkv1beta1.IPStatusDeleting,
										PodID:  "bar",
									},
								},
								IPv6: map[string]*networkv1beta1.IP{
									"fd00::1": {
										IP:     "fd00::1",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "foo",
									},
									"fd00::2": {
										IP:     "fd00::2",
										Status: networkv1beta1.IPStatusDeleting,
										PodID:  "bar",
									},
								},
							},
						},
					},
				},
			},
			wantErr: assert.NoError,
			checkResult: func(t *testing.T, node *networkv1beta1.Node) {
				assert.Nil(t, node.Status.NetworkInterfaces["eni-1"])
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &ReconcileNode{
				aliyun: tt.fields.aliyun,
				tracer: trace.NewNoopTracerProvider().Tracer(""),
			}
			tt.wantErr(t, n.handleStatus(tt.args.ctx, tt.args.node), fmt.Sprintf("handleStatus(%v, %v)", tt.args.ctx, tt.args.node))

			tt.checkResult(t, tt.args.node)
		})
	}
}

func TestReconcileNode_adjustPool(t *testing.T) {
	type args struct {
		ctx  context.Context
		node *networkv1beta1.Node
	}
	tests := []struct {
		name      string
		args      args
		wantErr   assert.ErrorAssertionFunc
		checkFunc func(t *testing.T, node *networkv1beta1.Node)
	}{
		{
			name: "should not release trunk eni",
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{EnableIPv4: true, EnableIPv6: true},
						Pool:    &networkv1beta1.PoolSpec{MaxPoolSize: 0},
					},
					Status: networkv1beta1.NodeStatus{
						NetworkInterfaces: map[string]*networkv1beta1.NetworkInterface{
							"eni-1": {
								ID:                          "eni-1",
								Status:                      "InUse",
								NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								IPv4: map[string]*networkv1beta1.IP{
									"127.0.0.1": {
										IP:      "127.0.0.1",
										Status:  networkv1beta1.IPStatusValid,
										Primary: true,
										PodID:   "",
									},
									"127.0.0.2": {
										IP:     "127.0.0.2",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "",
									},
								},
							},
							"eni-2": {
								ID:     "eni-2",
								Status: "Deleting",
								IPv4: map[string]*networkv1beta1.IP{
									"127.0.1.1": {
										IP:      "127.0.1.1",
										Status:  networkv1beta1.IPStatusValid,
										Primary: true,
										PodID:   "",
									},
									"127.0.1.2": {
										IP:     "127.0.1.2",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "",
									},
								},
							},
						},
					},
				},
			},
			wantErr: assert.NoError,
			checkFunc: func(t *testing.T, node *networkv1beta1.Node) {
				assert.Equal(t, 2, len(node.Status.NetworkInterfaces))
				assert.Equal(t, "InUse", node.Status.NetworkInterfaces["eni-1"].Status)
				assert.Equal(t, networkv1beta1.IPStatusValid, node.Status.NetworkInterfaces["eni-1"].IPv4["127.0.0.1"].Status)
				assert.Equal(t, networkv1beta1.IPStatusDeleting, node.Status.NetworkInterfaces["eni-1"].IPv4["127.0.0.2"].Status)
				assert.Equal(t, "Deleting", node.Status.NetworkInterfaces["eni-2"].Status)
			},
		},
		{
			name: "should not release any",
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{EnableIPv4: true, EnableIPv6: true},
						Pool:    &networkv1beta1.PoolSpec{MaxPoolSize: 3},
					},
					Status: networkv1beta1.NodeStatus{
						NetworkInterfaces: map[string]*networkv1beta1.NetworkInterface{
							"eni-1": {
								ID:                          "eni-1",
								Status:                      "InUse",
								NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								IPv4: map[string]*networkv1beta1.IP{
									"127.0.0.1": {
										IP:      "127.0.0.1",
										Status:  networkv1beta1.IPStatusValid,
										Primary: true,
										PodID:   "foo",
									},
									"127.0.0.2": {
										IP:     "127.0.0.2",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "",
									},
								},
							},
							"eni-2": {
								ID:                          "eni-2",
								Status:                      "InUse",
								NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								IPv4: map[string]*networkv1beta1.IP{
									"127.0.1.1": {
										IP:      "127.0.1.1",
										Status:  networkv1beta1.IPStatusValid,
										Primary: true,
										PodID:   "",
									},
									"127.0.1.2": {
										IP:     "127.0.1.2",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "",
									},
								},
							},
						},
					},
				},
			},
			wantErr: assert.NoError,
			checkFunc: func(t *testing.T, node *networkv1beta1.Node) {
				assert.Equal(t, 2, len(node.Status.NetworkInterfaces))
				assert.Equal(t, "InUse", node.Status.NetworkInterfaces["eni-1"].Status)
				assert.Equal(t, "InUse", node.Status.NetworkInterfaces["eni-2"].Status)
				assert.Equal(t, &networkv1beta1.NetworkInterface{
					ID:                          "eni-1",
					Status:                      "InUse",
					NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
					NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
					IPv4: map[string]*networkv1beta1.IP{
						"127.0.0.1": {
							IP:      "127.0.0.1",
							Status:  networkv1beta1.IPStatusValid,
							Primary: true,
							PodID:   "foo",
						},
						"127.0.0.2": {
							IP:     "127.0.0.2",
							Status: networkv1beta1.IPStatusValid,
							PodID:  "",
						},
					},
				}, node.Status.NetworkInterfaces["eni-1"])

				assert.Equal(t, &networkv1beta1.NetworkInterface{
					ID:                          "eni-2",
					Status:                      "InUse",
					NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
					NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
					IPv4: map[string]*networkv1beta1.IP{
						"127.0.1.1": {
							IP:      "127.0.1.1",
							Status:  networkv1beta1.IPStatusValid,
							Primary: true,
							PodID:   "",
						},
						"127.0.1.2": {
							IP:     "127.0.1.2",
							Status: networkv1beta1.IPStatusValid,
							PodID:  "",
						},
					},
				}, node.Status.NetworkInterfaces["eni-2"])
			},
		},
		{
			name: "v4 v6 not equal, delete eni",
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{EnableIPv4: true, EnableIPv6: true},
						Pool:    &networkv1beta1.PoolSpec{MaxPoolSize: 1},
					},
					Status: networkv1beta1.NodeStatus{
						NetworkInterfaces: map[string]*networkv1beta1.NetworkInterface{
							"eni-1": {
								ID:                          "eni-1",
								Status:                      "InUse",
								NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								IPv4: map[string]*networkv1beta1.IP{
									"127.0.0.1": {
										IP:      "127.0.0.1",
										Status:  networkv1beta1.IPStatusValid,
										Primary: true,
										PodID:   "",
									},
									"127.0.0.2": {
										IP:     "127.0.0.2",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "",
									},
								},
								IPv6: map[string]*networkv1beta1.IP{},
							},
							"eni-2": {
								ID:                          "eni-2",
								Status:                      "InUse",
								NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								IPv4: map[string]*networkv1beta1.IP{
									"127.0.1.1": {
										IP:      "127.0.1.1",
										Status:  networkv1beta1.IPStatusValid,
										Primary: true,
										PodID:   "",
									},
									"127.0.1.2": {
										IP:     "127.0.1.2",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "",
									},
								},
								IPv6: map[string]*networkv1beta1.IP{
									"fd00::1": {
										IP:     "fd00::1",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "",
									},
									"fd00::2": {
										IP:     "fd00::2",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "",
									},
								},
							},
						},
					},
				},
			},
			wantErr: assert.NoError,
			checkFunc: func(t *testing.T, node *networkv1beta1.Node) {
				assert.Equal(t, 2, len(node.Status.NetworkInterfaces))
				assert.Equal(t, "InUse", node.Status.NetworkInterfaces["eni-1"].Status)
				assert.Equal(t, networkv1beta1.IPStatusValid, node.Status.NetworkInterfaces["eni-1"].IPv4["127.0.0.1"].Status)
				assert.Equal(t, networkv1beta1.IPStatusDeleting, node.Status.NetworkInterfaces["eni-1"].IPv4["127.0.0.2"].Status)
				assert.Equal(t, "Deleting", node.Status.NetworkInterfaces["eni-2"].Status)
			},
		},
		{
			name: "v4 v6 not equal",
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{EnableIPv4: true, EnableIPv6: true},
						Pool:    &networkv1beta1.PoolSpec{MaxPoolSize: 3},
					},
					Status: networkv1beta1.NodeStatus{
						NetworkInterfaces: map[string]*networkv1beta1.NetworkInterface{
							"eni-1": {
								ID:                          "eni-1",
								Status:                      "InUse",
								NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								IPv4: map[string]*networkv1beta1.IP{
									"127.0.0.1": {
										IP:      "127.0.0.1",
										Status:  networkv1beta1.IPStatusValid,
										Primary: true,
										PodID:   "",
									},
									"127.0.0.2": {
										IP:     "127.0.0.2",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "",
									},
								},
								IPv6: map[string]*networkv1beta1.IP{
									"fd00::2": {
										IP:     "fd00::2",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "",
									},
								},
							},
							"eni-2": {
								ID:                          "eni-2",
								Status:                      "InUse",
								NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								IPv4: map[string]*networkv1beta1.IP{
									"127.0.1.1": {
										IP:      "127.0.1.1",
										Status:  networkv1beta1.IPStatusValid,
										Primary: true,
										PodID:   "",
									},
									"127.0.1.2": {
										IP:     "127.0.1.2",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "",
									},
								},
								IPv6: map[string]*networkv1beta1.IP{
									"fd00::1": {
										IP:     "fd00::1",
										Status: networkv1beta1.IPStatusValid,
										PodID:  "",
									},
								},
							},
						},
					},
				},
			},
			wantErr: assert.NoError,
			checkFunc: func(t *testing.T, node *networkv1beta1.Node) {
				assert.Equal(t, 2, len(node.Status.NetworkInterfaces))
				assert.Equal(t, "InUse", node.Status.NetworkInterfaces["eni-1"].Status)
				assert.Equal(t, networkv1beta1.IPStatusValid, node.Status.NetworkInterfaces["eni-1"].IPv4["127.0.0.1"].Status)
				assert.Equal(t, networkv1beta1.IPStatusValid, node.Status.NetworkInterfaces["eni-1"].IPv4["127.0.0.2"].Status)

				assert.Equal(t, "InUse", node.Status.NetworkInterfaces["eni-2"].Status)
				assert.Equal(t, networkv1beta1.IPStatusDeleting, node.Status.NetworkInterfaces["eni-2"].IPv4["127.0.1.2"].Status)
				assert.Equal(t, networkv1beta1.IPStatusValid, node.Status.NetworkInterfaces["eni-2"].IPv6["fd00::1"].Status)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &ReconcileNode{
				tracer: trace.NewNoopTracerProvider().Tracer(""),
			}
			tt.wantErr(t, n.adjustPool(tt.args.ctx, tt.args.node), fmt.Sprintf("adjustPool(%v, %v)", tt.args.ctx, tt.args.node))

			tt.checkFunc(t, tt.args.node)
		})
	}
}

func TestUpdateCrCondition(t *testing.T) {
	tests := []struct {
		name      string
		options   []*eniOptions
		checkFunc func(t *testing.T, options []*eniOptions)
	}{
		{
			name: "no errors",
			options: []*eniOptions{
				{
					eniRef: &networkv1beta1.NetworkInterface{},
				},
			},
			checkFunc: func(t *testing.T, options []*eniOptions) {
				assert.Equal(t, []*eniOptions{
					{
						eniRef: &networkv1beta1.NetworkInterface{},
					},
				}, options)
			},
		},
		{
			name: "ip not enough error",
			options: []*eniOptions{
				{
					eniRef: &networkv1beta1.NetworkInterface{
						VSwitchID: "test-vswitch",
					},
					errors: []error{
						vswpool.ErrNoAvailableVSwitch,
					},
				},
			},
			checkFunc: func(t *testing.T, options []*eniOptions) {
				assert.Equal(t, "test-vswitch", options[0].eniRef.VSwitchID)
				_, ok := options[0].eniRef.Conditions[ConditionInsufficientIP]
				assert.True(t, ok)
			},
		},
		{
			name: "generic error",
			options: []*eniOptions{
				{
					eniRef: &networkv1beta1.NetworkInterface{
						VSwitchID: "test-vswitch",
					},
					errors: []error{
						errors.New("generic error"),
					},
				},
			},
			checkFunc: func(t *testing.T, options []*eniOptions) {
				_, ok := options[0].eniRef.Conditions[ConditionOperationErr]
				assert.True(t, ok)
			},
		},
		{
			name: "multiple errors",
			options: []*eniOptions{
				{
					eniRef: &networkv1beta1.NetworkInterface{
						VSwitchID: "test-vswitch",
					},
					errors: []error{
						vswpool.ErrNoAvailableVSwitch,
						errors.New("generic error"),
					},
				},
			},
			checkFunc: func(t *testing.T, options []*eniOptions) {
				_, ok := options[0].eniRef.Conditions[ConditionOperationErr]
				assert.True(t, ok)
				_, ok = options[0].eniRef.Conditions[ConditionInsufficientIP]
				assert.True(t, ok)
			},
		},
	}

	for _, tt := range tests {
		updateCrCondition(tt.options)
		tt.checkFunc(t, tt.options)
	}
}

var _ = Describe("Node Controller", func() {
	ctx := context.Background()

	BeforeEach(func() {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
		}
		err := k8sClient.Create(ctx, node)
		Expect(err).NotTo(HaveOccurred())
	})
	AfterEach(func() {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
		}
		err := k8sClient.Delete(ctx, node)
		Expect(err).NotTo(HaveOccurred())
	})

	Context("Check update node status", func() {
		It("Empty eni, should report InsufficientIP", func() {
			updateNodeCondition(ctx, k8sClient, "foo", nil)

			node := &corev1.Node{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "foo"}, node)
			Expect(err).NotTo(HaveOccurred())

			_, result := lo.Find(node.Status.Conditions, func(item corev1.NodeCondition) bool {
				if item.Type == "SufficientIP" && item.Status == "False" && item.Reason == "InsufficientIP" {
					return true
				}
				return false
			})
			Expect(result).To(BeTrue())
		})

		It("Patch should update the old status", func() {
			node := &corev1.Node{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "foo"}, node)
			Expect(err).NotTo(HaveOccurred())

			node.Status.Conditions = append(node.Status.Conditions, corev1.NodeCondition{
				Type:               "SufficientIP",
				Status:             "True",
				LastHeartbeatTime:  metav1.Time{},
				LastTransitionTime: metav1.Time{},
				Reason:             "SufficientIP",
				Message:            "",
			})
			err = k8sClient.Status().Update(ctx, node)
			Expect(err).NotTo(HaveOccurred())

			updateNodeCondition(ctx, k8sClient, "foo", nil)

			node = &corev1.Node{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "foo"}, node)
			Expect(err).NotTo(HaveOccurred())

			count := 0
			lo.ForEach(node.Status.Conditions, func(item corev1.NodeCondition, index int) {
				if item.Type == "SufficientIP" {
					count++
				}
			})
			Expect(count).To(Equal(1))
		})

		It("Empty eni should be SufficientIP", func() {
			updateNodeCondition(ctx, k8sClient, "foo", []*eniOptions{
				{
					eniTypeKey: eniTypeKey{},
					eniRef:     nil,
					addIPv4N:   0,
					addIPv6N:   0,
					errors:     nil,
				},
			})

			node := &corev1.Node{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "foo"}, node)
			Expect(err).NotTo(HaveOccurred())

			_, result := lo.Find(node.Status.Conditions, func(item corev1.NodeCondition) bool {
				if item.Type == "SufficientIP" && item.Status == "True" && item.Reason == "SufficientIP" {
					return true
				}
				return false
			})
			Expect(result).To(BeTrue())
		})
	})
})
