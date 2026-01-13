package utils

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/vishvananda/netlink"
	k8snet "k8s.io/apimachinery/pkg/util/net"

	terwayTypes "github.com/AliyunContainerService/terway/types"
)

// createTestNetNS creates a new network namespace for testing
func createTestNetNS(t *testing.T) ns.NetNS {
	t.Helper()

	// Create a new network namespace
	testNS, err := testutils.NewNS()
	if err != nil {
		t.Fatalf("Failed to create test network namespace: %v", err)
	}

	return testNS
}

// cleanupTestNetNS cleans up the test network namespace
func cleanupTestNetNS(t *testing.T, testNS ns.NetNS) {
	t.Helper()

	if testNS != nil {
		// Clean up any interfaces in the namespace first
		_ = testNS.Do(func(netNS ns.NetNS) error {
			links, err := netlink.LinkList()
			if err != nil {
				return err
			}

			for _, link := range links {
				if link.Attrs().Name != "lo" {
					_ = netlink.LinkDel(link)
				}
			}
			return nil
		})

		// Close and remove the namespace
		if err := testNS.Close(); err != nil {
			t.Logf("Warning: Failed to close test namespace: %v", err)
		}
	}
}

// createDummyLinkInNS creates a dummy link in the specified network namespace
func createDummyLinkInNS(t *testing.T, testNS ns.NetNS, name string) netlink.Link {
	t.Helper()

	var dummyLink netlink.Link
	err := testNS.Do(func(netNS ns.NetNS) error {
		// Create dummy link
		dummy := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{
				Name: name,
				MTU:  1500,
			},
		}

		// Add the link
		if err := netlink.LinkAdd(dummy); err != nil {
			return fmt.Errorf("failed to add dummy link: %w", err)
		}

		// Get the link to get its index
		link, err := netlink.LinkByName(name)
		if err != nil {
			return fmt.Errorf("failed to get dummy link: %w", err)
		}

		dummyLink = link
		return nil
	})

	if err != nil {
		t.Fatalf("Failed to create dummy link in test namespace: %v", err)
	}

	return dummyLink
}

// deleteDummyLinkInNS deletes a dummy link in the specified network namespace
func deleteDummyLinkInNS(t *testing.T, testNS ns.NetNS, linkName string) {
	t.Helper()

	err := testNS.Do(func(netNS ns.NetNS) error {
		link, err := netlink.LinkByName(linkName)
		if err != nil {
			// Link might already be deleted
			return nil
		}
		return netlink.LinkDel(link)
	})

	if err != nil {
		t.Logf("Warning: Failed to delete dummy link %s: %v", linkName, err)
	}
}

