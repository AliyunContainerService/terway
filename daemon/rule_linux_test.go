//go:build linux

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	"github.com/AliyunContainerService/terway/plugin/datapath"
	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/plugin/driver/utils"
	"github.com/AliyunContainerService/terway/rpc"
	terwayTypes "github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
	"k8s.io/klog/v2"
)

func TestGenerateConfig(t *testing.T) {
	setUp := &types.SetupConfig{
		ContainerIPNet: &terwayTypes.IPNetSet{
			IPv4: &net.IPNet{
				IP:   net.ParseIP("10.0.0.2"),
				Mask: net.CIDRMask(32, 32),
			},
			IPv6: &net.IPNet{
				IP:   net.ParseIP("fd0::2"),
				Mask: net.CIDRMask(128, 128),
			},
		},
		GatewayIP: &terwayTypes.IPSet{
			IPv4: net.ParseIP("169.254.169.254"),
			IPv6: net.ParseIP("fd0::1"),
		},
		ENIIndex: 1,
	}
	eni := &netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Index: 1,
			Name:  "eth1",
		},
	}
	eniConf := datapath.GenerateENICfgForPolicy(setUp, eni, 1)

	assert.True(t, eniConf.Routes[0].Equal(netlink.Route{
		Dst: &net.IPNet{
			IP:   net.ParseIP("0.0.0.0"),
			Mask: net.CIDRMask(0, 0),
		},
		Gw:        net.ParseIP("169.254.169.254"),
		Table:     1,
		LinkIndex: 1,
		Scope:     netlink.SCOPE_UNIVERSE,
		Flags:     int(netlink.FLAG_ONLINK),
	}))
	assert.True(t, eniConf.Routes[2].Equal(netlink.Route{
		Dst: &net.IPNet{
			IP:   net.ParseIP("::"),
			Mask: net.CIDRMask(0, 0),
		},
		Gw:        net.ParseIP("fd0::1"),
		Table:     1,
		LinkIndex: 1,
		Scope:     netlink.SCOPE_UNIVERSE,
		Flags:     int(netlink.FLAG_ONLINK),
	}))
}

func TestGenerateHostPeerCfgForPolicy(t *testing.T) {
	setUp := &types.SetupConfig{
		ContainerIPNet: &terwayTypes.IPNetSet{
			IPv4: &net.IPNet{
				IP:   net.ParseIP("10.0.0.2"),
				Mask: net.CIDRMask(32, 32),
			},
			IPv6: &net.IPNet{
				IP:   net.ParseIP("fd0::2"),
				Mask: net.CIDRMask(128, 128),
			},
		},
		GatewayIP: &terwayTypes.IPSet{
			IPv4: net.ParseIP("169.254.169.254"),
			IPv6: net.ParseIP("fd0::1"),
		},
		ENIIndex: 1,
	}
	veth := &netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Index: 1,
			Name:  "calixxx",
		},
	}
	vethConf := datapath.GenerateHostPeerCfgForPolicy(setUp, veth, 1)
	assert.Equal(t, 2, len(vethConf.Routes))
	assert.Equal(t, 4, len(vethConf.Rules))
}

// setupTestNetNS creates a test network namespace for testing
func setupTestNetNS(t *testing.T) ns.NetNS {
	t.Helper()

	testNS, err := testutils.NewNS()
	require.NoError(t, err, "Failed to create test network namespace")

	return testNS
}

// cleanupTestNetNS cleans up the test network namespace
func cleanupTestNetNS(t *testing.T, testNS ns.NetNS) {
	t.Helper()

	if testNS != nil {
		// Close the namespace
		err := testNS.Close()
		if err != nil {
			t.Logf("Warning: Failed to close test namespace: %v", err)
		}

		// Unmount the namespace
		err = testutils.UnmountNS(testNS)
		if err != nil {
			t.Logf("Warning: Failed to unmount test namespace: %v", err)
		}
	}
}

