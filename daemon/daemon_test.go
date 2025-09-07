package daemon

import (
	"net/netip"
	"testing"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/eni"
	factorymocks "github.com/AliyunContainerService/terway/pkg/factory/mocks"
	k8smocks "github.com/AliyunContainerService/terway/pkg/k8s/mocks"
	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"

	"github.com/stretchr/testify/assert"
)

func Test_checkInstance1(t *testing.T) {
	nodecap.SetNodeCapabilities("erdma", "true")

	type args struct {
		limit      *client.Limits
		daemonMode string
		config     *daemon.Config
	}
	tests := []struct {
		name     string
		args     args
		v4       bool
		v6       bool
		trunking bool
		erdma    bool
	}{
		{
			name: "unsupported instance",
			args: args{
				limit:      &client.Limits{},
				daemonMode: "ENIMultiIP",
				config: &daemon.Config{
					IPStack:           "dual",
					EnableENITrunking: true,
					EnableERDMA:       true,
				},
			},
			v4:       true,
			v6:       false,
			trunking: false,
			erdma:    false,
		},
		{
			name: "supported instance",
			args: args{
				limit: &client.Limits{
					Adapters:              10,
					TotalAdapters:         15,
					IPv4PerAdapter:        10,
					IPv6PerAdapter:        10,
					MemberAdapterLimit:    10,
					MaxMemberAdapterLimit: 10,
					ERdmaAdapters:         2,
				},
				daemonMode: "ENIMultiIP",
				config: &daemon.Config{
					IPStack:           "dual",
					EnableENITrunking: true,
					EnableERDMA:       true,
				},
			},
			v4:       true,
			v6:       true,
			trunking: true,
			erdma:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := checkInstance(tt.args.limit, tt.args.daemonMode, tt.args.config)
			assert.Equalf(t, tt.v4, got, "v4(%v, %v, %v)", tt.args.limit, tt.args.daemonMode, tt.args.config)
			assert.Equalf(t, tt.v6, got1, "v6(%v, %v, %v)", tt.args.limit, tt.args.daemonMode, tt.args.config)
			assert.Equalf(t, tt.trunking, tt.args.config.EnableENITrunking, "trunking(%v, %v, %v)", tt.args.limit, tt.args.daemonMode, tt.args.config)
			assert.Equalf(t, tt.erdma, tt.args.config.EnableERDMA, "erdma(%v, %v, %v)", tt.args.limit, tt.args.daemonMode, tt.args.config)
		})
	}
}

func Test_initTrunk(t *testing.T) {
	type args struct {
		config     *daemon.Config
		poolConfig *daemon.PoolConfig
		k8sClient  *k8smocks.Kubernetes
		f          *factorymocks.Factory
	}
	tests := []struct {
		name     string
		args     args
		preStart func(args)
		want     string
		wantErr  assert.ErrorAssertionFunc
	}{
		{
			name: "empty trunk id should create new trunk",
			args: args{
				config: &daemon.Config{
					IPStack:           "dual",
					EnableENITrunking: true,
					EnableERDMA:       true,
				},
				poolConfig: &daemon.PoolConfig{
					MaxENI: 2,
				},
				k8sClient: k8smocks.NewKubernetes(t),
				f:         factorymocks.NewFactory(t),
			},
			preStart: func(args args) {
				args.k8sClient.On("GetTrunkID").Return("")
				args.f.On("CreateNetworkInterface", 1, 0, "trunk").Return(&daemon.ENI{
					ID:               "eni-1",
					MAC:              "",
					SecurityGroupIDs: nil,
					Trunk:            true,
					ERdma:            false,
					PrimaryIP:        types.IPSet{},
					GatewayIP:        types.IPSet{},
					VSwitchCIDR:      types.IPNetSet{},
					VSwitchID:        "",
				}, []netip.Addr{}, []netip.Addr{}, nil)
				args.f.On("GetAttachedNetworkInterface", "").Return([]*daemon.ENI{
					{
						ID:               "eni-1",
						MAC:              "",
						SecurityGroupIDs: nil,
						Trunk:            false,
						ERdma:            false,
						PrimaryIP:        types.IPSet{},
						GatewayIP:        types.IPSet{},
						VSwitchCIDR:      types.IPNetSet{},
						VSwitchID:        "",
					},
				}, nil)
			},
			want:    "eni-1",
			wantErr: assert.NoError,
		}, {
			name: "reuse exist trunk eni",
			args: args{
				config: &daemon.Config{
					IPStack:           "dual",
					EnableENITrunking: true,
					EnableERDMA:       true,
				},
				poolConfig: &daemon.PoolConfig{
					MaxENI: 2,
				},
				k8sClient: k8smocks.NewKubernetes(t),
				f:         factorymocks.NewFactory(t),
			},
			preStart: func(args args) {
				args.k8sClient.On("GetTrunkID").Return("")
				args.f.On("GetAttachedNetworkInterface", "").Return([]*daemon.ENI{
					{
						ID:               "eni-1",
						MAC:              "",
						SecurityGroupIDs: nil,
						Trunk:            true,
						ERdma:            false,
						PrimaryIP:        types.IPSet{},
						GatewayIP:        types.IPSet{},
						VSwitchCIDR:      types.IPNetSet{},
						VSwitchID:        "",
					},
				}, nil)
			},
			want:    "eni-1",
			wantErr: assert.NoError,
		}, {
			name: "disable trunk if can not create more",
			args: args{
				config: &daemon.Config{
					IPStack:           "dual",
					EnableENITrunking: true,
					EnableERDMA:       true,
				},
				poolConfig: &daemon.PoolConfig{
					MaxENI: 2,
				},
				k8sClient: k8smocks.NewKubernetes(t),
				f:         factorymocks.NewFactory(t),
			},
			preStart: func(args args) {
				args.k8sClient.On("GetTrunkID").Return("")
				args.f.On("GetAttachedNetworkInterface", "").Return([]*daemon.ENI{
					{
						ID: "eni-1",
					},
					{
						ID: "eni-2",
					},
				}, nil)
			},
			want:    "",
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.preStart(tt.args)

			got, err := initTrunk(tt.args.config, tt.args.poolConfig, tt.args.k8sClient, tt.args.f)
			if !tt.wantErr(t, err) {
				return
			}
			assert.Equal(t, tt.want, got)
			if got == "" {
				assert.False(t, tt.args.config.EnableENITrunking)
			}
		})
	}
}

