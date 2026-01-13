//go:build privileged

package main

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/plugin/datapath"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/rpc"
	terwayTypes "github.com/AliyunContainerService/terway/types"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/containernetworking/cni/pkg/skel"
	cniTypes "github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"github.com/vishvananda/netlink"
	"google.golang.org/grpc"
)

type CniSuite struct {
	suite.Suite
	cniArgs *cniCmdArgs
	netNS   ns.NetNS
	logger  logr.Logger
}

func (s *CniSuite) SetupTest() {
	var err error
	s.netNS, err = testutils.NewNS()
	s.Require().NoError(err)

	s.logger = testr.New(s.T())

	s.cniArgs = &cniCmdArgs{
		conf: &types.CNIConf{
			NetConf: cniTypes.NetConf{
				CNIVersion: "0.4.0",
				Name:       "terway",
				Type:       "terway",
			},
			MTU:           1500,
			VlanStripType: types.VlanStripTypeVlan,
		},
		netNS: s.netNS,
		k8sArgs: &types.K8SArgs{
			K8S_POD_NAME:               cniTypes.UnmarshallableString("test-pod"),
			K8S_POD_NAMESPACE:          cniTypes.UnmarshallableString("test-ns"),
			K8S_POD_INFRA_CONTAINER_ID: cniTypes.UnmarshallableString("test-container-id"),
		},
		inputArgs: &skel.CmdArgs{
			ContainerID: "test-container-id",
			Netns:       s.netNS.Path(),
			IfName:      "eth0",
			StdinData:   []byte(`{"cniVersion": "0.4.0", "name": "terway", "type": "terway"}`),
		},
	}
}

func (s *CniSuite) TearDownTest() {
	if s.netNS != nil {
		_ = s.netNS.Close()
		_ = testutils.UnmountNS(s.netNS)
	}
}

func TestCni(t *testing.T) {
	suite.Run(t, new(CniSuite))
}

func (s *CniSuite) TestParseCheckConf() {
	_, err := parseCheckConf(s.cniArgs.inputArgs, &rpc.NetConf{
		BasicInfo: &rpc.BasicInfo{
			PodIP: &rpc.IPSet{
				IPv4: "192.168.1.2",
			},
			PodCIDR: &rpc.IPSet{
				IPv4: "192.168.1.0/24",
			},
			GatewayIP: &rpc.IPSet{
				IPv4: "192.168.1.1",
			},
		},
		IfName: "eth0",
	}, s.cniArgs.conf, rpc.IPType_TypeENIMultiIP)
	s.Require().NoError(err)
}

func (s *CniSuite) TestGetDatePath() {
	s.Equal(types.VPCRoute, getDatePath(rpc.IPType_TypeVPCIP, types.VlanStripTypeVlan, false))
	s.Equal(types.ExclusiveENI, getDatePath(rpc.IPType_TypeVPCENI, types.VlanStripTypeVlan, false))
	s.Equal(types.Vlan, getDatePath(rpc.IPType_TypeVPCENI, types.VlanStripTypeVlan, true))
	s.Equal(types.IPVlan, getDatePath(rpc.IPType_TypeENIMultiIP, types.VlanStripTypeVlan, false))
	s.Equal(types.Vlan, getDatePath(rpc.IPType_TypeENIMultiIP, types.VlanStripTypeVlan, true))
	s.Equal(types.IPVlan, getDatePath(rpc.IPType_TypeENIMultiIP, types.VlanStripTypeFilter, true))
}

func (s *CniSuite) TestGetCmdArgs() {
	// Test case 1: Normal case with valid arguments
	validConf := &types.CNIConf{
		NetConf: cniTypes.NetConf{
			CNIVersion: "0.4.0",
			Name:       "terway",
			Type:       "terway",
		},
		MTU: 1500,
	}

	confData, err := json.Marshal(validConf)
	s.Require().NoError(err)

	args := &skel.CmdArgs{
		ContainerID: "test-container-id",
		Netns:       s.netNS.Path(),
		IfName:      "eth0",
		StdinData:   confData,
		Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
	}

	cmdArgs, err := getCmdArgs(args)
	s.Require().NoError(err)
	s.Require().NotNil(cmdArgs)

	// Verify CNIConf
	s.Equal(validConf.CNIVersion, cmdArgs.conf.CNIVersion)
	s.Equal(validConf.Name, cmdArgs.conf.Name)
	s.Equal(validConf.Type, cmdArgs.conf.Type)
	s.Equal(validConf.MTU, cmdArgs.conf.MTU)

	// Verify K8SArgs
	s.Equal("test-pod", string(cmdArgs.k8sArgs.K8S_POD_NAME))
	s.Equal("test-ns", string(cmdArgs.k8sArgs.K8S_POD_NAMESPACE))
	s.Equal("test-container-id", string(cmdArgs.k8sArgs.K8S_POD_INFRA_CONTAINER_ID))

	// Verify NetNS
	s.Equal(s.netNS.Path(), cmdArgs.netNS.Path())

	// Verify inputArgs
	s.Equal(args, cmdArgs.inputArgs)

	// Clean up
	err = cmdArgs.Close()
	s.Require().NoError(err)
}

