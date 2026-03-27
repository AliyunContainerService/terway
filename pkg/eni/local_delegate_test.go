package eni

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/types/daemon"
)

// TestLocalDelegate_Allocate_WrongResourceType tests Allocate with wrong resource type
func TestLocalDelegate_Allocate_WrongResourceType(t *testing.T) {
	delegate := &LocalDelegate{
		eniIPAMs: make(map[ENIID]*ENILocalIPAM),
	}

	ctx := context.Background()
	cni := &daemon.CNI{PodID: "test-pod"}

	wrongRequest := &localDelegateMockResourceRequest{resourceType: ResourceTypeVeth}

	respCh, traces := delegate.Allocate(ctx, cni, wrongRequest)

	assert.Nil(t, respCh)
	assert.Len(t, traces, 1)
	assert.Equal(t, ResourceTypeMismatch, traces[0].Condition)
}

// TestLocalDelegate_Allocate_Active_IPv4 tests Allocate with active delegate and IPv4
func TestLocalDelegate_Allocate_Active_IPv4(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:         "eni-test123",
		MacAddress: "00:11:22:33:44:55",
		VSwitchID:  "vsw-test123",
		IPv4CIDR:   "192.168.1.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{
				Prefix: "192.168.1.0/28",
				Status: networkv1beta1.IPPrefixStatusValid,
			},
		},
	}

	ipam := NewENILocalIPAMFromPrefix("eni-test123", "00:11:22:33:44:55", eni, false)
	assert.NotNil(t, ipam)

	delegate := &LocalDelegate{
		eniIPAMs:   map[ENIID]*ENILocalIPAM{"eni-test123": ipam},
		notifier:   NewNotifier(),
		enableIPv4: true,
		enableIPv6: false,
	}

	ctx := context.Background()
	cni := &daemon.CNI{PodID: "default/test-pod"}
	request := NewLocalIPRequest()

	respCh, traces := delegate.Allocate(ctx, cni, request)

	assert.NotNil(t, respCh)
	assert.Nil(t, traces)

	resp := <-respCh
	assert.NotNil(t, resp)
	assert.Nil(t, resp.Err)
	assert.Len(t, resp.NetworkConfigs, 1)

	resource, ok := resp.NetworkConfigs[0].(*LocalIPResource)
	assert.True(t, ok)
	assert.Equal(t, "default/test-pod", resource.PodID)
	assert.Equal(t, "eni-test123", resource.ENI.ID)
	assert.True(t, resource.IP.IPv4.IsValid())
}

// TestLocalDelegate_Release_Inactive tests Release when delegate is inactive
func TestLocalDelegate_Release_Inactive(t *testing.T) {
	delegate := &LocalDelegate{}

	ctx := context.Background()
	cni := &daemon.CNI{PodID: "test-pod"}
	resource := &LocalIPResource{}

	ok, err := delegate.Release(ctx, cni, resource)

	assert.False(t, ok)
	assert.Nil(t, err)
}

// TestLocalDelegate_Release_Active tests Release with active delegate
func TestLocalDelegate_Release_Active(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:         "eni-test123",
		MacAddress: "00:11:22:33:44:55",
		VSwitchID:  "vsw-test123",
		IPv4CIDR:   "192.168.1.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{
				Prefix: "192.168.1.0/28",
				Status: networkv1beta1.IPPrefixStatusValid,
			},
		},
	}

	ipam := NewENILocalIPAMFromPrefix("eni-test123", "00:11:22:33:44:55", eni, false)
	assert.NotNil(t, ipam)

	delegate := &LocalDelegate{
		eniIPAMs:   map[ENIID]*ENILocalIPAM{"eni-test123": ipam},
		notifier:   NewNotifier(),
		enableIPv4: true,
		enableIPv6: false,
	}

	ctx := context.Background()
	cni := &daemon.CNI{PodID: "default/test-pod"}
	request := NewLocalIPRequest()

	respCh, _ := delegate.Allocate(ctx, cni, request)
	resp := <-respCh
	assert.NotNil(t, resp)
	assert.Nil(t, resp.Err)

	resource := resp.NetworkConfigs[0].(*LocalIPResource)

	ok, err := delegate.Release(ctx, cni, resource)

	assert.True(t, ok)
	assert.Nil(t, err)
}

