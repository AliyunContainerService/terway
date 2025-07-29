package veth

import (
	"context"
	"net"
	"runtime"
	"testing"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
)

func TestVeth(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var err error
	hostNS, err := testutils.NewNS()
	assert.NoError(t, err)

	containerNS, err := testutils.NewNS()
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

	_ = hostNS.Do(func(netNS ns.NetNS) error {
		err = netlink.LinkAdd(&netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{Name: "eni"},
		})

		mac, err := net.ParseMAC("02:00:00:11:22:33")
		require.NoError(t, err)

		cfg := &Veth{
			PeerName: "hostveth",
			IfName:   "eth0",
			MTU:      1500,
			HwAddr:   mac,
		}

		err = Setup(context.Background(), cfg, containerNS)
		require.NoError(t, err)
		return nil
	})

	_ = containerNS.Do(func(netNS ns.NetNS) error {
		vlan, err := netlink.LinkByName("eth0")
		assert.NoError(t, err)
		assert.Equal(t, "veth", vlan.Type())
		return nil
	})
}