func (s *CniSuite) TestGetCmdArgsWithDefaultMTU() {
	// Test case 2: MTU not set, should use default
	confWithoutMTU := &types.CNIConf{
		NetConf: cniTypes.NetConf{
			CNIVersion: "0.4.0",
			Name:       "terway",
			Type:       "terway",
		},
		// MTU not set, should default to 1500
	}

	confData, err := json.Marshal(confWithoutMTU)
	s.Require().NoError(err)

	args := &skel.CmdArgs{
		ContainerID: "test-container-id",
		Netns:       s.netNS.Path(),
		IfName:      "eth0",
		StdinData:   confData,
		Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
	}

	cmdArgs, err := getCmdArgs(args)
	s.Require().NoError(err)
	s.Require().NotNil(cmdArgs)

	// Verify MTU is set to default
	s.Equal(1500, cmdArgs.conf.MTU)

	// Clean up
	err = cmdArgs.Close()
	s.Require().NoError(err)
}

func (s *CniSuite) TestGetCmdArgsInvalidNetNS() {
	// Test case 3: Invalid network namespace path
	validConf := &types.CNIConf{
		NetConf: cniTypes.NetConf{
			CNIVersion: "0.4.0",
			Name:       "terway",
			Type:       "terway",
		},
		MTU: 1500,
	}

	confData, err := json.Marshal(validConf)
	s.Require().NoError(err)

	args := &skel.CmdArgs{
		ContainerID: "test-container-id",
		Netns:       "/invalid/netns/path",
		IfName:      "eth0",
		StdinData:   confData,
		Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
	}

	cmdArgs, err := getCmdArgs(args)
	s.Require().Error(err)
	s.Require().Nil(cmdArgs)
}

func (s *CniSuite) TestGetCmdArgsInvalidJSON() {
	// Test case 4: Invalid JSON in StdinData
	args := &skel.CmdArgs{
		ContainerID: "test-container-id",
		Netns:       s.netNS.Path(),
		IfName:      "eth0",
		StdinData:   []byte(`{"invalid": json`), // Invalid JSON
		Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
	}

	cmdArgs, err := getCmdArgs(args)
	s.Require().Error(err)
	s.Require().Nil(cmdArgs)

	// Verify it's a CNI decoding error
	cniErr, ok := err.(*cniTypes.Error)
	s.Require().True(ok)
	s.Equal(cniTypes.ErrDecodingFailure, cniErr.Code)
}

func (s *CniSuite) TestGetCmdArgsInvalidK8SArgs() {
	// Test case 5: Invalid K8S args format
	validConf := &types.CNIConf{
		NetConf: cniTypes.NetConf{
			CNIVersion: "0.4.0",
			Name:       "terway",
			Type:       "terway",
		},
		MTU: 1500,
	}

	confData, err := json.Marshal(validConf)
	s.Require().NoError(err)

	args := &skel.CmdArgs{
		ContainerID: "test-container-id",
		Netns:       s.netNS.Path(),
		IfName:      "eth0",
		StdinData:   confData,
		Args:        "INVALID_ARGS_FORMAT", // Invalid args format
	}

	cmdArgs, err := getCmdArgs(args)
	s.Require().Error(err)
	s.Require().Nil(cmdArgs)

	// Verify it's a CNI decoding error
	cniErr, ok := err.(*cniTypes.Error)
	s.Require().True(ok)
	s.Equal(cniTypes.ErrDecodingFailure, cniErr.Code)
}

func (s *CniSuite) TestGetCmdArgsWithAdditionalConfig() {
	// Test case 6: Configuration with additional fields
	confWithExtras := &types.CNIConf{
		NetConf: cniTypes.NetConf{
			CNIVersion: "0.4.0",
			Name:       "terway",
			Type:       "terway",
		},
		MTU:              1500,
		HostVethPrefix:   "veth",
		ENIIPVirtualType: "ipvlan",
		VlanStripType:    types.VlanStripTypeVlan,
		Debug:            true,
	}

	confData, err := json.Marshal(confWithExtras)
	s.Require().NoError(err)

	args := &skel.CmdArgs{
		ContainerID: "test-container-id",
		Netns:       s.netNS.Path(),
		IfName:      "eth0",
		StdinData:   confData,
		Args:        "K8S_POD_NAME=test-pod;K8S_POD_NAMESPACE=test-ns;K8S_POD_INFRA_CONTAINER_ID=test-container-id",
	}

	cmdArgs, err := getCmdArgs(args)
	s.Require().NoError(err)
	s.Require().NotNil(cmdArgs)

	// Verify additional fields are preserved
	s.Equal("veth", cmdArgs.conf.HostVethPrefix)
	s.Equal("ipvlan", cmdArgs.conf.ENIIPVirtualType)
	s.Equal(types.VlanStripTypeVlan, string(cmdArgs.conf.VlanStripType))
	s.True(cmdArgs.conf.Debug)

	// Clean up
	err = cmdArgs.Close()
	s.Require().NoError(err)
}

