//go:build privileged

package main

import (
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
