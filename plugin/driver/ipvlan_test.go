package driver

import (
	"net"
	"os"
	"runtime"
	"testing"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

type tearDownNetlinkTest func()

func skipUnlessRoot(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("Test requires root privileges.")
	}
}

func setUpNetlinkTest(t *testing.T) tearDownNetlinkTest {
	skipUnlessRoot(t)

	// new temporary namespace so we don't pollute the host
	// lock thread since the namespace is thread local
	runtime.LockOSThread()
	var err error
	ns, err := netns.New()
	if err != nil {
		t.Fatal("Failed to create newns", ns)
	}

	return func() {
		ns.Close()
		runtime.UnlockOSThread()
	}
}

func TestRedirectRule(t *testing.T) {
	teardown := setUpNetlinkTest(t)
	defer teardown()

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
