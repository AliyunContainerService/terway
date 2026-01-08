//go:build privileged

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"

	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/rpc"
	terwayTypes "github.com/AliyunContainerService/terway/types"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/containernetworking/cni/pkg/skel"
	cniTypes "github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
	"google.golang.org/grpc"
)

func TestParseSetupConf(t *testing.T) {
	ctx := context.Background()
	args := &skel.CmdArgs{
		IfName: "eth0",
	}

	netns, err := testutils.NewNS()
	require.NoError(t, err)
	defer func() {
		err := netns.Close()
		require.NoError(t, err)

		err = testutils.UnmountNS(netns)
		require.NoError(t, err)
	}()

	var dummy *netlink.Dummy
	err = netns.Do(func(ns ns.NetNS) error {
		dummy = &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{
				Name: "eth0",
			},
		}
		err = netlink.LinkAdd(dummy)

		return err
	})
	require.NoError(t, err)

	conf := &types.CNIConf{
		NetConf: cniTypes.NetConf{
			CNIVersion: "0.4.0",
			Name:       "terway",
			Type:       "terway",
		},
		MTU: 1500,
	}

	t.Run("TypeVPCIP configuration", func(t *testing.T) {
		alloc := &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				ServiceCIDR: &rpc.IPSet{
					IPv4: "172.16.0.0/12",
				},
				PodCIDR: &rpc.IPSet{
					IPv4: "192.168.1.0/24",
				},
			},
		}

		setupConfig, err := parseSetupConf(ctx, args, alloc, conf, rpc.IPType_TypeVPCIP)
		assert.NoError(t, err)
		assert.NotNil(t, setupConfig)
		assert.Equal(t, types.VPCRoute, setupConfig.DP)
		assert.Equal(t, "eth0", setupConfig.ContainerIfName)
		assert.Equal(t, 1500, setupConfig.MTU)
		assert.NotNil(t, setupConfig.ContainerIPNet)
		assert.NotNil(t, setupConfig.ContainerIPNet.IPv4)
		assert.Nil(t, setupConfig.ContainerIPNet.IPv6)
		assert.Nil(t, setupConfig.GatewayIP)
	})

	t.Run("TypeENIMultiIP configuration with ENI info", func(t *testing.T) {
		alloc := &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				ServiceCIDR: &rpc.IPSet{
					IPv4: "172.16.0.0/12",
				},
				PodIP: &rpc.IPSet{
					IPv4: "192.168.1.10",
				},
				PodCIDR: &rpc.IPSet{
					IPv4: "192.168.1.0/24",
				},
				GatewayIP: &rpc.IPSet{
					IPv4: "192.168.1.1",
				},
			},
			ENIInfo: &rpc.ENIInfo{
				MAC:   dummy.Attrs().HardwareAddr.String(),
				Trunk: false,
				Vid:   0,
			},
		}

		netns.Do(func(ns ns.NetNS) error {
			setupConfig, err := parseSetupConf(ctx, args, alloc, conf, rpc.IPType_TypeENIMultiIP)
			assert.NoError(t, err)
			assert.NotNil(t, setupConfig)
			assert.Equal(t, types.IPVlan, setupConfig.DP)
			assert.Equal(t, "eth0", setupConfig.ContainerIfName)
			assert.Equal(t, 1500, setupConfig.MTU)
			assert.NotNil(t, setupConfig.ContainerIPNet)
			assert.NotNil(t, setupConfig.GatewayIP)
			return err
		})

	})

	t.Run("TypeVPCENI configuration with trunk ENI", func(t *testing.T) {
		alloc := &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				ServiceCIDR: &rpc.IPSet{
					IPv4: "172.16.0.0/12",
				},
				PodIP: &rpc.IPSet{
					IPv4: "192.168.1.10",
				},
				PodCIDR: &rpc.IPSet{
					IPv4: "192.168.1.0/24",
				},
				GatewayIP: &rpc.IPSet{
					IPv4: "192.168.1.1",
				},
			},
			ENIInfo: &rpc.ENIInfo{
				MAC:   dummy.Attrs().HardwareAddr.String(),
				Trunk: true,
				Vid:   100,
			},
		}

		confWithVlanStripType := &types.CNIConf{
			NetConf:       conf.NetConf,
			MTU:           1500,
			VlanStripType: "vlan",
		}

		netns.Do(func(ns ns.NetNS) error {
			setupConfig, err := parseSetupConf(ctx, args, alloc, confWithVlanStripType, rpc.IPType_TypeVPCENI)
			assert.NoError(t, err)
			assert.NotNil(t, setupConfig)
			assert.Equal(t, types.Vlan, setupConfig.DP)
			assert.True(t, setupConfig.StripVlan)
			assert.Equal(t, 100, setupConfig.Vid)
			return nil
		})

	})

	t.Run("Configuration with extra routes", func(t *testing.T) {
		alloc := &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				ServiceCIDR: &rpc.IPSet{
					IPv4: "172.16.0.0/12",
				},
				PodIP: &rpc.IPSet{
					IPv4: "192.168.1.10",
				},
				PodCIDR: &rpc.IPSet{
					IPv4: "192.168.1.0/24",
				},
				GatewayIP: &rpc.IPSet{
					IPv4: "192.168.1.1",
				},
			},
			ExtraRoutes: []*rpc.Route{
				{
					Dst: "10.0.0.0/8",
				},
			},
		}

		setupConfig, err := parseSetupConf(ctx, args, alloc, conf, rpc.IPType_TypeENIMultiIP)
		assert.NoError(t, err)
		assert.NotNil(t, setupConfig)
		assert.Len(t, setupConfig.ExtraRoutes, 1)
	})

	t.Run("Invalid subnet in TypeVPCIP configuration", func(t *testing.T) {
		alloc := &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				ServiceCIDR: &rpc.IPSet{
					IPv4: "172.16.0.0/12",
				},
				PodCIDR: &rpc.IPSet{
					IPv4: "invalid-subnet",
				},
			},
		}

		setupConfig, err := parseSetupConf(ctx, args, alloc, conf, rpc.IPType_TypeVPCIP)
		assert.Error(t, err)
		assert.Nil(t, setupConfig)
	})

	t.Run("TypeENIMultiIP configuration with VfId", func(t *testing.T) {
		vfID := uint32(1)
		alloc := &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				ServiceCIDR: &rpc.IPSet{
					IPv4: "172.16.0.0/12",
				},
				PodIP: &rpc.IPSet{
					IPv4: "192.168.1.10",
				},
				PodCIDR: &rpc.IPSet{
					IPv4: "192.168.1.0/24",
				},
				GatewayIP: &rpc.IPSet{
					IPv4: "192.168.1.1",
				},
			},
			ENIInfo: &rpc.ENIInfo{
				MAC:   "00:11:22:33:44:55",
				VfId:  &vfID,
				Trunk: false,
			},
		}

		// This test verifies that when VfId is set, prepareVF is called
		// Since prepareVF requires privileged access and VF setup,
		// it will fail in test environment, confirming the code path is executed
		setupConfig, err := parseSetupConf(ctx, args, alloc, conf, rpc.IPType_TypeENIMultiIP)
		// prepareVF will fail in test environment, so we expect an error
		// This confirms the code path that calls prepareVF is executed
		assert.Error(t, err)
		assert.Nil(t, setupConfig)
	})
}

