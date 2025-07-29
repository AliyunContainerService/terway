//go:build privileged

package main

import (
	"encoding/json"
	"testing"

	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/containernetworking/cni/pkg/skel"
	cniTypes "github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/suite"
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
