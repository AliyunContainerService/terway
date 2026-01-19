package node

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/eni/ops"
	"github.com/AliyunContainerService/terway/pkg/internal/testutil"
	vswpool "github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.opentelemetry.io/otel/trace/noop"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
					NetworkInterface: &networkv1beta1.Nic{},
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
					NetworkInterface: &networkv1beta1.Nic{},
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
					NetworkInterface: &networkv1beta1.Nic{},
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
					NetworkInterface: &networkv1beta1.Nic{},
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
					NetworkInterface: &networkv1beta1.Nic{},
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
					NetworkInterface: &networkv1beta1.Nic{},
					IP:               &networkv1beta1.IP{},
				},
			},
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = MetaIntoCtx(ctx)
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
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
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
					eniRef: &networkv1beta1.Nic{
						NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
						NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
					},
					eniTypeKey: trunkKey,
				},
				{
					eniRef: &networkv1beta1.Nic{
						NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
						NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
					},
					eniTypeKey: trunkKey,
				},
				{
					eniRef: &networkv1beta1.Nic{
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
						NetworkInterface: &networkv1beta1.Nic{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"192.168.0.2": {
						IP: &networkv1beta1.IP{
							IP:     "192.168.0.2",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.Nic{
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
						NetworkInterface: &networkv1beta1.Nic{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"fd00::2": {
						IP: &networkv1beta1.IP{
							IP:     "fd00::2",
							Status: networkv1beta1.IPStatusDeleting,
						},
						NetworkInterface: &networkv1beta1.Nic{
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
						NetworkInterface: &networkv1beta1.Nic{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"192.168.0.2": {
						IP: &networkv1beta1.IP{
							IP:     "192.168.0.2",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.Nic{
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
						NetworkInterface: &networkv1beta1.Nic{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"fd00::2": {
						IP: &networkv1beta1.IP{
							IP:     "fd00::2",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.Nic{
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
						NetworkInterface: &networkv1beta1.Nic{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"192.168.0.2": {
						IP: &networkv1beta1.IP{
							IP:     "192.168.0.2",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.Nic{
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
						NetworkInterface: &networkv1beta1.Nic{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"fd00::2": {
						IP: &networkv1beta1.IP{
							IP:     "fd00::2",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.Nic{
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
						NetworkInterface: &networkv1beta1.Nic{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"192.168.0.2": {
						IP: &networkv1beta1.IP{
							IP:     "192.168.0.2",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.Nic{
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
						NetworkInterface: &networkv1beta1.Nic{
							ID:     "eni-1",
							Status: "InUse",
						},
					},
					"fd00::2": {
						IP: &networkv1beta1.IP{
							IP:     "fd00::2",
							Status: networkv1beta1.IPStatusValid,
						},
						NetworkInterface: &networkv1beta1.Nic{
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
			resul := assignIPFromLocalPool(tt.args.log, tt.args.podsMapper, tt.args.ipv4Map, tt.args.ipv6Map, false, &networkv1beta1.Node{})
			tt.checkResultFunc(t, resul)
			tt.checkPodsMapFunc(t, tt.args.podsMapper)
		})
	}
}

func TestReconcileNode_assignIP(t *testing.T) {

	type fields struct {
		aliyun aliyunClient.OpenAPI
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
				aliyun: func() aliyunClient.OpenAPI {
					openAPI := mocks.NewOpenAPI(t)
					openAPI.On("AssignPrivateIPAddressV2", mock.Anything, mock.Anything).Return([]aliyunClient.IPSet{
						{
							IPAddress: netip.MustParseAddr("192.168.0.1").String(),
						},
						{
							IPAddress: netip.MustParseAddr("192.168.0.2").String(),
						},
					}, nil)
					openAPI.On("AssignIpv6AddressesV2", mock.Anything, mock.Anything).Return([]aliyunClient.IPSet{
						{
							IPAddress: netip.MustParseAddr("fd00::1").String(),
						},
						{
							IPAddress: netip.MustParseAddr("fd00::2").String(),
						},
					}, nil)
					return openAPI
				}(),
			},
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				opt: &eniOptions{
					eniRef: &networkv1beta1.Nic{
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
				tracer: noop.NewTracerProvider().Tracer(""),
			}
			tt.wantErr(t, n.assignIP(tt.args.ctx, &networkv1beta1.Node{
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
			}, tt.args.opt), fmt.Sprintf("assignIP(%v, %v)", tt.args.ctx, tt.args.opt))

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
						eniRef: &networkv1beta1.Nic{
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
						eniRef: &networkv1beta1.Nic{
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
						eniRef: &networkv1beta1.Nic{
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
			assignEniWithOptions(context.Background(), tt.args.node, tt.args.toAdd, tt.args.options, nil, tt.args.filterFunc)

			tt.checkResult(t, tt.args.options)
		})
	}
}

func Test_assignEniWithOptions_AttachingENI(t *testing.T) {
	// Setup mock task queue
	notifyCh := make(chan string, 10)
	mockAPI := mocks.NewOpenAPI(t)
	tracer := noop.NewTracerProvider().Tracer("test")
	executor := ops.NewExecutor(mockAPI, tracer)
	taskQueue := NewENITaskQueue(context.Background(), executor, notifyCh)

	// Add an Attaching ENI with 5 requested IPs to the task queue
	taskQueue.tasks["eni-attaching"] = &ENITaskRecord{
		ENIID:              "eni-attaching",
		Status:             TaskStatusRunning,
		RequestedIPv4Count: 5,
		RequestedIPv6Count: 0,
	}

	t.Run("attaching ENI satisfies full demand", func(t *testing.T) {
		node := &networkv1beta1.Node{
			Spec: networkv1beta1.NodeSpec{
				NodeCap: networkv1beta1.NodeCap{
					IPv4PerAdapter: 10,
					IPv6PerAdapter: 10,
				},
				ENISpec: &networkv1beta1.ENISpec{
					EnableIPv4: true,
					EnableIPv6: false,
				},
			},
			Status: networkv1beta1.NodeStatus{
				NetworkInterfaces: map[string]*networkv1beta1.Nic{
					"eni-attaching": {
						ID:     "eni-attaching",
						Status: aliyunClient.ENIStatusAttaching,
					},
				},
			},
		}

		options := []*eniOptions{
			{
				eniTypeKey: secondaryKey,
				eniRef:     node.Status.NetworkInterfaces["eni-attaching"],
			},
			{
				eniTypeKey: secondaryKey, // New ENI slot
			},
		}

		// Need 3 IPs, but Attaching ENI already requested 5
		assignEniWithOptions(context.Background(), node, 3, options, taskQueue, func(o *eniOptions) bool { return true })

		// Attaching ENI should not request more IPs
		assert.Equal(t, 0, options[0].addIPv4N)
		// New ENI slot should also not request (demand is satisfied)
		assert.Equal(t, 0, options[1].addIPv4N)
	})

	t.Run("attaching ENI partially satisfies demand", func(t *testing.T) {
		// Add a task with 3 requested IPs
		taskQueue.tasks["eni-partial"] = &ENITaskRecord{
			ENIID:              "eni-partial",
			Status:             TaskStatusRunning,
			RequestedIPv4Count: 3,
			RequestedIPv6Count: 0,
		}

		node := &networkv1beta1.Node{
			Spec: networkv1beta1.NodeSpec{
				NodeCap: networkv1beta1.NodeCap{
					IPv4PerAdapter: 10,
					IPv6PerAdapter: 10,
				},
				ENISpec: &networkv1beta1.ENISpec{
					EnableIPv4: true,
					EnableIPv6: false,
				},
			},
			Status: networkv1beta1.NodeStatus{
				NetworkInterfaces: map[string]*networkv1beta1.Nic{
					"eni-partial": {
						ID:     "eni-partial",
						Status: aliyunClient.ENIStatusAttaching,
					},
				},
			},
		}

		options := []*eniOptions{
			{
				eniTypeKey: secondaryKey,
				eniRef:     node.Status.NetworkInterfaces["eni-partial"],
			},
			{
				eniTypeKey: secondaryKey, // New ENI slot
			},
		}

		// Need 5 IPs, but Attaching ENI only requested 3, so need 2 more
		assignEniWithOptions(context.Background(), node, 5, options, taskQueue, func(o *eniOptions) bool { return true })

		// Attaching ENI should not request more IPs (can't add to Attaching)
		assert.Equal(t, 0, options[0].addIPv4N)
		// New ENI slot should request the remaining 2 IPs
		assert.Equal(t, 2, options[1].addIPv4N)
	})

	t.Run("attaching ENI without task queue entry", func(t *testing.T) {
		node := &networkv1beta1.Node{
			Spec: networkv1beta1.NodeSpec{
				NodeCap: networkv1beta1.NodeCap{
					IPv4PerAdapter: 10,
					IPv6PerAdapter: 10,
				},
				ENISpec: &networkv1beta1.ENISpec{
					EnableIPv4: true,
					EnableIPv6: false,
				},
			},
			Status: networkv1beta1.NodeStatus{
				NetworkInterfaces: map[string]*networkv1beta1.Nic{
					"eni-unknown": {
						ID:     "eni-unknown",
						Status: aliyunClient.ENIStatusAttaching,
					},
				},
			},
		}

		options := []*eniOptions{
			{
				eniTypeKey: secondaryKey,
				eniRef:     node.Status.NetworkInterfaces["eni-unknown"],
			},
			{
				eniTypeKey: secondaryKey, // New ENI slot
			},
		}

		// Need 3 IPs, Attaching ENI not in queue (e.g., controller restart)
		assignEniWithOptions(context.Background(), node, 3, options, taskQueue, func(o *eniOptions) bool { return true })

		// Attaching ENI should not request more IPs
		assert.Equal(t, 0, options[0].addIPv4N)
		// New ENI slot should request all 3 IPs
		assert.Equal(t, 3, options[1].addIPv4N)
	})
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
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
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
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
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
				assert.Equal(t, &networkv1beta1.Nic{
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

				assert.Equal(t, &networkv1beta1.Nic{
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
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
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
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
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
		{
			name: "ignore if time not meet",
			args: args{
				ctx: func() context.Context {
					ctx := MetaIntoCtx(context.TODO())
					MetaCtx(ctx).LastGCTime = time.Now()
					return ctx
				}(),
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{EnableIPv4: true, EnableIPv6: true},
						Pool: &networkv1beta1.PoolSpec{
							MaxPoolSize:    0,
							PoolSyncPeriod: "10s",
						},
					},
					Status: networkv1beta1.NodeStatus{
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
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
				assert.Equal(t, 1, len(node.Status.NetworkInterfaces))
				assert.Equal(t, "InUse", node.Status.NetworkInterfaces["eni-2"].Status)
				assert.Equal(t, networkv1beta1.IPStatusValid, node.Status.NetworkInterfaces["eni-2"].IPv4["127.0.1.2"].Status)
				assert.Equal(t, networkv1beta1.IPStatusValid, node.Status.NetworkInterfaces["eni-2"].IPv6["fd00::1"].Status)
			},
		},
		{
			name: "idle ip reclaim - no reclaim policy",
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{EnableIPv4: true},
						Pool:    &networkv1beta1.PoolSpec{MaxPoolSize: 3, MinPoolSize: 1},
					},
					Status: networkv1beta1.NodeStatus{
						LastModifiedTime: metav1.NewTime(time.Now().Add(-time.Hour)), // old modification
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
							"eni-1": {
								ID:     "eni-1",
								Status: "InUse",
								IPv4: map[string]*networkv1beta1.IP{
									"192.168.0.1": {IP: "192.168.0.1", Status: networkv1beta1.IPStatusValid, Primary: true, PodID: "eni-primary"},
									"192.168.0.2": {IP: "192.168.0.2", Status: networkv1beta1.IPStatusValid, PodID: "pod-1"},
									"192.168.0.3": {IP: "192.168.0.3", Status: networkv1beta1.IPStatusValid}, // idle
									"192.168.0.4": {IP: "192.168.0.4", Status: networkv1beta1.IPStatusValid}, // idle
									"192.168.0.5": {IP: "192.168.0.5", Status: networkv1beta1.IPStatusValid}, // idle
									"192.168.0.6": {IP: "192.168.0.6", Status: networkv1beta1.IPStatusValid}, // idle
								},
							},
						},
					},
				},
			},
			wantErr: assert.NoError,
			checkFunc: func(t *testing.T, node *networkv1beta1.Node) {
				// Without reclaim policy, should use normal logic: idles(4) - maxPoolSize(3) = 1, so delete 1 IP
				deletingCount := 0
				validCount := 0
				for _, ip := range node.Status.NetworkInterfaces["eni-1"].IPv4 {
					if ip.Status == networkv1beta1.IPStatusDeleting {
						deletingCount++
					} else if ip.Status == networkv1beta1.IPStatusValid {
						validCount++
					}
				}
				assert.Equal(t, 1, deletingCount, "should delete exactly 1 IP")
				assert.Equal(t, 5, validCount, "should have 5 valid IPs (1 primary + 1 used + 3 remaining idle)")
			},
		},
		{
			name: "idle ip reclaim - with reclaim policy and extra deletion",
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{EnableIPv4: true},
						Pool: &networkv1beta1.PoolSpec{
							MaxPoolSize: 5,
							MinPoolSize: 2,
							Reclaim: &networkv1beta1.IPReclaimPolicy{
								After:     &metav1.Duration{Duration: 30 * time.Minute},
								Interval:  &metav1.Duration{Duration: 10 * time.Minute},
								BatchSize: 2,
							},
						},
					},
					Status: networkv1beta1.NodeStatus{
						LastModifiedTime:      metav1.NewTime(time.Now().Add(-45 * time.Minute)), // past 30min
						NextIdleIPReclaimTime: metav1.NewTime(time.Now().Add(-5 * time.Minute)),  // past reclaim time
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
							"eni-1": {
								ID:     "eni-1",
								Status: "InUse",
								IPv4: map[string]*networkv1beta1.IP{
									"192.168.0.1": {IP: "192.168.0.1", Status: networkv1beta1.IPStatusValid, Primary: true, PodID: "eni-primary"},
									"192.168.0.2": {IP: "192.168.0.2", Status: networkv1beta1.IPStatusValid, PodID: "pod-1"},
									"192.168.0.3": {IP: "192.168.0.3", Status: networkv1beta1.IPStatusValid}, // idle
									"192.168.0.4": {IP: "192.168.0.4", Status: networkv1beta1.IPStatusValid}, // idle
									"192.168.0.5": {IP: "192.168.0.5", Status: networkv1beta1.IPStatusValid}, // idle
									"192.168.0.6": {IP: "192.168.0.6", Status: networkv1beta1.IPStatusValid}, // idle
								},
							},
						},
					},
				},
			},
			wantErr: assert.NoError,
			checkFunc: func(t *testing.T, node *networkv1beta1.Node) {
				// With reclaim policy: normal toDel = idles(4) - maxPoolSize(5) = -1, max(0,-1)=0
				// Extra deletion: min(batchSize, maxPoolSize - minPoolSize) = min(2, 3) = 2
				// Total deletion: 0 + 2 = 2
				deletingCount := 0
				validCount := 0
				for _, ip := range node.Status.NetworkInterfaces["eni-1"].IPv4 {
					if ip.Status == networkv1beta1.IPStatusDeleting {
						deletingCount++
					} else if ip.Status == networkv1beta1.IPStatusValid {
						validCount++
					}
				}
				assert.Equal(t, 2, deletingCount, "should delete exactly 2 IPs due to reclaim policy")
				assert.Equal(t, 4, validCount, "should have 4 valid IPs (1 primary + 1 used + 2 remaining idle)")
				// NextIdleIPReclaimTime should be updated
				assert.True(t, node.Status.NextIdleIPReclaimTime.After(time.Now().Add(9*time.Minute)))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &ReconcileNode{
				tracer: noop.NewTracerProvider().Tracer(""),
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
					eniRef: &networkv1beta1.Nic{},
				},
			},
			checkFunc: func(t *testing.T, options []*eniOptions) {
				assert.Equal(t, []*eniOptions{
					{
						eniRef: &networkv1beta1.Nic{},
					},
				}, options)
			},
		},
		{
			name: "ip not enough error",
			options: []*eniOptions{
				{
					eniRef: &networkv1beta1.Nic{
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
					eniRef: &networkv1beta1.Nic{
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
					eniRef: &networkv1beta1.Nic{
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

func Test_calculateToDel(t *testing.T) {
	type args struct {
		ctx  context.Context
		node *networkv1beta1.Node
	}
	tests := []struct {
		name string
		args args
		want int
	}{
		{
			name: "no reclaim policy should use normal logic",
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{
							EnableIPv4: true,
						},
						Pool: &networkv1beta1.PoolSpec{
							MaxPoolSize: 5,
							MinPoolSize: 1,
						},
					},
					Status: networkv1beta1.NodeStatus{
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
							"eni-1": {
								Status: "InUse",
								IPv4: map[string]*networkv1beta1.IP{
									"192.168.0.1": {Status: networkv1beta1.IPStatusValid, Primary: true, PodID: "eni-primary"}, // primary
									"192.168.0.2": {Status: networkv1beta1.IPStatusValid, PodID: "pod-1"},                      // used
									"192.168.0.3": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.4": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.5": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.6": {Status: networkv1beta1.IPStatusValid},                                      // idle
								},
							},
						},
					},
				},
			},
			want: -1, // idles(4) - maxPoolSize(5) = -1, no reclaim policy returns raw toDel
		},
		{
			name: "with reclaim policy but not yet time",
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{
							EnableIPv4: true,
						},
						Pool: &networkv1beta1.PoolSpec{
							MaxPoolSize: 5,
							MinPoolSize: 1,
							Reclaim: &networkv1beta1.IPReclaimPolicy{
								After:     &metav1.Duration{Duration: 30 * time.Minute},
								Interval:  &metav1.Duration{Duration: 10 * time.Minute},
								BatchSize: 2,
							},
						},
					},
					Status: networkv1beta1.NodeStatus{
						LastModifiedTime: metav1.NewTime(time.Now().Add(-10 * time.Minute)), // not yet 30min
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
							"eni-1": {
								Status: "InUse",
								IPv4: map[string]*networkv1beta1.IP{
									"192.168.0.1": {Status: networkv1beta1.IPStatusValid, Primary: true, PodID: "eni-primary"}, // primary
									"192.168.0.2": {Status: networkv1beta1.IPStatusValid, PodID: "pod-1"},                      // used
									"192.168.0.3": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.4": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.5": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.6": {Status: networkv1beta1.IPStatusValid},                                      // idle
								},
							},
						},
					},
				},
			},
			want: -1, // not yet time, should return normal toDel = idles(4) - maxPoolSize(5) = -1
		},
		{
			name: "with reclaim policy and time reached",
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{
							EnableIPv4: true,
						},
						Pool: &networkv1beta1.PoolSpec{
							MaxPoolSize: 5,
							MinPoolSize: 1,
							Reclaim: &networkv1beta1.IPReclaimPolicy{
								After:     &metav1.Duration{Duration: 30 * time.Minute},
								Interval:  &metav1.Duration{Duration: 10 * time.Minute},
								BatchSize: 2,
							},
						},
					},
					Status: networkv1beta1.NodeStatus{
						LastModifiedTime:      metav1.NewTime(time.Now().Add(-40 * time.Minute)), // past 30min
						NextIdleIPReclaimTime: metav1.NewTime(time.Now().Add(-40 * time.Minute)),
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
							"eni-1": {
								Status: "InUse",
								IPv4: map[string]*networkv1beta1.IP{
									"192.168.0.1": {Status: networkv1beta1.IPStatusValid, Primary: true, PodID: "eni-primary"}, // primary
									"192.168.0.2": {Status: networkv1beta1.IPStatusValid, PodID: "pod-1"},                      // used
									"192.168.0.3": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.4": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.5": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.6": {Status: networkv1beta1.IPStatusValid},                                      // idle
								},
							},
						},
					},
				},
			},
			want: 2, // NextIdleIPReclaimTime not set yet, return normal toDel
		},
		{
			name: "with reclaim policy and batch size limit",
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{
							EnableIPv4: true,
						},
						Pool: &networkv1beta1.PoolSpec{
							MaxPoolSize: 10,
							MinPoolSize: 5,
							Reclaim: &networkv1beta1.IPReclaimPolicy{
								After:     &metav1.Duration{Duration: 30 * time.Minute},
								Interval:  &metav1.Duration{Duration: 10 * time.Minute},
								BatchSize: 3,
							},
						},
					},
					Status: networkv1beta1.NodeStatus{
						LastModifiedTime:      metav1.NewTime(time.Now().Add(-40 * time.Minute)),
						NextIdleIPReclaimTime: metav1.NewTime(time.Now().Add(-40 * time.Minute)),
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
							"eni-1": {
								Status: "InUse",
								IPv4: map[string]*networkv1beta1.IP{
									"192.168.0.1": {Status: networkv1beta1.IPStatusValid, Primary: true, PodID: "eni-primary"}, // primary
									"192.168.0.2": {Status: networkv1beta1.IPStatusValid, PodID: "pod-1"},                      // used
									"192.168.0.3": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.4": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.5": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.6": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.7": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.8": {Status: networkv1beta1.IPStatusValid},                                      // idle
								},
							},
						},
					},
				},
			},
			want: 1,
		},
		{
			name: "reclaim respects min pool size",
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{
							EnableIPv4: true,
						},
						Pool: &networkv1beta1.PoolSpec{
							MaxPoolSize: 10,
							MinPoolSize: 2,
							Reclaim: &networkv1beta1.IPReclaimPolicy{
								After:     &metav1.Duration{Duration: 30 * time.Minute},
								Interval:  &metav1.Duration{Duration: 10 * time.Minute},
								BatchSize: 5,
							},
						},
					},
					Status: networkv1beta1.NodeStatus{
						LastModifiedTime:      metav1.NewTime(time.Now().Add(-40 * time.Minute)),
						NextIdleIPReclaimTime: metav1.NewTime(time.Now().Add(-40 * time.Minute)),
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
							"eni-1": {
								Status: "InUse",
								IPv4: map[string]*networkv1beta1.IP{
									"192.168.0.1": {Status: networkv1beta1.IPStatusValid, Primary: true, PodID: "eni-primary"}, // primary
									"192.168.0.2": {Status: networkv1beta1.IPStatusValid, PodID: "pod-1"},                      // used
									"192.168.0.3": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.4": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.5": {Status: networkv1beta1.IPStatusValid},                                      // idle
								},
							},
						},
					},
				},
			},
			want: 1,
		},
		{
			name: "reclaim extra",
			args: args{
				ctx: MetaIntoCtx(context.TODO()),
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{
							EnableIPv4: true,
						},
						Pool: &networkv1beta1.PoolSpec{
							MaxPoolSize: 5,
							MinPoolSize: 2,
							Reclaim: &networkv1beta1.IPReclaimPolicy{
								After:     &metav1.Duration{Duration: 30 * time.Minute},
								Interval:  &metav1.Duration{Duration: 10 * time.Minute},
								BatchSize: 5,
							},
						},
					},
					Status: networkv1beta1.NodeStatus{
						LastModifiedTime:      metav1.NewTime(time.Now().Add(-40 * time.Minute)),
						NextIdleIPReclaimTime: metav1.NewTime(time.Now().Add(-40 * time.Minute)),
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
							"eni-1": {
								Status: "InUse",
								IPv4: map[string]*networkv1beta1.IP{
									"192.168.0.1": {Status: networkv1beta1.IPStatusValid, Primary: true, PodID: "eni-primary"}, // primary
									"192.168.0.2": {Status: networkv1beta1.IPStatusValid, PodID: "pod-1"},                      // used
									"192.168.0.3": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.4": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.5": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.6": {Status: networkv1beta1.IPStatusValid},                                      // idle
									"192.168.0.7": {Status: networkv1beta1.IPStatusValid},                                      // idle
								},
							},
						},
					},
				},
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateToDel(tt.args.ctx, tt.args.node)
			assert.Equal(t, tt.want, got)
		})
	}
}

var _ = Describe("Test ReconcileNode", func() {
	ctx := context.Background()

	name := "foo"
	typeNamespacedName := types.NamespacedName{
		Name: name,
	}

	var (
		openAPI    *mocks.OpenAPI
		vpcClient  *mocks.VPC
		ecsClient  *mocks.ECS
		switchPool *vswpool.SwitchPool
	)

	BeforeEach(func() {
		openAPI = mocks.NewOpenAPI(GinkgoT())
		vpcClient = mocks.NewVPC(GinkgoT())
		ecsClient = mocks.NewECS(GinkgoT())

		openAPI.On("GetVPC").Return(vpcClient).Maybe()
		openAPI.On("GetECS").Return(ecsClient).Maybe()
		var err error
		switchPool, err = vswpool.NewSwitchPool(100, "10m")
		Expect(err).NotTo(HaveOccurred())

		k8sNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name},
		}
		err = k8sClient.Create(ctx, k8sNode)
		Expect(err).NotTo(HaveOccurred())

		node := &networkv1beta1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					"alibabacloud.com/lingjun-worker": "true",
				},
			},
			Spec: networkv1beta1.NodeSpec{
				NodeMetadata: networkv1beta1.NodeMetadata{
					InstanceID:   "instanceID",
					InstanceType: "instanceType",
					RegionID:     "regionID",
					ZoneID:       "zone-1",
				},
				NodeCap: networkv1beta1.NodeCap{
					Adapters:              3,
					EriQuantity:           0,
					TotalAdapters:         2,
					IPv4PerAdapter:        10,
					IPv6PerAdapter:        0,
					MemberAdapterLimit:    0,
					MaxMemberAdapterLimit: 0,
					InstanceBandwidthRx:   0,
					InstanceBandwidthTx:   0,
				},
				ENISpec: &networkv1beta1.ENISpec{
					Tag:                 nil,
					TagFilter:           nil,
					VSwitchOptions:      []string{"vsw-1"},
					SecurityGroupIDs:    []string{"sg-1"},
					ResourceGroupID:     "",
					EnableIPv4:          true,
					EnableIPv6:          false,
					EnableERDMA:         false,
					EnableTrunk:         false,
					VSwitchSelectPolicy: "most",
				},
				Pool: &networkv1beta1.PoolSpec{
					MaxPoolSize: 0,
					MinPoolSize: 0,
				},
				Flavor: []networkv1beta1.Flavor{
					{
						NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
						NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
						Count:                       2,
					},
				},
			},
		}

		err = k8sClient.Create(ctx, node)
		Expect(err).NotTo(HaveOccurred())

		By("create 5 pedning pods")
		for i := 0; i < 5; i++ {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("pod-%d", i),
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test",
							Image: "foo",
						},
					},
					NodeName: name,
				},
			}
			err := k8sClient.Create(ctx, pod)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	AfterEach(func() {
		k8sNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name},
		}
		err := k8sClient.Delete(ctx, k8sNode)
		Expect(err).NotTo(HaveOccurred())

		node := &networkv1beta1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name},
		}
		err = k8sClient.Delete(ctx, node)
		Expect(err).NotTo(HaveOccurred())

		for i := 0; i < 5; i++ {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("pod-%d", i),
					Namespace: "default",
				},
			}
			err := k8sClient.Delete(ctx, pod, &client.DeleteOptions{
				GracePeriodSeconds: func() *int64 {
					i := int64(0)
					return &i
				}(),
				Preconditions:     nil,
				PropagationPolicy: nil,
				Raw:               nil,
				DryRun:            nil,
			})
			Expect(err).NotTo(HaveOccurred())
		}

		// wait for all pods to be deleted
		Eventually(func() error {
			pods := &corev1.PodList{}
			err := k8sClient.List(ctx, pods, client.MatchingFields{"spec.nodeName": name})
			if err != nil {
				return err
			}
			if len(pods.Items) == 0 {
				return nil
			}
			return fmt.Errorf("pods still exist")
		}).WithTimeout(10 * time.Second).Should(Succeed())
	})

	Context("Test attach failure - syncTaskQueueStatus with failed task", func() {
		It("Should mark ENI as Deleting when task queue reports failure", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Setup mock API
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, _, _ = mockHelper.GetMocks()
			openAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil).Maybe()
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{}, nil).Maybe()

			// Create reconciler with task queue
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				WithDefaults().
				Build()

			By("Setting up node with Attaching ENI")
			eniAttaching := BuildENIWithCustomIPs("eni-1", aliyunClient.ENIStatusAttaching, nil, nil)
			eniAttaching.NetworkInterfaceType = networkv1beta1.ENITypeSecondary

			node := NewNodeFactory("test-node").
				WithECS().
				WithInstanceID("i-test").
				WithExistingENIs(eniAttaching).
				Build()

			By("Injecting failed task into queue")
			now := time.Now()
			reconciler.eniTaskQueue.tasks["eni-1"] = &ENITaskRecord{
				ENIID:              "eni-1",
				Operation:          OpAttach,
				InstanceID:         "i-test",
				NodeName:           "test-node",
				Status:             TaskStatusFailed,
				CreatedAt:          now.Add(-1 * time.Minute),
				CompletedAt:        &now,
				RequestedIPv4Count: 5,
				RequestedIPv6Count: 0,
				ENIInfo:            nil,
				Error:              fmt.Errorf("attach timeout"),
			}

			By("Calling syncTaskQueueStatus to process failed task")
			reconciler.syncTaskQueueStatus(ctx, node)

			By("Verifying ENI status changed to Deleting")
			Expect(node.Status.NetworkInterfaces).To(HaveKey("eni-1"))
			Expect(node.Status.NetworkInterfaces["eni-1"].Status).To(Equal(aliyunClient.ENIStatusDeleting))
			Expect(node.Status.NetworkInterfaces["eni-1"].NetworkInterfaceType).To(Equal(networkv1beta1.ENITypeSecondary))

			By("Verifying status changed flag is set")
			Expect(MetaCtx(ctx).StatusChanged.Load()).To(BeTrue())
		})

		It("Should mark ENI as Deleting when task queue reports timeout", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Setup mock API
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, _, _ = mockHelper.GetMocks()
			openAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil).Maybe()
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{}, nil).Maybe()

			// Create reconciler with task queue
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				WithDefaults().
				Build()

			By("Setting up node with Attaching ENI")
			eniAttaching := BuildENIWithCustomIPs("eni-1", aliyunClient.ENIStatusAttaching, nil, nil)
			eniAttaching.NetworkInterfaceType = networkv1beta1.ENITypeSecondary

			node := NewNodeFactory("test-node").
				WithECS().
				WithInstanceID("i-test").
				WithExistingENIs(eniAttaching).
				Build()

			By("Injecting timeout task into queue")
			now := time.Now()
			reconciler.eniTaskQueue.tasks["eni-1"] = &ENITaskRecord{
				ENIID:              "eni-1",
				Operation:          OpAttach,
				InstanceID:         "i-test",
				NodeName:           "test-node",
				Status:             TaskStatusTimeout,
				CreatedAt:          now.Add(-5 * time.Minute),
				CompletedAt:        &now,
				RequestedIPv4Count: 3,
				RequestedIPv6Count: 0,
				ENIInfo:            nil,
				Error:              fmt.Errorf("attach timeout after 5m"),
			}

			By("Calling syncTaskQueueStatus to process timeout task")
			reconciler.syncTaskQueueStatus(ctx, node)

			By("Verifying ENI status changed to Deleting")
			Expect(node.Status.NetworkInterfaces).To(HaveKey("eni-1"))
			Expect(node.Status.NetworkInterfaces["eni-1"].Status).To(Equal(aliyunClient.ENIStatusDeleting))
		})
	})

	Context("Test attach success - syncTaskQueueStatus with completed task", func() {
		It("Should update ENI status and info when task queue reports completion", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Setup mock API
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, _, _ = mockHelper.GetMocks()
			openAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil).Maybe()
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{}, nil).Maybe()

			// Create reconciler with task queue
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				WithDefaults().
				Build()

			By("Setting up node with Attaching ENI")
			eniAttaching := BuildENIWithCustomIPs("eni-1", aliyunClient.ENIStatusAttaching, nil, nil)
			eniAttaching.NetworkInterfaceType = networkv1beta1.ENITypeSecondary

			node := NewNodeFactory("test-node").
				WithECS().
				WithInstanceID("i-test").
				WithExistingENIs(eniAttaching).
				Build()

			By("Creating completed task with ENI info")
			now := time.Now()
			eniInfo := BuildMockENI("eni-1", aliyunClient.ENITypeSecondary, aliyunClient.ENIStatusInUse,
				"vsw-1", "zone-1", []string{"10.0.0.1", "10.0.0.2"}, []string{"2001:db8::1"})
			eniInfo.MacAddress = "aa:bb:cc:dd:ee:ff"
			eniInfo.SecurityGroupIDs = []string{"sg-1", "sg-2"}
			eniInfo.NetworkInterfaceTrafficMode = "Standard"

			reconciler.eniTaskQueue.tasks["eni-1"] = &ENITaskRecord{
				ENIID:              "eni-1",
				Operation:          OpAttach,
				InstanceID:         "i-test",
				NodeName:           "test-node",
				Status:             TaskStatusCompleted,
				CreatedAt:          now.Add(-1 * time.Minute),
				CompletedAt:        &now,
				RequestedIPv4Count: 2,
				RequestedIPv6Count: 1,
				ENIInfo:            eniInfo,
				Error:              nil,
			}

			By("Calling syncTaskQueueStatus to process completed task")
			reconciler.syncTaskQueueStatus(ctx, node)

			By("Verifying ENI status updated to InUse")
			Expect(node.Status.NetworkInterfaces).To(HaveKey("eni-1"))
			nic := node.Status.NetworkInterfaces["eni-1"]
			Expect(nic.Status).To(Equal(aliyunClient.ENIStatusInUse))
			Expect(nic.MacAddress).To(Equal("aa:bb:cc:dd:ee:ff"))
			Expect(nic.SecurityGroupIDs).To(Equal([]string{"sg-1", "sg-2"}))
			Expect(nic.PrimaryIPAddress).To(Equal("10.0.0.1"))
			Expect(nic.NetworkInterfaceTrafficMode).To(Equal(networkv1beta1.NetworkInterfaceTrafficMode("Standard")))

			By("Verifying IPv4 addresses are converted correctly")
			Expect(nic.IPv4).To(HaveLen(2))
			Expect(nic.IPv4).To(HaveKey("10.0.0.1"))
			Expect(nic.IPv4).To(HaveKey("10.0.0.2"))
			Expect(nic.IPv4["10.0.0.1"].IP).To(Equal("10.0.0.1"))
			Expect(nic.IPv4["10.0.0.1"].Primary).To(BeTrue())

			By("Verifying IPv6 addresses are converted correctly")
			Expect(nic.IPv6).To(HaveLen(1))
			Expect(nic.IPv6).To(HaveKey("2001:db8::1"))
			Expect(nic.IPv6["2001:db8::1"].IP).To(Equal("2001:db8::1"))

			By("Verifying status changed flag is set")
			Expect(MetaCtx(ctx).StatusChanged.Load()).To(BeTrue())
		})

		It("Should mark ENI as Deleting when completed task has nil ENIInfo", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Setup mock API
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, _, _ = mockHelper.GetMocks()
			openAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil).Maybe()
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{}, nil).Maybe()

			// Create reconciler with task queue
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				WithDefaults().
				Build()

			By("Setting up node with Attaching ENI")
			eniAttaching := BuildENIWithCustomIPs("eni-1", aliyunClient.ENIStatusAttaching, nil, nil)
			eniAttaching.NetworkInterfaceType = networkv1beta1.ENITypeSecondary

			node := NewNodeFactory("test-node").
				WithECS().
				WithInstanceID("i-test").
				WithExistingENIs(eniAttaching).
				Build()

			By("Injecting completed task with nil ENIInfo")
			now := time.Now()
			reconciler.eniTaskQueue.tasks["eni-1"] = &ENITaskRecord{
				ENIID:              "eni-1",
				Operation:          OpAttach,
				InstanceID:         "i-test",
				NodeName:           "test-node",
				Status:             TaskStatusCompleted,
				CreatedAt:          now.Add(-1 * time.Minute),
				CompletedAt:        &now,
				RequestedIPv4Count: 2,
				RequestedIPv6Count: 0,
				ENIInfo:            nil,
				Error:              nil,
			}

			By("Calling syncTaskQueueStatus to process completed task with nil ENIInfo")
			reconciler.syncTaskQueueStatus(ctx, node)

			By("Verifying ENI status changed to Deleting when ENIInfo is nil")
			Expect(node.Status.NetworkInterfaces).To(HaveKey("eni-1"))
			Expect(node.Status.NetworkInterfaces["eni-1"].Status).To(Equal(aliyunClient.ENIStatusDeleting))
		})

		It("Should track warm-up allocations when warm-up is not completed", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Setup mock API
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, _, _ = mockHelper.GetMocks()
			openAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil).Maybe()
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{}, nil).Maybe()

			// Create reconciler with task queue
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				WithDefaults().
				Build()

			By("Setting up node with Attaching ENI and warm-up target")
			eniAttaching := BuildENIWithCustomIPs("eni-1", aliyunClient.ENIStatusAttaching, nil, nil)
			eniAttaching.NetworkInterfaceType = networkv1beta1.ENITypeSecondary

			node := NewNodeFactory("test-node").
				WithECS().
				WithInstanceID("i-test").
				WithExistingENIs(eniAttaching).
				Build()
			node.Status.WarmUpCompleted = false
			node.Status.WarmUpTarget = 10
			node.Status.WarmUpAllocatedCount = 3

			By("Creating completed task with ENI info")
			now := time.Now()
			eniInfo := BuildMockENI("eni-1", aliyunClient.ENITypeSecondary, aliyunClient.ENIStatusInUse,
				"vsw-1", "zone-1", []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}, []string{"2001:db8::1", "2001:db8::2"})

			reconciler.eniTaskQueue.tasks["eni-1"] = &ENITaskRecord{
				ENIID:              "eni-1",
				Operation:          OpAttach,
				InstanceID:         "i-test",
				NodeName:           "test-node",
				Status:             TaskStatusCompleted,
				CreatedAt:          now.Add(-1 * time.Minute),
				CompletedAt:        &now,
				RequestedIPv4Count: 3,
				RequestedIPv6Count: 2,
				ENIInfo:            eniInfo,
				Error:              nil,
			}

			By("Calling syncTaskQueueStatus to process completed task")
			reconciler.syncTaskQueueStatus(ctx, node)

			By("Verifying warm-up allocated count is updated")
			// max(3 IPv4, 2 IPv6) = 3, so WarmUpAllocatedCount should be 3 + 3 = 6
			Expect(node.Status.WarmUpAllocatedCount).To(Equal(6))
		})

		It("Should not track warm-up allocations when warm-up is completed", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Setup mock API
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, _, _ = mockHelper.GetMocks()
			openAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil).Maybe()
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{}, nil).Maybe()

			// Create reconciler with task queue
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				WithDefaults().
				Build()

			By("Setting up node with Attaching ENI and warm-up completed")
			eniAttaching := BuildENIWithCustomIPs("eni-1", aliyunClient.ENIStatusAttaching, nil, nil)
			eniAttaching.NetworkInterfaceType = networkv1beta1.ENITypeSecondary

			node := NewNodeFactory("test-node").
				WithECS().
				WithInstanceID("i-test").
				WithExistingENIs(eniAttaching).
				Build()
			node.Status.WarmUpCompleted = true
			node.Status.WarmUpTarget = 10
			node.Status.WarmUpAllocatedCount = 5

			By("Creating completed task with ENI info")
			now := time.Now()
			eniInfo := BuildMockENI("eni-1", aliyunClient.ENITypeSecondary, aliyunClient.ENIStatusInUse,
				"vsw-1", "zone-1", []string{"10.0.0.1", "10.0.0.2"}, []string{"2001:db8::1"})

			reconciler.eniTaskQueue.tasks["eni-1"] = &ENITaskRecord{
				ENIID:              "eni-1",
				Operation:          OpAttach,
				InstanceID:         "i-test",
				NodeName:           "test-node",
				Status:             TaskStatusCompleted,
				CreatedAt:          now.Add(-1 * time.Minute),
				CompletedAt:        &now,
				RequestedIPv4Count: 2,
				RequestedIPv6Count: 1,
				ENIInfo:            eniInfo,
				Error:              nil,
			}

			By("Calling syncTaskQueueStatus to process completed task")
			reconciler.syncTaskQueueStatus(ctx, node)

			By("Verifying warm-up allocated count is not updated when warm-up is completed")
			Expect(node.Status.WarmUpAllocatedCount).To(Equal(5))
		})
	})

	Context("Test assign err", func() {
		It("Test assign err", func() {

			instanceID := "instanceID"
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, &aliyunClient.DescribeNetworkInterfaceOptions{
				InstanceID: &instanceID,
			}).Return([]*aliyunClient.NetworkInterface{
				{
					Status:             "InUse",
					MacAddress:         "",
					NetworkInterfaceID: "eni-1",
					VPCID:              "",
					VSwitchID:          "vsw-1",
					PrivateIPAddress:   "127.0.0.1",
					PrivateIPSets: []aliyunClient.IPSet{
						{
							Primary:   true,
							IPAddress: "127.0.0.1",
							IPName:    "",
							IPStatus:  "",
						},
					},
					ZoneID:                      "",
					SecurityGroupIDs:            nil,
					ResourceGroupID:             "",
					IPv6Set:                     nil,
					Tags:                        nil,
					Type:                        "Secondary",
					InstanceID:                  "",
					NetworkInterfaceTrafficMode: "Standard",
				},
				{
					Status:             "InUse",
					MacAddress:         "",
					NetworkInterfaceID: "eni-2",
					VPCID:              "",
					VSwitchID:          "vsw-1",
					PrivateIPAddress:   "127.0.0.2",
					PrivateIPSets: []aliyunClient.IPSet{
						{
							Primary:   true,
							IPAddress: "127.0.0.2",
							IPName:    "",
							IPStatus:  "",
						},
					},
					ZoneID:                      "",
					SecurityGroupIDs:            nil,
					ResourceGroupID:             "",
					IPv6Set:                     nil,
					Tags:                        nil,
					Type:                        "Secondary",
					InstanceID:                  "",
					NetworkInterfaceTrafficMode: "Standard",
				},
			}, nil).Maybe()
			vpcClient.On("DescribeVSwitchByID", mock.Anything, "vsw-1").Return(&vpc.VSwitch{
				VSwitchId:               "vsw-1",
				ZoneId:                  "zone-1",
				AvailableIpAddressCount: 10,
				CidrBlock:               "172.0.0.0/16",
				Ipv6CidrBlock:           "fd00::/64",
			}, nil).Maybe()

			bo1 := backoff.Backoff(backoff.ENIIPOps)
			bo2 := backoff.Backoff(backoff.ENIIPOps)
			openAPI.On("AssignPrivateIPAddressV2", mock.Anything, &aliyunClient.AssignPrivateIPAddressOptions{
				NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-1",
					IPCount:            1,
				},
				Backoff: &bo1.Backoff,
			}).Return([]aliyunClient.IPSet{
				{
					Primary:   false,
					IPAddress: "127.0.0.3",
					IPName:    "ip-127.0.0.3",
					IPStatus:  "Available",
				},
			}, nil).Once()
			openAPI.On("AssignPrivateIPAddressV2", mock.Anything, &aliyunClient.AssignPrivateIPAddressOptions{
				NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
					NetworkInterfaceID: "eni-2",
					IPCount:            1,
				},
				Backoff: &bo2.Backoff,
			}).Return([]aliyunClient.IPSet{
				{
					Primary:   false,
					IPAddress: "127.0.0.4",
					IPName:    "ip-127.0.0.4",
					IPStatus:  "Available",
				},
			}, nil).Once()

			By("reconcile")
			controllerReconciler := NewReconcilerBuilder().
				WithClient(k8sClient).
				WithScheme(k8sClient.Scheme()).
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				WithDefaults().
				Build()

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("check this cr")
			node := &networkv1beta1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: name},
			}
			err = k8sClient.Get(ctx, typeNamespacedName, node)
			Expect(err).NotTo(HaveOccurred())

			Expect(len(node.Status.NetworkInterfaces)).To(Equal(2))

			By("eni status should be InUse")
			Expect(node.Status.NetworkInterfaces["eni-1"].Status).To(Equal("InUse"))
			Expect(node.Status.NetworkInterfaces["eni-2"].Status).To(Equal("InUse"))

			Expect(node.Status.NetworkInterfaces["eni-1"].NetworkInterfaceType).To(Equal(networkv1beta1.ENITypeSecondary))
			Expect(node.Status.NetworkInterfaces["eni-2"].NetworkInterfaceType).To(Equal(networkv1beta1.ENITypeSecondary))

			By("check pods ip should allocated")
			Expect(node.Status.NetworkInterfaces["eni-1"].IPv4["127.0.0.1"].Primary).To(Equal(true))
			Expect(node.Status.NetworkInterfaces["eni-1"].IPv4["127.0.0.1"].PodID).To(Not(BeEmpty()))
			Expect(node.Status.NetworkInterfaces["eni-2"].IPv4["127.0.0.2"].Primary).To(Equal(true))
			Expect(node.Status.NetworkInterfaces["eni-2"].IPv4["127.0.0.2"].PodID).To(Not(BeEmpty()))

			Expect(node.Status.NetworkInterfaces["eni-1"].IPv4["127.0.0.3"].PodID).To(Not(BeEmpty()))
			Expect(node.Status.NetworkInterfaces["eni-2"].IPv4["127.0.0.4"].PodID).To(Not(BeEmpty()))
		})
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

	Context("Test syncWithAPI", func() {
		It("Should sync network interfaces from API correctly", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)
			MetaCtx(ctx).NeedSyncOpenAPI.Store(true)

			// Setup mock API
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, vpcClient, ecsClient = mockHelper.GetMocks()

			// Setup vSwitch
			mockHelper.SetupVSwitch("vsw-1", &vpc.VSwitch{
				VSwitchId:               "vsw-1",
				ZoneId:                  "zone-1",
				AvailableIpAddressCount: 10,
				CidrBlock:               "192.168.0.0/16",
				Ipv6CidrBlock:           "fd00::/64",
			})

			// Setup API to return ENIs: Primary (ignored), 2 Secondary ENIs
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
				// Primary ENI - should be filtered out
				BuildMockENI("eni-1", "Primary", "InUse", "", "", nil, nil),
				// Secondary ENI without IPs
				BuildMockENI("eni-2", "Secondary", "InUse", "vsw-1", "zone-1", nil, nil),
				// Secondary ENI with IPs
				BuildMockENI("eni-3", "Secondary", "InUse", "vsw-1", "zone-1",
					[]string{"192.168.0.1", "192.168.0.2"}, nil),
			}, nil).Maybe()

			// Create node with existing ENIs (eni-3 and eni-4)
			// eni-4 should be removed as it's not in the API response
			eni3 := BuildENIWithCustomIPs("eni-3", aliyunClient.ENIStatusInUse,
				map[string]*networkv1beta1.IP{
					"192.168.0.1": {IP: "192.168.0.1", Primary: true},
				},
				map[string]*networkv1beta1.IP{
					"fd00::1": {IP: "fd00::1"},
				},
			)
			eni4 := BuildENIWithCustomIPs("eni-4", aliyunClient.ENIStatusInUse,
				map[string]*networkv1beta1.IP{
					"192.168.0.1": {IP: "192.168.0.1", Primary: true},
				},
				map[string]*networkv1beta1.IP{
					"fd00::1": {IP: "fd00::1"},
				},
			)

			// Use NodeFactory with existing ENIs
			node := NewNodeFactory("foo").
				WithECS().
				WithInstanceID("test-instance").
				WithExistingENIs(eni3, eni4).
				Build()

			// Use ReconcilerBuilder to create reconciler
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				WithSyncPeriod(time.Hour).
				WithDefaults().
				Build()

			By("Syncing network interfaces with API")
			err := reconciler.syncWithAPI(ctx, node)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying network interfaces were updated correctly")
			Expect(node.Status.NetworkInterfaces).To(HaveLen(2))
			Expect(node.Status.NetworkInterfaces["eni-2"].IPv4CIDR).To(Equal("192.168.0.0/16"))
			Expect(node.Status.NetworkInterfaces["eni-3"].IPv4).To(HaveLen(2))
			Expect(node.Status.NetworkInterfaces["eni-4"]).To(BeNil())
		})

		It("Should not sync openAPI if degradation", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)
			MetaCtx(ctx).NeedSyncOpenAPI.Store(true)

			openAPI.On("GetVPC").Return(vpcClient).Maybe()

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
					NetworkInterfaces: map[string]*networkv1beta1.Nic{
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
			v := viper.New()
			v.Set("degradation", "l1")
			reconciler := &ReconcileNode{
				aliyun:             openAPI,
				vswpool:            switchPool,
				fullSyncNodePeriod: time.Hour,
				tracer:             noop.NewTracerProvider().Tracer(""),
				v:                  v,
			}

			By("Syncing network interfaces with API")
			err := reconciler.syncWithAPI(ctx, node)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Test createENI with async queue", func() {
		It("Should create ENI and submit async attach task", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Use NodeFactory to create test node
			node := NewNodeFactory("test-node").
				WithECS().
				WithInstanceID("i-1").
				WithZone("cn-hangzhou-k").
				WithVSwitches("vsw-1").
				WithTags(map[string]string{"k1": "v1"}).
				Build()

			// Use MockAPIHelper to setup API expectations
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, _, _ = mockHelper.GetMocks()

			// Setup the ENI creation flow - only CreateNetworkInterfaceV2 is called synchronously
			mockHelper.SetupCreateENI("eni-1", aliyunClient.ENITypeSecondary,
				WithMacAddress("00:00:00:00:00:01"),
				WithIPv4("192.168.0.1"),
			)

			// AttachNetworkInterfaceV2 and DescribeNetworkInterfaceV2 are used by async queue
			openAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil).Maybe()
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
				BuildMockENI("eni-1", aliyunClient.ENITypeSecondary, aliyunClient.ENIStatusInUse,
					"vsw-1", "cn-hangzhou-k", []string{"192.168.0.1"}, nil),
			}, nil).Maybe()

			// Setup vSwitch in pool
			switchPool.Add(&vswpool.Switch{
				ID:               "vsw-1",
				Zone:             "cn-hangzhou-k",
				AvailableIPCount: 100,
				IPv4CIDR:         "192.168.0.0/16",
				IPv6CIDR:         "fd00::/64",
			})

			// Use ReconcilerBuilder to create reconciler
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				WithENIBatchSize(10).
				WithDefaults().
				Build()

			By("Creating ENI")
			opt := &eniOptions{
				eniTypeKey: secondaryKey,
				addIPv4N:   5,
				addIPv6N:   0,
			}

			err := reconciler.createENI(ctx, node, opt)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying ENI is in Attaching status in node CRD")
			Expect(node.Status.NetworkInterfaces).To(HaveKey("eni-1"))
			eni := node.Status.NetworkInterfaces["eni-1"]
			Expect(eni.ID).To(Equal("eni-1"))
			Expect(eni.Status).To(Equal(aliyunClient.ENIStatusAttaching))
			Expect(eni.VSwitchID).To(Equal("vsw-1"))
			Expect(eni.NetworkInterfaceType).To(Equal(networkv1beta1.ENITypeSecondary))

			By("Verifying task was submitted to queue")
			task, ok := reconciler.eniTaskQueue.GetTaskStatus("eni-1")
			Expect(ok).To(BeTrue(), "Task should be in queue")
			Expect(task.ENIID).To(Equal("eni-1"))
			Expect(task.InstanceID).To(Equal("i-1"))
			Expect(task.NodeName).To(Equal("test-node"))
			Expect(task.RequestedIPv4Count).To(Equal(5))
			Expect(task.RequestedIPv6Count).To(Equal(0))

			By("Verifying status changed flag is set")
			Expect(MetaCtx(ctx).StatusChanged.Load()).To(BeTrue())
		})

		It("Should handle ENI creation API failure", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Use NodeFactory to create test node
			node := NewNodeFactory("test-node").
				WithECS().
				WithInstanceID("i-1").
				WithZone("cn-hangzhou-k").
				WithVSwitches("vsw-1").
				WithTags(map[string]string{"k1": "v1"}).
				Build()

			// Setup mock to return error
			createErr := fmt.Errorf("create eni failed")
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, _, _ = mockHelper.GetMocks()
			mockHelper.SetupCreateENIWithError(createErr)

			// Setup vSwitch in pool
			switchPool.Add(&vswpool.Switch{
				ID:               "vsw-1",
				Zone:             "cn-hangzhou-k",
				AvailableIPCount: 100,
				IPv4CIDR:         "192.168.0.0/16",
				IPv6CIDR:         "fd00::/64",
			})

			// Use ReconcilerBuilder to create reconciler
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				WithENIBatchSize(10).
				WithDefaults().
				Build()

			By("Creating ENI with expected failure")
			opt := &eniOptions{
				eniTypeKey: secondaryKey,
				addIPv4N:   0,
				addIPv6N:   0,
			}

			err := reconciler.createENI(ctx, node, opt)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("create eni failed"))

			By("Verifying the ENI was not added to node status")
			Expect(node.Status.NetworkInterfaces).To(BeEmpty())

			By("Verifying no task was submitted to queue")
			_, ok := reconciler.eniTaskQueue.GetTaskStatus("eni-1")
			Expect(ok).To(BeFalse(), "No task should be in queue after creation failure")
		})

		It("Should handle VSwitchID not found error", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Use NodeFactory to create test node with no vSwitch options
			node := NewNodeFactory("test-node").
				WithECS().
				WithInstanceID("i-1").
				WithZone("cn-hangzhou-k").
				WithVSwitches(). // Empty vSwitch options
				WithTags(map[string]string{"k1": "v1"}).
				Build()

			// Use ReconcilerBuilder to create reconciler
			// Note: Not adding any vSwitch to switchPool, so GetOne will fail
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, _, _ = mockHelper.GetMocks()

			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				WithENIBatchSize(10).
				WithDefaults().
				Build()

			By("Creating ENI with no available VSwitches")
			opt := &eniOptions{
				eniTypeKey: secondaryKey,
				addIPv4N:   0,
				addIPv6N:   0,
			}

			err := reconciler.createENI(ctx, node, opt)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, vswpool.ErrNoAvailableVSwitch)).To(BeTrue())

			By("Verifying the ENI was not added to node status")
			Expect(node.Status.NetworkInterfaces).To(BeEmpty())
		})
	})

	Context("Test ensureAsyncTasks - Recovery on Restart", func() {
		It("Should submit recovery task for Attaching ENI after restart", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Simulate restart scenario: Node CR has Attaching ENI but task queue is empty
			eniAttaching := BuildENIWithCustomIPs("eni-restart-1", aliyunClient.ENIStatusAttaching, nil, nil)
			eniInUse := BuildENIWithCustomIPs("eni-inuse", aliyunClient.ENIStatusInUse, nil, nil)

			node := NewNodeFactory("test-node").
				WithECS().
				WithInstanceID("i-test").
				WithExistingENIs(eniAttaching, eniInUse).
				Build()

			// Setup mock API - ECS attach is idempotent, won't fail on retry
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, _, _ = mockHelper.GetMocks()
			openAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil).Maybe()
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
				{
					NetworkInterfaceID: "eni-restart-1",
					Status:             aliyunClient.ENIStatusInUse,
				},
			}, nil).Maybe()

			// Create reconciler with task queue
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				WithDefaults().
				Build()

			// Verify task queue is empty initially
			Expect(reconciler.eniTaskQueue.HasPendingTasks("test-node")).To(BeFalse())

			By("Calling ensureAsyncTasks to trigger recovery")
			reconciler.ensureAsyncTasks(ctx, node)

			By("Verifying recovery task was submitted for Attaching ENI")
			task, ok := reconciler.eniTaskQueue.GetTaskStatus("eni-restart-1")
			Expect(ok).To(BeTrue(), "Recovery task should be submitted")
			Expect(task.ENIID).To(Equal("eni-restart-1"))
			Expect(task.InstanceID).To(Equal("i-test"))

			By("Verifying no task was submitted for InUse ENI")
			_, ok = reconciler.eniTaskQueue.GetTaskStatus("eni-inuse")
			Expect(ok).To(BeFalse(), "No task should be submitted for InUse ENI")
		})

		It("Should not submit duplicate task if task already exists", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Setup mock API
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, _, _ = mockHelper.GetMocks()
			openAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil).Maybe()
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
				{
					NetworkInterfaceID: "eni-existing",
					Status:             aliyunClient.ENIStatusInUse,
				},
			}, nil).Maybe()

			// Create reconciler with task queue
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				WithDefaults().
				Build()

			// Pre-add a task to the queue
			reconciler.eniTaskQueue.SubmitAttach(ctx, "eni-existing", "i-test", "", "test-node", 5, 0)

			// Wait for task to be added
			task, ok := reconciler.eniTaskQueue.GetTaskStatus("eni-existing")
			Expect(ok).To(BeTrue())
			originalIPv4Count := task.RequestedIPv4Count

			// Create node with Attaching ENI
			eniAttaching := BuildENIWithCustomIPs("eni-existing", aliyunClient.ENIStatusAttaching, nil, nil)
			node := NewNodeFactory("test-node").
				WithECS().
				WithInstanceID("i-test").
				WithExistingENIs(eniAttaching).
				Build()

			By("Calling ensureAsyncTasks - should not submit duplicate")
			reconciler.ensureAsyncTasks(ctx, node)

			By("Verifying task was not overwritten")
			task, ok = reconciler.eniTaskQueue.GetTaskStatus("eni-existing")
			Expect(ok).To(BeTrue())
			// The original task should not be replaced - IP count should remain the same
			Expect(task.RequestedIPv4Count).To(Equal(originalIPv4Count))
		})

		It("Should handle multiple Attaching ENIs on restart", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Create multiple Attaching ENIs
			eni1 := BuildENIWithCustomIPs("eni-multi-1", aliyunClient.ENIStatusAttaching, nil, nil)
			eni2 := BuildENIWithCustomIPs("eni-multi-2", aliyunClient.ENIStatusAttaching, nil, nil)
			eni3 := BuildENIWithCustomIPs("eni-multi-3", aliyunClient.ENIStatusAttaching, nil, nil)

			node := NewNodeFactory("test-node").
				WithECS().
				WithInstanceID("i-test").
				WithExistingENIs(eni1, eni2, eni3).
				Build()

			// Setup mock API
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, _, _ = mockHelper.GetMocks()
			openAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil).Maybe()
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
				{NetworkInterfaceID: "eni-multi-1", Status: aliyunClient.ENIStatusInUse},
			}, nil).Maybe()

			// Create reconciler
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				WithDefaults().
				Build()

			By("Calling ensureAsyncTasks")
			reconciler.ensureAsyncTasks(ctx, node)

			By("Verifying all Attaching ENIs got recovery tasks")
			for _, eniID := range []string{"eni-multi-1", "eni-multi-2", "eni-multi-3"} {
				task, ok := reconciler.eniTaskQueue.GetTaskStatus(eniID)
				Expect(ok).To(BeTrue(), "Recovery task should be submitted for %s", eniID)
				Expect(task.InstanceID).To(Equal("i-test"))
			}
		})
	})

	Context("Test handleStatus", func() {
		It("Should handle ENI status correctly", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Create ENI-1 with mixed IP statuses (some deleting, some valid)
			eni1 := BuildENIWithCustomIPs("eni-1", aliyunClient.ENIStatusInUse,
				map[string]*networkv1beta1.IP{
					"192.168.0.1": {
						IP:     "192.168.0.1",
						Status: networkv1beta1.IPStatusDeleting,
					},
					"192.168.0.2": {
						IP:     "192.168.0.2",
						Status: networkv1beta1.IPStatusValid,
					},
				},
				map[string]*networkv1beta1.IP{
					"fd00::1": {
						IP:     "fd00::1",
						Status: networkv1beta1.IPStatusDeleting,
					},
					"fd00::2": {
						IP:     "fd00::2",
						Status: networkv1beta1.IPStatusValid,
					},
				},
			)

			// Create ENI-2 and ENI-3 with deleting/detaching status
			eni2 := BuildENIWithCustomIPs("eni-2", aliyunClient.ENIStatusDeleting, nil, nil)
			eni3 := BuildENIWithCustomIPs("eni-3", aliyunClient.ENIStatusDetaching, nil, nil)

			// Use NodeFactory with existing ENIs
			node := NewNodeFactory("test-node").
				WithECS().
				WithExistingENIs(eni1, eni2, eni3).
				Build()

			// Setup mocks using MockAPIHelper
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, vpcClient, ecsClient = mockHelper.GetMocks()

			// Setup IP unassignment for eni-1
			mockHelper.SetupUnassignIP("eni-1", nil)
			mockHelper.SetupUnassignIPv6("eni-1", nil)

			// Setup ENI deletion for eni-2
			ecsClient.On("DetachNetworkInterface", mock.Anything, "eni-2", mock.Anything, mock.Anything).Return(nil)
			mockHelper.SetupWaitForENI("eni-2", aliyunClient.ENIStatusAvailable,
				&aliyunClient.NetworkInterface{
					NetworkInterfaceID: "eni-2",
					Status:             aliyunClient.ENIStatusAvailable,
				}, nil)
			mockHelper.SetupDeleteENI("eni-2", nil)

			// Setup ENI deletion for eni-3
			ecsClient.On("DetachNetworkInterface", mock.Anything, "eni-3", mock.Anything, mock.Anything).Return(nil)
			mockHelper.SetupWaitForENI("eni-3", aliyunClient.ENIStatusAvailable,
				&aliyunClient.NetworkInterface{
					NetworkInterfaceID: "eni-3",
					Status:             aliyunClient.ENIStatusAvailable,
				}, nil)
			mockHelper.SetupDeleteENI("eni-3", nil)

			// Setup DescribeNetworkInterface to verify IP removal
			openAPI.On("DescribeNetworkInterfaceV2", mock.Anything, mock.Anything).Return([]*aliyunClient.NetworkInterface{
				{
					NetworkInterfaceID: "eni-1",
					Status:             aliyunClient.ENIStatusInUse,
					PrivateIPSets: []aliyunClient.IPSet{
						{IPAddress: "192.168.0.2"},
					},
					IPv6Set: []aliyunClient.IPSet{
						{IPAddress: "fd00::2"},
					},
				},
			}, nil)

			// Use ReconcilerBuilder to create reconciler
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithDefaults().
				Build()

			By("Processing network interfaces with different statuses")
			err := reconciler.handleStatus(ctx, node)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying ENIs were handled correctly")
			Expect(node.Status.NetworkInterfaces).To(HaveKey("eni-1"))
			Expect(node.Status.NetworkInterfaces).NotTo(HaveKey("eni-2"))
			Expect(node.Status.NetworkInterfaces).NotTo(HaveKey("eni-3"))

			By("Verifying IP status was handled correctly")
			eni1Result := node.Status.NetworkInterfaces["eni-1"]
			Expect(eni1Result.IPv4).To(HaveLen(1))
			Expect(eni1Result.IPv4).To(HaveKey("192.168.0.2"))
			Expect(eni1Result.IPv4).NotTo(HaveKey("192.168.0.1"))
			Expect(eni1Result.IPv6).To(HaveLen(1))
			Expect(eni1Result.IPv6).To(HaveKey("fd00::2"))
			Expect(eni1Result.IPv6).NotTo(HaveKey("fd00::1"))
		})

		It("Should handle errors during ENI deletion", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Create ENI with deleting status
			eni1 := BuildENIWithCustomIPs("eni-1", aliyunClient.ENIStatusDeleting, nil, nil)

			// Use NodeFactory with existing ENI
			node := NewNodeFactory("test-node").
				WithECS().
				WithExistingENI(eni1).
				Build()

			// Setup mock to return error on detach
			detachErr := fmt.Errorf("failed to detach ENI")
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, vpcClient, ecsClient = mockHelper.GetMocks()
			ecsClient.On("DetachNetworkInterface", mock.Anything, "eni-1", mock.Anything, mock.Anything).Return(detachErr)

			// Use ReconcilerBuilder to create reconciler
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithDefaults().
				Build()

			By("Attempting to process ENI with API error(will retry)")
			err := reconciler.handleStatus(ctx, node)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying ENI was not removed from node status despite error")
			Expect(node.Status.NetworkInterfaces).To(HaveKey("eni-1"))
		})

		It("Should handle IP deletion errors", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Create ENI with one IPv4 address in deleting status
			eni1 := BuildENIWithCustomIPs("eni-1", aliyunClient.ENIStatusInUse,
				map[string]*networkv1beta1.IP{
					"192.168.0.1": {
						IP:     "192.168.0.1",
						Status: networkv1beta1.IPStatusDeleting,
					},
				},
				map[string]*networkv1beta1.IP{},
			)

			// Use NodeFactory with existing ENI
			node := NewNodeFactory("test-node").
				WithECS().
				WithExistingENI(eni1).
				Build()

			// Setup mock to return error on IP unassignment
			ipDeleteErr := fmt.Errorf("failed to unassign private IP")
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, vpcClient, ecsClient = mockHelper.GetMocks()
			mockHelper.SetupUnassignIP("eni-1", ipDeleteErr)

			// Use ReconcilerBuilder to create reconciler
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithDefaults().
				Build()

			By("Attempting to delete IP with API error(will retry)")
			err := reconciler.handleStatus(ctx, node)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying IP was not removed despite error")
			Expect(node.Status.NetworkInterfaces["eni-1"].IPv4).To(HaveKey("192.168.0.1"))
		})

		It("Should handle empty network interfaces", func() {
			ctx := context.TODO()
			ctx = MetaIntoCtx(ctx)

			// Use NodeFactory to create node with no existing ENIs
			node := NewNodeFactory("test-node").
				WithECS().
				Build()

			// Setup mock API helper
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, vpcClient, ecsClient = mockHelper.GetMocks()

			// Use ReconcilerBuilder to create reconciler
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithDefaults().
				Build()

			By("Processing node with no network interfaces")
			err := reconciler.handleStatus(ctx, node)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Test validateENI", func() {
		It("Should not validate an ENI in deleting state", func() {
			ctx := context.TODO()

			// Create ENI with deleting status
			eni := BuildENIWithCustomIPs("eni-1", aliyunClient.ENIStatusDeleting, nil, nil)

			option := &eniOptions{
				eniRef:     eni,
				eniTypeKey: secondaryKey,
			}

			reconciler := &ReconcileNode{}

			result := reconciler.validateENI(ctx, option, []eniTypeKey{secondaryKey})
			Expect(result).To(BeFalse())
		})

		It("Should validate a ready ENI with available IPs", func() {
			ctx := context.TODO()

			// Setup mock API with vSwitch that has available IPs
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, vpcClient, ecsClient = mockHelper.GetMocks()
			mockHelper.SetupVSwitch("vsw-1", &vpc.VSwitch{
				VSwitchId:               "vsw-1",
				ZoneId:                  "zone-1",
				AvailableIpAddressCount: 10,
				CidrBlock:               "192.168.0.0/16",
				Ipv6CidrBlock:           "fd00::/64",
			})

			// Create ENI with InUse status
			eni := BuildENIWithCustomIPs("eni-1", aliyunClient.ENIStatusInUse, nil, nil)
			eni.VSwitchID = "vsw-1"
			eni.NetworkInterfaceType = networkv1beta1.ENITypeSecondary
			eni.NetworkInterfaceTrafficMode = networkv1beta1.NetworkInterfaceTrafficModeStandard

			option := &eniOptions{
				eniRef:     eni,
				eniTypeKey: secondaryKey,
			}

			// Use ReconcilerBuilder to create reconciler
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				Build()

			result := reconciler.validateENI(ctx, option, []eniTypeKey{secondaryKey})
			Expect(result).To(BeTrue())
		})

		It("Should not validate an ENI with mismatched type", func() {
			ctx := context.TODO()

			// Create ENI with secondary type
			eni := BuildENIWithCustomIPs("eni-1", aliyunClient.ENIStatusInUse, nil, nil)

			option := &eniOptions{
				eniRef:     eni,
				eniTypeKey: secondaryKey,
			}

			reconciler := &ReconcileNode{}

			// Try to validate with trunk type filter (should fail)
			result := reconciler.validateENI(ctx, option, []eniTypeKey{trunkKey})
			Expect(result).To(BeFalse())
		})

		It("Should validate an option without associated ENI", func() {
			ctx := context.TODO()

			// Option without ENI reference (for new ENI creation)
			option := &eniOptions{
				eniRef:     nil,
				eniTypeKey: secondaryKey,
			}

			reconciler := &ReconcileNode{}

			result := reconciler.validateENI(ctx, option, []eniTypeKey{secondaryKey})
			Expect(result).To(BeTrue())
		})

		It("Should not validate an ENI without available IPs", func() {
			ctx := context.TODO()

			// Setup mock API with vSwitch that has NO available IPs
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, vpcClient, ecsClient = mockHelper.GetMocks()
			mockHelper.SetupVSwitch("vsw-1", &vpc.VSwitch{
				VSwitchId:               "vsw-1",
				ZoneId:                  "zone-1",
				AvailableIpAddressCount: 0, // No available IPs
				CidrBlock:               "192.168.0.0/16",
				Ipv6CidrBlock:           "fd00::/64",
			})

			// Create ENI with InUse status
			eni := BuildENIWithCustomIPs("eni-1", aliyunClient.ENIStatusInUse, nil, nil)
			eni.VSwitchID = "vsw-1"
			eni.NetworkInterfaceType = networkv1beta1.ENITypeSecondary
			eni.NetworkInterfaceTrafficMode = networkv1beta1.NetworkInterfaceTrafficModeStandard

			option := &eniOptions{
				eniRef:     eni,
				eniTypeKey: secondaryKey,
			}

			// Use ReconcilerBuilder to create reconciler
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(switchPool).
				Build()

			result := reconciler.validateENI(ctx, option, []eniTypeKey{secondaryKey})
			Expect(result).To(BeFalse())
		})

		It("Should not validate an ENI with unavailable vSwitch information", func() {
			ctx := context.TODO()

			// Create new vSwitch pool for this test
			vsw, err := vswpool.NewSwitchPool(100, "10m")
			Expect(err).NotTo(HaveOccurred())

			// Setup mock API to return error when looking up vSwitch
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, vpcClient, ecsClient = mockHelper.GetMocks()
			mockHelper.SetupVSwitchWithError("vsw-not-exist", fmt.Errorf("vSwitch not found"))

			// Create ENI referencing non-existent vSwitch
			eni := BuildENIWithCustomIPs("eni-1", aliyunClient.ENIStatusInUse, nil, nil)
			eni.VSwitchID = "vsw-not-exist"
			eni.NetworkInterfaceType = networkv1beta1.ENITypeSecondary
			eni.NetworkInterfaceTrafficMode = networkv1beta1.NetworkInterfaceTrafficModeStandard

			option := &eniOptions{
				eniRef:     eni,
				eniTypeKey: secondaryKey,
			}

			// Use ReconcilerBuilder with the new vSwitch pool
			reconciler := NewReconcilerBuilder().
				WithAliyun(openAPI).
				WithVSwitchPool(vsw).
				Build()

			result := reconciler.validateENI(ctx, option, []eniTypeKey{secondaryKey})
			Expect(result).To(BeFalse())
		})
	})

	Context("Test isDaemonSupportNodeRuntime", func() {
		var testNodeName string

		BeforeEach(func() {
			testNodeName = "test-daemon-support-node"
		})

		AfterEach(func() {
			// Clean up any test pods created during the test
			podList := &corev1.PodList{}
			err := k8sClient.List(ctx, podList, client.InNamespace("kube-system"), client.MatchingLabels{"app": "terway-eniip"})
			if err == nil {
				for _, pod := range podList.Items {
					if pod.Spec.NodeName == testNodeName {
						err := k8sClient.Delete(ctx, &pod, &client.DeleteOptions{
							GracePeriodSeconds: func() *int64 {
								i := int64(0)
								return &i
							}(),
						})
						if err != nil {
							GinkgoT().Logf("Failed to delete test pod %s: %v", pod.Name, err)
						}
					}
				}
			}

			// Wait for pods to be deleted
			Eventually(func() bool {
				podList := &corev1.PodList{}
				err := k8sClient.List(ctx, podList, client.InNamespace("kube-system"), client.MatchingLabels{"app": "terway-eniip"})
				if err != nil {
					return false
				}
				for _, pod := range podList.Items {
					if pod.Spec.NodeName == testNodeName {
						return false
					}
				}
				return true
			}).WithTimeout(10 * time.Second).Should(BeTrue())
		})

		It("Should return true when no terway-eniip pods exist", func() {
			ctx := MetaIntoCtx(context.Background())

			result := isDaemonSupportNodeRuntime(ctx, k8sClient, testNodeName)
			Expect(result).To(BeTrue())
		})

		It("Should return true when terway-eniip pod has invalid image format", func() {
			ctx := MetaIntoCtx(context.Background())

			// Create a pod with invalid image format
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "terway-eniip-invalid-image",
					Namespace: "kube-system",
					Labels: map[string]string{
						"app": "terway-eniip",
					},
				},
				Spec: corev1.PodSpec{
					NodeName: testNodeName,
					Containers: []corev1.Container{
						{
							Name:  "terway",
							Image: "invalid-image-format", // no colon
						},
					},
				},
			}

			err := k8sClient.Create(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			result := isDaemonSupportNodeRuntime(ctx, k8sClient, testNodeName)
			Expect(result).To(BeTrue())
		})

		It("Should return false when terway version is less than v1.11.3", func() {
			ctx := MetaIntoCtx(context.Background())

			// Create a pod with version less than v1.11.3
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "terway-eniip-old-version",
					Namespace: "kube-system",
					Labels: map[string]string{
						"app": "terway-eniip",
					},
				},
				Spec: corev1.PodSpec{
					NodeName: testNodeName,
					Containers: []corev1.Container{
						{
							Name:  "terway",
							Image: "terway:v1.11.2", // less than v1.11.3
						},
					},
				},
			}

			err := k8sClient.Create(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			result := isDaemonSupportNodeRuntime(ctx, k8sClient, testNodeName)
			Expect(result).To(BeFalse())
		})

		It("Should return false when terway version is less than v1.11.3 (v2)", func() {
			ctx := MetaIntoCtx(context.Background())

			// Create a pod with version less than v1.11.3
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "terway-eniip-old-version",
					Namespace: "kube-system",
					Labels: map[string]string{
						"app": "terway-eniip",
					},
				},
				Spec: corev1.PodSpec{
					NodeName: testNodeName,
					Containers: []corev1.Container{
						{
							Name:  "terway",
							Image: "terway:v1.11.2-xxxxx", // less than v1.11.3
						},
					},
				},
			}

			err := k8sClient.Create(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			result := isDaemonSupportNodeRuntime(ctx, k8sClient, testNodeName)
			Expect(result).To(BeFalse())
		})

		It("Should return true when terway version is equal to v1.11.3", func() {
			ctx := MetaIntoCtx(context.Background())

			// Create a pod with version equal to v1.11.3
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "terway-eniip-equal-version",
					Namespace: "kube-system",
					Labels: map[string]string{
						"app": "terway-eniip",
					},
				},
				Spec: corev1.PodSpec{
					NodeName: testNodeName,
					Containers: []corev1.Container{
						{
							Name:  "terway",
							Image: "terway:v1.11.3", // equal to v1.11.3
						},
					},
				},
			}

			err := k8sClient.Create(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			result := isDaemonSupportNodeRuntime(ctx, k8sClient, testNodeName)
			Expect(result).To(BeTrue())
		})

		It("Should return true when terway version is greater than v1.11.3", func() {
			ctx := MetaIntoCtx(context.Background())

			// Create a pod with version greater than v1.11.3
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "terway-eniip-new-version",
					Namespace: "kube-system",
					Labels: map[string]string{
						"app": "terway-eniip",
					},
				},
				Spec: corev1.PodSpec{
					NodeName: testNodeName,
					Containers: []corev1.Container{
						{
							Name:  "terway",
							Image: "terway:v1.12.0", // greater than v1.11.3
						},
					},
				},
			}

			err := k8sClient.Create(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			result := isDaemonSupportNodeRuntime(ctx, k8sClient, testNodeName)
			Expect(result).To(BeTrue())
		})

		It("Should return true when terway version is greater than v1.11.3 (v2)", func() {
			ctx := MetaIntoCtx(context.Background())

			// Create a pod with version greater than v1.11.3
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "terway-eniip-new-version",
					Namespace: "kube-system",
					Labels: map[string]string{
						"app": "terway-eniip",
					},
				},
				Spec: corev1.PodSpec{
					NodeName: testNodeName,
					Containers: []corev1.Container{
						{
							Name:  "terway",
							Image: "terway:v1.12.0-xxxx", // greater than v1.11.3
						},
					},
				},
			}

			err := k8sClient.Create(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			result := isDaemonSupportNodeRuntime(ctx, k8sClient, testNodeName)
			Expect(result).To(BeTrue())
		})

		It("Should return true when image has multiple colons", func() {
			ctx := MetaIntoCtx(context.Background())

			// Create a pod with image having multiple colons
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "terway-eniip-registry-image",
					Namespace: "kube-system",
					Labels: map[string]string{
						"app": "terway-eniip",
					},
				},
				Spec: corev1.PodSpec{
					NodeName: testNodeName,
					Containers: []corev1.Container{
						{
							Name:  "terway",
							Image: "registry.com:5000/terway:v1.12.0", // multiple colons
						},
					},
				},
			}

			err := k8sClient.Create(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			result := isDaemonSupportNodeRuntime(ctx, k8sClient, testNodeName)
			Expect(result).To(BeTrue())
		})
	})

	Context("Test init", func() {
		{
			It("register should succeed", func() {
				v, ok := register.Controllers[ControllerName]
				Expect(ok).To(BeTrue())

				mgr, ctx := testutil.NewManager(cfg, openAPI, k8sClient)
				err := v.Creator(mgr, ctx)

				Expect(err).To(Not(HaveOccurred()))
			})
		}
	})

	Context("Node deletion", func() {
		It("Should remove finalizer and clean up cache and tasks when node is deleted", func() {
			ctx := context.TODO()
			nodeName := "deleted-node"

			// 1. Create a node in k8s with a finalizer
			node := NewNodeFactory(nodeName).
				WithECS().
				WithExistingENIs(NewENIFactory().WithBaseID("eni").WithCount(1).Build()...).
				Build()
			node.Finalizers = []string{finalizer}

			err := k8sClient.Create(ctx, node)
			Expect(err).NotTo(HaveOccurred())

			// 2. Mark for deletion by calling Delete
			// It won't actually be deleted from the API server because of the finalizer
			err = k8sClient.Delete(ctx, node)
			Expect(err).NotTo(HaveOccurred())

			// 3. Refresh node to see deletion timestamp
			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, node)
			Expect(err).NotTo(HaveOccurred())
			Expect(node.DeletionTimestamp.IsZero()).To(BeFalse())

			// 4. Setup reconciler
			mockHelper := NewMockAPIHelperWithT(GinkgoT())
			openAPI, _, _ = mockHelper.GetMocks()
			openAPI.On("AttachNetworkInterfaceV2", mock.Anything, mock.Anything).Return(nil).Maybe()

			reconciler := NewReconcilerBuilder().
				WithClient(k8sClient).
				WithAliyun(openAPI).
				WithDefaults().
				Build()

			// 5. Add node to cache and task queue to verify cleanup
			reconciler.cache.Store(nodeName, &NodeStatus{})
			reconciler.eniTaskQueue.SubmitAttach(ctx, "eni-1", "instance-1", "", nodeName, 1, 0)
			Expect(reconciler.eniTaskQueue.GetAttachingCount(nodeName)).To(Equal(1))

			// 6. Reconcile
			request := reconcile.Request{NamespacedName: types.NamespacedName{Name: nodeName}}
			_, err = reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			// 7. Verify finalizer removal and cleanup
			By("Verifying finalizer is removed or node is gone")
			updatedNode := &networkv1beta1.Node{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, updatedNode)
			if err == nil {
				Expect(updatedNode.Finalizers).NotTo(ContainElement(finalizer))
			} else {
				Expect(k8sErr.IsNotFound(err)).To(BeTrue())
			}

			By("Verifying cache is cleared")
			_, ok := reconciler.cache.Load(nodeName)
			Expect(ok).To(BeFalse())

			By("Verifying task queue is cleared")
			Expect(reconciler.eniTaskQueue.GetAttachingCount(nodeName)).To(Equal(0))
		})
	})
})