func TestParseTearDownConf(t *testing.T) {
	conf := &types.CNIConf{
		NetConf: cniTypes.NetConf{
			CNIVersion: "0.4.0",
			Name:       "terway",
			Type:       "terway",
		},
		MTU: 1500,
	}

	t.Run("Valid teardown configuration", func(t *testing.T) {
		alloc := &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				ServiceCIDR: &rpc.IPSet{
					IPv4: "172.16.0.0/12",
				},
				PodIP: &rpc.IPSet{
					IPv4: "192.168.1.10",
				},
				PodCIDR: &rpc.IPSet{
					IPv4: "192.168.1.0/24",
				},
			},
		}

		tearDownCfg, err := parseTearDownConf(alloc, conf, rpc.IPType_TypeENIMultiIP)
		assert.NoError(t, err)
		assert.NotNil(t, tearDownCfg)
		assert.Equal(t, types.IPVlan, tearDownCfg.DP)
		assert.NotNil(t, tearDownCfg.ContainerIPNet)
		assert.NotNil(t, tearDownCfg.ServiceCIDR)
	})

	t.Run("TypeVPCIP teardown configuration", func(t *testing.T) {
		alloc := &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				ServiceCIDR: &rpc.IPSet{
					IPv4: "172.16.0.0/12",
				},
				PodCIDR: &rpc.IPSet{
					IPv4: "192.168.1.0/24",
				},
			},
		}

		tearDownCfg, err := parseTearDownConf(alloc, conf, rpc.IPType_TypeVPCIP)
		assert.NoError(t, err)
		assert.NotNil(t, tearDownCfg)
		assert.Equal(t, types.VPCRoute, tearDownCfg.DP)
		assert.NotNil(t, tearDownCfg.ContainerIPNet)
		assert.NotNil(t, tearDownCfg.ContainerIPNet.IPv4)
	})

	t.Run("Missing basic info", func(t *testing.T) {
		alloc := &rpc.NetConf{}

		tearDownCfg, err := parseTearDownConf(alloc, conf, rpc.IPType_TypeENIMultiIP)
		assert.Error(t, err)
		assert.Nil(t, tearDownCfg)
		assert.Contains(t, err.Error(), "return empty pod alloc info")
	})
}