// createTestVethPair creates a veth pair for testing, all veth pair is under testNS
func createTestVethPair(t *testing.T, testNS ns.NetNS, hostVethName, containerVethName string) (netlink.Link, netlink.Link) {
	t.Helper()

	var hostVeth, containerVeth netlink.Link

	// Create veth pair in host namespace
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name: hostVethName,
		},
		PeerName: containerVethName,
	}

	err := testNS.Do(func(netNS ns.NetNS) error {
		err := netlink.LinkAdd(veth)
		if err != nil {
			return err
		}

		hostVeth, err = netlink.LinkByName(hostVethName)
		if err != nil {
			return err
		}
		containerVeth, err = netlink.LinkByName(containerVethName)
		if err != nil {
			return err
		}
		return nil
	})

	require.NoError(t, err, "Failed to create veth pair in test namespace")

	return hostVeth, containerVeth
}

// createTestENI creates a dummy ENI for testing
func createTestENI(t *testing.T, testNS ns.NetNS, name, mac string) netlink.Link {
	t.Helper()

	var eni netlink.Link
	err := testNS.Do(func(netNS ns.NetNS) error {
		// Create dummy link as ENI
		dummy := &netlink.Dummy{
			LinkAttrs: netlink.LinkAttrs{
				Name:         name,
				HardwareAddr: mustParseMAC(t, mac),
				MTU:          1500,
			},
		}

		err := netlink.LinkAdd(dummy)
		if err != nil {
			return err
		}

		eni, err = netlink.LinkByName(name)
		return err
	})
	require.NoError(t, err, "Failed to create test ENI")

	return eni
}

// mustParseMAC parses MAC address and panics on error
func mustParseMAC(t *testing.T, mac string) net.HardwareAddr {
	t.Helper()

	hw, err := net.ParseMAC(mac)
	require.NoError(t, err, "Failed to parse MAC address: %s", mac)
	return hw
}

// createTestPodResources creates test PodResources
func createTestPodResources(t *testing.T, podName, namespace string, netConf []*rpc.NetConf) daemon.PodResources {
	t.Helper()

	netConfJSON, err := json.Marshal(netConf)
	require.NoError(t, err, "Failed to marshal netConf")

	return daemon.PodResources{
		PodInfo: &daemon.PodInfo{
			Name:           podName,
			Namespace:      namespace,
			PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
		},
		NetConf: string(netConfJSON),
	}
}

// createTestNetConf creates test NetConf
func createTestNetConf(t *testing.T, ifName, podIP, gatewayIP, eniMAC string, trunk bool) *rpc.NetConf {
	t.Helper()

	return &rpc.NetConf{
		IfName: ifName,
		BasicInfo: &rpc.BasicInfo{
			PodIP: &rpc.IPSet{
				IPv4: podIP,
			},
			GatewayIP: &rpc.IPSet{
				IPv4: gatewayIP,
			},
		},
		ENIInfo: &rpc.ENIInfo{
			MAC:   eniMAC,
			Trunk: trunk,
		},
	}
}

// isRoot checks if the current process is running as root
func isRoot() bool {
	return os.Geteuid() == 0
}

// generateUniqueName generates a unique name for test interfaces
func generateUniqueName(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano()%1000000)
}

func TestRuleSync_Success(t *testing.T) {
	// Skip if not running as root
	if !isRoot() {
		t.Skip("Test requires root privileges")
	}

	testNS := setupTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// Test parameters
	podName := "test-pod"
	namespace := "default"
	hostVethName := generateUniqueName("cali")
	containerVethName := "eth0"
	eniName := generateUniqueName("test-eni")
	eniMAC := "02:42:ac:11:00:01"
	podIP := "10.0.0.2"
	gatewayIP := "10.0.0.1"

	// Create test resources
	createTestVethPair(t, testNS, hostVethName, containerVethName)

	createTestENI(t, testNS, eniName, eniMAC)

	// Create test configuration
	netConf := []*rpc.NetConf{
		createTestNetConf(t, "eth0", podIP, gatewayIP, eniMAC, false),
	}

	podResources := createTestPodResources(t, podName, namespace, netConf)

	// Run ruleSync in test namespace
	ctx := context.Background()
	err := testNS.Do(func(netNS ns.NetNS) error {
		return ruleSync(ctx, podResources)
	})
	require.NoError(t, err, "ruleSync should not return error")
}