// TestLocalDelegate_syncNodeCR_PrefixMode_NoPanic verifies that syncNodeCR does not panic
// when an existing Prefix-mode ENI IPAM receives an UpdateIPs call triggered by the Node CR
// containing IPv4 IPs alongside IPv4Prefixes — the original nil map panic scenario.
func TestLocalDelegate_syncNodeCR_PrefixMode_NoPanic(t *testing.T) {
	// Build a Node CR that has an InUse ENI with both IPv4Prefix and IPv4 IPs.
	nodeCR := &networkv1beta1.Node{
		Status: networkv1beta1.NodeStatus{
			NetworkInterfaces: map[string]*networkv1beta1.Nic{
				"eni-prefix-sync": {
					ID:         "eni-prefix-sync",
					MacAddress: "00:11:22:33:44:66",
					VSwitchID:  "vsw-sync-test",
					Status:     "InUse",
					IPv4CIDR:   "10.1.0.0/24",
					IPv4Prefix: []networkv1beta1.IPPrefix{
						{
							Prefix: "10.1.0.0/28",
							Status: networkv1beta1.IPPrefixStatusValid,
						},
					},
					// IPv4 IPs also present — triggers UpdateIPs on the Prefix-mode IPAM
					IPv4: map[string]*networkv1beta1.IP{
						"10.1.0.1": {
							IP:      "10.1.0.1",
							Primary: true,
							Status:  networkv1beta1.IPStatusValid,
						},
					},
				},
			},
		},
	}
	nodeCR.Name = "test-node"

	// Register the CRD scheme and build a fake client (logic is trivial: single Get + in-memory update)
	scheme := runtime.NewScheme()
	require.NoError(t, networkv1beta1.AddToScheme(scheme))
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nodeCR).Build()

	// Pre-populate the delegate with a Prefix-mode IPAM for the same ENI.
	// This is the state that triggers the panic: existing IPAM is Prefix mode,
	// syncNodeCR calls UpdateIPs on it.
	existingIPAM := NewENILocalIPAMFromPrefix("eni-prefix-sync", "00:11:22:33:44:66", &networkv1beta1.Nic{
		ID:         "eni-prefix-sync",
		MacAddress: "00:11:22:33:44:66",
		VSwitchID:  "vsw-sync-test",
		IPv4CIDR:   "10.1.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{
				Prefix: "10.1.0.0/28",
				Status: networkv1beta1.IPPrefixStatusValid,
			},
		},
	}, false)

	delegate := &LocalDelegate{
		client:     fakeClient,
		nodeName:   "test-node",
		eniIPAMs:   map[ENIID]*ENILocalIPAM{"eni-prefix-sync": existingIPAM},
		enableIPv4: true,
		enableIPv6: false,
	}

	ctx := context.Background()

	// Must NOT panic — regression test for the nil map panic
	assert.NotPanics(t, func() {
		delegate.syncNodeCR(ctx)
	})

	// Prefix state must be intact after sync
	delegate.lock.RLock()
	ipam := delegate.eniIPAMs["eni-prefix-sync"]
	delegate.lock.RUnlock()

	require.NotNil(t, ipam)
	assert.Contains(t, ipam.ipv4PrefixMap, "10.1.0.0/28")
}

// TestLocalDelegate_Priority verifies LocalDelegate has higher priority than CRDV2.
func TestLocalDelegate_Priority(t *testing.T) {
	delegate := &LocalDelegate{}
	assert.Equal(t, 200, delegate.Priority())
	assert.Greater(t, delegate.Priority(), (&CRDV2{}).Priority())
}

// TestLocalDelegate_Dispose returns 0.
func TestLocalDelegate_Dispose(t *testing.T) {
	delegate := &LocalDelegate{}
	assert.Equal(t, 0, delegate.Dispose(5))
}

