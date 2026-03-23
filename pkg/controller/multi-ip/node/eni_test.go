package node

import (
	"reflect"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

func Test_sortNetworkInterface(t *testing.T) {
	type args struct {
		node *networkv1beta1.Node
	}
	tests := []struct {
		name string
		args args
		want []*networkv1beta1.Nic
	}{
		{
			name: "trunk",
			args: args{
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{
							EnableIPv4: true,
							EnableIPv6: true,
						},
					},
					Status: networkv1beta1.NodeStatus{
						NextSyncOpenAPITime: metav1.Time{},
						LastSyncOpenAPITime: metav1.Time{},
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
							"eni-1": {
								ID:                          "eni-1",
								NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								IPv4: map[string]*networkv1beta1.IP{
									"1": nil,
									"2": nil,
								},
							},
							"trunk": {
								ID:                          "trunk",
								NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							},
						},
					},
				},
			},
			want: []*networkv1beta1.Nic{
				{
					ID:                          "trunk",
					NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
					NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
				},
				{
					ID:                          "eni-1",
					NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
					NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
					IPv4: map[string]*networkv1beta1.IP{
						"1": nil,
						"2": nil,
					},
				},
			},
		},
		{
			name: "normal pods",
			args: args{
				node: &networkv1beta1.Node{
					Spec: networkv1beta1.NodeSpec{
						ENISpec: &networkv1beta1.ENISpec{
							EnableIPv4: true,
							EnableIPv6: true,
						},
					},
					Status: networkv1beta1.NodeStatus{
						NextSyncOpenAPITime: metav1.Time{},
						LastSyncOpenAPITime: metav1.Time{},
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
							"eni-1": {
								ID:                          "eni-1",
								NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
							},
							"eni-2": {
								ID:                          "eni-2",
								NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
								NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
								IPv4: map[string]*networkv1beta1.IP{
									"1": nil,
									"2": nil,
								},
							},
						},
					},
				},
			},
			want: []*networkv1beta1.Nic{
				{
					ID:                          "eni-2",
					NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
					NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
					IPv4: map[string]*networkv1beta1.IP{
						"1": nil,
						"2": nil,
					},
				},
				{
					ID:                          "eni-1",
					NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
					NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, reflect.DeepEqual(tt.want, sortNetworkInterface(tt.args.node)), "sortNetworkInterface(%v)", tt.args.node)
		})
	}
}