func TestRuleSync_NoPodInfo(t *testing.T) {
	// Test with nil PodInfo
	podResources := daemon.PodResources{
		PodInfo: nil,
	}

	ctx := context.Background()
	err := ruleSync(ctx, podResources)
	assert.NoError(t, err, "ruleSync should return no error when PodInfo is nil")
}

func TestRuleSync_WrongNetworkType(t *testing.T) {
	// Test with wrong network type
	podResources := daemon.PodResources{
		PodInfo: &daemon.PodInfo{
			Name:           "test-pod",
			Namespace:      "default",
			PodNetworkType: daemon.PodNetworkTypeVPCENI, // Wrong type
		},
	}

	ctx := context.Background()
	err := ruleSync(ctx, podResources)
	assert.NoError(t, err, "ruleSync should return no error for wrong network type")
}

func TestRuleSync_InvalidNetConf(t *testing.T) {
	// Test with invalid NetConf JSON
	podResources := daemon.PodResources{
		PodInfo: &daemon.PodInfo{
			Name:           "test-pod",
			Namespace:      "default",
			PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
		},
		NetConf: "invalid json",
	}

	ctx := context.Background()
	err := ruleSync(ctx, podResources)
	assert.NoError(t, err, "ruleSync should return no error for invalid NetConf")
}

func TestRuleSync_EmptyNetConf(t *testing.T) {
	// Test with empty NetConf
	netConf := []*rpc.NetConf{}
	podResources := createTestPodResources(t, "test-pod", "default", netConf)

	ctx := context.Background()
	err := ruleSync(ctx, podResources)
	assert.NoError(t, err, "ruleSync should return no error for empty NetConf")
}

func TestRuleSync_MissingBasicInfo(t *testing.T) {
	// Test with missing BasicInfo
	netConf := []*rpc.NetConf{
		{
			IfName: "eth0",
			// BasicInfo is nil
			ENIInfo: &rpc.ENIInfo{
				MAC: "02:42:ac:11:00:01",
			},
		},
	}
	podResources := createTestPodResources(t, "test-pod", "default", netConf)

	ctx := context.Background()
	err := ruleSync(ctx, podResources)
	assert.NoError(t, err, "ruleSync should return no error for missing BasicInfo")
}

func TestRuleSync_MissingENIInfo(t *testing.T) {
	// Test with missing ENIInfo
	netConf := []*rpc.NetConf{
		{
			IfName: "eth0",
			BasicInfo: &rpc.BasicInfo{
				PodIP: &rpc.IPSet{
					IPv4: "10.0.0.2",
				},
			},
			// ENIInfo is nil
		},
	}
	podResources := createTestPodResources(t, "test-pod", "default", netConf)

	ctx := context.Background()
	err := ruleSync(ctx, podResources)
	assert.NoError(t, err, "ruleSync should return no error for missing ENIInfo")
}

func TestRuleSync_MissingPodIP(t *testing.T) {
	// Test with missing PodIP
	netConf := []*rpc.NetConf{
		{
			IfName: "eth0",
			BasicInfo: &rpc.BasicInfo{
				// PodIP is nil
				GatewayIP: &rpc.IPSet{
					IPv4: "10.0.0.1",
				},
			},
			ENIInfo: &rpc.ENIInfo{
				MAC: "02:42:ac:11:00:01",
			},
		},
	}
	podResources := createTestPodResources(t, "test-pod", "default", netConf)

	ctx := context.Background()
	err := ruleSync(ctx, podResources)
	assert.NoError(t, err, "ruleSync should return no error for missing PodIP")
}