func TestParseCheckConf(t *testing.T) {
	args := &skel.CmdArgs{
		IfName: "eth0",
	}

	netns, err := testutils.NewNS()
	require.NoError(t, err)
	defer func() {
		err := netns.Close()
		require.NoError(t, err)

		err = testutils.UnmountNS(netns)
		require.NoError(t, err)
	}()

	var dummy *netlink.Dummy
	err = netns.Do(func(ns ns.NetNS) error {
		dummy = &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{
				Name: "eth0",
			},
		}
		err = netlink.LinkAdd(dummy)

		return err
	})
	require.NoError(t, err)

	conf := &types.CNIConf{
		NetConf: cniTypes.NetConf{
			CNIVersion: "0.4.0",
			Name:       "terway",
			Type:       "terway",
		},
		MTU:           1500,
		VlanStripType: "vlan",
	}

	t.Run("Valid check configuration", func(t *testing.T) {
		alloc := &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				PodIP: &rpc.IPSet{
					IPv4: "192.168.1.10",
				},
				PodCIDR: &rpc.IPSet{
					IPv4: "192.168.1.0/24",
				},
				GatewayIP: &rpc.IPSet{
					IPv4: "192.168.1.1",
				},
			},
			ENIInfo: &rpc.ENIInfo{
				MAC:   dummy.Attrs().HardwareAddr.String(),
				Trunk: false,
			},
		}

		netns.Do(func(netNS ns.NetNS) error {
			checkConfig, err := parseCheckConf(args, alloc, conf, rpc.IPType_TypeENIMultiIP)
			assert.NoError(t, err)
			assert.NotNil(t, checkConfig)
			assert.Equal(t, types.IPVlan, checkConfig.DP)
			assert.Equal(t, "eth0", checkConfig.ContainerIfName)
			assert.Equal(t, 1500, checkConfig.MTU)
			assert.NotNil(t, checkConfig.ContainerIPNet)
			assert.NotNil(t, checkConfig.GatewayIP)

			return nil
		})

	})

	t.Run("Trunk ENI check configuration", func(t *testing.T) {
		alloc := &rpc.NetConf{
			BasicInfo: &rpc.BasicInfo{
				PodIP: &rpc.IPSet{
					IPv4: "192.168.1.10",
				},
				PodCIDR: &rpc.IPSet{
					IPv4: "192.168.1.0/24",
				},
				GatewayIP: &rpc.IPSet{
					IPv4: "192.168.1.1",
				},
			},
			ENIInfo: &rpc.ENIInfo{
				MAC:   dummy.Attrs().HardwareAddr.String(),
				Trunk: true,
			},
		}

		netns.Do(func(netNS ns.NetNS) error {
			checkConfig, err := parseCheckConf(args, alloc, conf, rpc.IPType_TypeVPCENI)
			assert.NoError(t, err)
			assert.NotNil(t, checkConfig)
			assert.Equal(t, types.Vlan, checkConfig.DP)
			assert.True(t, checkConfig.TrunkENI)

			return nil
		})

	})
}

