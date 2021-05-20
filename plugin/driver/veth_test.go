package driver

import (
	"fmt"
	"net"
	"os"
	"testing"

	terwayTypes "github.com/AliyunContainerService/terway/types"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/stretchr/testify/suite"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

type VETHSuite struct {
	suite.Suite
	VETH1Cfg *SetupConfig
	VETH2Cfg *SetupConfig

	Driver NetnsDriver
}

func TestVETHSuite(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("Test requires root privileges.")
	}
	suite.Run(t, new(VETHSuite))
}

func (s *VETHSuite) SetupSuite() {
	link, err := netlink.LinkByName("eth0")
	if err != nil {
		panic(err)
	}

	s.Driver = NewVETHDriver(true, true)
	s.VETH1Cfg = &SetupConfig{
		HostVETHName:    "veth1",
		ContainerIfName: "eth0",
		ContainerIPNet: &terwayTypes.IPNetSet{
			IPv4: &net.IPNet{
				IP:   net.ParseIP("192.168.100.100"),
				Mask: net.CIDRMask(32, 32),
			},
			IPv6: &net.IPNet{
				IP:   net.ParseIP("fd00::100"),
				Mask: net.CIDRMask(128, 128),
			},
		},
		GatewayIP: &terwayTypes.IPSet{
			IPv4: net.ParseIP("192.168.100.253"),
			IPv6: net.ParseIP("fd00::253"),
		},
		MTU:         1500,
		ENIIndex:    link.Attrs().Index,
		ExtraRoutes: []types.Route{},
	}
	s.VETH2Cfg = &SetupConfig{
		HostVETHName:    "veth2",
		ContainerIfName: "eth0",
		ContainerIPNet:  &terwayTypes.IPNetSet{},
		GatewayIP:       &terwayTypes.IPSet{},
		MTU:             1500,
		ENIIndex:        link.Attrs().Index,
		ExtraRoutes:     []types.Route{},
	}

	current, err := netns.Get()
	s.NoError(err)

	_, err = netns.NewNamed("veth1")
	if err != nil {
		panic(err)
	}
	_, err = netns.NewNamed("veth2")
	if err != nil {
		panic(err)
	}

	_ = netns.Set(current)
}

func (s *VETHSuite) TearDownSuite() {
	err := netns.DeleteNamed("veth1")
	s.NoError(err)
	err = netns.DeleteNamed("veth2")
	s.NoError(err)
}

func (s *VETHSuite) TestSetup() {
	ns1, err := ns.GetNS("/var/run/netns/veth1")
	s.NoError(err)
	defer ns1.Close()
	err = s.Driver.Setup(s.VETH1Cfg, ns1)
	s.NoError(err)

	// 1. check eth0 in ns
	err = ns1.Do(func(netNS ns.NetNS) error {
		cLink, err := netlink.LinkByName(s.VETH1Cfg.ContainerIfName)
		if err != nil {
			return err
		}
		ok, err := FindIP(cLink, s.VETH1Cfg.ContainerIPNet)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no match ipv4 addr found")
		}

		changed, err := EnsureLinkUp(cLink)
		if err != nil {
			return err
		}
		if changed {
			return fmt.Errorf("link is not up")
		}
		return err
	})
	s.NoError(err)

	parentLink, err := netlink.LinkByIndex(s.VETH1Cfg.ENIIndex)
	s.NoError(err)

	tableID := getRouteTableID(parentLink.Attrs().Index)

	// 2. check ip rule
	if s.VETH1Cfg.ContainerIPNet.IPv4 != nil {
		err = FindIPRules(s.VETH1Cfg.ContainerIPNet.IPv4, func(rule *netlink.Rule) error {
			switch rule.Table {
			case tableID:
				s.Equal(fromContainerPriority, rule.Priority)
			case mainRouteTable:
				s.Equal(toContainerPriority, rule.Priority)
			default:
				return fmt.Errorf("found unexpected ip rule %s", rule.String())
			}
			return nil
		})
		s.NoError(err)
	}
	if s.VETH1Cfg.ContainerIPNet.IPv6 != nil {
		err = FindIPRules(s.VETH1Cfg.ContainerIPNet.IPv6, func(rule *netlink.Rule) error {
			switch rule.Table {
			case tableID:
				s.Equal(fromContainerPriority, rule.Priority)
			case mainRouteTable:
				s.Equal(toContainerPriority, rule.Priority)
			default:
				return fmt.Errorf("found unexpected ip rule %s", rule.String())
			}
			return nil
		})
		s.NoError(err)
	}

	// 3. teradown
	err = s.Driver.Teardown(&TeardownCfg{
		HostVETHName:    s.VETH1Cfg.HostVETHName,
		ContainerIfName: s.VETH1Cfg.ContainerIfName,
		ContainerIPNet:  s.VETH1Cfg.ContainerIPNet,
	}, ns1)
	s.NoError(err)
}

func FindIP(link netlink.Link, ipNetSet *terwayTypes.IPNetSet) (bool, error) {
	exec := func(ip net.IP, family int) (bool, error) {
		addrList, err := netlink.AddrList(link, family)
		if err != nil {
			return false, err
		}
		for _, addr := range addrList {
			if addr.IP.Equal(ip) {
				return true, nil
			}
		}
		return false, nil
	}
	if ipNetSet.IPv4 != nil {
		ok, err := exec(ipNetSet.IPv4.IP, netlink.FAMILY_V4)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	if ipNetSet.IPv6 != nil {
		ok, err := exec(ipNetSet.IPv6.IP, netlink.FAMILY_V6)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}