func (s *CniSuite) TestCniCmdArgs_GetCNIConf() {
	// Test normal case
	conf := s.cniArgs.GetCNIConf()
	s.Require().NotNil(conf)
	s.Equal("0.4.0", conf.CNIVersion)
	s.Equal("terway", conf.Name)
	s.Equal(1500, conf.MTU)

	// Test with nil args
	var nilArgs *cniCmdArgs
	conf = nilArgs.GetCNIConf()
	s.Nil(conf)

	// Test with nil conf
	argsWithNilConf := &cniCmdArgs{
		conf:      nil,
		netNS:     s.netNS,
		k8sArgs:   s.cniArgs.k8sArgs,
		inputArgs: s.cniArgs.inputArgs,
	}
	conf = argsWithNilConf.GetCNIConf()
	s.Nil(conf)
}

func (s *CniSuite) TestCniCmdArgs_GetK8SConfig() {
	// Test normal case
	k8sConfig := s.cniArgs.GetK8SConfig()
	s.Require().NotNil(k8sConfig)
	s.Equal("test-pod", string(k8sConfig.K8S_POD_NAME))
	s.Equal("test-ns", string(k8sConfig.K8S_POD_NAMESPACE))
	s.Equal("test-container-id", string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID))

	// Test with nil args
	var nilArgs *cniCmdArgs
	k8sConfig = nilArgs.GetK8SConfig()
	s.Nil(k8sConfig)

	// Test with nil k8sArgs
	argsWithNilK8sArgs := &cniCmdArgs{
		conf:      s.cniArgs.conf,
		netNS:     s.netNS,
		k8sArgs:   nil,
		inputArgs: s.cniArgs.inputArgs,
	}
	k8sConfig = argsWithNilK8sArgs.GetK8SConfig()
	s.Nil(k8sConfig)
}

func (s *CniSuite) TestCniCmdArgs_GetInputArgs() {
	// Test normal case
	inputArgs := s.cniArgs.GetInputArgs()
	s.Require().NotNil(inputArgs)
	s.Equal("test-container-id", inputArgs.ContainerID)
	s.Equal(s.netNS.Path(), inputArgs.Netns)
	s.Equal("eth0", inputArgs.IfName)

	// Test with nil args
	var nilArgs *cniCmdArgs
	inputArgs = nilArgs.GetInputArgs()
	s.Nil(inputArgs)

	// Test with nil inputArgs (should still return nil, not panic)
	argsWithNilInputArgs := &cniCmdArgs{
		conf:      s.cniArgs.conf,
		netNS:     s.netNS,
		k8sArgs:   s.cniArgs.k8sArgs,
		inputArgs: nil,
	}
	inputArgs = argsWithNilInputArgs.GetInputArgs()
	s.Nil(inputArgs)
}

func (s *CniSuite) TestCniCmdArgs_GetNetNSPath() {
	// Test normal case
	netNSPath := s.cniArgs.GetNetNSPath()
	s.NotEmpty(netNSPath)
	s.Equal(s.netNS.Path(), netNSPath)

	// Test with nil args
	var nilArgs *cniCmdArgs
	netNSPath = nilArgs.GetNetNSPath()
	s.Empty(netNSPath)

	// Test with nil netNS
	argsWithNilNetNS := &cniCmdArgs{
		conf:      s.cniArgs.conf,
		netNS:     nil,
		k8sArgs:   s.cniArgs.k8sArgs,
		inputArgs: s.cniArgs.inputArgs,
	}
	netNSPath = argsWithNilNetNS.GetNetNSPath()
	s.Empty(netNSPath)
}

// Mock TerwayBackendClient
type mockTerwayBackendClient struct{}

func (m *mockTerwayBackendClient) AllocIP(ctx context.Context, req *rpc.AllocIPRequest, opts ...grpc.CallOption) (*rpc.AllocIPReply, error) {
	return &rpc.AllocIPReply{
		Success: true,
		IPv4:    true,
		IPv6:    false,
		NetConfs: []*rpc.NetConf{
			{
				BasicInfo: &rpc.BasicInfo{
					PodIP: &rpc.IPSet{
						IPv4: "10.0.0.1",
					},
					GatewayIP: &rpc.IPSet{
						IPv4: "10.0.0.1",
					},
					ServiceCIDR: &rpc.IPSet{
						IPv4: "10.96.0.0/12",
					},
				},
				ENIInfo: &rpc.ENIInfo{
					MAC: "00:11:22:33:44:55",
				},
				IfName: "eth0",
			},
		},
		IPType: rpc.IPType_TypeENIMultiIP,
	}, nil
}

