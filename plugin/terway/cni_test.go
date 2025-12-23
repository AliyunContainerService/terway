package main

import (
	"context"
	"testing"

	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/containernetworking/cni/pkg/skel"
	cniTypes "github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
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
