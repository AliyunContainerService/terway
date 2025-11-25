package node

import (
	"reflect"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
				log: logr.Discard(),
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