func TestRuleSync_HostVethNotFound(t *testing.T) {
	// Skip if not running as root
	if !isRoot() {
		t.Skip("Test requires root privileges")
	}

	testNS := setupTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// Test parameters
	podName := "test-pod"
	namespace := "default"
	eniName := generateUniqueName("test-eni")
	eniMAC := "02:42:ac:11:00:01"
	podIP := "10.0.0.2"
	gatewayIP := "10.0.0.1"

	// Create ENI but no veth pair
	createTestENI(t, testNS, eniName, eniMAC)

	// Create test configuration
	netConf := []*rpc.NetConf{
		createTestNetConf(t, "eth0", podIP, gatewayIP, eniMAC, false),
	}

	podResources := createTestPodResources(t, podName, namespace, netConf)

	// Run ruleSync in test namespace
	ctx := context.Background()
	err := testNS.Do(func(netNS ns.NetNS) error {
		return ruleSync(ctx, podResources)
	})
	require.NoError(t, err, "ruleSync should not return error when host veth not found")
}

func TestRuleSync_ENINotFound(t *testing.T) {
	// Skip if not running as root
	if !isRoot() {
		t.Skip("Test requires root privileges")
	}

	testNS := setupTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// Test parameters
	podName := "test-pod"
	namespace := "default"
	hostVethName := generateUniqueName("cali")
	containerVethName := "eth0"
	//eniMAC := "02:42:ac:11:00:01"
	podIP := "10.0.0.2"
	gatewayIP := "10.0.0.1"

	// Create veth pair but no ENI
	createTestVethPair(t, testNS, hostVethName, containerVethName)

	// Create test configuration with non-existent ENI MAC
	netConf := []*rpc.NetConf{
		createTestNetConf(t, "eth0", podIP, gatewayIP, "02:42:ac:11:00:99", false),
	}

	podResources := createTestPodResources(t, podName, namespace, netConf)

	// Run ruleSync in test namespace
	ctx := context.Background()
	err := testNS.Do(func(netNS ns.NetNS) error {
		return ruleSync(ctx, podResources)
	})
	require.NoError(t, err, "ruleSync should not return error when ENI not found")
}

func TestRuleSync_TrunkMode(t *testing.T) {
	// Skip if not running as root
	if !isRoot() {
		t.Skip("Test requires root privileges")
	}

	testNS := setupTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// Test parameters
	podName := "test-pod"
	namespace := "default"
	hostVethName := generateUniqueName("cali")
	containerVethName := "eth0"
	eniName := generateUniqueName("test-eni")
	eniMAC := "02:42:ac:11:00:01"
	podIP := "10.0.0.2"
	gatewayIP := "10.0.0.1"
	eniGatewayIP := "10.0.0.254"

	// Create test resources
	createTestVethPair(t, testNS, hostVethName, containerVethName)

	createTestENI(t, testNS, eniName, eniMAC)

	// Create test configuration with trunk mode
	netConf := []*rpc.NetConf{
		{
			IfName: "eth0",
			BasicInfo: &rpc.BasicInfo{
				PodIP: &rpc.IPSet{
					IPv4: podIP,
				},
				GatewayIP: &rpc.IPSet{
					IPv4: gatewayIP,
				},
			},
			ENIInfo: &rpc.ENIInfo{
				MAC:   eniMAC,
				Trunk: true,
				GatewayIP: &rpc.IPSet{
					IPv4: eniGatewayIP,
				},
			},
		},
	}

	podResources := createTestPodResources(t, podName, namespace, netConf)

	// Run ruleSync
	ctx := context.Background()
	err := ruleSync(ctx, podResources)
	require.NoError(t, err, "ruleSync should not return error in trunk mode")
}