func Test_mergeIPMap(t *testing.T) {
	type args struct {
		log     logr.Logger
		remote  map[string]*networkv1beta1.IP
		current map[string]*networkv1beta1.IP
	}
	tests := []struct {
		name   string
		args   args
		expect map[string]*networkv1beta1.IP
	}{
		{
			name: "keep exist",
			args: args{
				log: logr.Discard(),
				remote: map[string]*networkv1beta1.IP{
					"1": {
						IP: "1",
					},
					"2": {
						IP: "2",
					},
					"3": {
						IP: "3",
					},
				},
				current: map[string]*networkv1beta1.IP{
					"1": {
						IP:     "1",
						Status: networkv1beta1.IPStatusDeleting,
					},
					"3": {
						IP: "3",
					},
				},
			},
			expect: map[string]*networkv1beta1.IP{
				"1": {
					IP:     "1",
					Status: networkv1beta1.IPStatusDeleting,
				},
				"2": {
					IP: "2",
				},
				"3": {
					IP: "3",
				},
			},
		},
		{
			name: "delete none exist",
			args: args{
				log: logr.Discard(),
				remote: map[string]*networkv1beta1.IP{
					"1": {
						IP: "1",
					},
				},
				current: map[string]*networkv1beta1.IP{
					"1": {
						IP:     "1",
						Status: networkv1beta1.IPStatusDeleting,
					},
					"3": {
						IP: "3",
					},
				},
			},
			expect: map[string]*networkv1beta1.IP{
				"1": {
					IP:     "1",
					Status: networkv1beta1.IPStatusDeleting,
				},
			},
		},
		{
			name: "eflo failed ip should be synced",
			args: args{
				log: logr.Discard(),
				remote: map[string]*networkv1beta1.IP{
					"ipName": {
						IP:     "",
						IPName: "ipName",
						Status: networkv1beta1.IPStatusDeleting,
					},
				},
				current: map[string]*networkv1beta1.IP{
					"1": {
						IP:     "1",
						Status: networkv1beta1.IPStatusDeleting,
					},
				},
			},
			expect: map[string]*networkv1beta1.IP{
				"ipName": {
					IP:     "",
					IPName: "ipName",
					Status: networkv1beta1.IPStatusDeleting,
				},
			},
		},
		{
			name: "eflo ip not found in temote but in use",
			args: args{
				log: logr.Discard(),
				remote: map[string]*networkv1beta1.IP{
					"ipName": {
						IP:     "",
						IPName: "ipName",
						Status: networkv1beta1.IPStatusDeleting,
					},
					"ipName2": {
						IP:     "127.0.0.1",
						IPName: "ipName2",
						Status: networkv1beta1.IPStatusValid,
					},
				},
				current: map[string]*networkv1beta1.IP{
					"1": {
						IP:     "1",
						PodID:  "foo",
						PodUID: "bar",
						Status: networkv1beta1.IPStatusValid,
					},
				},
			},
			expect: map[string]*networkv1beta1.IP{
				"ipName": {
					IP:     "",
					IPName: "ipName",
					Status: networkv1beta1.IPStatusDeleting,
				},
				"ipName2": {
					IP:     "127.0.0.1",
					IPName: "ipName2",
					Status: networkv1beta1.IPStatusValid,
				},
				"1": {
					IP:     "1",
					PodID:  "foo",
					PodUID: "bar",
					Status: networkv1beta1.IPStatusInvalid,
				},
			},
		},
		{
			name: "primary case",
			args: args{
				log:    logr.Discard(),
				remote: map[string]*networkv1beta1.IP{
					// somehow primary ip is not in remote
				},
				current: map[string]*networkv1beta1.IP{
					"ipName": {
						IP:      "",
						IPName:  "ipName",
						Primary: true,
						Status:  networkv1beta1.IPStatusValid,
					},
				},
			},
			expect: map[string]*networkv1beta1.IP{
				"ipName": {
					IP:      "",
					IPName:  "ipName",
					Primary: true,
					Status:  networkv1beta1.IPStatusValid,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mergeIPMap(tt.args.log, tt.args.remote, tt.args.current)

			assert.True(t, reflect.DeepEqual(tt.expect, tt.args.current), "mergeIPMap(%v, %v)", tt.args.current, tt.args.remote)
		})
	}
}

func Test_mergeIPPrefixes(t *testing.T) {
	frozenExpireAt := metav1.Now()

	type args struct {
		remote  []aliyunClient.Prefix
		current []networkv1beta1.IPPrefix
	}
	tests := []struct {
		name   string
		args   args
		expect []networkv1beta1.IPPrefix
	}{
		{
			name: "remote exists local missing: add as Valid",
			args: args{
				remote:  []aliyunClient.Prefix{"10.0.0.0/28"},
				current: nil,
			},
			expect: []networkv1beta1.IPPrefix{
				{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
			},
		},
		{
			name: "remote exists local exists: preserve full local record including FrozenExpireAt",
			args: args{
				remote: []aliyunClient.Prefix{"10.0.0.0/28"},
				current: []networkv1beta1.IPPrefix{
					{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusFrozen, FrozenExpireAt: frozenExpireAt},
				},
			},
			expect: []networkv1beta1.IPPrefix{
				{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusFrozen, FrozenExpireAt: frozenExpireAt},
			},
		},
		{
			name: "remote missing local Valid: mark as Invalid",
			args: args{
				remote: []aliyunClient.Prefix{},
				current: []networkv1beta1.IPPrefix{
					{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
				},
			},
			expect: []networkv1beta1.IPPrefix{
				{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusInvalid},
			},
		},
		{
			name: "remote missing local Frozen: mark as Invalid",
			args: args{
				remote: []aliyunClient.Prefix{},
				current: []networkv1beta1.IPPrefix{
					{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusFrozen, FrozenExpireAt: frozenExpireAt},
				},
			},
			expect: []networkv1beta1.IPPrefix{
				{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusInvalid, FrozenExpireAt: frozenExpireAt},
			},
		},
		{
			name: "remote missing local Deleting: remove from CR (unassign completed, cloud confirmed removal)",
			args: args{
				remote: []aliyunClient.Prefix{},
				current: []networkv1beta1.IPPrefix{
					{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusDeleting},
				},
			},
			expect: []networkv1beta1.IPPrefix{},
		},
		{
			name: "remote missing local already Invalid: keep Invalid unchanged",
			args: args{
				remote: []aliyunClient.Prefix{},
				current: []networkv1beta1.IPPrefix{
					{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusInvalid},
				},
			},
			expect: []networkv1beta1.IPPrefix{
				{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusInvalid},
			},
		},
		{
			name: "Invalid must not transition back to Valid when remote reappears",
			args: args{
				// Prefix reappears in remote after being marked Invalid locally.
				// The local record was Invalid, but remote now has it — treat as new Valid.
				// (This tests Pass 1: remote-exists path always wins over local Invalid.)
				remote: []aliyunClient.Prefix{"10.0.0.0/28"},
				current: []networkv1beta1.IPPrefix{
					{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusInvalid},
				},
			},
			// When remote has it, Pass 1 preserves the local record as-is (Invalid).
			// Invalid is a terminal state — the controller must explicitly clean it up,
			// not silently flip it back to Valid on re-appearance.
			expect: []networkv1beta1.IPPrefix{
				{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusInvalid},
			},
		},
		{
			name: "mixed: some in remote some not",
			args: args{
				remote: []aliyunClient.Prefix{"10.0.0.0/28", "10.0.0.32/28"},
				current: []networkv1beta1.IPPrefix{
					{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
					{Prefix: "10.0.0.16/28", Status: networkv1beta1.IPPrefixStatusFrozen, FrozenExpireAt: frozenExpireAt},
					{Prefix: "10.0.0.32/28", Status: networkv1beta1.IPPrefixStatusDeleting},
				},
			},
			expect: []networkv1beta1.IPPrefix{
				// in remote: preserve local
				{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
				// in remote: new
				{Prefix: "10.0.0.32/28", Status: networkv1beta1.IPPrefixStatusDeleting},
				// not in remote: mark Invalid
				{Prefix: "10.0.0.16/28", Status: networkv1beta1.IPPrefixStatusInvalid, FrozenExpireAt: frozenExpireAt},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeIPPrefixes(logr.Discard(), tt.args.remote, tt.args.current)
			assert.ElementsMatch(t, tt.expect, got, "mergeIPPrefixes result mismatch")
		})
	}
}

func TestIPUsage(t *testing.T) {
	type args struct {
		eniIP map[string]*networkv1beta1.IP
	}
	tests := []struct {
		name  string
		args  args
		want  int
		want1 int
	}{
		{
			name: "idle",
			args: args{
				eniIP: map[string]*networkv1beta1.IP{
					"1": {
						IP:     "1",
						Status: networkv1beta1.IPStatusDeleting,
					},
					"2": {
						IP:    "2",
						PodID: "2",
					},
					"3": {
						IP:    "3",
						PodID: "3",
					},
				},
			},
			want:  1,
			want1: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := IPUsage(tt.args.eniIP)
			assert.Equalf(t, tt.want, got, "IPUsage(%v)", tt.args.eniIP)
			assert.Equalf(t, tt.want1, got1, "IPUsage(%v)", tt.args.eniIP)
		})
	}
}

func Test_processFrozenExpireAt_MultipleExpired(t *testing.T) {
	log := logr.Discard()
	now := time.Now()
	node := &networkv1beta1.Node{
		Status: networkv1beta1.NodeStatus{
			NetworkInterfaces: map[string]*networkv1beta1.Nic{
				"eni-1": {
					ID: "eni-1",
					IPv4Prefix: []networkv1beta1.IPPrefix{
						{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusFrozen, FrozenExpireAt: metav1.NewTime(now.Add(-1 * time.Hour))},
						{Prefix: "10.0.1.0/28", Status: networkv1beta1.IPPrefixStatusFrozen, FrozenExpireAt: metav1.NewTime(now.Add(1 * time.Hour))},
						{Prefix: "10.0.2.0/28", Status: networkv1beta1.IPPrefixStatusFrozen, FrozenExpireAt: metav1.NewTime(now.Add(-1 * time.Hour))},
					},
				},
			},
		},
	}

	got := processFrozenExpireAt(log, node)
	assert.True(t, got)
	assert.Equal(t, networkv1beta1.IPPrefixStatusValid, node.Status.NetworkInterfaces["eni-1"].IPv4Prefix[0].Status)
	assert.True(t, node.Status.NetworkInterfaces["eni-1"].IPv4Prefix[0].FrozenExpireAt.IsZero())
	assert.Equal(t, networkv1beta1.IPPrefixStatusFrozen, node.Status.NetworkInterfaces["eni-1"].IPv4Prefix[1].Status)
	assert.False(t, node.Status.NetworkInterfaces["eni-1"].IPv4Prefix[1].FrozenExpireAt.IsZero())
	assert.Equal(t, networkv1beta1.IPPrefixStatusValid, node.Status.NetworkInterfaces["eni-1"].IPv4Prefix[2].Status)
	assert.True(t, node.Status.NetworkInterfaces["eni-1"].IPv4Prefix[2].FrozenExpireAt.IsZero())
}

func Test_processFrozenExpireAt_IPv6Expired(t *testing.T) {
	log := logr.Discard()
	now := time.Now()
	node := &networkv1beta1.Node{
		Status: networkv1beta1.NodeStatus{
			NetworkInterfaces: map[string]*networkv1beta1.Nic{
				"eni-1": {
					ID: "eni-1",
					IPv6Prefix: []networkv1beta1.IPPrefix{
						{Prefix: "2001:db8::/64", Status: networkv1beta1.IPPrefixStatusFrozen, FrozenExpireAt: metav1.NewTime(now.Add(-1 * time.Hour))},
					},
				},
			},
		},
	}

	got := processFrozenExpireAt(log, node)
	assert.True(t, got)
	assert.Equal(t, networkv1beta1.IPPrefixStatusValid, node.Status.NetworkInterfaces["eni-1"].IPv6Prefix[0].Status)
	assert.True(t, node.Status.NetworkInterfaces["eni-1"].IPv6Prefix[0].FrozenExpireAt.IsZero())
}

func Test_processFrozenExpireAt(t *testing.T) {
	now := time.Now()
	type args struct {
		log  logr.Logger
		node *networkv1beta1.Node
	}
	tests := []struct {
		name     string
		args     args
		want     bool
		validate func(t *testing.T, node *networkv1beta1.Node)
	}{
		{
			name: "Frozen prefix 未过期 → 不变",
			args: args{
				log: logr.Discard(),
				node: &networkv1beta1.Node{
					Status: networkv1beta1.NodeStatus{
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
							"eni-1": {
								IPv4Prefix: []networkv1beta1.IPPrefix{
									{
										Prefix:         "10.0.0.0/28",
										Status:         networkv1beta1.IPPrefixStatusFrozen,
										FrozenExpireAt: metav1.NewTime(now.Add(1 * time.Hour)),
									},
								},
							},
						},
					},
				},
			},
			want: false,
			validate: func(t *testing.T, node *networkv1beta1.Node) {
				assert.Equal(t, networkv1beta1.IPPrefixStatusFrozen, node.Status.NetworkInterfaces["eni-1"].IPv4Prefix[0].Status)
				assert.False(t, node.Status.NetworkInterfaces["eni-1"].IPv4Prefix[0].FrozenExpireAt.IsZero())
			},
		},
		{
			name: "Frozen prefix 已过期 → 回退为 Valid，FrozenExpireAt 清零",
			args: args{
				log: logr.Discard(),
				node: &networkv1beta1.Node{
					Status: networkv1beta1.NodeStatus{
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
							"eni-1": {
								IPv4Prefix: []networkv1beta1.IPPrefix{
									{
										Prefix:         "10.0.0.0/28",
										Status:         networkv1beta1.IPPrefixStatusFrozen,
										FrozenExpireAt: metav1.NewTime(now.Add(-1 * time.Hour)),
									},
								},
							},
						},
					},
				},
			},
			want: true,
			validate: func(t *testing.T, node *networkv1beta1.Node) {
				assert.Equal(t, networkv1beta1.IPPrefixStatusValid, node.Status.NetworkInterfaces["eni-1"].IPv4Prefix[0].Status)
				assert.True(t, node.Status.NetworkInterfaces["eni-1"].IPv4Prefix[0].FrozenExpireAt.IsZero())
			},
		},
		{
			name: "FrozenExpireAt 为零值 → 不变（不回退）",
			args: args{
				log: logr.Discard(),
				node: &networkv1beta1.Node{
					Status: networkv1beta1.NodeStatus{
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
							"eni-1": {
								IPv4Prefix: []networkv1beta1.IPPrefix{
									{
										Prefix:         "10.0.0.0/28",
										Status:         networkv1beta1.IPPrefixStatusFrozen,
										FrozenExpireAt: metav1.Time{},
									},
								},
							},
						},
					},
				},
			},
			want: false,
			validate: func(t *testing.T, node *networkv1beta1.Node) {
				assert.Equal(t, networkv1beta1.IPPrefixStatusFrozen, node.Status.NetworkInterfaces["eni-1"].IPv4Prefix[0].Status)
				assert.True(t, node.Status.NetworkInterfaces["eni-1"].IPv4Prefix[0].FrozenExpireAt.IsZero())
			},
		},
		{
			name: "Valid prefix 有 FrozenExpireAt → 不变（只处理 Frozen 状态）",
			args: args{
				log: logr.Discard(),
				node: &networkv1beta1.Node{
					Status: networkv1beta1.NodeStatus{
						NetworkInterfaces: map[string]*networkv1beta1.Nic{
							"eni-1": {
								IPv4Prefix: []networkv1beta1.IPPrefix{
									{
										Prefix:         "10.0.0.0/28",
										Status:         networkv1beta1.IPPrefixStatusValid,
										FrozenExpireAt: metav1.NewTime(now.Add(-1 * time.Hour)),
									},
								},
							},
						},
					},
				},
			},
			want: false,
			validate: func(t *testing.T, node *networkv1beta1.Node) {
				assert.Equal(t, networkv1beta1.IPPrefixStatusValid, node.Status.NetworkInterfaces["eni-1"].IPv4Prefix[0].Status)
				assert.False(t, node.Status.NetworkInterfaces["eni-1"].IPv4Prefix[0].FrozenExpireAt.IsZero())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := processFrozenExpireAt(tt.args.log, tt.args.node)
			assert.Equal(t, tt.want, got)
			if tt.validate != nil {
				tt.validate(t, tt.args.node)
			}
		})
	}
}