// TestLocalDelegate_DualStack_SameENI verifies that dual-stack allocation comes from the same ENI.
func TestLocalDelegate_DualStack_SameENI(t *testing.T) {
	eni1 := &networkv1beta1.Nic{
		ID:         "eni-ds1",
		MacAddress: "00:11:22:33:44:55",
		VSwitchID:  "vsw-ds1",
		IPv4CIDR:   "10.0.0.0/24",
		IPv6CIDR:   "fd00::/112",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}

	ipam1 := NewENILocalIPAMFromPrefix("eni-ds1", "00:11:22:33:44:55", eni1, false)
	require.NotNil(t, ipam1)

	delegate := &LocalDelegate{
		eniIPAMs:   map[ENIID]*ENILocalIPAM{"eni-ds1": ipam1},
		notifier:   NewNotifier(),
		enableIPv4: true,
		enableIPv6: true,
	}

	ctx := context.Background()
	cni := &daemon.CNI{PodID: "default/dual-pod"}
	request := NewLocalIPRequest()

	respCh, _ := delegate.Allocate(ctx, cni, request)
	resp := <-respCh
	require.NotNil(t, resp)
	require.Nil(t, resp.Err)
	require.Len(t, resp.NetworkConfigs, 1)

	resource, ok := resp.NetworkConfigs[0].(*LocalIPResource)
	require.True(t, ok)
	assert.Equal(t, "eni-ds1", resource.ENI.ID)
	assert.True(t, resource.IP.IPv4.IsValid(), "IPv4 must be allocated")
	assert.True(t, resource.IP.IPv6.IsValid(), "IPv6 must be allocated")
}

// TestLocalDelegate_DualStack_RollbackOnIPv6Failure verifies that if IPv6 allocation
// fails on an ENI, the IPv4 allocation is rolled back and the next ENI is tried.
func TestLocalDelegate_DualStack_RollbackOnIPv6Failure(t *testing.T) {
	eni1 := &networkv1beta1.Nic{
		ID:         "eni-v4only",
		MacAddress: "00:11:22:33:44:01",
		VSwitchID:  "vsw-1",
		IPv4CIDR:   "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
		// No IPv6 prefix -> IPv6 allocation will fail
	}

	eni2 := &networkv1beta1.Nic{
		ID:         "eni-dualstack",
		MacAddress: "00:11:22:33:44:02",
		VSwitchID:  "vsw-2",
		IPv4CIDR:   "10.0.1.0/24",
		IPv6CIDR:   "fd00:1::/112",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.1.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "fd00:1::/120", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}

	ipam1 := NewENILocalIPAMFromPrefix("eni-v4only", "00:11:22:33:44:01", eni1, false)
	ipam2 := NewENILocalIPAMFromPrefix("eni-dualstack", "00:11:22:33:44:02", eni2, false)

	delegate := &LocalDelegate{
		eniIPAMs: map[ENIID]*ENILocalIPAM{
			"eni-v4only":    ipam1,
			"eni-dualstack": ipam2,
		},
		notifier:   NewNotifier(),
		enableIPv4: true,
		enableIPv6: true,
	}

	ctx := context.Background()
	cni := &daemon.CNI{PodID: "default/ds-pod"}
	request := NewLocalIPRequest()

	respCh, _ := delegate.Allocate(ctx, cni, request)
	resp := <-respCh
	require.NotNil(t, resp)
	require.Nil(t, resp.Err)

	resource, ok := resp.NetworkConfigs[0].(*LocalIPResource)
	require.True(t, ok)

	// Must come from the ENI that has both IPv4 and IPv6
	assert.Equal(t, "eni-dualstack", resource.ENI.ID)
	assert.True(t, resource.IP.IPv4.IsValid())
	assert.True(t, resource.IP.IPv6.IsValid())

	// Verify that eni-v4only's IPv4 was rolled back (no allocations left)
	assert.Empty(t, ipam1.podToPrefixV4, "IPv4 should be rolled back on eni-v4only")
}

// TestLocalDelegate_Allocate_ReturnsNil_WhenInactive verifies Manager fall-through behavior.
func TestLocalDelegate_Allocate_ReturnsNil_WhenInactive(t *testing.T) {
	delegate := &LocalDelegate{
		notifier: NewNotifier(),
	}

	ctx := context.Background()
	cni := &daemon.CNI{PodID: "test-pod"}
	request := NewLocalIPRequest()

	respCh, traces := delegate.Allocate(ctx, cni, request)
	assert.Nil(t, respCh, "nil channel signals Manager to try next NetworkInterface")
	assert.Nil(t, traces)
}

