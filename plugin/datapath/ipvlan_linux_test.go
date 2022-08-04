//go:build privileged

package datapath

import (
	"net"
	"runtime"
	"testing"

	types2 "github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/types"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/vishvananda/netlink"
)

func TestRedirectRule(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var err error
	hostNS, err := testutils.NewNS()
	assert.NoError(t, err)

	containerNS, err := testutils.NewNS()
	assert.NoError(t, err)

	err = hostNS.Set()
	assert.NoError(t, err)

	defer func() {
		err := containerNS.Close()
		assert.NoError(t, err)

		err = testutils.UnmountNS(containerNS)
		assert.NoError(t, err)

		err = hostNS.Close()
		assert.NoError(t, err)

		err = testutils.UnmountNS(hostNS)
		assert.NoError(t, err)
	}()

	foo := &netlink.Ifb{
		LinkAttrs: netlink.LinkAttrs{
			Name: "foo",
		},
	}
	if err := netlink.LinkAdd(foo); err != nil {
		t.Fatal(err)
	}

	bar := &netlink.Ifb{
		LinkAttrs: netlink.LinkAttrs{
			Name: "bar",
		},
	}
	if err := netlink.LinkAdd(bar); err != nil {
		t.Fatal(err)
	}

	link, err := netlink.LinkByName("foo")
	if err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		t.Fatal(err)
	}
	redir, err := netlink.LinkByName("bar")
	if err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkSetUp(redir); err != nil {
		t.Fatal(err)
	}

	// add qdisc
	qdisc := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: link.Attrs().Index,
			Parent:    netlink.HANDLE_CLSACT,
			Handle:    netlink.HANDLE_CLSACT & 0xffff0000,
		},
		QdiscType: "clsact",
	}
	if err := netlink.QdiscAdd(qdisc); err != nil {
		t.Fatalf("add qdisc error, %s", err)
	}

	// add filter
	parent := uint32(netlink.HANDLE_CLSACT&0xffff0000 | netlink.HANDLE_MIN_EGRESS&0x0000ffff)

	_, cidr, err := net.ParseCIDR("192.168.0.0/16")
	if err != nil {
		t.Fatalf("parse cidr error, %v", err)
	}

	rule, err := dstIPRule(link.Attrs().Index, cidr, redir.Attrs().Index, netlink.TCA_INGRESS_REDIR)
	if err != nil {
		t.Fatalf("failed to create rule, %v", err)
	}

	u32 := rule.toU32Filter()
	u32.Parent = parent
	if err := netlink.FilterAdd(u32); err != nil {
		t.Fatalf("failed to add filter, %v", err)
	}

	// get filter
	filters, err := netlink.FilterList(link, parent)
	if err != nil {
		t.Fatalf("failed to list filter, %v", err)
	}
	if len(filters) != 1 {
		t.Fatalf("filters not match")
	}

	if !rule.isMatch(filters[0]) {
		t.Fatalf("filter not match the rule")
	}
}