func TestRuleSync_IPv6(t *testing.T) {
	// Skip if not running as root
	if !isRoot() {
		t.Skip("Test requires root privileges")
	}

	testNS := setupTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// Test parameters
	podName := "test-pod"
	namespace := "default"
	hostVethName := generateUniqueName("cali")
	containerVethName := "eth0"
	eniName := generateUniqueName("test-eni")
	eniMAC := "02:42:ac:11:00:01"
	podIP := "2001:db8::2"
	gatewayIP := "2001:db8::1"

	// Create test resources
	createTestVethPair(t, testNS, hostVethName, containerVethName)

	createTestENI(t, testNS, eniName, eniMAC)

	// Create test configuration with IPv6
	netConf := []*rpc.NetConf{
		{
			IfName: "eth0",
			BasicInfo: &rpc.BasicInfo{
				PodIP: &rpc.IPSet{
					IPv6: podIP,
				},
				GatewayIP: &rpc.IPSet{
					IPv6: gatewayIP,
				},
			},
			ENIInfo: &rpc.ENIInfo{
				MAC: eniMAC,
			},
		},
	}

	podResources := createTestPodResources(t, podName, namespace, netConf)

	// Run ruleSync
	ctx := context.Background()
	err := ruleSync(ctx, podResources)
	require.NoError(t, err, "ruleSync should not return error with IPv6")
}

func TestRuleSync_WithRouteAndRuleVerification(t *testing.T) {
	// Skip if not running as root
	if !isRoot() {
		t.Skip("Test requires root privileges")
	}

	testNS := setupTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// Test parameters
	podName := "test-pod"
	namespace := "default"
	hostVethName := generateUniqueName("cali")
	containerVethName := "eth0"
	eniName := generateUniqueName("test-eni")
	eniMAC := "02:42:ac:11:00:01"
	podIP := "10.0.0.2"
	gatewayIP := "10.0.0.1"

	// Create test resources
	createTestVethPair(t, testNS, hostVethName, containerVethName)

	createTestENI(t, testNS, eniName, eniMAC)

	// Create test configuration
	netConf := []*rpc.NetConf{
		createTestNetConf(t, "eth0", podIP, gatewayIP, eniMAC, false),
	}

	podResources := createTestPodResources(t, podName, namespace, netConf)

	// Run ruleSync in test namespace
	ctx := context.Background()
	err := testNS.Do(func(netNS ns.NetNS) error {
		return ruleSync(ctx, podResources)
	})
	require.NoError(t, err, "ruleSync should not return error")
}

func TestRuleSync_MultipleNetConf(t *testing.T) {
	// Skip if not running as root
	if !isRoot() {
		t.Skip("Test requires root privileges")
	}

	testNS := setupTestNetNS(t)
	defer cleanupTestNetNS(t, testNS)

	// Test parameters
	podName := "test-pod"
	namespace := "default"
	hostVethName1 := generateUniqueName("cali1")
	hostVethName2 := generateUniqueName("cali2")
	containerVethName1 := "eth0"
	containerVethName2 := "eth1"
	eniName1 := generateUniqueName("eni-1")
	eniName2 := generateUniqueName("eni-2")
	eniMAC1 := "02:42:ac:11:00:01"
	eniMAC2 := "02:42:ac:11:00:02"
	podIP1 := "10.0.0.2"
	podIP2 := "10.0.0.3"
	gatewayIP1 := "10.0.0.1"
	gatewayIP2 := "10.0.0.1"

	// Create test resources
	createTestVethPair(t, testNS, hostVethName1, containerVethName1)

	createTestVethPair(t, testNS, hostVethName2, containerVethName2)

	createTestENI(t, testNS, eniName1, eniMAC1)

	createTestENI(t, testNS, eniName2, eniMAC2)

	// Create test configuration with multiple NetConf
	netConf := []*rpc.NetConf{
		createTestNetConf(t, "eth0", podIP1, gatewayIP1, eniMAC1, false),
		createTestNetConf(t, "eth1", podIP2, gatewayIP2, eniMAC2, false),
	}

	podResources := createTestPodResources(t, podName, namespace, netConf)

	// Run ruleSync in test namespace
	ctx := context.Background()
	ctx = logr.NewContext(ctx, klog.NewKlogr())
	err := testNS.Do(func(netNS ns.NetNS) error {
		return ruleSync(ctx, podResources)
	})
	require.NoError(t, err, "ruleSync should not return error with multiple NetConf")

	// Verify routes and rules
	// because of we verify link type , this check can not be done
}