func TestGetPodIPs(t *testing.T) {
	tests := []struct {
		name     string
		netConfs []*rpc.NetConf
		expected []string
	}{
		{
			name: "SingleNetConfWithIPv4",
			netConfs: []*rpc.NetConf{
				{
					IfName: "eth0",
					BasicInfo: &rpc.BasicInfo{
						PodIP: &rpc.IPSet{
							IPv4: "10.0.0.1",
						},
					},
				},
			},
			expected: []string{"10.0.0.1"},
		},
		{
			name: "SingleNetConfWithIPv6",
			netConfs: []*rpc.NetConf{
				{
					IfName: "eth0",
					BasicInfo: &rpc.BasicInfo{
						PodIP: &rpc.IPSet{
							IPv6: "fe80::1",
						},
					},
				},
			},
			expected: []string{"fe80::1"},
		},
		{
			name: "MultipleNetConfsWithIPv4AndIPv6",
			netConfs: []*rpc.NetConf{
				{
					IfName: "eth0",
					BasicInfo: &rpc.BasicInfo{
						PodIP: &rpc.IPSet{
							IPv4: "10.0.0.1",
							IPv6: "fe80::1",
						},
					},
				},
				{
					IfName: "eth0",
					BasicInfo: &rpc.BasicInfo{
						PodIP: &rpc.IPSet{
							IPv4: "10.0.0.2",
						},
					},
				},
			},
			expected: []string{"10.0.0.1", "fe80::1", "10.0.0.2"},
		},
		{
			name: "WithNonDefaultIfName",
			netConfs: []*rpc.NetConf{
				{
					IfName: "eth1",
					BasicInfo: &rpc.BasicInfo{
						PodIP: &rpc.IPSet{
							IPv4: "10.0.0.1",
						},
					},
				},
			},
			expected: []string{},
		},
		{
			name: "WithoutBasicInfo",
			netConfs: []*rpc.NetConf{
				{
					IfName:    "eth0",
					BasicInfo: nil,
				},
			},
			expected: []string{},
		},
		{
			name: "WithoutPodIP",
			netConfs: []*rpc.NetConf{
				{
					IfName: "eth0",
					BasicInfo: &rpc.BasicInfo{
						PodIP: nil,
					},
				},
			},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getPodIPs(tt.netConfs)
			if len(got) != len(tt.expected) {
				t.Errorf("getPodIPs() = %v, want %v", got, tt.expected)
			}

			for i, ip := range got {
				if ip != tt.expected[i] {
					t.Errorf("getPodIPs()[%d] = %v, want %v", i, ip, tt.expected[i])
				}
			}
		})
	}
}

