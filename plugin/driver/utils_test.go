//go:build privileged
// +build privileged

package driver

import (
	"net"
	"runtime"
	"testing"

	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/vishvananda/netlink"
)

func TestEnsureVlanUntagger(t *testing.T) {
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

	err = EnsureVlanUntagger(eni)
	if err != nil {
		t.Errorf("error ensure vlan untagger, %v", err)
		t.Fail()
	}
}

var containerIPNet = &net.IPNet{
	IP:   net.ParseIP("169.10.0.10"),
	Mask: net.CIDRMask(24, 32),
}

var containerIPNetIPv6 = &net.IPNet{
	IP:   net.ParseIP("fd00:10::10"),
	Mask: net.CIDRMask(120, 128),
}

var eth0IPNet = &net.IPNet{
	IP:   net.ParseIP("169.20.0.10"),
	Mask: net.CIDRMask(24, 32),
}

var eth0IPNetIPv6 = &net.IPNet{
	IP:   net.ParseIP("fd00:20::10"),
	Mask: net.CIDRMask(120, 128),
}

var ipv4GW = net.ParseIP("169.10.0.253")
var ipv6GW = net.ParseIP("fd00:10::fffd")

var serviceCIDR = &net.IPNet{
	IP:   net.ParseIP("169.30.0.10"),
	Mask: net.CIDRMask(24, 32),
}

var serviceCIDRIPv6 = &net.IPNet{
	IP:   net.ParseIP("fd00:30::1234"),
	Mask: net.CIDRMask(120, 128),
}