// TestRuleSyncWithGomonkey tests ruleSync function with gomonkey mocks
func TestRuleSyncWithGomonkey(t *testing.T) {
	tests := []struct {
		name          string
		podResources  daemon.PodResources
		setupMocks    func() *gomonkey.Patches
		expectedError bool
	}{
		{
			name: "success_case",
			podResources: daemon.PodResources{
				PodInfo: &daemon.PodInfo{
					Name:           "test-pod",
					Namespace:      "default",
					PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
				},
				NetConf: `[{"BasicInfo":{"PodIP":{"IPv4":"10.0.0.2"}},"ENIInfo":{"MAC":"00:11:22:33:44:55"}}]`,
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock nodecap.GetNodeCapabilities
				patches.ApplyFunc(nodecap.GetNodeCapabilities, func(key string) string {
					return "veth"
				})

				// Mock netlink.LinkList
				patches.ApplyFunc(netlink.LinkList, func() ([]netlink.Link, error) {
					hostVeth := &netlink.Veth{
						LinkAttrs: netlink.LinkAttrs{
							Index: 10,
							Name:  "cali12345678901",
						},
					}
					eni := &netlink.Device{
						LinkAttrs: netlink.LinkAttrs{
							Index:        20,
							Name:         "eth1",
							HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
						},
					}
					return []netlink.Link{hostVeth, eni}, nil
				})

				// Mock utils.GetRouteTableID
				patches.ApplyFunc(utils.GetRouteTableID, func(index int) int {
					return 1000 + index
				})

				// Mock utils.EnsureRoute
				patches.ApplyFunc(utils.EnsureRoute, func(ctx context.Context, route *netlink.Route) (bool, error) {
					return true, nil
				})

				// Mock utils.EnsureIPRule
				patches.ApplyFunc(utils.EnsureIPRule, func(ctx context.Context, rule *netlink.Rule) (bool, error) {
					return true, nil
				})

				return patches
			},
			expectedError: false,
		},
		{
			name: "unsupported_datapath",
			podResources: daemon.PodResources{
				PodInfo: &daemon.PodInfo{
					Name:           "test-pod",
					Namespace:      "default",
					PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
				},
				NetConf: `[]`,
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock nodecap.GetNodeCapabilities to return unsupported datapath
				patches.ApplyFunc(nodecap.GetNodeCapabilities, func(key string) string {
					return "ipvlan"
				})

				return patches
			},
			expectedError: false,
		},
		{
			name: "netlink_LinkList_error",
			podResources: daemon.PodResources{
				PodInfo: &daemon.PodInfo{
					Name:           "test-pod",
					Namespace:      "default",
					PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
				},
				NetConf: `[{"BasicInfo":{"PodIP":{"IPv4":"10.0.0.2"}},"ENIInfo":{"MAC":"00:11:22:33:44:55"}}]`,
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock nodecap.GetNodeCapabilities
				patches.ApplyFunc(nodecap.GetNodeCapabilities, func(key string) string {
					return "veth"
				})

				// Mock netlink.LinkList to return error
				patches.ApplyFunc(netlink.LinkList, func() ([]netlink.Link, error) {
					return nil, fmt.Errorf("failed to list links")
				})

				return patches
			},
			expectedError: true,
		},
		{
			name: "trunk_mode_without_gateway",
			podResources: daemon.PodResources{
				PodInfo: &daemon.PodInfo{
					Name:           "test-pod",
					Namespace:      "default",
					PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
				},
				NetConf: `[{"BasicInfo":{"PodIP":{"IPv4":"10.0.0.2"},"GatewayIP":{"IPv4":"10.0.0.1"}},"ENIInfo":{"MAC":"00:11:22:33:44:55","Trunk":true}}]`,
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock nodecap.GetNodeCapabilities
				patches.ApplyFunc(nodecap.GetNodeCapabilities, func(key string) string {
					return "veth"
				})

				// Mock netlink.LinkList
				patches.ApplyFunc(netlink.LinkList, func() ([]netlink.Link, error) {
					hostVeth := &netlink.Veth{
						LinkAttrs: netlink.LinkAttrs{
							Index: 10,
							Name:  "cali12345678901",
						},
					}
					eni := &netlink.Device{
						LinkAttrs: netlink.LinkAttrs{
							Index:        20,
							Name:         "eth1",
							HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
						},
					}
					return []netlink.Link{hostVeth, eni}, nil
				})

				return patches
			},
			expectedError: false,
		},
		{
			name: "trunk_mode_with_gateway",
			podResources: daemon.PodResources{
				PodInfo: &daemon.PodInfo{
					Name:           "test-pod",
					Namespace:      "default",
					PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
				},
				NetConf: `[{"BasicInfo":{"PodIP":{"IPv4":"10.0.0.2"},"GatewayIP":{"IPv4":"10.0.0.1"}},"ENIInfo":{"MAC":"00:11:22:33:44:55","Trunk":true,"GatewayIP":{"IPv4":"10.0.0.254"}}}]`,
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock nodecap.GetNodeCapabilities
				patches.ApplyFunc(nodecap.GetNodeCapabilities, func(key string) string {
					return "veth"
				})

				// Mock netlink.LinkList
				patches.ApplyFunc(netlink.LinkList, func() ([]netlink.Link, error) {
					hostVeth := &netlink.Veth{
						LinkAttrs: netlink.LinkAttrs{
							Index: 10,
							Name:  "cali12345678901",
						},
					}
					eni := &netlink.Device{
						LinkAttrs: netlink.LinkAttrs{
							Index:        20,
							Name:         "eth1",
							HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
						},
					}
					return []netlink.Link{hostVeth, eni}, nil
				})

				// Mock utils.GetRouteTableID
				patches.ApplyFunc(utils.GetRouteTableID, func(index int) int {
					return 1000 + index
				})

				// Mock utils.EnsureRoute
				patches.ApplyFunc(utils.EnsureRoute, func(ctx context.Context, route *netlink.Route) (bool, error) {
					return true, nil
				})

				// Mock utils.EnsureIPRule
				patches.ApplyFunc(utils.EnsureIPRule, func(ctx context.Context, rule *netlink.Rule) (bool, error) {
					return true, nil
				})

				return patches
			},
			expectedError: false,
		},
		{
			name: "ipv6_support",
			podResources: daemon.PodResources{
				PodInfo: &daemon.PodInfo{
					Name:           "test-pod",
					Namespace:      "default",
					PodNetworkType: daemon.PodNetworkTypeENIMultiIP,
				},
				NetConf: `[{"BasicInfo":{"PodIP":{"IPv6":"2001:db8::2"},"GatewayIP":{"IPv6":"2001:db8::1"}},"ENIInfo":{"MAC":"00:11:22:33:44:55"}}]`,
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()

				// Mock nodecap.GetNodeCapabilities
				patches.ApplyFunc(nodecap.GetNodeCapabilities, func(key string) string {
					return "veth"
				})

				// Mock netlink.LinkList
				patches.ApplyFunc(netlink.LinkList, func() ([]netlink.Link, error) {
					hostVeth := &netlink.Veth{
						LinkAttrs: netlink.LinkAttrs{
							Index: 10,
							Name:  "cali12345678901",
						},
					}
					eni := &netlink.Device{
						LinkAttrs: netlink.LinkAttrs{
							Index:        20,
							Name:         "eth1",
							HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
						},
					}
					return []netlink.Link{hostVeth, eni}, nil
				})

				// Mock utils.GetRouteTableID
				patches.ApplyFunc(utils.GetRouteTableID, func(index int) int {
					return 1000 + index
				})

				// Mock utils.EnsureRoute
				patches.ApplyFunc(utils.EnsureRoute, func(ctx context.Context, route *netlink.Route) (bool, error) {
					return true, nil
				})

				// Mock utils.EnsureIPRule
				patches.ApplyFunc(utils.EnsureIPRule, func(ctx context.Context, rule *netlink.Rule) (bool, error) {
					return true, nil
				})

				return patches
			},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mocks
			patches := tt.setupMocks()
			defer patches.Reset()
			defer runtime.GC()

			// Execute test
			ctx := context.Background()
			err := ruleSync(ctx, tt.podResources)

			// Verify results
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
