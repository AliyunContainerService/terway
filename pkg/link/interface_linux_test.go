package link

import (
	"errors"
	"net"
	"runtime"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vishvananda/netlink"
)

func TestGetDeviceNumber(t *testing.T) {
	id, err := GetDeviceNumber("00")
	assert.True(t, errors.Is(err, ErrNotFound))
	assert.Equal(t, int32(0), id)
}

func TestGetDeviceNumber_LinkListError(t *testing.T) {
	runtime.GC()
	patches := gomonkey.ApplyFunc(netlink.LinkList, func() ([]netlink.Link, error) {
		return nil, errors.New("netlink failure")
	})
	defer func() {
		patches.Reset()
		runtime.GC()
	}()

	id, err := GetDeviceNumber("aa:bb:cc:dd:ee:ff")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error get link list")
	assert.Equal(t, int32(0), id)
}

func TestGetDeviceNumber_MatchFound(t *testing.T) {
	runtime.GC()
	patches := gomonkey.ApplyFunc(netlink.LinkList, func() ([]netlink.Link, error) {
		device := &netlink.Device{}
		device.LinkAttrs.HardwareAddr = net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
		device.LinkAttrs.Index = 5
		return []netlink.Link{device}, nil
	})
	defer func() {
		patches.Reset()
		runtime.GC()
	}()

	id, err := GetDeviceNumber("aa:bb:cc:dd:ee:ff")
	assert.NoError(t, err)
	assert.Equal(t, int32(5), id)
}