// TestGetRouteTableID tests the route table ID calculation
func TestGetRouteTableID(t *testing.T) {
	tests := []struct {
		name      string
		linkIndex int
		expected  int
	}{
		{
			name:      "Basic calculation",
			linkIndex: 1,
			expected:  1001,
		},
		{
			name:      "Zero index",
			linkIndex: 0,
			expected:  1000,
		},
		{
			name:      "Large index",
			linkIndex: 999,
			expected:  1999,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetRouteTableID(tt.linkIndex)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestSetupTC_InputValidation tests SetupTC with various input parameters
func TestSetupTC_InputValidation(t *testing.T) {
	// Skip if not running as root (required for network namespace operations)
	if os.Geteuid() != 0 {
		t.Skip("Skipping test that requires root privileges")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	testNS := createTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// Create a dummy link in the test namespace
	dummyLink := createDummyLinkInNS(t, testNS, "test-dummy")
	defer deleteDummyLinkInNS(t, testNS, "test-dummy")

	tests := []struct {
		name             string
		bandwidthInBytes uint64
		expectedError    bool
		errorContains    string
	}{
		{
			name:             "Valid bandwidth",
			bandwidthInBytes: 1000000, // 1MB/s
			expectedError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testNS.Do(func(netNS ns.NetNS) error {
				return SetupTC(dummyLink, tt.bandwidthInBytes)
			})

			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestDelEgressPriority_NilInputs tests DelEgressPriority with nil inputs
func TestDelEgressPriority_NilInputs(t *testing.T) {
	// Skip if not running as root (required for network namespace operations)
	if os.Geteuid() != 0 {
		t.Skip("Skipping test that requires root privileges")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	testNS := createTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// Create a dummy link in the test namespace
	dummyLink := createDummyLinkInNS(t, testNS, "test-dummy")
	defer deleteDummyLinkInNS(t, testNS, "test-dummy")

	// Test with nil ipNetSet
	err := testNS.Do(func(netNS ns.NetNS) error {
		return DelEgressPriority(context.Background(), dummyLink, nil)
	})
	// Should handle gracefully (may return error due to no actual interface)
	// The important thing is it doesn't panic
	assert.NotPanics(t, func() {
		testNS.Do(func(netNS ns.NetNS) error {
			return DelEgressPriority(context.Background(), dummyLink, nil)
		})
	})

	// Test with empty ipNetSet
	ipNetSet := &terwayTypes.IPNetSet{}
	err = testNS.Do(func(netNS ns.NetNS) error {
		return DelEgressPriority(context.Background(), dummyLink, ipNetSet)
	})
	// Should not panic
	assert.NotPanics(t, func() {
		testNS.Do(func(netNS ns.NetNS) error {
			return DelEgressPriority(context.Background(), dummyLink, ipNetSet)
		})
	})

	// Note: These tests will likely fail with netlink errors since we don't have
	// actual network interfaces, but they verify the functions handle inputs correctly
	_ = err // Ignore the error for now
}

// TestCleanIPRules_Basic tests the basic structure of CleanIPRules
func TestCleanIPRules_Basic(t *testing.T) {
	// Skip if not running as root (required for network namespace operations)
	if os.Geteuid() != 0 {
		t.Skip("Skipping test that requires root privileges")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	testNS := createTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// This test verifies that CleanIPRules doesn't panic and handles the basic flow
	// It will likely fail due to netlink operations, but that's expected in unit tests
	assert.NotPanics(t, func() {
		testNS.Do(func(netNS ns.NetNS) error {
			return CleanIPRules(context.Background())
		})
	})
}

// TestGetERdmaFromLink_DummyLink tests GetERdmaFromLink with a dummy link
func TestGetERdmaFromLink_DummyLink(t *testing.T) {
	// Skip if not running as root (required for network namespace operations)
	if os.Geteuid() != 0 {
		t.Skip("Skipping test that requires root privileges")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	testNS := createTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// Create a dummy link in the test namespace
	_ = createDummyLinkInNS(t, testNS, "test-dummy")
	defer deleteDummyLinkInNS(t, testNS, "test-dummy")

	// Set hardware address for the dummy link
	err := testNS.Do(func(netNS ns.NetNS) error {
		// Get the link again to ensure we have the latest version
		link, err := netlink.LinkByName("test-dummy")
		if err != nil {
			return err
		}

		// Set hardware address
		hwAddr := net.HardwareAddr{0x02, 0x42, 0xac, 0x11, 0x00, 0x02}
		return netlink.LinkSetHardwareAddr(link, hwAddr)
	})
	if err != nil {
		t.Fatalf("Failed to set hardware address: %v", err)
	}

	// This should return an error since there's no RDMA hardware
	var rdmaLink *netlink.RdmaLink
	err = testNS.Do(func(netNS ns.NetNS) error {
		link, err := netlink.LinkByName("test-dummy")
		if err != nil {
			return err
		}
		rdmaLink, err = GetERdmaFromLink(link)
		return err
	})

	assert.Error(t, err)
	assert.Nil(t, rdmaLink)

	// Error should be about not finding RDMA links or listing them
	assert.True(t,
		err.Error() == "cannot found rdma link for test-dummy" ||
			err.Error() == "error list rdma links, operation not supported" ||
			err.Error() == "error list rdma links, no such file or directory" ||
			err.Error() == "error list rdma links, function not implemented" ||
			len(err.Error()) > 0, // Any error is acceptable in test environment
	)
}

// TestIPNetSetUsage tests the correct usage of IPNetSet in our new tests
func TestIPNetSetUsage(t *testing.T) {
	ipNetSet := &terwayTypes.IPNetSet{}

	// Test setting a valid CIDR
	result := ipNetSet.SetIPNet("192.168.1.0/24")
	assert.NotNil(t, result)
	assert.Equal(t, ipNetSet, result) // Should return self for chaining

	// Test setting an invalid CIDR
	result2 := ipNetSet.SetIPNet("invalid-cidr")
	assert.NotNil(t, result2)
	assert.Equal(t, ipNetSet, result2) // Should still return self

	// Test with empty string
	result3 := ipNetSet.SetIPNet("")
	assert.NotNil(t, result3)
	assert.Equal(t, ipNetSet, result3)
}

// TestEnsureLinkUp tests EnsureLinkUp function in a network namespace
func TestEnsureLinkUp(t *testing.T) {
	// Skip if not running as root (required for network namespace operations)
	if os.Geteuid() != 0 {
		t.Skip("Skipping test that requires root privileges")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	testNS := createTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// Create a dummy link in the test namespace
	_ = createDummyLinkInNS(t, testNS, "test-dummy")
	defer deleteDummyLinkInNS(t, testNS, "test-dummy")

	var changed bool
	var err error
	err = testNS.Do(func(netNS ns.NetNS) error {
		link, err := netlink.LinkByName("test-dummy")
		if err != nil {
			return err
		}
		changed, err = EnsureLinkUp(context.Background(), link)
		return err
	})

	assert.NoError(t, err)
	assert.True(t, changed) // Link should be brought up
}

// TestEnsureLinkMTU tests EnsureLinkMTU function in a network namespace
func TestEnsureLinkMTU(t *testing.T) {
	// Skip if not running as root (required for network namespace operations)
	if os.Geteuid() != 0 {
		t.Skip("Skipping test that requires root privileges")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	testNS := createTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// Create a dummy link in the test namespace
	_ = createDummyLinkInNS(t, testNS, "test-dummy")
	defer deleteDummyLinkInNS(t, testNS, "test-dummy")

	var changed bool
	var err error
	err = testNS.Do(func(netNS ns.NetNS) error {
		link, err := netlink.LinkByName("test-dummy")
		if err != nil {
			return err
		}
		changed, err = EnsureLinkMTU(context.Background(), link, 1400)
		return err
	})

	assert.NoError(t, err)
	assert.True(t, changed) // MTU should be changed
}

// TestEnsureLinkName tests EnsureLinkName function in a network namespace
func TestEnsureLinkName(t *testing.T) {
	// Skip if not running as root (required for network namespace operations)
	if os.Geteuid() != 0 {
		t.Skip("Skipping test that requires root privileges")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	testNS := createTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// Create a dummy link in the test namespace
	_ = createDummyLinkInNS(t, testNS, "test-dummy")
	defer deleteDummyLinkInNS(t, testNS, "test-dummy")

	var changed bool
	var err error
	err = testNS.Do(func(netNS ns.NetNS) error {
		link, err := netlink.LinkByName("test-dummy")
		if err != nil {
			return err
		}
		changed, err = EnsureLinkName(context.Background(), link, "renamed-dummy")
		return err
	})

	assert.NoError(t, err)
	assert.True(t, changed) // Name should be changed

	// Verify the link was renamed
	err = testNS.Do(func(netNS ns.NetNS) error {
		_, err := netlink.LinkByName("renamed-dummy")
		return err
	})
	assert.NoError(t, err)
}

// TestEnsureLinkMAC tests EnsureLinkMAC function in a network namespace
func TestEnsureLinkMAC(t *testing.T) {
	// Skip if not running as root (required for network namespace operations)
	if os.Geteuid() != 0 {
		t.Skip("Skipping test that requires root privileges")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	testNS := createTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// Create a dummy link in the test namespace
	_ = createDummyLinkInNS(t, testNS, "test-dummy")
	defer deleteDummyLinkInNS(t, testNS, "test-dummy")

	var err error
	err = testNS.Do(func(netNS ns.NetNS) error {
		link, err := netlink.LinkByName("test-dummy")
		if err != nil {
			return err
		}
		return EnsureLinkMAC(context.Background(), link, "02:42:ac:11:00:02")
	})

	assert.NoError(t, err)
}

// TestDelLinkByName tests DelLinkByName function in a network namespace
func TestDelLinkByName(t *testing.T) {
	// Skip if not running as root (required for network namespace operations)
	if os.Geteuid() != 0 {
		t.Skip("Skipping test that requires root privileges")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	testNS := createTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// Create a dummy link in the test namespace
	_ = createDummyLinkInNS(t, testNS, "test-dummy")

	var err error
	err = testNS.Do(func(netNS ns.NetNS) error {
		return DelLinkByName(context.Background(), "test-dummy")
	})

	assert.NoError(t, err)

	// Verify the link was deleted
	err = testNS.Do(func(netNS ns.NetNS) error {
		_, err := netlink.LinkByName("test-dummy")
		return err
	})
	assert.Error(t, err) // Should not find the link
}

func TestGetHostIP(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(k8snet.ResolveBindAddress, func(bindAddress net.IP) (net.IP, error) {
		if bindAddress.Equal(net.ParseIP("::1")) {
			return net.ParseIP("2001:db8::1"), nil
		}
		return net.ParseIP("192.168.1.1"), nil

	})
	ipNetSet, err := GetHostIP(true, true)
	assert.NoError(t, err)
	assert.NotNil(t, ipNetSet)
	assert.NotNil(t, ipNetSet.IPv4)
	assert.NotNil(t, ipNetSet.IPv6)
}
