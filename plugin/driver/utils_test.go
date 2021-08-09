package driver

import (
	"testing"

	"github.com/vishvananda/netlink"
)

func TestEnsureVlanUntagger(t *testing.T) {
	skipUnlessRoot(t)
	link, err := netlink.LinkByName("eth0")
	if err != nil {
		t.Errorf("error found eth0 interface, %v", err)
		t.Fail()
	}
	err = EnsureVlanUntagger(link)
	if err != nil {
		t.Errorf("error ensure vlan untagger, %v", err)
		t.Fail()
	}
}