func TestGetDatePath(t *testing.T) {
	testCases := []struct {
		name          string
		ipType        rpc.IPType
		vlanStripType types.VlanStripType
		trunk         bool
		expectedDP    types.DataPath
		shouldPanic   bool
	}{
		{
			name:        "TypeVPCIP",
			ipType:      rpc.IPType_TypeVPCIP,
			expectedDP:  types.VPCRoute,
			shouldPanic: false,
		},
		{
			name:        "TypeVPCENI non-trunk",
			ipType:      rpc.IPType_TypeVPCENI,
			trunk:       false,
			expectedDP:  types.ExclusiveENI,
			shouldPanic: false,
		},
		{
			name:        "TypeVPCENI trunk",
			ipType:      rpc.IPType_TypeVPCENI,
			trunk:       true,
			expectedDP:  types.Vlan,
			shouldPanic: false,
		},
		{
			name:          "TypeENIMultiIP non-trunk",
			ipType:        rpc.IPType_TypeENIMultiIP,
			trunk:         false,
			vlanStripType: types.VlanStripTypeVlan,
			expectedDP:    types.IPVlan,
			shouldPanic:   false,
		},
		{
			name:          "TypeENIMultiIP trunk with vlan strip",
			ipType:        rpc.IPType_TypeENIMultiIP,
			trunk:         true,
			vlanStripType: types.VlanStripTypeVlan,
			expectedDP:    types.Vlan,
			shouldPanic:   false,
		},
		{
			name:          "TypeENIMultiIP trunk with filter strip",
			ipType:        rpc.IPType_TypeENIMultiIP,
			trunk:         true,
			vlanStripType: types.VlanStripTypeFilter,
			expectedDP:    types.IPVlan,
			shouldPanic:   false,
		},
		{
			name:        "Unsupported IP type",
			ipType:      rpc.IPType(999),
			shouldPanic: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.shouldPanic {
				assert.Panics(t, func() {
					getDatePath(tc.ipType, tc.vlanStripType, tc.trunk)
				})
			} else {
				dp := getDatePath(tc.ipType, tc.vlanStripType, tc.trunk)
				assert.Equal(t, tc.expectedDP, dp)
			}
		})
	}
}