func (m *mockTerwayBackendClient) ReleaseIP(ctx context.Context, req *rpc.ReleaseIPRequest, opts ...grpc.CallOption) (*rpc.ReleaseIPReply, error) {
	return &rpc.ReleaseIPReply{Success: true}, nil
}

func (m *mockTerwayBackendClient) GetIPInfo(ctx context.Context, req *rpc.GetInfoRequest, opts ...grpc.CallOption) (*rpc.GetInfoReply, error) {
	return &rpc.GetInfoReply{}, nil
}

func (m *mockTerwayBackendClient) RecordEvent(ctx context.Context, req *rpc.EventRequest, opts ...grpc.CallOption) (*rpc.EventReply, error) {
	return &rpc.EventReply{}, nil
}

func TestDoCmdAdd(t *testing.T) {
	tests := []struct {
		name            string
		setupMocks      func() *gomonkey.Patches
		expectedError   bool
		expectedIPNet   bool
		expectedGateway bool
	}{
		{
			name: "Success case with IPv4",
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock utils.GetHostIP
				patches.ApplyFunc(utils.GetHostIP, func(ipv4, ipv6 bool) (*terwayTypes.IPNetSet, error) {
					return &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.1"),
							Mask: net.CIDRMask(32, 32),
						},
					}, nil
				})

				// Mock utils.EnsureHostNsConfig
				patches.ApplyFunc(utils.EnsureHostNsConfig, func(ipv4, ipv6 bool) error {
					return nil
				})

				// Mock utils.GrabFileLock
				patches.ApplyFunc(utils.GrabFileLock, func(path string) (*utils.Locker, error) {
					return &utils.Locker{}, nil
				})

				// Mock datapath.CheckIPVLanAvailable
				patches.ApplyFunc(datapath.CheckIPVLanAvailable, func() (bool, error) {
					return true, nil
				})

				// Mock datapath.NewIPVlanDriver().Setup
				patches.ApplyFunc(datapath.NewIPVlanDriver, func() *datapath.IPvlanDriver {
					return &datapath.IPvlanDriver{}
				})

				// Mock link.GetDeviceNumber to return a fake device ID
				patches.ApplyFunc(link.GetDeviceNumber, func(mac string) (int32, error) {
					return 1, nil
				})

				// Mock prepareVF function
				patches.ApplyFunc(prepareVF, func(ctx context.Context, id int, mac string) (int32, error) {
					return 1, nil
				})

				// Mock netlink.LinkByIndex to return a fake link
				patches.ApplyFunc(netlink.LinkByIndex, func(index int) (netlink.Link, error) {
					return &netlink.Dummy{
						LinkAttrs: netlink.LinkAttrs{
							Index:        index,
							Name:         "fake-eni",
							HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
						},
					}, nil
				})

				// Mock netlink.LinkByName to return a fake link
				patches.ApplyFunc(netlink.LinkByName, func(name string) (netlink.Link, error) {
					return &netlink.Dummy{
						LinkAttrs: netlink.LinkAttrs{
							Index: 1,
							Name:  name,
						},
					}, nil
				})

				// Mock utils.LinkAdd to avoid real link creation
				patches.ApplyFunc(utils.LinkAdd, func(ctx context.Context, link netlink.Link) error {
					return nil
				})

				// Mock utils.LinkDel to avoid real link deletion
				patches.ApplyFunc(utils.LinkDel, func(ctx context.Context, link netlink.Link) error {
					return nil
				})

				// Mock utils.EnsureLinkName to avoid real link name changes
				patches.ApplyFunc(utils.EnsureLinkName, func(ctx context.Context, link netlink.Link, name string) (bool, error) {
					return false, nil
				})

				// Mock utils.EnsureLinkMTU to avoid real MTU changes
				patches.ApplyFunc(utils.EnsureLinkMTU, func(ctx context.Context, link netlink.Link, mtu int) (bool, error) {
					return false, nil
				})

				// Mock utils.EnsureLinkUp to avoid real link state changes
				patches.ApplyFunc(utils.EnsureLinkUp, func(ctx context.Context, link netlink.Link) (bool, error) {
					return false, nil
				})

				// Mock utils.EnsureAddr to avoid real address assignment
				patches.ApplyFunc(utils.EnsureAddr, func(ctx context.Context, link netlink.Link, addr *netlink.Addr) (bool, error) {
					return false, nil
				})

				// Mock utils.EnsureNeigh to avoid real neighbor setup
				patches.ApplyFunc(utils.EnsureNeigh, func(ctx context.Context, neigh *netlink.Neigh) (bool, error) {
					return false, nil
				})

				// Mock utils.EnsureRoute to avoid real route setup
				patches.ApplyFunc(utils.EnsureRoute, func(ctx context.Context, route *netlink.Route) (bool, error) {
					return false, nil
				})

				// Mock utils.EnsureIPRule to avoid real IP rule setup
				patches.ApplyFunc(utils.EnsureIPRule, func(ctx context.Context, rule *netlink.Rule) (bool, error) {
					return false, nil
				})

				// Mock utils.EnsureVlanUntagger to avoid real vlan operations
				patches.ApplyFunc(utils.EnsureVlanUntagger, func(ctx context.Context, link netlink.Link) error {
					return nil
				})

				return patches
			},
			expectedError:   false,
			expectedIPNet:   true,
			expectedGateway: true,
		},
		{
			name: "AllocIP failure",
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock client.AllocIP to return error
				patches.ApplyMethodReturn(&mockTerwayBackendClient{}, "AllocIP",
					&rpc.AllocIPReply{Success: false}, assert.AnError)

				return patches
			},
			expectedError:   true,
			expectedIPNet:   false,
			expectedGateway: false,
		},
		{
			name: "GetHostIP failure",
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock utils.GetHostIP to return error
				patches.ApplyFunc(utils.GetHostIP, func(ipv4, ipv6 bool) (*terwayTypes.IPNetSet, error) {
					return nil, assert.AnError
				})

				return patches
			},
			expectedError:   true,
			expectedIPNet:   false,
			expectedGateway: false,
		},
		{
			name: "EnsureHostNsConfig failure",
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock utils.GetHostIP
				patches.ApplyFunc(utils.GetHostIP, func(ipv4, ipv6 bool) (*terwayTypes.IPNetSet, error) {
					return &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.1"),
							Mask: net.CIDRMask(32, 32),
						},
					}, nil
				})

				// Mock utils.EnsureHostNsConfig to return error
				patches.ApplyFunc(utils.EnsureHostNsConfig, func(ipv4, ipv6 bool) error {
					return assert.AnError
				})

				return patches
			},
			expectedError:   true,
			expectedIPNet:   false,
			expectedGateway: false,
		},
		{
			name: "GrabFileLock failure",
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock utils.GetHostIP
				patches.ApplyFunc(utils.GetHostIP, func(ipv4, ipv6 bool) (*terwayTypes.IPNetSet, error) {
					return &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.1"),
							Mask: net.CIDRMask(32, 32),
						},
					}, nil
				})

				// Mock utils.EnsureHostNsConfig
				patches.ApplyFunc(utils.EnsureHostNsConfig, func(ipv4, ipv6 bool) error {
					return nil
				})

				// Mock utils.GrabFileLock to return error
				patches.ApplyFunc(utils.GrabFileLock, func(path string) (*utils.Locker, error) {
					return nil, assert.AnError
				})

				return patches
			},
			expectedError:   true,
			expectedIPNet:   false,
			expectedGateway: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup patches
			patches := tt.setupMocks()
			defer patches.Reset()

			// Create test network namespace
			testNS, err := ns.GetNS("/proc/self/ns/net")
			if err != nil {
				t.Skipf("Skipping test due to inability to create test namespace: %v", err)
			}
			defer testNS.Close()

			// Create test cmdArgs
			cmdArgs := &cniCmdArgs{
				conf: &types.CNIConf{
					NetConf: cniTypes.NetConf{
						CNIVersion: "0.4.0",
						Name:       "terway",
						Type:       "terway",
					},
					MTU: 1500,
				},
				netNS: testNS,
				k8sArgs: &types.K8SArgs{
					K8S_POD_NAME:               "test-pod",
					K8S_POD_NAMESPACE:          "test-ns",
					K8S_POD_INFRA_CONTAINER_ID: "test-container-id",
				},
				inputArgs: &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       testNS.Path(),
					IfName:      "eth0",
				},
			}

			// Create mock client
			client := &mockTerwayBackendClient{}

			// Execute doCmdAdd
			ctx := context.Background()
			containerIPNet, gatewayIPSet, err := doCmdAdd(ctx, client, cmdArgs)

			// Assertions
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.expectedIPNet {
				assert.NotNil(t, containerIPNet)
			} else {
				assert.Nil(t, containerIPNet)
			}

			if tt.expectedGateway {
				assert.NotNil(t, gatewayIPSet)
			} else {
				assert.Nil(t, gatewayIPSet)
			}
		})
	}
}