// TestLocalDelegate_Release_ReturnsNil_WhenInactive verifies Manager fall-through on Release.
func TestLocalDelegate_Release_ReturnsNil_WhenInactive(t *testing.T) {
	delegate := &LocalDelegate{
		notifier: NewNotifier(),
	}

	ctx := context.Background()
	cni := &daemon.CNI{PodID: "test-pod"}
	resource := &LocalIPResource{}

	released, err := delegate.Release(ctx, cni, resource)
	assert.False(t, released)
	assert.Nil(t, err)
}

// TestLocalDelegate_Statuses verifies Statuses() returns correct prefix snapshots.
func TestLocalDelegate_Statuses(t *testing.T) {
	eni1 := &networkv1beta1.Nic{
		ID:         "eni-status1",
		MacAddress: "00:11:22:33:44:01",
		VSwitchID:  "vsw-status",
		IPv4CIDR:   "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	eni2 := &networkv1beta1.Nic{
		ID:         "eni-status2",
		MacAddress: "00:11:22:33:44:02",
		VSwitchID:  "vsw-status",
		IPv4CIDR:   "10.0.1.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.1.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}

	ipam1 := NewENILocalIPAMFromPrefix("eni-status1", "00:11:22:33:44:01", eni1, false)
	ipam2 := NewENILocalIPAMFromPrefix("eni-status2", "00:11:22:33:44:02", eni2, false)

	_, err := ipam1.AllocateIPv4("pod-1")
	require.NoError(t, err)

	delegate := &LocalDelegate{
		eniIPAMs: map[ENIID]*ENILocalIPAM{
			"eni-status1": ipam1,
			"eni-status2": ipam2,
		},
	}

	statuses := delegate.Statuses()
	assert.Len(t, statuses, 2)

	found := map[string]Status{}
	for _, s := range statuses {
		found[s.NetworkInterfaceID] = s
	}

	s1 := found["eni-status1"]
	assert.Equal(t, "Prefix", s1.Type)
	assert.Equal(t, "InUse", s1.Status)
	assert.Equal(t, "00:11:22:33:44:01", s1.MAC)
	require.Len(t, s1.IPv4Prefixes, 1)
	assert.Equal(t, 1, s1.IPv4Prefixes[0].Used)

	s2 := found["eni-status2"]
	assert.Equal(t, "Prefix", s2.Type)
	require.Len(t, s2.IPv4Prefixes, 1)
	assert.Equal(t, 0, s2.IPv4Prefixes[0].Used)
}

// TestLocalDelegate_Statuses_Empty verifies Statuses() returns empty when no IPAMs exist.
func TestLocalDelegate_Statuses_Empty(t *testing.T) {
	delegate := &LocalDelegate{
		eniIPAMs: make(map[ENIID]*ENILocalIPAM),
	}
	statuses := delegate.Statuses()
	assert.Empty(t, statuses)
}

// TestLocalDelegate_Allocate_CtxCancelled verifies allocation fails when context is cancelled.
func TestLocalDelegate_Allocate_CtxCancelled(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:         "eni-ctx-cancel",
		MacAddress: "00:11:22:33:44:cc",
		VSwitchID:  "vsw-ctx",
		IPv4CIDR:   "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/30", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-ctx-cancel", "00:11:22:33:44:cc", eni, false)

	for i := 0; i < 4; i++ {
		_, err := ipam.AllocateIPv4(fmt.Sprintf("fill-%d", i))
		require.NoError(t, err)
	}

	delegate := &LocalDelegate{
		eniIPAMs:   map[ENIID]*ENILocalIPAM{"eni-ctx-cancel": ipam},
		notifier:   NewNotifier(),
		enableIPv4: true,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cni := &daemon.CNI{PodID: "default/blocked-pod"}
	request := NewLocalIPRequest()

	respCh, _ := delegate.Allocate(ctx, cni, request)
	require.NotNil(t, respCh)

	cancel()

	resp := <-respCh
	require.NotNil(t, resp)
	assert.Error(t, resp.Err)
}

// TestLocalDelegate_syncNodeCR_AddNewENI verifies that syncNodeCR adds a new ENI IPAM.
func TestLocalDelegate_syncNodeCR_AddNewENI(t *testing.T) {
	nodeCR := &networkv1beta1.Node{
		Status: networkv1beta1.NodeStatus{
			NetworkInterfaces: map[string]*networkv1beta1.Nic{
				"eni-existing": {
					ID:         "eni-existing",
					MacAddress: "00:11:22:33:44:e1",
					Status:     "InUse",
					IPv4CIDR:   "10.0.0.0/24",
					IPv4Prefix: []networkv1beta1.IPPrefix{
						{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
					},
				},
				"eni-new": {
					ID:         "eni-new",
					MacAddress: "00:11:22:33:44:e2",
					Status:     "InUse",
					IPv4CIDR:   "10.0.1.0/24",
					IPv4Prefix: []networkv1beta1.IPPrefix{
						{Prefix: "10.0.1.0/28", Status: networkv1beta1.IPPrefixStatusValid},
					},
				},
			},
		},
	}
	nodeCR.Name = "test-node-add"

	scheme := runtime.NewScheme()
	require.NoError(t, networkv1beta1.AddToScheme(scheme))
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nodeCR).Build()

	existingIPAM := NewENILocalIPAMFromPrefix("eni-existing", "00:11:22:33:44:e1", &networkv1beta1.Nic{
		ID:       "eni-existing",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}, false)

	delegate := &LocalDelegate{
		client:     fakeClient,
		nodeName:   "test-node-add",
		eniIPAMs:   map[ENIID]*ENILocalIPAM{"eni-existing": existingIPAM},
		enableIPv4: true,
	}

	delegate.syncNodeCR(context.Background())

	delegate.lock.RLock()
	defer delegate.lock.RUnlock()
	assert.Len(t, delegate.eniIPAMs, 2)
	assert.Contains(t, delegate.eniIPAMs, ENIID("eni-new"))
}

// TestLocalDelegate_syncNodeCR_RemoveENI verifies that syncNodeCR removes an ENI IPAM
// when the ENI disappears and has no allocations.
func TestLocalDelegate_syncNodeCR_RemoveENI(t *testing.T) {
	nodeCR := &networkv1beta1.Node{
		Status: networkv1beta1.NodeStatus{
			NetworkInterfaces: map[string]*networkv1beta1.Nic{
				"eni-remaining": {
					ID:         "eni-remaining",
					MacAddress: "00:11:22:33:44:r1",
					Status:     "InUse",
					IPv4CIDR:   "10.0.0.0/24",
					IPv4Prefix: []networkv1beta1.IPPrefix{
						{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
					},
				},
			},
		},
	}
	nodeCR.Name = "test-node-remove"

	scheme := runtime.NewScheme()
	require.NoError(t, networkv1beta1.AddToScheme(scheme))
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nodeCR).Build()

	ipamRemaining := NewENILocalIPAMFromPrefix("eni-remaining", "00:11:22:33:44:r1", &networkv1beta1.Nic{
		ID:       "eni-remaining",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}, false)

	ipamRemoved := NewENILocalIPAMFromPrefix("eni-removed", "00:11:22:33:44:r2", &networkv1beta1.Nic{
		ID:       "eni-removed",
		IPv4CIDR: "10.0.1.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.1.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}, false)

	delegate := &LocalDelegate{
		client:   fakeClient,
		nodeName: "test-node-remove",
		eniIPAMs: map[ENIID]*ENILocalIPAM{
			"eni-remaining": ipamRemaining,
			"eni-removed":   ipamRemoved,
		},
		enableIPv4: true,
	}

	delegate.syncNodeCR(context.Background())

	delegate.lock.RLock()
	defer delegate.lock.RUnlock()
	assert.Len(t, delegate.eniIPAMs, 1)
	assert.Contains(t, delegate.eniIPAMs, ENIID("eni-remaining"))
	assert.NotContains(t, delegate.eniIPAMs, ENIID("eni-removed"))
}

// TestLocalDelegate_syncNodeCR_RetainENIWithAllocations verifies that an ENI disappearing
// from the Node CR is NOT removed if it still has allocations (graceful drain).
func TestLocalDelegate_syncNodeCR_RetainENIWithAllocations(t *testing.T) {
	nodeCR := &networkv1beta1.Node{
		Status: networkv1beta1.NodeStatus{
			NetworkInterfaces: map[string]*networkv1beta1.Nic{},
		},
	}
	nodeCR.Name = "test-node-retain"

	scheme := runtime.NewScheme()
	require.NoError(t, networkv1beta1.AddToScheme(scheme))
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nodeCR).Build()

	ipamWithAllocs := NewENILocalIPAMFromPrefix("eni-with-allocs", "00:11:22:33:44:a1", &networkv1beta1.Nic{
		ID:       "eni-with-allocs",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}, false)
	_, err := ipamWithAllocs.AllocateIPv4("pod-1")
	require.NoError(t, err)

	delegate := &LocalDelegate{
		client:     fakeClient,
		nodeName:   "test-node-retain",
		eniIPAMs:   map[ENIID]*ENILocalIPAM{"eni-with-allocs": ipamWithAllocs},
		enableIPv4: true,
	}

	delegate.syncNodeCR(context.Background())

	delegate.lock.RLock()
	defer delegate.lock.RUnlock()
	assert.Len(t, delegate.eniIPAMs, 1, "ENI with allocations should be retained during graceful drain")
}

// TestLocalDelegate_Release_WrongResourceType verifies Release returns false for wrong types.
func TestLocalDelegate_Release_WrongResourceType(t *testing.T) {
	delegate := &LocalDelegate{
		eniIPAMs: map[ENIID]*ENILocalIPAM{"eni-1": {}},
		notifier: NewNotifier(),
	}

	ok, err := delegate.Release(context.Background(), &daemon.CNI{PodID: "pod-1"}, &mockNetworkResource{resourceType: ResourceTypeVeth})
	assert.False(t, ok)
	assert.NoError(t, err)
}

// TestLocalDelegate_Release_ENINotFound verifies Release returns false when ENI ID doesn't match.
func TestLocalDelegate_Release_ENINotFound(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:       "eni-release-nf",
		IPv4CIDR: "10.0.0.0/24",
		IPv4Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "10.0.0.0/28", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-release-nf", "aa:bb:cc:dd:ee:ff", eni, false)

	delegate := &LocalDelegate{
		eniIPAMs:   map[ENIID]*ENILocalIPAM{"eni-release-nf": ipam},
		notifier:   NewNotifier(),
		enableIPv4: true,
	}

	resource := &LocalIPResource{
		ENI: daemon.ENI{ID: "eni-different"},
	}
	ok, err := delegate.Release(context.Background(), &daemon.CNI{PodID: "pod-1"}, resource)
	assert.False(t, ok)
	assert.NoError(t, err)
}

// TestLocalDelegate_Allocate_IPv6Only verifies IPv6-only allocation.
func TestLocalDelegate_Allocate_IPv6Only(t *testing.T) {
	eni := &networkv1beta1.Nic{
		ID:         "eni-v6only-alloc",
		MacAddress: "00:11:22:33:44:v6",
		VSwitchID:  "vsw-v6",
		IPv6CIDR:   "fd00::/112",
		IPv6Prefix: []networkv1beta1.IPPrefix{
			{Prefix: "fd00::/120", Status: networkv1beta1.IPPrefixStatusValid},
		},
	}
	ipam := NewENILocalIPAMFromPrefix("eni-v6only-alloc", "00:11:22:33:44:v6", eni, false)

	delegate := &LocalDelegate{
		eniIPAMs:   map[ENIID]*ENILocalIPAM{"eni-v6only-alloc": ipam},
		notifier:   NewNotifier(),
		enableIPv4: false,
		enableIPv6: true,
	}

	ctx := context.Background()
	cni := &daemon.CNI{PodID: "default/v6-pod"}
	request := NewLocalIPRequest()

	respCh, _ := delegate.Allocate(ctx, cni, request)
	resp := <-respCh
	require.NotNil(t, resp)
	require.Nil(t, resp.Err)

	resource, ok := resp.NetworkConfigs[0].(*LocalIPResource)
	require.True(t, ok)
	assert.False(t, resource.IP.IPv4.IsValid())
	assert.True(t, resource.IP.IPv6.IsValid())
}

// localDelegateMockResourceRequest is a mock implementation of ResourceRequest for testing
type localDelegateMockResourceRequest struct {
	resourceType ResourceType
}

func (m *localDelegateMockResourceRequest) ResourceType() ResourceType {
	return m.resourceType
}