func TestFilterENINotFound(t *testing.T) {
	tests := []struct {
		name          string
		podResources  []daemon.PodResources
		attachedENIID map[string]*daemon.ENI
		expect        []daemon.PodResources
	}{
		{
			name: "ENI exists by ENIID",
			podResources: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{Type: daemon.ResourceTypeENIIP, ENIID: "eni-1", ID: "eni-1.10.0.0.2"},
					},
				},
			},
			attachedENIID: map[string]*daemon.ENI{
				"eni-1": {ID: "eni-1", MAC: "02:11:22:33:44:55"},
			},
			expect: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{Type: daemon.ResourceTypeENIIP, ENIID: "eni-1", ID: "eni-1.10.0.0.2"},
					},
				},
			},
		},
		{
			name: "ENI not exists by ENIID",
			podResources: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{Type: daemon.ResourceTypeENIIP, ENIID: "eni-2", ID: "eni-2.10.0.0.3"},
					},
				},
			},
			attachedENIID: map[string]*daemon.ENI{
				"eni-1": {ID: "eni-1", MAC: "02:11:22:33:44:55"},
			},
			expect: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{},
				},
			},
		},
		{
			name: "ENI exists by MAC (ENIID empty)",
			podResources: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{Type: daemon.ResourceTypeENIIP, ENIID: "", ID: "02:11:22:33:44:55.10.0.0.4"},
					},
				},
			},
			attachedENIID: map[string]*daemon.ENI{
				"eni-3": {ID: "eni-3", MAC: "02:11:22:33:44:55"},
			},
			expect: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{Type: daemon.ResourceTypeENIIP, ENIID: "", ID: "02:11:22:33:44:55.10.0.0.4"},
					},
				},
			},
		},
		{
			name: "ENI not exists by MAC (ENIID empty)",
			podResources: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{Type: daemon.ResourceTypeENIIP, ENIID: "", ID: "02:11:22:33:44:99.10.0.0.5"},
					},
				},
			},
			attachedENIID: map[string]*daemon.ENI{
				"eni-4": {ID: "eni-4", MAC: "02:11:22:33:44:55"},
			},
			expect: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{},
				},
			},
		},
		{
			name: "Resource type not ENIIP should not be filtered",
			podResources: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{Type: "otherType", ENIID: "eni-1", ID: "eni-1.10.0.0.6"},
					},
				},
			},
			attachedENIID: map[string]*daemon.ENI{},
			expect: []daemon.PodResources{
				{
					Resources: []daemon.ResourceItem{
						{Type: "otherType", ENIID: "eni-1", ID: "eni-1.10.0.0.6"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterENINotFound(tt.podResources, tt.attachedENIID)
			if len(got) != len(tt.expect) {
				t.Errorf("length mismatch: got %v, want %v", got, tt.expect)
				return
			}
			for i := range got {
				if len(got[i].Resources) != len(tt.expect[i].Resources) {
					t.Errorf("resources length mismatch: got %v, want %v", got[i].Resources, tt.expect[i].Resources)
					continue
				}
				for j := range got[i].Resources {
					if got[i].Resources[j] != tt.expect[i].Resources[j] {
						t.Errorf("resource mismatch at [%d][%d]: got %+v, want %+v", i, j, got[i].Resources[j], tt.expect[i].Resources[j])
					}
				}
			}
		})
	}
}

func TestDefaultForNetConf(t *testing.T) {
	t.Run("empty netConf", func(t *testing.T) {
		err := defaultForNetConf([]*rpc.NetConf{})
		if err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})
	t.Run("default interface not set", func(t *testing.T) {
		netConfs := []*rpc.NetConf{{IfName: "eth1"}}
		err := defaultForNetConf(netConfs)
		if err == nil || err.Error() != "default interface is not set" {
			t.Errorf("expected default interface is not set, got %v", err)
		}
	})
	t.Run("duplicate default route", func(t *testing.T) {
		netConfs := []*rpc.NetConf{{IfName: "eth0", DefaultRoute: true}, {IfName: "eth0", DefaultRoute: true}}
		err := defaultForNetConf(netConfs)
		if err == nil || err.Error() != "default route is dumplicated" {
			t.Errorf("expected default route is dumplicated, got %v", err)
		}
	})
	t.Run("set default route if not set", func(t *testing.T) {
		netConfs := []*rpc.NetConf{{IfName: "eth0"}}
		err := defaultForNetConf(netConfs)
		if err != nil {
			t.Errorf("expected nil, got %v", err)
		}
		if !netConfs[0].DefaultRoute {
			t.Errorf("expected DefaultRoute to be true")
		}
	})
}