func TestInitializeWarmUp(t *testing.T) {
	tests := []struct {
		name            string
		node            *networkv1beta1.Node
		expectTarget    int
		expectCount     int
		expectCompleted bool
	}{
		{
			name: "New node with warm-up configured",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					Pool: &networkv1beta1.PoolSpec{
						WarmUpSize: 10,
					},
				},
				Status: networkv1beta1.NodeStatus{},
			},
			expectTarget:    10,
			expectCount:     0,
			expectCompleted: false,
		},
		{
			name: "Node without warm-up configured",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					Pool: &networkv1beta1.PoolSpec{
						WarmUpSize: 0,
					},
				},
				Status: networkv1beta1.NodeStatus{},
			},
			expectTarget:    0,
			expectCount:     0,
			expectCompleted: true,
		},
		{
			name: "Existing node with warm-up already initialized",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					Pool: &networkv1beta1.PoolSpec{
						WarmUpSize: 10,
					},
				},
				Status: networkv1beta1.NodeStatus{
					WarmUpTarget:         5,
					WarmUpAllocatedCount: 3,
					WarmUpCompleted:      false,
				},
			},
			expectTarget:    5,
			expectCount:     3,
			expectCompleted: false,
		},
		{
			name: "Existing node already completed",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					Pool: &networkv1beta1.PoolSpec{
						WarmUpSize: 10,
					},
				},
				Status: networkv1beta1.NodeStatus{
					WarmUpCompleted: true,
				},
			},
			expectTarget:    0,
			expectCount:     0,
			expectCompleted: true,
		},
		{
			name: "Node with nil pool spec",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					Pool: nil,
				},
				Status: networkv1beta1.NodeStatus{},
			},
			expectTarget:    0,
			expectCount:     0,
			expectCompleted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &ReconcileNode{}
			reconciler.initializeWarmUp(tt.node)

			assert.Equal(t, tt.expectTarget, tt.node.Status.WarmUpTarget)
			assert.Equal(t, tt.expectCount, tt.node.Status.WarmUpAllocatedCount)
			assert.Equal(t, tt.expectCompleted, tt.node.Status.WarmUpCompleted)
		})
	}
}