func TestCmdAdd(t *testing.T) {
	tests := []struct {
		name          string
		setupArgs     func() *skel.CmdArgs
		setupMocks    func() *gomonkey.Patches
		expectedError bool
		errorContains string
	}{
		{
			name: "Success case",
			setupArgs: func() *skel.CmdArgs {
				netns, _ := testutils.NewNS()
				conf := &types.CNIConf{
					NetConf: cniTypes.NetConf{
						CNIVersion: "0.4.0",
						Name:       "terway",
						Type:       "terway",
					},
					MTU: 1500,
				}
				confData, _ := json.Marshal(conf)
				return &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       netns.Path(),
					IfName:      "eth0",
					StdinData:   confData,
					Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
				}
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockClient := &mockTerwayBackendClient{}
				mockConn := &grpc.ClientConn{}

				// Mock grpc.ClientConn.Close to avoid nil pointer dereference
				patches.ApplyMethod(&grpc.ClientConn{}, "Close", func() error {
					return nil
				})

				// Mock getNetworkClient
				patches.ApplyFunc(getNetworkClient, func(ctx context.Context) (rpc.TerwayBackendClient, *grpc.ClientConn, error) {
					return mockClient, mockConn, nil
				})

				// Mock doCmdAdd
				patches.ApplyFunc(doCmdAdd, func(ctx context.Context, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) (*terwayTypes.IPNetSet, *terwayTypes.IPSet, error) {
					_, ipNet, _ := net.ParseCIDR("192.168.1.10/24")
					return &terwayTypes.IPNetSet{
							IPv4: ipNet,
						}, &terwayTypes.IPSet{
							IPv4: net.ParseIP("192.168.1.1"),
						}, nil
				})

				// Mock cniTypes.PrintResult
				patches.ApplyFunc(cniTypes.PrintResult, func(obj interface{}, version string) error {
					return nil
				})

				return patches
			},
			expectedError: false,
		},
		{
			name: "getCmdArgs error",
			setupArgs: func() *skel.CmdArgs {
				return &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       "/invalid/netns",
					IfName:      "eth0",
					StdinData:   []byte(`invalid json`),
					Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
				}
			},
			setupMocks: func() *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: true,
		},
		{
			name: "getNetworkClient error",
			setupArgs: func() *skel.CmdArgs {
				netns, _ := testutils.NewNS()
				conf := &types.CNIConf{
					NetConf: cniTypes.NetConf{
						CNIVersion: "0.4.0",
						Name:       "terway",
						Type:       "terway",
					},
					MTU: 1500,
				}
				confData, _ := json.Marshal(conf)
				return &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       netns.Path(),
					IfName:      "eth0",
					StdinData:   confData,
					Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
				}
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				// Mock getNetworkClient to return error
				patches.ApplyFunc(getNetworkClient, func(ctx context.Context) (rpc.TerwayBackendClient, *grpc.ClientConn, error) {
					return nil, nil, errors.New("connection failed")
				})
				return patches
			},
			expectedError: true,
		},
		{
			name: "doCmdAdd error",
			setupArgs: func() *skel.CmdArgs {
				netns, _ := testutils.NewNS()
				conf := &types.CNIConf{
					NetConf: cniTypes.NetConf{
						CNIVersion: "0.4.0",
						Name:       "terway",
						Type:       "terway",
					},
					MTU: 1500,
				}
				confData, _ := json.Marshal(conf)
				return &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       netns.Path(),
					IfName:      "eth0",
					StdinData:   confData,
					Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
				}
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockClient := &mockTerwayBackendClient{}
				mockConn := &grpc.ClientConn{}

				// Mock grpc.ClientConn.Close to avoid nil pointer dereference
				patches.ApplyMethod(&grpc.ClientConn{}, "Close", func() error {
					return nil
				})

				// Mock getNetworkClient
				patches.ApplyFunc(getNetworkClient, func(ctx context.Context) (rpc.TerwayBackendClient, *grpc.ClientConn, error) {
					return mockClient, mockConn, nil
				})

				// Mock doCmdAdd to return error
				patches.ApplyFunc(doCmdAdd, func(ctx context.Context, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) (*terwayTypes.IPNetSet, *terwayTypes.IPSet, error) {
					return nil, nil, errors.New("failed to add")
				})

				// Mock cniTypes.PrintResult
				patches.ApplyFunc(cniTypes.PrintResult, func(obj interface{}, version string) error {
					return nil
				})

				return patches
			},
			expectedError: true,
			errorContains: "failed to do add",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.setupArgs()
			patches := tt.setupMocks()
			defer patches.Reset()

			err := cmdAdd(args)
			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCmdDel(t *testing.T) {
	tests := []struct {
		name          string
		setupArgs     func() *skel.CmdArgs
		setupMocks    func() *gomonkey.Patches
		expectedError bool
		errorContains string
	}{
		{
			name: "Empty Netns should return nil",
			setupArgs: func() *skel.CmdArgs {
				return &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       "",
					IfName:      "eth0",
				}
			},
			setupMocks: func() *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: false,
		},
		{
			name: "Success case",
			setupArgs: func() *skel.CmdArgs {
				netns, _ := testutils.NewNS()
				conf := &types.CNIConf{
					NetConf: cniTypes.NetConf{
						CNIVersion: "0.4.0",
						Name:       "terway",
						Type:       "terway",
					},
					MTU: 1500,
				}
				confData, _ := json.Marshal(conf)
				return &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       netns.Path(),
					IfName:      "eth0",
					StdinData:   confData,
					Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
				}
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockClient := &mockTerwayBackendClient{}
				mockConn := &grpc.ClientConn{}

				// Mock grpc.ClientConn.Close to avoid nil pointer dereference
				patches.ApplyMethod(&grpc.ClientConn{}, "Close", func() error {
					return nil
				})

				// Mock getNetworkClient
				patches.ApplyFunc(getNetworkClient, func(ctx context.Context) (rpc.TerwayBackendClient, *grpc.ClientConn, error) {
					return mockClient, mockConn, nil
				})

				// Mock doCmdDel
				patches.ApplyFunc(doCmdDel, func(ctx context.Context, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) error {
					return nil
				})

				// Mock cniTypes.PrintResult
				patches.ApplyFunc(cniTypes.PrintResult, func(obj interface{}, version string) error {
					return nil
				})

				return patches
			},
			expectedError: false,
		},
		{
			name: "getNetworkClient error",
			setupArgs: func() *skel.CmdArgs {
				netns, _ := testutils.NewNS()
				conf := &types.CNIConf{
					NetConf: cniTypes.NetConf{
						CNIVersion: "0.4.0",
						Name:       "terway",
						Type:       "terway",
					},
					MTU: 1500,
				}
				confData, _ := json.Marshal(conf)
				return &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       netns.Path(),
					IfName:      "eth0",
					StdinData:   confData,
					Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
				}
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				// Mock getNetworkClient to return error
				patches.ApplyFunc(getNetworkClient, func(ctx context.Context) (rpc.TerwayBackendClient, *grpc.ClientConn, error) {
					return nil, nil, errors.New("connection failed")
				})
				return patches
			},
			expectedError: true,
			errorContains: "error create grpc client",
		},
		{
			name: "doCmdDel error",
			setupArgs: func() *skel.CmdArgs {
				netns, _ := testutils.NewNS()
				conf := &types.CNIConf{
					NetConf: cniTypes.NetConf{
						CNIVersion: "0.4.0",
						Name:       "terway",
						Type:       "terway",
					},
					MTU: 1500,
				}
				confData, _ := json.Marshal(conf)
				return &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       netns.Path(),
					IfName:      "eth0",
					StdinData:   confData,
					Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
				}
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockClient := &mockTerwayBackendClient{}
				mockConn := &grpc.ClientConn{}

				// Mock grpc.ClientConn.Close to avoid nil pointer dereference
				patches.ApplyMethod(&grpc.ClientConn{}, "Close", func() error {
					return nil
				})

				// Mock getNetworkClient
				patches.ApplyFunc(getNetworkClient, func(ctx context.Context) (rpc.TerwayBackendClient, *grpc.ClientConn, error) {
					return mockClient, mockConn, nil
				})

				// Mock doCmdDel to return error
				patches.ApplyFunc(doCmdDel, func(ctx context.Context, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) error {
					return errors.New("failed to delete")
				})

				// Mock cniTypes.PrintResult
				patches.ApplyFunc(cniTypes.PrintResult, func(obj interface{}, version string) error {
					return nil
				})

				return patches
			},
			expectedError: true,
			errorContains: "failed to do del",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.setupArgs()
			patches := tt.setupMocks()
			defer patches.Reset()

			err := cmdDel(args)
			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCmdCheck(t *testing.T) {
	tests := []struct {
		name          string
		setupArgs     func() *skel.CmdArgs
		setupMocks    func() *gomonkey.Patches
		expectedError bool
		errorContains string
	}{
		{
			name: "Empty Netns should return nil",
			setupArgs: func() *skel.CmdArgs {
				return &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       "",
					IfName:      "eth0",
				}
			},
			setupMocks: func() *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectedError: false,
		},
		{
			name: "Success case",
			setupArgs: func() *skel.CmdArgs {
				netns, _ := testutils.NewNS()
				conf := &types.CNIConf{
					NetConf: cniTypes.NetConf{
						CNIVersion: "0.4.0",
						Name:       "terway",
						Type:       "terway",
					},
					MTU: 1500,
				}
				confData, _ := json.Marshal(conf)
				return &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       netns.Path(),
					IfName:      "eth0",
					StdinData:   confData,
					Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
				}
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockClient := &mockTerwayBackendClient{}
				mockConn := &grpc.ClientConn{}

				// Mock grpc.ClientConn.Close to avoid nil pointer dereference
				patches.ApplyMethod(&grpc.ClientConn{}, "Close", func() error {
					return nil
				})

				// Mock getNetworkClient
				patches.ApplyFunc(getNetworkClient, func(ctx context.Context) (rpc.TerwayBackendClient, *grpc.ClientConn, error) {
					return mockClient, mockConn, nil
				})

				// Mock doCmdCheck
				patches.ApplyFunc(doCmdCheck, func(ctx context.Context, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) error {
					return nil
				})

				return patches
			},
			expectedError: false,
		},
		{
			name: "getCmdArgs error with NSPathNotExist",
			setupArgs: func() *skel.CmdArgs {
				conf := &types.CNIConf{
					NetConf: cniTypes.NetConf{
						CNIVersion: "0.4.0",
						Name:       "terway",
						Type:       "terway",
					},
					MTU: 1500,
				}
				confData, _ := json.Marshal(conf)
				return &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       "/invalid/netns/path",
					IfName:      "eth0",
					StdinData:   confData,
					Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
				}
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				// Mock getCmdArgs to return an error that will be checked by isNSPathNotExist
				// We'll use a custom error type that implements the interface
				patches.ApplyFunc(getCmdArgs, func(args *skel.CmdArgs) (*cniCmdArgs, error) {
					// Return an error that will be caught by isNSPathNotExist check
					// Since we can't easily create NSPathNotExistErr, we'll mock getCmdArgs to return nil
					// and let the actual getCmdArgs be called, which will fail for invalid path
					return nil, errors.New("invalid netns path")
				})
				// Mock isNSPathNotExist to return true for this test
				patches.ApplyFunc(isNSPathNotExist, func(err error) bool {
					return err != nil && err.Error() == "invalid netns path"
				})
				return patches
			},
			expectedError: false, // Should return nil for NSPathNotExist
		},
		{
			name: "getNetworkClient error",
			setupArgs: func() *skel.CmdArgs {
				netns, _ := testutils.NewNS()
				conf := &types.CNIConf{
					NetConf: cniTypes.NetConf{
						CNIVersion: "0.4.0",
						Name:       "terway",
						Type:       "terway",
					},
					MTU: 1500,
				}
				confData, _ := json.Marshal(conf)
				return &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       netns.Path(),
					IfName:      "eth0",
					StdinData:   confData,
					Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
				}
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				// Mock getNetworkClient to return error
				patches.ApplyFunc(getNetworkClient, func(ctx context.Context) (rpc.TerwayBackendClient, *grpc.ClientConn, error) {
					return nil, nil, errors.New("connection failed")
				})
				return patches
			},
			expectedError: true,
			errorContains: "error create grpc client",
		},
		{
			name: "doCmdCheck error",
			setupArgs: func() *skel.CmdArgs {
				netns, _ := testutils.NewNS()
				conf := &types.CNIConf{
					NetConf: cniTypes.NetConf{
						CNIVersion: "0.4.0",
						Name:       "terway",
						Type:       "terway",
					},
					MTU: 1500,
				}
				confData, _ := json.Marshal(conf)
				return &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       netns.Path(),
					IfName:      "eth0",
					StdinData:   confData,
					Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
				}
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				mockClient := &mockTerwayBackendClient{}
				mockConn := &grpc.ClientConn{}

				// Mock grpc.ClientConn.Close to avoid nil pointer dereference
				patches.ApplyMethod(&grpc.ClientConn{}, "Close", func() error {
					return nil
				})

				// Mock getNetworkClient
				patches.ApplyFunc(getNetworkClient, func(ctx context.Context) (rpc.TerwayBackendClient, *grpc.ClientConn, error) {
					return mockClient, mockConn, nil
				})

				// Mock doCmdCheck to return error
				patches.ApplyFunc(doCmdCheck, func(ctx context.Context, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) error {
					return errors.New("check failed")
				})

				return patches
			},
			expectedError: true,
			errorContains: "check failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.setupArgs()
			patches := tt.setupMocks()
			defer patches.Reset()

			err := cmdCheck(args)
			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