func TestToRPCMapping(t *testing.T) {
	res := eni.Status{
		NetworkInterfaceID:   "eni-1",
		MAC:                  "02:11:22:33:44:55",
		Type:                 "eni",
		AllocInhibitExpireAt: "2024-01-01T00:00:00Z",
		Status:               "InUse",
		Usage:                [][]string{{"pod1", "10.0.0.1"}, {"pod2", "10.0.0.2"}},
	}
	mapping := toRPCMapping(res)
	if mapping.NetworkInterfaceID != "eni-1" || mapping.MAC != "02:11:22:33:44:55" || mapping.Type != "eni" || mapping.AllocInhibitExpireAt != "2024-01-01T00:00:00Z" || mapping.Status != "InUse" {
		t.Errorf("unexpected mapping fields: %+v", mapping)
	}
	if len(mapping.Info) != 2 || mapping.Info[0] != "pod1  10.0.0.1" || mapping.Info[1] != "pod2  10.0.0.2" {
		t.Errorf("unexpected mapping.Info: %+v", mapping.Info)
	}
}

func TestSetRequestAndExtractIPs(t *testing.T) {
	item := daemon.ResourceItem{
		ENIID: "eni-1",
		IPv4:  "10.0.0.1",
		IPv6:  "fe80::1",
	}
	req := eni.NewLocalIPRequest()
	setRequest(req, item)
	if req.NetworkInterfaceID != "eni-1" {
		t.Errorf("expected NetworkInterfaceID=eni-1, got %s", req.NetworkInterfaceID)
	}
	if req.IPv4.String() != "10.0.0.1" {
		t.Errorf("expected IPv4=10.0.0.1, got %s", req.IPv4.String())
	}
	if req.IPv6.String() != "fe80::1" {
		t.Errorf("expected IPv6=fe80::1, got %s", req.IPv6.String())
	}

	v4, v6, eniID := extractIPs(item)
	if v4.String() != "10.0.0.1" || v6.String() != "fe80::1" || eniID != "eni-1" {
		t.Errorf("extractIPs got %s, %s, %s", v4.String(), v6.String(), eniID)
	}
}

func TestParseNetworkResource(t *testing.T) {
	item := daemon.ResourceItem{
		Type:   daemon.ResourceTypeENIIP,
		ENIID:  "eni-1",
		ENIMAC: "02:11:22:33:44:55",
		IPv4:   "10.0.0.1",
		IPv6:   "fe80::1",
	}
	res := parseNetworkResource(item)
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	local, ok := res.(*eni.LocalIPResource)
	if !ok {
		t.Fatalf("expected *eni.LocalIPResource, got %T", res)
	}
	if local.ENI.ID != "eni-1" || local.ENI.MAC != "02:11:22:33:44:55" {
		t.Errorf("unexpected ENI: %+v", local.ENI)
	}
	if local.IP.IPv4.String() != "10.0.0.1" || local.IP.IPv6.String() != "fe80::1" {
		t.Errorf("unexpected IP: %+v", local.IP)
	}

	item.Type = "otherType"
	if parseNetworkResource(item) != nil {
		t.Errorf("expected nil for non-ENIIP/ENI type")
	}
}