func TestShouldPerformWarmUp(t *testing.T) {
	tests := []struct {
		name     string
		node     *networkv1beta1.Node
		expected bool
	}{
		{
			name: "Warm-up not completed and target set",
			node: &networkv1beta1.Node{
				Status: networkv1beta1.NodeStatus{
					WarmUpTarget:    10,
					WarmUpCompleted: false,
				},
			},
			expected: true,
		},
		{
			name: "Warm-up completed",
			node: &networkv1beta1.Node{
				Status: networkv1beta1.NodeStatus{
					WarmUpTarget:    10,
					WarmUpCompleted: true,
				},
			},
			expected: false,
		},
		{
			name: "No warm-up target set",
			node: &networkv1beta1.Node{
				Status: networkv1beta1.NodeStatus{
					WarmUpTarget:    0,
					WarmUpCompleted: false,
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &ReconcileNode{}
			result := reconciler.shouldPerformWarmUp(tt.node)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateWarmUpDemand(t *testing.T) {
	tests := []struct {
		name     string
		node     *networkv1beta1.Node
		expected int
	}{
		{
			name: "Need more IPs for warm-up",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					NodeCap: networkv1beta1.NodeCap{
						Adapters:       3,
						IPv4PerAdapter: 10,
					},
					ENISpec: &networkv1beta1.ENISpec{
						EnableIPv4: true,
					},
				},
				Status: networkv1beta1.NodeStatus{
					WarmUpTarget:         15,
					WarmUpAllocatedCount: 3,
					WarmUpCompleted:      false,
					NetworkInterfaces: map[string]*networkv1beta1.Nic{
						"eni-1": {
							Status: aliyunClient.ENIStatusInUse,
							IPv4: map[string]*networkv1beta1.IP{
								"10.0.0.1": {Primary: true, Status: networkv1beta1.IPStatusValid},
								"10.0.0.2": {Primary: false, Status: networkv1beta1.IPStatusValid},
								"10.0.0.3": {Primary: false, Status: networkv1beta1.IPStatusValid},
							},
						},
					},
				},
			},
			expected: 15, // remaining=15-3=12, currentTotal=3, min(3+12, 30)=15
		},
		{
			name: "Partial warmup progress",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					NodeCap: networkv1beta1.NodeCap{
						Adapters:       3,
						IPv4PerAdapter: 10,
					},
					ENISpec: &networkv1beta1.ENISpec{
						EnableIPv4: true,
					},
				},
				Status: networkv1beta1.NodeStatus{
					WarmUpTarget:         10,
					WarmUpAllocatedCount: 8,
					WarmUpCompleted:      false,
					NetworkInterfaces: map[string]*networkv1beta1.Nic{
						"eni-1": {
							Status: aliyunClient.ENIStatusInUse,
							IPv4: map[string]*networkv1beta1.IP{
								"10.0.0.1": {Primary: true, Status: networkv1beta1.IPStatusValid},
								"10.0.0.2": {Primary: false, Status: networkv1beta1.IPStatusValid},
								"10.0.0.3": {Primary: false, Status: networkv1beta1.IPStatusValid},
								"10.0.0.4": {Primary: false, Status: networkv1beta1.IPStatusValid},
								"10.0.0.5": {Primary: false, Status: networkv1beta1.IPStatusValid},
								"10.0.0.6": {Primary: false, Status: networkv1beta1.IPStatusValid},
								"10.0.0.7": {Primary: false, Status: networkv1beta1.IPStatusValid},
							},
						},
					},
				},
			},
			expected: 9, // remaining=10-8=2, currentTotal=7, min(7+2, 30)=9
		},
		{
			name: "Warm-up target already reached by WarmUpAllocatedCount",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					NodeCap: networkv1beta1.NodeCap{
						Adapters:       3,
						IPv4PerAdapter: 10,
					},
					ENISpec: &networkv1beta1.ENISpec{
						EnableIPv4: true,
					},
				},
				Status: networkv1beta1.NodeStatus{
					WarmUpTarget:         5,
					WarmUpAllocatedCount: 5,
					WarmUpCompleted:      false,
					NetworkInterfaces: map[string]*networkv1beta1.Nic{
						"eni-1": {
							Status: aliyunClient.ENIStatusInUse,
							IPv4: map[string]*networkv1beta1.IP{
								"10.0.0.1": {Primary: true, Status: networkv1beta1.IPStatusValid},
								"10.0.0.2": {Primary: false, Status: networkv1beta1.IPStatusValid},
								"10.0.0.3": {Primary: false, Status: networkv1beta1.IPStatusValid},
							},
						},
					},
				},
			},
			expected: 0, // remaining=5-5=0, return 0
		},
		{
			name: "Warm-up completed",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					NodeCap: networkv1beta1.NodeCap{
						Adapters:       3,
						IPv4PerAdapter: 10,
					},
					ENISpec: &networkv1beta1.ENISpec{
						EnableIPv4: true,
					},
				},
				Status: networkv1beta1.NodeStatus{
					WarmUpTarget:         10,
					WarmUpAllocatedCount: 10,
					WarmUpCompleted:      true,
					NetworkInterfaces:    map[string]*networkv1beta1.Nic{},
				},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &ReconcileNode{}
			result := reconciler.calculateWarmUpDemand(tt.node)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCheckWarmUpCompletion(t *testing.T) {
	tests := []struct {
		name            string
		node            *networkv1beta1.Node
		expectCompleted bool
	}{
		{
			name: "Allocated count reaches target",
			node: &networkv1beta1.Node{
				Status: networkv1beta1.NodeStatus{
					WarmUpTarget:         10,
					WarmUpAllocatedCount: 10,
					WarmUpCompleted:      false,
				},
			},
			expectCompleted: true,
		},
		{
			name: "Allocated count exceeds target",
			node: &networkv1beta1.Node{
				Status: networkv1beta1.NodeStatus{
					WarmUpTarget:         10,
					WarmUpAllocatedCount: 15,
					WarmUpCompleted:      false,
				},
			},
			expectCompleted: true,
		},
		{
			name: "Allocated count below target",
			node: &networkv1beta1.Node{
				Status: networkv1beta1.NodeStatus{
					WarmUpTarget:         10,
					WarmUpAllocatedCount: 5,
					WarmUpCompleted:      false,
				},
			},
			expectCompleted: false,
		},
		{
			name: "Already completed",
			node: &networkv1beta1.Node{
				Status: networkv1beta1.NodeStatus{
					WarmUpTarget:         10,
					WarmUpAllocatedCount: 5,
					WarmUpCompleted:      true,
				},
			},
			expectCompleted: true,
		},
		{
			name: "No warm-up target",
			node: &networkv1beta1.Node{
				Status: networkv1beta1.NodeStatus{
					WarmUpTarget:         0,
					WarmUpAllocatedCount: 5,
					WarmUpCompleted:      false,
				},
			},
			expectCompleted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &ReconcileNode{}
			reconciler.checkWarmUpCompletion(tt.node)
			assert.Equal(t, tt.expectCompleted, tt.node.Status.WarmUpCompleted)
		})
	}
}