// Mock implementations are not needed as we're using gomonkey to patch the actual functions

func TestDoCmdDel(t *testing.T) {
	tests := []struct {
		name          string
		setupMocks    func() *gomonkey.Patches
		expectedError bool
	}{
		{
			name: "Success case",
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock utils.GrabFileLock
				patches.ApplyFunc(utils.GrabFileLock, func(path string) (*utils.Locker, error) {
					return &utils.Locker{}, nil
				})

				// Mock utils.GenericTearDown
				patches.ApplyFunc(utils.GenericTearDown, func(ctx context.Context, netNS ns.NetNS) error {
					return nil
				})

				// Mock client.GetIPInfo
				patches.ApplyMethodReturn(&mockTerwayBackendClient{}, "GetIPInfo",
					&rpc.GetInfoReply{
						Success: true,
						IPv4:    true,
						IPv6:    false,
						NetConfs: []*rpc.NetConf{
							{
								BasicInfo: &rpc.BasicInfo{
									PodIP: &rpc.IPSet{
										IPv4: "10.0.0.1",
									},
									PodCIDR: &rpc.IPSet{
										IPv4: "10.0.0.0/24",
									},
									ServiceCIDR: &rpc.IPSet{
										IPv4: "10.96.0.0/12",
									},
								},
								ENIInfo: &rpc.ENIInfo{
									MAC: "00:11:22:33:44:55",
								},
								IfName: "eth0",
							},
						},
						IPType: rpc.IPType_TypeENIMultiIP,
					}, nil)

				// Mock client.ReleaseIP
				patches.ApplyMethodReturn(&mockTerwayBackendClient{}, "ReleaseIP",
					&rpc.ReleaseIPReply{Success: true}, nil)

				// Mock link.GetDeviceNumber
				patches.ApplyFunc(link.GetDeviceNumber, func(mac string) (int32, error) {
					return 1, nil
				})

				// Mock datapath.CheckIPVLanAvailable
				patches.ApplyFunc(datapath.CheckIPVLanAvailable, func() (bool, error) {
					return true, nil
				})

				// Mock datapath.NewIPVlanDriver().Teardown
				patches.ApplyFunc(datapath.NewIPVlanDriver, func() *datapath.IPvlanDriver {
					return &datapath.IPvlanDriver{}
				})

				return patches
			},
			expectedError: false,
		},
		{
			name: "GetIPInfo failure",
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock utils.GrabFileLock
				patches.ApplyFunc(utils.GrabFileLock, func(path string) (*utils.Locker, error) {
					return &utils.Locker{}, nil
				})

				// Mock utils.GenericTearDown
				patches.ApplyFunc(utils.GenericTearDown, func(ctx context.Context, netNS ns.NetNS) error {
					return nil
				})

				// Mock client.GetIPInfo to return error
				patches.ApplyMethodReturn(&mockTerwayBackendClient{}, "GetIPInfo",
					&rpc.GetInfoReply{}, assert.AnError)

				return patches
			},
			expectedError: true,
		},
		{
			name: "GenericTearDown failure",
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock utils.GrabFileLock
				patches.ApplyFunc(utils.GrabFileLock, func(path string) (*utils.Locker, error) {
					return &utils.Locker{}, nil
				})

				// Mock utils.GenericTearDown to return error
				patches.ApplyFunc(utils.GenericTearDown, func(ctx context.Context, netNS ns.NetNS) error {
					return assert.AnError
				})

				return patches
			},
			expectedError: false, // GenericTearDown errors are swallowed
		},
		{
			name: "CRD not found",
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock utils.GrabFileLock
				patches.ApplyFunc(utils.GrabFileLock, func(path string) (*utils.Locker, error) {
					return &utils.Locker{}, nil
				})

				// Mock utils.GenericTearDown
				patches.ApplyFunc(utils.GenericTearDown, func(ctx context.Context, netNS ns.NetNS) error {
					return nil
				})

				// Mock client.GetIPInfo to return CRD not found error
				patches.ApplyMethodReturn(&mockTerwayBackendClient{}, "GetIPInfo",
					&rpc.GetInfoReply{
						Error: rpc.Error_ErrCRDNotFound,
					}, nil)

				return patches
			},
			expectedError: false, // CRD not found errors are swallowed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup patches
			patches := tt.setupMocks()
			defer patches.Reset()

			// Create test network namespace
			testNS, err := ns.GetNS("/proc/self/ns/net")
			if err != nil {
				t.Skipf("Skipping test due to inability to create test namespace: %v", err)
			}
			defer testNS.Close()

			// Create test cmdArgs
			cmdArgs := &cniCmdArgs{
				conf: &types.CNIConf{
					NetConf: cniTypes.NetConf{
						CNIVersion: "0.4.0",
						Name:       "terway",
						Type:       "terway",
					},
					MTU: 1500,
				},
				netNS: testNS,
				k8sArgs: &types.K8SArgs{
					K8S_POD_NAME:               "test-pod",
					K8S_POD_NAMESPACE:          "test-ns",
					K8S_POD_INFRA_CONTAINER_ID: "test-container-id",
				},
				inputArgs: &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       testNS.Path(),
					IfName:      "eth0",
				},
			}

			// Create mock client
			client := &mockTerwayBackendClient{}

			// Execute doCmdDel
			ctx := context.Background()
			err = doCmdDel(ctx, client, cmdArgs)

			// Assertions
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDoCmdCheck(t *testing.T) {
	tests := []struct {
		name          string
		setupMocks    func() *gomonkey.Patches
		expectedError bool
	}{
		{
			name: "Success case with IPv4",
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock client.GetIPInfo
				patches.ApplyMethodReturn(&mockTerwayBackendClient{}, "GetIPInfo",
					&rpc.GetInfoReply{
						Success: true,
						IPv4:    true,
						IPv6:    false,
						NetConfs: []*rpc.NetConf{
							{
								BasicInfo: &rpc.BasicInfo{
									PodIP: &rpc.IPSet{
										IPv4: "10.0.0.1",
									},
									PodCIDR: &rpc.IPSet{
										IPv4: "10.0.0.0/24",
									},
									GatewayIP: &rpc.IPSet{
										IPv4: "10.0.0.1",
									},
									ServiceCIDR: &rpc.IPSet{
										IPv4: "10.96.0.0/12",
									},
								},
								ENIInfo: &rpc.ENIInfo{
									MAC: "00:11:22:33:44:55",
								},
								IfName: "eth0",
							},
						},
						IPType: rpc.IPType_TypeENIMultiIP,
					}, nil)

				// Mock utils.GetHostIP
				patches.ApplyFunc(utils.GetHostIP, func(ipv4, ipv6 bool) (*terwayTypes.IPNetSet, error) {
					return &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.1"),
							Mask: net.CIDRMask(32, 32),
						},
					}, nil
				})

				// Mock utils.EnsureHostNsConfig
				patches.ApplyFunc(utils.EnsureHostNsConfig, func(ipv4, ipv6 bool) error {
					return nil
				})

				// Mock utils.GrabFileLock
				patches.ApplyFunc(utils.GrabFileLock, func(path string) (*utils.Locker, error) {
					return &utils.Locker{}, nil
				})

				// Mock link.GetDeviceNumber
				patches.ApplyFunc(link.GetDeviceNumber, func(mac string) (int32, error) {
					return 1, nil
				})

				// Mock datapath.CheckIPVLanAvailable
				patches.ApplyFunc(datapath.CheckIPVLanAvailable, func() (bool, error) {
					return true, nil
				})

				// Mock datapath.NewIPVlanDriver().Check
				patches.ApplyFunc(datapath.NewIPVlanDriver, func() *datapath.IPvlanDriver {
					return &datapath.IPvlanDriver{}
				})

				// Mock netlink.LinkByIndex
				patches.ApplyFunc(netlink.LinkByIndex, func(index int) (netlink.Link, error) {
					return &netlink.Dummy{
						LinkAttrs: netlink.LinkAttrs{
							Index:        index,
							Name:         "fake-eni",
							HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
						},
					}, nil
				})

				// Mock netlink.LinkByName
				patches.ApplyFunc(netlink.LinkByName, func(name string) (netlink.Link, error) {
					return &netlink.Dummy{
						LinkAttrs: netlink.LinkAttrs{
							Index: 1,
							Name:  name,
						},
					}, nil
				})

				// Mock utils.EnsureLinkUp
				patches.ApplyFunc(utils.EnsureLinkUp, func(ctx context.Context, link netlink.Link) (bool, error) {
					return false, nil
				})

				// Mock utils.EnsureLinkMTU
				patches.ApplyFunc(utils.EnsureLinkMTU, func(ctx context.Context, link netlink.Link, mtu int) (bool, error) {
					return false, nil
				})

				// Mock utils.EnsureNetConfSet
				patches.ApplyFunc(utils.EnsureNetConfSet, func(ipv4, ipv6 bool) error {
					return nil
				})

				return patches
			},
			expectedError: false,
		},
		{
			name: "GetIPInfo failure",
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock client.GetIPInfo to return error
				patches.ApplyMethodReturn(&mockTerwayBackendClient{}, "GetIPInfo",
					&rpc.GetInfoReply{}, assert.AnError)

				return patches
			},
			expectedError: false, // GetIPInfo errors are swallowed in doCmdCheck
		},
		{
			name: "No valid IP type",
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock client.GetIPInfo to return no valid IP type
				patches.ApplyMethodReturn(&mockTerwayBackendClient{}, "GetIPInfo",
					&rpc.GetInfoReply{
						Success: true,
						IPv4:    false,
						IPv6:    false,
					}, nil)

				return patches
			},
			expectedError: true,
		},
		{
			name: "GetHostIP failure",
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock client.GetIPInfo
				patches.ApplyMethodReturn(&mockTerwayBackendClient{}, "GetIPInfo",
					&rpc.GetInfoReply{
						Success: true,
						IPv4:    true,
						IPv6:    false,
						NetConfs: []*rpc.NetConf{
							{
								BasicInfo: &rpc.BasicInfo{
									PodIP: &rpc.IPSet{
										IPv4: "10.0.0.1",
									},
									PodCIDR: &rpc.IPSet{
										IPv4: "10.0.0.0/24",
									},
									GatewayIP: &rpc.IPSet{
										IPv4: "10.0.0.1",
									},
									ServiceCIDR: &rpc.IPSet{
										IPv4: "10.96.0.0/12",
									},
								},
								ENIInfo: &rpc.ENIInfo{
									MAC: "00:11:22:33:44:55",
								},
								IfName: "eth0",
							},
						},
						IPType: rpc.IPType_TypeENIMultiIP,
					}, nil)

				// Mock utils.GetHostIP to return error
				patches.ApplyFunc(utils.GetHostIP, func(ipv4, ipv6 bool) (*terwayTypes.IPNetSet, error) {
					return nil, assert.AnError
				})

				return patches
			},
			expectedError: true,
		},
		{
			name: "EnsureHostNsConfig failure",
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock client.GetIPInfo
				patches.ApplyMethodReturn(&mockTerwayBackendClient{}, "GetIPInfo",
					&rpc.GetInfoReply{
						Success: true,
						IPv4:    true,
						IPv6:    false,
						NetConfs: []*rpc.NetConf{
							{
								BasicInfo: &rpc.BasicInfo{
									PodIP: &rpc.IPSet{
										IPv4: "10.0.0.1",
									},
									PodCIDR: &rpc.IPSet{
										IPv4: "10.0.0.0/24",
									},
									GatewayIP: &rpc.IPSet{
										IPv4: "10.0.0.1",
									},
									ServiceCIDR: &rpc.IPSet{
										IPv4: "10.96.0.0/12",
									},
								},
								ENIInfo: &rpc.ENIInfo{
									MAC: "00:11:22:33:44:55",
								},
								IfName: "eth0",
							},
						},
						IPType: rpc.IPType_TypeENIMultiIP,
					}, nil)

				// Mock utils.GetHostIP
				patches.ApplyFunc(utils.GetHostIP, func(ipv4, ipv6 bool) (*terwayTypes.IPNetSet, error) {
					return &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.1"),
							Mask: net.CIDRMask(32, 32),
						},
					}, nil
				})

				// Mock utils.EnsureHostNsConfig to return error
				patches.ApplyFunc(utils.EnsureHostNsConfig, func(ipv4, ipv6 bool) error {
					return assert.AnError
				})

				return patches
			},
			expectedError: true,
		},
		{
			name: "GrabFileLock failure",
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock client.GetIPInfo
				patches.ApplyMethodReturn(&mockTerwayBackendClient{}, "GetIPInfo",
					&rpc.GetInfoReply{
						Success: true,
						IPv4:    true,
						IPv6:    false,
						NetConfs: []*rpc.NetConf{
							{
								BasicInfo: &rpc.BasicInfo{
									PodIP: &rpc.IPSet{
										IPv4: "10.0.0.1",
									},
									PodCIDR: &rpc.IPSet{
										IPv4: "10.0.0.0/24",
									},
									GatewayIP: &rpc.IPSet{
										IPv4: "10.0.0.1",
									},
									ServiceCIDR: &rpc.IPSet{
										IPv4: "10.96.0.0/12",
									},
								},
								ENIInfo: &rpc.ENIInfo{
									MAC: "00:11:22:33:44:55",
								},
								IfName: "eth0",
							},
						},
						IPType: rpc.IPType_TypeENIMultiIP,
					}, nil)

				// Mock utils.GetHostIP
				patches.ApplyFunc(utils.GetHostIP, func(ipv4, ipv6 bool) (*terwayTypes.IPNetSet, error) {
					return &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.1"),
							Mask: net.CIDRMask(32, 32),
						},
					}, nil
				})

				// Mock utils.EnsureHostNsConfig
				patches.ApplyFunc(utils.EnsureHostNsConfig, func(ipv4, ipv6 bool) error {
					return nil
				})

				// Mock utils.GrabFileLock to return error
				patches.ApplyFunc(utils.GrabFileLock, func(path string) (*utils.Locker, error) {
					return nil, assert.AnError
				})

				return patches
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup patches
			patches := tt.setupMocks()
			defer patches.Reset()

			// Create test network namespace
			testNS, err := ns.GetNS("/proc/self/ns/net")
			if err != nil {
				t.Skipf("Skipping test due to inability to create test namespace: %v", err)
			}
			defer testNS.Close()

			// Create test cmdArgs
			cmdArgs := &cniCmdArgs{
				conf: &types.CNIConf{
					NetConf: cniTypes.NetConf{
						CNIVersion: "0.4.0",
						Name:       "terway",
						Type:       "terway",
					},
					MTU: 1500,
				},
				netNS: testNS,
				k8sArgs: &types.K8SArgs{
					K8S_POD_NAME:               "test-pod",
					K8S_POD_NAMESPACE:          "test-ns",
					K8S_POD_INFRA_CONTAINER_ID: "test-container-id",
				},
				inputArgs: &skel.CmdArgs{
					ContainerID: "test-container-id",
					Netns:       testNS.Path(),
					IfName:      "eth0",
				},
			}

			// Create mock client
			client := &mockTerwayBackendClient{}

			// Execute doCmdCheck
			ctx := context.Background()
			err = doCmdCheck(ctx, client, cmdArgs)

			// Assertions
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