func TestVerifyPodNetworkType(t *testing.T) {
	service := &networkService{
		daemonMode: daemon.ModeENIMultiIP,
	}

	tests := []struct {
		name           string
		podNetworkMode string
		daemonMode     string
		expected       bool
	}{
		{
			name:           "ENIMultiIP mode with ENIMultiIP pod",
			podNetworkMode: daemon.ModeENIMultiIP,
			daemonMode:     daemon.ModeENIMultiIP,
			expected:       true,
		},
		{
			name:           "ENIMultiIP mode with VPC pod",
			podNetworkMode: daemon.ModeVPC,
			daemonMode:     daemon.ModeENIMultiIP,
			expected:       true,
		},
		{
			name:           "ENIOnly mode with ENIOnly pod",
			podNetworkMode: daemon.ModeENIOnly,
			daemonMode:     daemon.ModeENIOnly,
			expected:       true,
		},
		{
			name:           "VPC mode with VPC pod",
			podNetworkMode: daemon.ModeVPC,
			daemonMode:     daemon.ModeVPC,
			expected:       true,
		},
		{
			name:           "Mismatched modes",
			podNetworkMode: daemon.ModeENIOnly,
			daemonMode:     daemon.ModeVPC,
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service.daemonMode = tt.daemonMode
			result := service.verifyPodNetworkType(tt.podNetworkMode)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDefaultIf(t *testing.T) {
	tests := []struct {
		name     string
		ifName   string
		expected bool
	}{
		{
			name:     "eth0 interface",
			ifName:   "eth0",
			expected: true,
		},
		{
			name:     "eth1 interface",
			ifName:   "eth1",
			expected: false,
		},
		{
			name:     "lo interface",
			ifName:   "lo",
			expected: false,
		},
		{
			name:     "empty interface",
			ifName:   "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := defaultIf(tt.ifName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetPodResources(t *testing.T) {
	tests := []struct {
		name     string
		input    []interface{}
		expected []daemon.PodResources
	}{
		{
			name:     "Empty list",
			input:    []interface{}{},
			expected: []daemon.PodResources{},
		},
		{
			name: "Valid pod resources",
			input: []interface{}{
				daemon.PodResources{
					PodInfo: &daemon.PodInfo{
						Namespace: "default",
						Name:      "test-pod",
					},
					Resources: []daemon.ResourceItem{
						{
							Type:  daemon.ResourceTypeENIIP,
							ENIID: "eni-1",
							IPv4:  "10.0.0.1",
						},
					},
				},
			},
			expected: []daemon.PodResources{
				{
					PodInfo: &daemon.PodInfo{
						Namespace: "default",
						Name:      "test-pod",
					},
					Resources: []daemon.ResourceItem{
						{
							Type:  daemon.ResourceTypeENIIP,
							ENIID: "eni-1",
							IPv4:  "10.0.0.1",
						},
					},
				},
			},
		},
		{
			name: "Mixed valid and invalid types",
			input: []interface{}{
				daemon.PodResources{
					PodInfo: &daemon.PodInfo{
						Namespace: "default",
						Name:      "test-pod",
					},
				},
				"invalid_type",
				123,
			},
			expected: []daemon.PodResources{
				{
					PodInfo: &daemon.PodInfo{
						Namespace: "default",
						Name:      "test-pod",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getPodResources(tt.input)
			assert.Equal(t, len(tt.expected), len(result))
			for i, expected := range tt.expected {
				if i < len(result) {
					assert.Equal(t, expected, result[i])
				}
			}
		})
	}
}

func TestCheckInstance_EdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		limit      *client.Limits
		daemonMode string
		config     *daemon.Config
		expectV4   bool
		expectV6   bool
	}{
		{
			name: "IPv4 only stack",
			limit: &client.Limits{
				Adapters:       10,
				IPv4PerAdapter: 10,
				IPv6PerAdapter: 0,
			},
			daemonMode: daemon.ModeENIMultiIP,
			config: &daemon.Config{
				IPStack: "ipv4",
			},
			expectV4: true,
			expectV6: false,
		},
		{
			name: "IPv6 only stack",
			limit: &client.Limits{
				Adapters:       10,
				IPv4PerAdapter: 0,
				IPv6PerAdapter: 10,
			},
			daemonMode: daemon.ModeENIMultiIP,
			config: &daemon.Config{
				IPStack: "ipv6",
			},
			expectV4: false,
			expectV6: true,
		},
		{
			name: "No adapters available",
			limit: &client.Limits{
				Adapters: 0,
			},
			daemonMode: daemon.ModeENIMultiIP,
			config: &daemon.Config{
				IPStack: "dual",
			},
			expectV4: false,
			expectV6: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v4, v6 := checkInstance(tt.limit, tt.daemonMode, tt.config)
			assert.Equal(t, tt.expectV4, v4)
			assert.Equal(t, tt.expectV6, v6)
		})
	}
}

func TestRunDevicePlugin(t *testing.T) {
	// Test that runDevicePlugin doesn't panic with various configurations
	tests := []struct {
		name       string
		daemonMode string
		config     *daemon.Config
		poolConfig *daemon.PoolConfig
	}{
		{
			name:       "ENIMultiIP mode",
			daemonMode: daemon.ModeENIMultiIP,
			config: &daemon.Config{
				EnableENITrunking: false,
			},
			poolConfig: &daemon.PoolConfig{
				MaxENI: 5,
			},
		},
		{
			name:       "ENIOnly mode",
			daemonMode: daemon.ModeENIOnly,
			config: &daemon.Config{
				EnableENITrunking: false,
			},
			poolConfig: &daemon.PoolConfig{
				MaxENI: 3,
			},
		},
		{
			name:       "VPC mode",
			daemonMode: daemon.ModeVPC,
			config: &daemon.Config{
				EnableENITrunking: false,
			},
			poolConfig: &daemon.PoolConfig{
				MaxENI: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This function should not panic
			assert.NotPanics(t, func() {
				runDevicePlugin(tt.daemonMode, tt.config, tt.poolConfig)
			})
		})
	}
}