func TestReconcileNode_poolSyncPeriod(t *testing.T) {
	tests := []struct {
		name     string
		user     string
		gcPeriod time.Duration
		want     time.Duration
	}{
		{
			name:     "empty user string, use system default",
			user:     "",
			gcPeriod: 30 * time.Second,
			want:     30 * time.Second,
		},
		{
			name:     "valid user duration",
			user:     "60s",
			gcPeriod: 30 * time.Second,
			want:     60 * time.Second,
		},
		{
			name:     "valid user duration with minutes",
			user:     "5m",
			gcPeriod: 30 * time.Second,
			want:     5 * time.Minute,
		},
		{
			name:     "invalid user duration, use system default",
			user:     "invalid",
			gcPeriod: 30 * time.Second,
			want:     30 * time.Second,
		},
		{
			name:     "user duration overrides system default",
			user:     "2m",
			gcPeriod: 1 * time.Minute,
			want:     2 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &ReconcileNode{
				gcPeriod: tt.gcPeriod,
			}
			got := n.poolSyncPeriod(tt.user)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestReconcileNode_requeueAfter(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		node        *networkv1beta1.Node
		gcPeriod    time.Duration
		want        time.Duration
		wantMin     time.Duration
		wantMax     time.Duration
		description string
	}{
		{
			name: "zero NextSyncOpenAPITime, use poolPeriod",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					Pool: &networkv1beta1.PoolSpec{
						PoolSyncPeriod: "",
					},
				},
				Status: networkv1beta1.NodeStatus{
					NextSyncOpenAPITime: metav1.Time{},
				},
			},
			gcPeriod:    30 * time.Second,
			want:        30 * time.Second,
			description: "When NextSyncOpenAPITime is zero, should use poolPeriod",
		},
		{
			name: "NextSyncOpenAPITime in future, less than poolPeriod",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					Pool: &networkv1beta1.PoolSpec{
						PoolSyncPeriod: "60s",
					},
				},
				Status: networkv1beta1.NodeStatus{
					NextSyncOpenAPITime: metav1.NewTime(now.Add(20 * time.Second)),
				},
			},
			gcPeriod:    30 * time.Second,
			wantMin:     19 * time.Second,
			wantMax:     21 * time.Second,
			description: "When NextSyncOpenAPITime is in future and less than poolPeriod, should use NextSyncOpenAPITime duration",
		},
		{
			name: "NextSyncOpenAPITime in future, greater than poolPeriod",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					Pool: &networkv1beta1.PoolSpec{
						PoolSyncPeriod: "30s",
					},
				},
				Status: networkv1beta1.NodeStatus{
					NextSyncOpenAPITime: metav1.NewTime(now.Add(60 * time.Second)),
				},
			},
			gcPeriod:    30 * time.Second,
			want:        30 * time.Second,
			description: "When NextSyncOpenAPITime is in future but greater than poolPeriod, should use poolPeriod",
		},
		{
			name: "NextSyncOpenAPITime in past",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					Pool: &networkv1beta1.PoolSpec{
						PoolSyncPeriod: "",
					},
				},
				Status: networkv1beta1.NodeStatus{
					NextSyncOpenAPITime: metav1.NewTime(now.Add(-10 * time.Second)),
				},
			},
			gcPeriod:    30 * time.Second,
			want:        30 * time.Second,
			description: "When NextSyncOpenAPITime is in past, should use poolPeriod",
		},
		{
			name: "poolPeriod less than 1s, should enforce minimum 1s",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					Pool: &networkv1beta1.PoolSpec{
						PoolSyncPeriod: "500ms",
					},
				},
				Status: networkv1beta1.NodeStatus{
					NextSyncOpenAPITime: metav1.Time{},
				},
			},
			gcPeriod:    500 * time.Millisecond,
			want:        1 * time.Second,
			description: "When poolPeriod is less than 1s, should enforce minimum 1s",
		},
		{
			name: "NextSyncOpenAPITime duration less than 1s, should enforce minimum 1s",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					Pool: &networkv1beta1.PoolSpec{
						PoolSyncPeriod: "60s",
					},
				},
				Status: networkv1beta1.NodeStatus{
					NextSyncOpenAPITime: metav1.NewTime(now.Add(500 * time.Millisecond)),
				},
			},
			gcPeriod:    30 * time.Second,
			want:        1 * time.Second,
			description: "When NextSyncOpenAPITime duration is less than 1s, should enforce minimum 1s",
		},
		{
			name: "custom poolSyncPeriod from user config",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					Pool: &networkv1beta1.PoolSpec{
						PoolSyncPeriod: "2m",
					},
				},
				Status: networkv1beta1.NodeStatus{
					NextSyncOpenAPITime: metav1.NewTime(now.Add(90 * time.Second)),
				},
			},
			gcPeriod:    30 * time.Second,
			wantMin:     89 * time.Second,
			wantMax:     91 * time.Second,
			description: "When user provides custom poolSyncPeriod, should use it and compare with NextSyncOpenAPITime",
		},
		{
			name: "NextSyncOpenAPITime exactly 1s in future",
			node: &networkv1beta1.Node{
				Spec: networkv1beta1.NodeSpec{
					Pool: &networkv1beta1.PoolSpec{
						PoolSyncPeriod: "60s",
					},
				},
				Status: networkv1beta1.NodeStatus{
					NextSyncOpenAPITime: metav1.NewTime(now.Add(1 * time.Second)),
				},
			},
			gcPeriod:    30 * time.Second,
			wantMin:     999 * time.Millisecond,
			wantMax:     1001 * time.Millisecond,
			description: "When NextSyncOpenAPITime is exactly 1s in future, should return approximately 1s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &ReconcileNode{
				gcPeriod: tt.gcPeriod,
			}
			got := n.requeueAfter(tt.node)

			if tt.want != 0 {
				assert.Equal(t, tt.want, got, tt.description)
			} else {
				if tt.wantMin != 0 && tt.wantMax != 0 {
					assert.GreaterOrEqual(t, got, tt.wantMin, tt.description)
					assert.LessOrEqual(t, got, tt.wantMax, tt.description)
				} else {
					assert.GreaterOrEqual(t, got, 1*time.Second, "result should be at least 1s")
				}
			}
		})
	}
}
