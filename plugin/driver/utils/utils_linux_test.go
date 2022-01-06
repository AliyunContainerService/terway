//go:build privileged
// +build privileged

package utils

import (
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