func TestDataPathIPvlanL2(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var err error
	hostNS, err := testutils.NewNS()
	assert.NoError(t, err)

	containerNS, err := testutils.NewNS()
	assert.NoError(t, err)

	err = hostNS.Set()
	assert.NoError(t, err)

	defer func() {
		err := containerNS.Close()
		assert.NoError(t, err)

		err = testutils.UnmountNS(containerNS)
		assert.NoError(t, err)

		err = hostNS.Close()
		assert.NoError(t, err)

		err = testutils.UnmountNS(hostNS)
		assert.NoError(t, err)
	}()

	err = netlink.LinkAdd(&netlink.Dummy{
		LinkAttrs: netlink.LinkAttrs{Name: "eni"},
	})
	assert.NoError(t, err)
	eni, err := netlink.LinkByName("eni")
	assert.NoError(t, err)

	cfg := &types2.SetupConfig{
		HostVETHName:    "hostipvl",
		ContainerIfName: "eth0",
		ContainerIPNet: &types.IPNetSet{
			IPv4: containerIPNet,
			IPv6: containerIPNetIPv6,
		},
		GatewayIP: &types.IPSet{
			IPv4: ipv4GW,
			IPv6: ipv6GW,
		},
		MTU:            1499,
		ENIIndex:       eni.Attrs().Index,
		StripVlan:      false,
		HostStackCIDRs: nil,
		Ingress:        0,
		Egress:         0,
		ServiceCIDR: &types.IPNetSet{
			IPv4: serviceCIDR,
			IPv6: serviceCIDRIPv6,
		},
		HostIPSet: &types.IPNetSet{
			IPv4: eth0IPNet,
			IPv6: eth0IPNetIPv6,
		},
		DefaultRoute: true,
	}
	d := NewIPVlanDriver()

	err = d.Setup(cfg, containerNS)
	assert.NoError(t, err)

	_ = containerNS.Do(func(netNS ns.NetNS) error {
		// addr
		containerLink, err := netlink.LinkByName(cfg.ContainerIfName)
		assert.NoError(t, err)
		assert.Equal(t, cfg.MTU, containerLink.Attrs().MTU)

		ok, err := FindIP(containerLink, cfg.ContainerIPNet)
		assert.NoError(t, err)
		assert.True(t, ok)

		// default via 169.10.10.253 dev eth0
		routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{
			Dst: nil,
		}, netlink.RT_FILTER_DST)
		assert.NoError(t, err)
		assert.Equal(t, len(routes), 1)

		assert.Equal(t, ipv4GW.String(), routes[0].Gw.String())

		// neigh should be added
		ok, err = FindNeigh(containerLink, eth0IPNet.IP, eni.Attrs().HardwareAddr)
		assert.NoError(t, err)
		assert.True(t, ok)

		ok, err = FindNeigh(containerLink, eth0IPNetIPv6.IP, eni.Attrs().HardwareAddr)
		assert.NoError(t, err)
		assert.True(t, ok)

		return nil
	})

	// check eni
	eni, err = netlink.LinkByIndex(eni.Attrs().Index)
	assert.NoError(t, err)
	assert.True(t, eni.Attrs().Flags&net.FlagUp != 0)

	addrs, err := netlink.AddrList(eni, netlink.FAMILY_V4)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(addrs))

	// check slave link (ipvl_x)
	slaveName := d.initSlaveName(eni.Attrs().Index)
	slaveLink, err := netlink.LinkByName(slaveName)
	assert.NoError(t, err)
	assert.Equal(t, cfg.MTU, slaveLink.Attrs().MTU)
	assert.True(t, slaveLink.Attrs().Flags&net.FlagUp != 0)

	// eth0's ip 169.20.20.10/32 dev ipvl_x
	ok, err := FindIP(slaveLink, &types.IPNetSet{
		IPv4: &net.IPNet{
			IP:   cfg.HostIPSet.IPv4.IP,
			Mask: net.CIDRMask(32, 32),
		},
		IPv6: &net.IPNet{
			IP:   cfg.HostIPSet.IPv6.IP,
			Mask: net.CIDRMask(128, 128),
		},
	})
	assert.NoError(t, err)
	assert.True(t, ok)

	// route 169.10.0.10 dev ipvl_x
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{
		Dst: &net.IPNet{
			IP:   cfg.ContainerIPNet.IPv4.IP,
			Mask: net.CIDRMask(32, 32),
		},
		LinkIndex: slaveLink.Attrs().Index,
	}, netlink.RT_FILTER_DST|netlink.RT_FILTER_OIF)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(routes))

	// add some route make sure we don't delete it
	dummyRoute := &netlink.Route{
		LinkIndex: slaveLink.Attrs().Index,
		Dst: &net.IPNet{
			IP:   net.ParseIP("169.99.0.10"),
			Mask: net.CIDRMask(32, 32),
		},
	}
	err = netlink.RouteAdd(dummyRoute)
	assert.NoError(t, err)

	// tear down
	err = utils.GenericTearDown(containerNS)
	assert.NoError(t, err)

	err = d.Teardown(&types2.TeardownCfg{
		HostVETHName:    cfg.HostVETHName,
		ContainerIfName: cfg.ContainerIfName,
		ContainerIPNet: &types.IPNetSet{
			IPv4: containerIPNet,
			IPv6: containerIPNetIPv6,
		},
		ENIIndex: eni.Attrs().Index,
	}, containerNS)
	assert.NoError(t, err)

	// check ipvl_x is up
	slaveLink, err = netlink.LinkByIndex(slaveLink.Attrs().Index)
	assert.NoError(t, err)
	assert.True(t, slaveLink.Attrs().Flags&net.FlagUp != 0)

	// route 169.10.0.10 dev ipvl_x should be removed
	routes, err = utils.FoundRoutes(&netlink.Route{
		Dst: &net.IPNet{
			IP:   cfg.ContainerIPNet.IPv4.IP,
			Mask: net.CIDRMask(32, 32),
		},
	})
	assert.NoError(t, err)
	assert.Equal(t, 0, len(routes))
}
