package node

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

// TestAssignEniPrefixWithOptions tests the prefix allocation logic for new ENI creation.
// Machine type: 3 ENIs, 15 slots per ENI
// IPv4: 1 slot is always used by primary IP, so 14 slots available for prefixes
// IPv6 dual-stack: each new ENI with IPv4 prefixes automatically gets 1 IPv6 prefix
// IPv6-only: uses IPv6PrefixCount (valid values: 0 or 1)
func TestAssignEniPrefixWithOptions(t *testing.T) {
	tests := []struct {
		name               string
		ipv4PrefixCount    int
		ipv6PrefixCount    int
		enableIPv4         bool
		enableIPv6         bool
		existingENIs       []*networkv1beta1.Nic
		expectedNewENIs    int   // Number of new ENIs that should have prefixes assigned
		expectedV4Prefixes []int // Expected IPv4 prefix count per new ENI
		expectedV6Prefixes []int // Expected IPv6 prefix count per new ENI
		description        string
	}{
		{
			name:               "0 prefix - no ENI should be created",
			ipv4PrefixCount:    0,
			enableIPv4:         true,
			enableIPv6:         false,
			existingENIs:       nil,
			expectedNewENIs:    0,
			expectedV4Prefixes: nil,
			expectedV6Prefixes: nil,
			description:        "When IPPrefixCount=0, no prefix should be assigned to any new ENI",
		},
		{
			name:               "1 prefix - 1 ENI (primary IP + 1 prefix)",
			ipv4PrefixCount:    1,
			enableIPv4:         true,
			enableIPv6:         false,
			existingENIs:       nil,
			expectedNewENIs:    1,
			expectedV4Prefixes: []int{1},
			expectedV6Prefixes: []int{0},
			description:        "When IPPrefixCount=1, create 1 ENI with 1 prefix",
		},
		{
			name:               "14 prefixes - 2 ENIs (10 + 4 due to API limit)",
			ipv4PrefixCount:    14,
			enableIPv4:         true,
			enableIPv6:         false,
			existingENIs:       nil,
			expectedNewENIs:    2,
			expectedV4Prefixes: []int{10, 4}, // API limit is 10 per CreateNetworkInterface
			expectedV6Prefixes: []int{0, 0},
			description:        "When IPPrefixCount=14, create 2 ENIs due to API limit (10+4)",
		},
		{
			name:               "15 prefixes - 2 ENIs (10 + 5 due to API limit)",
			ipv4PrefixCount:    15,
			enableIPv4:         true,
			enableIPv6:         false,
			existingENIs:       nil,
			expectedNewENIs:    2,
			expectedV4Prefixes: []int{10, 5}, // API limit is 10 per CreateNetworkInterface
			expectedV6Prefixes: []int{0, 0},
			description:        "When IPPrefixCount=15, need 2 ENIs: first with 10, second with 5",
		},
		{
			name:               "28 prefixes - 3 ENIs (10 + 10 + 8 due to API limit)",
			ipv4PrefixCount:    28,
			enableIPv4:         true,
			enableIPv6:         false,
			existingENIs:       nil,
			expectedNewENIs:    3,
			expectedV4Prefixes: []int{10, 10, 8}, // API limit is 10 per call
			expectedV6Prefixes: []int{0, 0, 0},
			description:        "When IPPrefixCount=28, need 3 ENIs due to API limit (10+10+8)",
		},
		{
			name:               "dual stack 1 prefix - auto 1 IPv6 prefix per ENI",
			ipv4PrefixCount:    1,
			enableIPv4:         true,
			enableIPv6:         true,
			existingENIs:       nil,
			expectedNewENIs:    1,
			expectedV4Prefixes: []int{1},
			expectedV6Prefixes: []int{1},
			description:        "Dual stack: each new ENI with IPv4 prefixes automatically gets 1 IPv6 prefix",
		},
		{
			name:               "dual stack 14 prefixes - 2 ENIs each with auto 1 IPv6 prefix",
			ipv4PrefixCount:    14,
			enableIPv4:         true,
			enableIPv6:         true,
			existingENIs:       nil,
			expectedNewENIs:    2,
			expectedV4Prefixes: []int{10, 4}, // API limit is 10 per CreateNetworkInterface
			expectedV6Prefixes: []int{1, 1},  // Dual-stack auto: 1 IPv6 prefix per ENI
			description:        "Dual stack: 2 ENIs, each with auto 1 IPv6 prefix",
		},
		{
			name:               "dual stack 15 prefixes - auto 1 IPv6 prefix per ENI",
			ipv4PrefixCount:    15,
			enableIPv4:         true,
			enableIPv6:         true,
			existingENIs:       nil,
			expectedNewENIs:    2,
			expectedV4Prefixes: []int{10, 5},
			expectedV6Prefixes: []int{1, 1}, // Dual-stack auto: 1 IPv6 prefix per ENI
			description:        "Dual stack: ipv6_prefix_count not needed, each ENI auto gets 1 IPv6 prefix",
		},
		{
			name:               "ipv6 only with ipv6 prefix count 1",
			ipv6PrefixCount:    1,
			enableIPv4:         false,
			enableIPv6:         true,
			existingENIs:       nil,
			expectedNewENIs:    1,
			expectedV4Prefixes: []int{0},
			expectedV6Prefixes: []int{1},
			description:        "IPv6-only: allocate 1 IPv6 prefix per IPv6PrefixCount",
		},
		{
			name:               "ipv6 only with ipv6 prefix count 0 - no ENI needed",
			ipv6PrefixCount:    0,
			enableIPv4:         false,
			enableIPv6:         true,
			existingENIs:       nil,
			expectedNewENIs:    0,
			expectedV4Prefixes: nil,
			expectedV6Prefixes: nil,
			description:        "IPv6-only with count=0: no prefix allocation",
		},
		{
			name:            "existing ENI with prefixes - allocate demand considering existing capacity",
			ipv4PrefixCount: 15,
			enableIPv4:      true,
			enableIPv6:      false,
			existingENIs: []*networkv1beta1.Nic{
				NewENIFactory().
					WithBaseID("existing-eni").
					WithIPv4Count(1).
					WithIPv4PrefixCount(10).
					WithStatus(aliyunClient.ENIStatusInUse).
					Build()[0],
			},
			expectedNewENIs: 1,
			// 15 total needed - 10 existing = 5 remaining
			// Existing ENI capacity: 15 - 1(IP) - 10(prefixes) = 4 free slots
			// New ENI needs: 5 - 4 = 1 prefix (existing ENI will get 4 via syncPrefixAllocation)
			expectedV4Prefixes: []int{1},
			expectedV6Prefixes: []int{0},
			description:        "With 10 existing prefixes and 4 free slots, new ENI only needs 1 prefix",
		},
		{
			name:            "existing ENI has enough prefixes - no new allocation",
			ipv4PrefixCount: 5,
			enableIPv4:      true,
			enableIPv6:      false,
			existingENIs: []*networkv1beta1.Nic{
				NewENIFactory().
					WithBaseID("existing-eni").
					WithIPv4Count(1).
					WithIPv4PrefixCount(5).
					WithStatus(aliyunClient.ENIStatusInUse).
					Build()[0],
			},
			expectedNewENIs:    0,
			expectedV4Prefixes: nil,
			expectedV6Prefixes: nil,
			description:        "Existing ENI already has required prefixes, no new allocation needed",
		},
		{
			name:            "attaching ENI with prefixes - count pending prefixes",
			ipv4PrefixCount: 15,
			enableIPv4:      true,
			enableIPv6:      false,
			existingENIs: []*networkv1beta1.Nic{
				NewENIFactory().
					WithBaseID("attaching-eni").
					WithIPv4Count(1).
					WithIPv4PrefixCount(10).
					WithStatus(aliyunClient.ENIStatusAttaching).
					Build()[0],
			},
			expectedNewENIs:    1,
			expectedV4Prefixes: []int{5}, // 15 - 10 pending = 5 more needed
			expectedV6Prefixes: []int{0},
			description:        "Attaching ENI's prefixes should be counted as pending",
		},
		// Test case: existing ENI without prefixes but with capacity
		// This tests the bug fix where we should NOT create a new ENI if existing ENIs
		// have enough capacity to add prefixes. syncPrefixAllocation will add prefixes
		// to the existing ENI instead.
		{
			name:            "existing ENI without prefixes but with capacity - no new ENI needed",
			ipv4PrefixCount: 5,
			enableIPv4:      true,
			enableIPv6:      false,
			existingENIs: []*networkv1beta1.Nic{
				NewENIFactory().
					WithBaseID("existing-eni-no-prefix").
					WithIPv4Count(1). // Only primary IP, no prefixes
					WithIPv4PrefixCount(0).
					WithStatus(aliyunClient.ENIStatusInUse).
					Build()[0],
			},
			expectedNewENIs:    0,   // Should NOT create new ENI
			expectedV4Prefixes: nil, // No prefixes assigned to new ENIs
			expectedV6Prefixes: nil,
			description:        "Existing ENI without prefixes but with capacity (14 slots) should be used by syncPrefixAllocation, not create new ENI",
		},
		{
			name:            "existing ENI with some IPs and no capacity - must create new ENI",
			ipv4PrefixCount: 5,
			enableIPv4:      true,
			enableIPv6:      false,
			existingENIs: []*networkv1beta1.Nic{
				NewENIFactory().
					WithBaseID("full-eni").
					WithIPv4Count(15). // Full capacity (15 IPs), no room for prefixes
					WithIPv4PrefixCount(0).
					WithStatus(aliyunClient.ENIStatusInUse).
					Build()[0],
			},
			expectedNewENIs:    1,
			expectedV4Prefixes: []int{5}, // Existing ENI is full, need new ENI
			expectedV6Prefixes: []int{0},
			description:        "Existing ENI is full (no capacity), must create new ENI with prefixes",
		},
		{
			name:            "existing ENI with partial capacity - partial new ENI needed",
			ipv4PrefixCount: 10,
			enableIPv4:      true,
			enableIPv6:      false,
			existingENIs: []*networkv1beta1.Nic{
				NewENIFactory().
					WithBaseID("partial-eni").
					WithIPv4Count(12). // 12 IPs used, 3 slots free for prefixes
					WithIPv4PrefixCount(0).
					WithStatus(aliyunClient.ENIStatusInUse).
					Build()[0],
			},
			expectedNewENIs:    1,
			expectedV4Prefixes: []int{7}, // 10 needed - 3 existing capacity = 7 from new ENI
			expectedV6Prefixes: []int{0},
			description:        "Existing ENI has 3 free slots, new ENI provides remaining 7 prefixes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build node with 3 ENIs capacity, 15 slots per ENI
			nodeFactory := NewNodeFactory("test-node").
				WithNodeCap(3, 15, 15).
				WithIPv4(tt.enableIPv4).
				WithIPv6(tt.enableIPv6).
				WithDefaultSecondaryFlavor(3)

			// Set prefix counts based on stack mode
			if tt.ipv4PrefixCount > 0 {
				nodeFactory.WithIPPrefixCount(tt.ipv4PrefixCount)
			}
			if tt.ipv6PrefixCount > 0 {
				nodeFactory.WithIPv6PrefixCount(tt.ipv6PrefixCount)
			}
			// For IPv6-only with explicit prefix mode, ensure EnableIPPrefix is set
			if tt.enableIPv6 && !tt.enableIPv4 && tt.ipv6PrefixCount > 0 {
				nodeFactory.enableIPPrefix = true
			}

			// Add existing ENIs
			for _, eni := range tt.existingENIs {
				nodeFactory.WithExistingENI(eni)
			}

			node := nodeFactory.Build()

			// Get ENI options
			options := getEniOptions(node)

			// Call the function under test
			ctx := context.Background()
			assignEniPrefixWithOptions(ctx, node, options, nil)

			// Count new ENIs with prefix assignments
			newENIsWithPrefixes := 0
			var actualV4Prefixes []int
			var actualV6Prefixes []int

			for _, opt := range options {
				if opt.eniRef != nil {
					continue // Skip existing ENIs
				}
				if opt.addIPv4PrefixN > 0 || opt.addIPv6PrefixN > 0 {
					newENIsWithPrefixes++
					actualV4Prefixes = append(actualV4Prefixes, opt.addIPv4PrefixN)
					actualV6Prefixes = append(actualV6Prefixes, opt.addIPv6PrefixN)
				}
			}

			// Assertions
			assert.Equal(t, tt.expectedNewENIs, newENIsWithPrefixes,
				"unexpected number of new ENIs with prefixes: %s", tt.description)

			if tt.expectedV4Prefixes != nil {
				assert.Equal(t, tt.expectedV4Prefixes, actualV4Prefixes,
					"unexpected IPv4 prefix distribution: %s", tt.description)
			}
			if tt.expectedV6Prefixes != nil {
				assert.Equal(t, tt.expectedV6Prefixes, actualV6Prefixes,
					"unexpected IPv6 prefix distribution: %s", tt.description)
			}
		})
	}
}

// TestSyncPrefixAllocation tests the prefix sync logic for existing InUse ENIs.
func TestSyncPrefixAllocation(t *testing.T) {
	tests := []struct {
		name                  string
		ipv4PrefixCount       int
		enableIPv4            bool
		enableIPv6            bool
		existingENIs          []*networkv1beta1.Nic
		expectedAssignCalls   int // Number of AssignIPv4Prefix calls expected
		expectedV6AssignCalls int // Number of AssignIPv6Prefix calls expected
		description           string
	}{
		{
			name:                  "no existing ENIs - nothing to sync",
			ipv4PrefixCount:       5,
			enableIPv4:            true,
			enableIPv6:            false,
			existingENIs:          nil,
			expectedAssignCalls:   0, // No InUse ENIs to sync
			expectedV6AssignCalls: 0,
			description:           "No InUse ENIs means no prefix sync, demand handled by assignEniPrefixWithOptions",
		},
		{
			name:            "ENI already has enough prefixes",
			ipv4PrefixCount: 5,
			enableIPv4:      true,
			enableIPv6:      false,
			existingENIs: []*networkv1beta1.Nic{
				NewENIFactory().
					WithBaseID("eni").
					WithIPv4Count(1).
					WithIPv4PrefixCount(5).
					WithStatus(aliyunClient.ENIStatusInUse).
					Build()[0],
			},
			expectedAssignCalls:   0,
			expectedV6AssignCalls: 0,
			description:           "ENI with 5 prefixes needs no more when target is 5",
		},
		{
			name:            "ENI needs more prefixes - partial allocation",
			ipv4PrefixCount: 5,
			enableIPv4:      true,
			enableIPv6:      false,
			existingENIs: []*networkv1beta1.Nic{
				NewENIFactory().
					WithBaseID("eni").
					WithIPv4Count(1).
					WithIPv4PrefixCount(2).
					WithStatus(aliyunClient.ENIStatusInUse).
					Build()[0],
			},
			expectedAssignCalls:   1, // Need to add 3 more prefixes
			expectedV6AssignCalls: 0,
			description:           "ENI with 2 prefixes needs 3 more to reach target of 5",
		},
		{
			name:            "skip Attaching ENI",
			ipv4PrefixCount: 5,
			enableIPv4:      true,
			enableIPv6:      false,
			existingENIs: []*networkv1beta1.Nic{
				NewENIFactory().
					WithBaseID("eni").
					WithIPv4Count(1).
					WithIPv4PrefixCount(0).
					WithStatus(aliyunClient.ENIStatusAttaching).
					Build()[0],
			},
			expectedAssignCalls:   0, // Attaching ENIs are skipped
			expectedV6AssignCalls: 0,
			description:           "Attaching ENIs should be skipped by syncPrefixAllocation",
		},
		{
			name:            "dual stack - sync IPv4 prefixes, auto-ensure 1 IPv6 prefix",
			ipv4PrefixCount: 5,
			enableIPv4:      true,
			enableIPv6:      true,
			existingENIs: []*networkv1beta1.Nic{
				NewENIFactory().
					WithBaseID("eni").
					WithIPv4Count(1).
					WithIPv6Count(1).
					WithIPv4PrefixCount(2).
					WithIPv6PrefixCount(0). // no IPv6 prefix yet
					WithStatus(aliyunClient.ENIStatusInUse).
					Build()[0],
			},
			expectedAssignCalls:   1, // Need 3 more IPv4 prefixes
			expectedV6AssignCalls: 0, // ENI has 0 IPv4 prefixes in Valid status initially, but after IPv4 sync it will have them; however the mock test only checks initial state
			description:           "Dual stack: sync IPv4 prefixes; IPv6 auto-assign happens in syncPrefixAllocation for ENIs with IPv4 prefixes",
		},
		{
			name:            "dual stack - ENI already has IPv6 prefix, no additional v6 assign",
			ipv4PrefixCount: 5,
			enableIPv4:      true,
			enableIPv6:      true,
			existingENIs: []*networkv1beta1.Nic{
				NewENIFactory().
					WithBaseID("eni").
					WithIPv4Count(1).
					WithIPv6Count(1).
					WithIPv4PrefixCount(2).
					WithIPv6PrefixCount(1). // already has 1 IPv6 prefix
					WithStatus(aliyunClient.ENIStatusInUse).
					Build()[0],
			},
			expectedAssignCalls:   1, // Need 3 more IPv4 prefixes
			expectedV6AssignCalls: 0, // Already has 1 IPv6 prefix, skip
			description:           "Dual stack: ENI already has 1 IPv6 prefix, no additional assignment needed",
		},
		{
			name:            "multiple ENIs - distribute prefixes across ENIs",
			ipv4PrefixCount: 10,
			enableIPv4:      true,
			enableIPv6:      false,
			existingENIs: []*networkv1beta1.Nic{
				NewENIFactory().
					WithBaseID("eni-1").
					WithIPv4Count(1).
					WithIPv4PrefixCount(5).
					WithStatus(aliyunClient.ENIStatusInUse).
					Build()[0],
				NewENIFactory().
					WithBaseID("eni-2").
					WithIPv4Count(1).
					WithIPv4PrefixCount(3).
					WithStatus(aliyunClient.ENIStatusInUse).
					Build()[0],
			},
			expectedAssignCalls:   1, // Only need 2 more total (10 - 5 - 3 = 2)
			expectedV6AssignCalls: 0,
			description:           "With 8 existing prefixes, only 2 more needed to reach 10",
		},
		{
			name:            "demand already satisfied - no allocation needed",
			ipv4PrefixCount: 5,
			enableIPv4:      true,
			enableIPv6:      false,
			existingENIs: []*networkv1beta1.Nic{
				NewENIFactory().
					WithBaseID("eni-1").
					WithIPv4Count(1).
					WithIPv4PrefixCount(3).
					WithStatus(aliyunClient.ENIStatusInUse).
					Build()[0],
				NewENIFactory().
					WithBaseID("eni-2").
					WithIPv4Count(1).
					WithIPv4PrefixCount(2).
					WithStatus(aliyunClient.ENIStatusInUse).
					Build()[0],
			},
			expectedAssignCalls:   0, // 3 + 2 = 5, already satisfied
			expectedV6AssignCalls: 0,
			description:           "Total 5 prefixes across 2 ENIs, target satisfied",
		},
		{
			name:            "attaching ENI in-flight prefixes reduce demand on InUse ENI",
			ipv4PrefixCount: 10,
			enableIPv4:      true,
			enableIPv6:      false,
			existingENIs: []*networkv1beta1.Nic{
				// InUse ENI with 3 prefixes
				NewENIFactory().
					WithBaseID("eni-inuse").
					WithIPv4Count(1).
					WithIPv4PrefixCount(3).
					WithStatus(aliyunClient.ENIStatusInUse).
					Build()[0],
				// Attaching (in-flight) ENI already carrying 7 prefixes
				NewENIFactory().
					WithBaseID("eni-attaching").
					WithIPv4Count(1).
					WithIPv4PrefixCount(7).
					WithStatus(aliyunClient.ENIStatusAttaching).
					Build()[0],
			},
			// total = 3 (InUse) + 7 (Attaching) = 10, demand satisfied → no assign call
			expectedAssignCalls:   0,
			expectedV6AssignCalls: 0,
			description:           "Attaching ENI's in-flight prefixes must be counted; demand is already satisfied",
		},
		{
			name:            "attaching ENI partially satisfies demand - only remaining needed on InUse ENI",
			ipv4PrefixCount: 10,
			enableIPv4:      true,
			enableIPv6:      false,
			existingENIs: []*networkv1beta1.Nic{
				// InUse ENI with 3 prefixes
				NewENIFactory().
					WithBaseID("eni-inuse").
					WithIPv4Count(1).
					WithIPv4PrefixCount(3).
					WithStatus(aliyunClient.ENIStatusInUse).
					Build()[0],
				// Attaching (in-flight) ENI carrying 4 prefixes
				NewENIFactory().
					WithBaseID("eni-attaching").
					WithIPv4Count(1).
					WithIPv4PrefixCount(4).
					WithStatus(aliyunClient.ENIStatusAttaching).
					Build()[0],
			},
			// total = 3 (InUse) + 4 (Attaching) = 7, remaining = 10 - 7 = 3 → 1 assign call on InUse ENI
			expectedAssignCalls:   1,
			expectedV6AssignCalls: 0,
			description:           "Attaching ENI partially satisfies demand; only 3 more needed on InUse ENI",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build node with 3 ENIs capacity, 15 slots per ENI
			nodeFactory := NewNodeFactory("test-node").
				WithNodeCap(3, 15, 15).
				WithIPv4(tt.enableIPv4).
				WithIPv6(tt.enableIPv6).
				WithIPPrefixCount(tt.ipv4PrefixCount).
				WithDefaultSecondaryFlavor(3)

			// Add existing ENIs
			for _, eni := range tt.existingENIs {
				nodeFactory.WithExistingENI(eni)
			}

			node := nodeFactory.Build()

			isDualStack := tt.enableIPv4 && tt.enableIPv6

			// Mirror the fixed syncPrefixAllocation logic:
			// Count prefixes on both InUse and Attaching (in-flight) ENIs to avoid over-allocation.
			totalV4Prefixes := 0
			inUseENICount := 0
			inUseENIsWithoutV6Prefix := 0
			inUseENIsWithV4Prefix := 0
			for _, eni := range node.Status.NetworkInterfaces {
				switch eni.Status {
				case aliyunClient.ENIStatusInUse:
					inUseENICount++
					v4p := countOccupiedPrefixes(eni.IPv4Prefix)
					v6p := countOccupiedPrefixes(eni.IPv6Prefix)
					totalV4Prefixes += v4p
					if v6p == 0 {
						inUseENIsWithoutV6Prefix++
					}
					if v4p > 0 {
						inUseENIsWithV4Prefix++
					}
				case aliyunClient.ENIStatusAttaching:
					totalV4Prefixes += countOccupiedPrefixes(eni.IPv4Prefix)
				}
			}

			remainingV4Demand := max(0, tt.ipv4PrefixCount-totalV4Prefixes)

			// Verify IPv4 expectations
			if tt.enableIPv4 {
				if tt.expectedAssignCalls > 0 {
					assert.Greater(t, remainingV4Demand, 0,
						"expected IPv4 assign calls but no demand: %s", tt.description)
					assert.Greater(t, inUseENICount, 0,
						"expected IPv4 assign calls but no InUse ENIs: %s", tt.description)
				} else {
					if inUseENICount > 0 {
						assert.Equal(t, 0, remainingV4Demand,
							"expected no IPv4 assign calls but has demand with InUse ENIs: %s", tt.description)
					}
				}
			}

			// Verify IPv6 expectations
			if tt.enableIPv6 {
				if isDualStack {
					// Dual-stack: IPv6 assign calls expected only for InUse ENIs that have
					// IPv4 prefixes but are missing an IPv6 prefix.
					if tt.expectedV6AssignCalls > 0 {
						assert.Greater(t, inUseENIsWithoutV6Prefix, 0,
							"expected IPv6 assign calls but all InUse ENIs already have IPv6 prefix: %s", tt.description)
						assert.Greater(t, inUseENIsWithV4Prefix, 0,
							"expected IPv6 assign calls but no InUse ENIs have IPv4 prefix: %s", tt.description)
					}
				}
			}
		})
	}
}

// TestPrefixCapacityCalculation verifies the capacity calculation for prefix allocation.
// Key insight: IPv4 always reserves 1 slot for primary IP, so max prefixes = IPv4PerAdapter - 1
func TestPrefixCapacityCalculation(t *testing.T) {
	tests := []struct {
		name              string
		ipv4PerAdapter    int
		expectedPrefixCap int
		description       string
	}{
		{
			name:              "standard capacity 15 slots",
			ipv4PerAdapter:    15,
			expectedPrefixCap: 14,
			description:       "15 slots - 1 primary IP = 14 prefixes max",
		},
		{
			name:              "standard capacity 10 slots",
			ipv4PerAdapter:    10,
			expectedPrefixCap: 9,
			description:       "10 slots - 1 primary IP = 9 prefixes max",
		},
		{
			name:              "minimum capacity 2 slots",
			ipv4PerAdapter:    2,
			expectedPrefixCap: 1,
			description:       "2 slots - 1 primary IP = 1 prefix max",
		},
		{
			name:              "edge case 1 slot - should still allow 1 prefix",
			ipv4PerAdapter:    1,
			expectedPrefixCap: 1, // Safety: min 1 prefix even with only 1 slot
			description:       "Edge case: ensure at least 1 prefix even with minimal capacity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build node with specified capacity
			node := NewNodeFactory("test-node").
				WithNodeCap(3, tt.ipv4PerAdapter, tt.ipv4PerAdapter).
				WithIPv4(true).
				WithIPv6(false).
				WithIPPrefixCount(100). // Request more than capacity to test limit
				WithDefaultSecondaryFlavor(3).
				Build()

			// Get options and assign prefixes
			options := getEniOptions(node)
			ctx := context.Background()
			assignEniPrefixWithOptions(ctx, node, options, nil)

			// Verify the first new ENI gets at most expectedPrefixCap prefixes
			for _, opt := range options {
				if opt.eniRef == nil && opt.addIPv4PrefixN > 0 {
					assert.LessOrEqual(t, opt.addIPv4PrefixN, tt.expectedPrefixCap,
						"prefix count exceeds capacity: %s", tt.description)
					break
				}
			}
		})
	}
}

// TestSyncPrefixAllocation_SlotWithPrimaryIP verifies that the primary IP occupies 1 slot,
// so an ENI with capacity=15 and 1 existing prefix can accept 13 more prefixes (15 - 1 primary - 1 existing).
func TestSyncPrefixAllocation_SlotWithPrimaryIP(t *testing.T) {
	// ENI has: 1 primary IP (slot 0) + 1 existing prefix (slot 1) => 13 free slots
	eni := NewENIFactory().
		WithBaseID("eni").
		WithIPv4Count(1).       // 1 primary IP
		WithIPv4PrefixCount(1). // 1 existing prefix
		WithStatus(aliyunClient.ENIStatusInUse).
		Build()[0]

	node := NewNodeFactory("test-node").
		WithNodeCap(3, 15, 15).
		WithIPv4(true).
		WithIPv6(false).
		WithIPPrefixCount(14). // want 14 total, already have 1 => need 13 more
		WithDefaultSecondaryFlavor(3).
		WithExistingENI(eni).
		Build()

	// Verify slot calculation: usedSlots = len(IPv4) + len(IPv4Prefix) = 1 + 1 = 2
	// freeSlots = 15 - 2 = 13
	// toAdd = min(13, 13) = 13
	usedSlots := len(eni.IPv4) + len(eni.IPv4Prefix)
	freeSlots := node.Spec.NodeCap.IPv4PerAdapter - usedSlots
	assert.Equal(t, 2, usedSlots, "primary IP + 1 prefix = 2 used slots")
	assert.Equal(t, 13, freeSlots, "15 - 2 = 13 free slots")

	// Remaining demand = 14 - 1 = 13
	totalV4 := countOccupiedPrefixes(eni.IPv4Prefix)
	remaining := 14 - totalV4
	assert.Equal(t, 13, remaining, "need 13 more prefixes")
	assert.LessOrEqual(t, remaining, freeSlots, "remaining demand fits in free slots")
}

// TestSyncPrefixAllocation_PrefixAndIPCoexist verifies that an ENI with both secondary IPs
// and prefixes is NOT skipped — ECS allows mixing secondary IPs and prefixes on the same ENI.
// The only constraint is that the combined slot count must not exceed IPv4PerAdapter.
func TestSyncPrefixAllocation_PrefixAndIPCoexist(t *testing.T) {
	// ENI capacity = 15; has 1 primary + 1 secondary IP + 1 prefix => 3 used slots, 12 free
	eni := NewENIFactory().
		WithBaseID("eni").
		WithIPv4Count(2).       // 1 primary + 1 secondary
		WithIPv4PrefixCount(1). // 1 existing prefix
		WithStatus(aliyunClient.ENIStatusInUse).
		Build()[0]

	node := NewNodeFactory("test-node").
		WithNodeCap(3, 15, 15).
		WithIPv4(true).
		WithIPv6(false).
		WithIPPrefixCount(10). // want 10 total, have 1 => need 9 more
		WithDefaultSecondaryFlavor(3).
		WithExistingENI(eni).
		Build()

	// Slot accounting: usedSlots = len(IPv4) + len(IPv4Prefix) = 2 + 1 = 3
	// freeSlots = 15 - 3 = 12; remaining demand = 10 - 1 = 9
	// => ENI should NOT be skipped; it can accept 9 more prefixes
	usedSlots := len(eni.IPv4) + len(eni.IPv4Prefix)
	freeSlots := node.Spec.NodeCap.IPv4PerAdapter - usedSlots
	assert.Equal(t, 3, usedSlots, "primary + 1 secondary + 1 prefix = 3 used slots")
	assert.Equal(t, 12, freeSlots, "15 - 3 = 12 free slots available for more prefixes")

	// Remaining demand = 10 - 1 existing = 9; fits within 12 free slots
	totalV4 := countOccupiedPrefixes(eni.IPv4Prefix)
	remaining := 10 - totalV4
	assert.Equal(t, 9, remaining, "need 9 more prefixes")
	assert.LessOrEqual(t, remaining, freeSlots,
		"mixed ENI (secondary IP + prefix) should still accept more prefixes up to slot limit")
}

// TestSyncPrefixAllocation_ENIFullyOccupied verifies that an ENI with all slots occupied
// (primary IP + secondary IPs filling all capacity) does not get prefix allocation.
func TestSyncPrefixAllocation_ENIFullyOccupied(t *testing.T) {
	// ENI capacity = 5; all 5 slots used by IPs (1 primary + 4 secondary)
	eni := NewENIFactory().
		WithBaseID("eni").
		WithIPv4Count(5). // 1 primary + 4 secondary, fills all 5 slots
		WithStatus(aliyunClient.ENIStatusInUse).
		Build()[0]

	node := NewNodeFactory("test-node").
		WithNodeCap(3, 5, 5).
		WithIPv4(true).
		WithIPv6(false).
		WithIPPrefixCount(3).
		WithDefaultSecondaryFlavor(3).
		WithExistingENI(eni).
		Build()

	// usedSlots = 5 (all IPs), freeSlots = 5 - 5 = 0
	usedSlots := len(eni.IPv4) + len(eni.IPv4Prefix)
	freeSlots := node.Spec.NodeCap.IPv4PerAdapter - usedSlots
	assert.Equal(t, 5, usedSlots, "all 5 slots used by IPs")
	assert.Equal(t, 0, freeSlots, "no free slots available")
}

// TestSyncPrefixAllocation_MultipleENIsDistribute verifies that prefix demand is distributed
// across multiple InUse ENIs when one ENI cannot satisfy the full demand.
func TestSyncPrefixAllocation_MultipleENIsDistribute(t *testing.T) {
	// ENI-1: 1 primary + 12 prefixes => 2 free slots
	eni1 := NewENIFactory().
		WithBaseID("eni-1").
		WithIPv4Count(1).
		WithIPv4PrefixCount(12).
		WithStatus(aliyunClient.ENIStatusInUse).
		Build()[0]

	// ENI-2: 1 primary + 0 prefixes => 14 free slots
	eni2 := NewENIFactory().
		WithBaseID("eni-2").
		WithIPv4Count(1).
		WithIPv4PrefixCount(0).
		WithStatus(aliyunClient.ENIStatusInUse).
		Build()[0]

	node := NewNodeFactory("test-node").
		WithNodeCap(3, 15, 15).
		WithIPv4(true).
		WithIPv6(false).
		WithIPPrefixCount(20). // want 20 total, have 12 => need 8 more
		WithDefaultSecondaryFlavor(3).
		WithExistingENI(eni1).
		WithExistingENI(eni2).
		Build()

	// ENI-1 free slots = 15 - 1 - 12 = 2
	freeSlots1 := node.Spec.NodeCap.IPv4PerAdapter - len(eni1.IPv4) - len(eni1.IPv4Prefix)
	// ENI-2 free slots = 15 - 1 - 0 = 14
	freeSlots2 := node.Spec.NodeCap.IPv4PerAdapter - len(eni2.IPv4) - len(eni2.IPv4Prefix)

	totalExisting := countOccupiedPrefixes(eni1.IPv4Prefix) + countOccupiedPrefixes(eni2.IPv4Prefix)
	remaining := 20 - totalExisting

	assert.Equal(t, 2, freeSlots1, "ENI-1 has 2 free slots")
	assert.Equal(t, 14, freeSlots2, "ENI-2 has 14 free slots")
	assert.Equal(t, 8, remaining, "need 8 more prefixes")
	// ENI-1 can provide 2, ENI-2 can provide the remaining 6
	assert.LessOrEqual(t, remaining, freeSlots1+freeSlots2, "combined capacity satisfies demand")
}

// TestAssignEniPrefixWithOptions_PrimaryIPReserved verifies that new ENI capacity
// correctly reserves 1 slot for the primary IP, so max prefixes = IPv4PerAdapter - 1.
func TestAssignEniPrefixWithOptions_PrimaryIPReserved(t *testing.T) {
	// IPv4PerAdapter = 5; primary IP takes 1 slot => max 4 prefixes per ENI
	node := NewNodeFactory("test-node").
		WithNodeCap(3, 5, 5).
		WithIPv4(true).
		WithIPv6(false).
		WithIPPrefixCount(100). // request more than capacity
		WithDefaultSecondaryFlavor(3).
		Build()

	options := getEniOptions(node)
	ctx := context.Background()
	assignEniPrefixWithOptions(ctx, node, options, nil)

	// Each new ENI should have at most IPv4PerAdapter - 1 = 4 prefixes
	maxPrefixCap := node.Spec.NodeCap.IPv4PerAdapter - 1
	for _, opt := range options {
		if opt.eniRef == nil && opt.addIPv4PrefixN > 0 {
			assert.LessOrEqual(t, opt.addIPv4PrefixN, maxPrefixCap,
				"new ENI prefix count must not exceed IPv4PerAdapter - 1 (primary IP reserved)")
		}
	}
}

// TestAssignEniPrefixWithOptions_ZeroCapacity verifies that when IPv4PerAdapter = 1
// (only room for primary IP), the function still safely handles the edge case.
func TestAssignEniPrefixWithOptions_ZeroCapacity(t *testing.T) {
	// IPv4PerAdapter = 1; primary IP takes the only slot => prefixCap = max(1, 1-1) = 1 (safety floor)
	node := NewNodeFactory("test-node").
		WithNodeCap(3, 1, 1).
		WithIPv4(true).
		WithIPv6(false).
		WithIPPrefixCount(5).
		WithDefaultSecondaryFlavor(3).
		Build()

	options := getEniOptions(node)
	ctx := context.Background()
	// Should not panic
	assert.NotPanics(t, func() {
		assignEniPrefixWithOptions(ctx, node, options, nil)
	})

	// Each new ENI should have at most 1 prefix (safety floor)
	for _, opt := range options {
		if opt.eniRef == nil {
			assert.LessOrEqual(t, opt.addIPv4PrefixN, 1,
				"with IPv4PerAdapter=1, at most 1 prefix per ENI (safety floor)")
		}
	}
}

// TestAssignEniPrefixWithOptions_DualStackParity verifies that in dual-stack mode,
// each new ENI with IPv4 prefixes automatically gets exactly 1 IPv6 prefix.
// The user does NOT need to configure ipv6_prefix_count in dual-stack mode.
func TestAssignEniPrefixWithOptions_DualStackParity(t *testing.T) {
	node := NewNodeFactory("test-node").
		WithNodeCap(3, 15, 15).
		WithIPv4(true).
		WithIPv6(true).
		WithIPPrefixCount(7). // Only IPv4 prefix count; IPv6 is auto
		WithDefaultSecondaryFlavor(3).
		Build()

	options := getEniOptions(node)
	ctx := context.Background()
	assignEniPrefixWithOptions(ctx, node, options, nil)

	// Every new ENI that has IPv4 prefixes must also have exactly 1 IPv6 prefix.
	for _, opt := range options {
		if opt.eniRef == nil {
			if opt.addIPv4PrefixN > 0 {
				assert.Equal(t, 1, opt.addIPv6PrefixN,
					"dual-stack: each new ENI with IPv4 prefixes must get exactly 1 IPv6 prefix")
			} else {
				assert.Equal(t, 0, opt.addIPv6PrefixN,
					"dual-stack: ENI without IPv4 prefixes should not get IPv6 prefix")
			}
		}
	}
}

// TestIsPrefixMode tests the prefix mode detection logic.
// Prefix mode is determined by the EnableIPPrefix field in the Node CR's ENISpec.
func TestIsPrefixMode(t *testing.T) {
	tests := []struct {
		name            string
		labels          map[string]string
		enableIPPrefix  bool
		ipv4PrefixCount int
		isLingJun       bool
		expectedResult  bool
	}{
		{
			name:            "prefix mode enabled - EnableIPPrefix true",
			labels:          map[string]string{},
			enableIPPrefix:  true,
			ipv4PrefixCount: 5,
			isLingJun:       false,
			expectedResult:  true,
		},
		{
			name:            "prefix mode disabled - EnableIPPrefix false",
			labels:          map[string]string{},
			enableIPPrefix:  false,
			ipv4PrefixCount: 5,
			isLingJun:       false,
			expectedResult:  false,
		},
		{
			name:            "prefix mode enabled - EnableIPPrefix true even with count 0",
			labels:          map[string]string{},
			enableIPPrefix:  true,
			ipv4PrefixCount: 0,
			isLingJun:       false,
			expectedResult:  true,
		},
		{
			name:            "prefix mode disabled - LingJun node (excluded even if EnableIPPrefix is true)",
			labels:          map[string]string{"alibabacloud.com/lingjun-worker": "true"},
			enableIPPrefix:  true,
			ipv4PrefixCount: 5,
			isLingJun:       true,
			expectedResult:  false,
		},
		{
			name:            "prefix mode disabled - nil ENISpec",
			labels:          map[string]string{},
			enableIPPrefix:  false,
			ipv4PrefixCount: 0,
			isLingJun:       false,
			expectedResult:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeFactory := NewNodeFactory("test-node").
				WithLabels(tt.labels)

			node := nodeFactory.Build()
			node.Spec.ENISpec.EnableIPPrefix = tt.enableIPPrefix
			node.Spec.ENISpec.IPv4PrefixCount = tt.ipv4PrefixCount

			// Test nil ENISpec case
			if tt.name == "prefix mode disabled - nil ENISpec" {
				node.Spec.ENISpec = nil
			}

			result := isPrefixMode(node)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

// newMetaCtx creates a context carrying a NodeStatus so that MetaCtx(ctx) is non-nil.
func newMetaCtx() context.Context {
	ns := &NodeStatus{
		NeedSyncOpenAPI: &atomic.Bool{},
		StatusChanged:   &atomic.Bool{},
	}
	return context.WithValue(context.Background(), ctxMetaKey{}, ns)
}

// TestSyncPrefixAllocation_Integration calls the real syncPrefixAllocation method
// via a ReconcileNode with a mocked OpenAPI to verify actual API calls.
func TestSyncPrefixAllocation_Integration(t *testing.T) {
	t.Run("IPv4 prefix demand triggers AssignPrivateIPAddressV2", func(t *testing.T) {
		mockHelper := NewMockAPIHelperWithT(t)
		openAPI, _, _ := mockHelper.GetMocks()

		eni := NewENIFactory().
			WithBaseID("eni").
			WithIPv4Count(1).
			WithIPv4PrefixCount(0).
			WithStatus(aliyunClient.ENIStatusInUse).
			Build()[0]

		node := NewNodeFactory("test-node").
			WithNodeCap(3, 15, 15).
			WithIPv4(true).
			WithIPv6(false).
			WithIPPrefixCount(3).
			WithDefaultSecondaryFlavor(3).
			WithExistingENI(eni).
			Build()

		openAPI.On("AssignPrivateIPAddressV2", mock.Anything, mock.Anything).
			Return([]aliyunClient.IPSet{
				{Prefix: "172.16.0.0/28"},
				{Prefix: "172.16.0.16/28"},
				{Prefix: "172.16.0.32/28"},
			}, nil).Once()

		reconciler := NewReconcilerBuilder().
			WithAliyun(openAPI).
			WithDefaults().
			Build()

		ctx := newMetaCtx()
		err := reconciler.syncPrefixAllocation(ctx, node)
		require.NoError(t, err)

		assert.Len(t, eni.IPv4Prefix, 3)
		assert.Equal(t, "172.16.0.0/28", eni.IPv4Prefix[0].Prefix)
		assert.Equal(t, networkv1beta1.IPPrefixStatusValid, eni.IPv4Prefix[0].Status)
		assert.True(t, MetaCtx(ctx).StatusChanged.Load())
	})

	t.Run("demand already met - no API call", func(t *testing.T) {
		mockHelper := NewMockAPIHelperWithT(t)
		openAPI, _, _ := mockHelper.GetMocks()

		eni := NewENIFactory().
			WithBaseID("eni").
			WithIPv4Count(1).
			WithIPv4PrefixCount(5).
			WithStatus(aliyunClient.ENIStatusInUse).
			Build()[0]

		node := NewNodeFactory("test-node").
			WithNodeCap(3, 15, 15).
			WithIPv4(true).
			WithIPv6(false).
			WithIPPrefixCount(5).
			WithDefaultSecondaryFlavor(3).
			WithExistingENI(eni).
			Build()

		reconciler := NewReconcilerBuilder().
			WithAliyun(openAPI).
			WithDefaults().
			Build()

		ctx := newMetaCtx()
		err := reconciler.syncPrefixAllocation(ctx, node)
		require.NoError(t, err)

		openAPI.AssertNotCalled(t, "AssignPrivateIPAddressV2", mock.Anything, mock.Anything)
	})

	t.Run("API error propagates", func(t *testing.T) {
		mockHelper := NewMockAPIHelperWithT(t)
		openAPI, _, _ := mockHelper.GetMocks()

		eni := NewENIFactory().
			WithBaseID("eni").
			WithIPv4Count(1).
			WithIPv4PrefixCount(0).
			WithStatus(aliyunClient.ENIStatusInUse).
			Build()[0]

		node := NewNodeFactory("test-node").
			WithNodeCap(3, 15, 15).
			WithIPv4(true).
			WithIPv6(false).
			WithIPPrefixCount(2).
			WithDefaultSecondaryFlavor(3).
			WithExistingENI(eni).
			Build()

		openAPI.On("AssignPrivateIPAddressV2", mock.Anything, mock.Anything).
			Return([]aliyunClient.IPSet(nil), errors.New("throttled")).Once()

		reconciler := NewReconcilerBuilder().
			WithAliyun(openAPI).
			WithDefaults().
			Build()

		ctx := newMetaCtx()
		err := reconciler.syncPrefixAllocation(ctx, node)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "throttled")
	})

	t.Run("dual-stack auto-assigns IPv6 prefix on ENI with IPv4 prefixes", func(t *testing.T) {
		mockHelper := NewMockAPIHelperWithT(t)
		openAPI, _, _ := mockHelper.GetMocks()

		eni := NewENIFactory().
			WithBaseID("eni").
			WithIPv4Count(1).
			WithIPv4PrefixCount(3).
			WithIPv6PrefixCount(0).
			WithStatus(aliyunClient.ENIStatusInUse).
			Build()[0]

		node := NewNodeFactory("test-node").
			WithNodeCap(3, 15, 15).
			WithIPv4(true).
			WithIPv6(true).
			WithIPPrefixCount(3).
			WithDefaultSecondaryFlavor(3).
			WithExistingENI(eni).
			Build()

		openAPI.On("AssignIpv6AddressesV2", mock.Anything, mock.Anything).
			Return([]aliyunClient.IPSet{
				{Prefix: "2408:4005::/80"},
			}, nil).Once()

		reconciler := NewReconcilerBuilder().
			WithAliyun(openAPI).
			WithDefaults().
			Build()

		ctx := newMetaCtx()
		err := reconciler.syncPrefixAllocation(ctx, node)
		require.NoError(t, err)

		assert.Len(t, eni.IPv6Prefix, 1)
		assert.Equal(t, "2408:4005::/80", eni.IPv6Prefix[0].Prefix)
	})

	t.Run("IPv6-only mode assigns IPv6 prefix based on count", func(t *testing.T) {
		mockHelper := NewMockAPIHelperWithT(t)
		openAPI, _, _ := mockHelper.GetMocks()

		eni := NewENIFactory().
			WithBaseID("eni").
			WithIPv4Count(0).
			WithIPv6Count(1).
			WithIPv6PrefixCount(0).
			WithStatus(aliyunClient.ENIStatusInUse).
			Build()[0]

		node := NewNodeFactory("test-node").
			WithNodeCap(3, 15, 15).
			WithIPv4(false).
			WithIPv6(true).
			WithIPv6PrefixCount(1).
			WithDefaultSecondaryFlavor(3).
			WithExistingENI(eni).
			Build()
		node.Spec.ENISpec.EnableIPPrefix = true

		openAPI.On("AssignIpv6AddressesV2", mock.Anything, mock.Anything).
			Return([]aliyunClient.IPSet{
				{Prefix: "fd00::/80"},
			}, nil).Once()

		reconciler := NewReconcilerBuilder().
			WithAliyun(openAPI).
			WithDefaults().
			Build()

		ctx := newMetaCtx()
		err := reconciler.syncPrefixAllocation(ctx, node)
		require.NoError(t, err)

		assert.Len(t, eni.IPv6Prefix, 1)
		assert.Equal(t, "fd00::/80", eni.IPv6Prefix[0].Prefix)
	})

	t.Run("skips Attaching ENIs but counts their prefixes", func(t *testing.T) {
		mockHelper := NewMockAPIHelperWithT(t)
		openAPI, _, _ := mockHelper.GetMocks()

		inUseENI := NewENIFactory().
			WithBaseID("eni-inuse").
			WithIPv4Count(1).
			WithIPv4PrefixCount(2).
			WithStatus(aliyunClient.ENIStatusInUse).
			Build()[0]

		attachingENI := NewENIFactory().
			WithBaseID("eni-attaching").
			WithIPv4Count(1).
			WithIPv4PrefixCount(3).
			WithStatus(aliyunClient.ENIStatusAttaching).
			Build()[0]

		node := NewNodeFactory("test-node").
			WithNodeCap(3, 15, 15).
			WithIPv4(true).
			WithIPv6(false).
			WithIPPrefixCount(5).
			WithDefaultSecondaryFlavor(3).
			WithExistingENI(inUseENI).
			WithExistingENI(attachingENI).
			Build()

		reconciler := NewReconcilerBuilder().
			WithAliyun(openAPI).
			WithDefaults().
			Build()

		ctx := newMetaCtx()
		err := reconciler.syncPrefixAllocation(ctx, node)
		require.NoError(t, err)
		// 2 (InUse) + 3 (Attaching) = 5 >= demand 5, so no API call
		openAPI.AssertNotCalled(t, "AssignPrivateIPAddressV2", mock.Anything, mock.Anything)
	})
}

// TestAssignIPv4Prefix_Direct exercises assignIPv4Prefix directly.
func TestAssignIPv4Prefix_Direct(t *testing.T) {
	t.Run("single batch", func(t *testing.T) {
		mockHelper := NewMockAPIHelperWithT(t)
		openAPI, _, _ := mockHelper.GetMocks()

		eni := &networkv1beta1.Nic{ID: "eni-test"}
		node := NewNodeFactory("n").Build()

		openAPI.On("AssignPrivateIPAddressV2", mock.Anything, mock.Anything).
			Return([]aliyunClient.IPSet{
				{Prefix: "10.0.0.0/28"},
				{Prefix: "10.0.0.16/28"},
			}, nil).Once()

		reconciler := NewReconcilerBuilder().
			WithAliyun(openAPI).
			WithDefaults().
			Build()

		ctx := newMetaCtx()
		err := reconciler.assignIPv4Prefix(ctx, node, eni, 2)
		require.NoError(t, err)

		assert.Len(t, eni.IPv4Prefix, 2)
		assert.Equal(t, networkv1beta1.IPPrefixStatusValid, eni.IPv4Prefix[0].Status)
		openAPI.AssertNumberOfCalls(t, "AssignPrivateIPAddressV2", 1)
	})

	t.Run("multi batch for >10 prefixes", func(t *testing.T) {
		mockHelper := NewMockAPIHelperWithT(t)
		openAPI, _, _ := mockHelper.GetMocks()

		eni := &networkv1beta1.Nic{ID: "eni-test"}
		node := NewNodeFactory("n").Build()

		// First call returns 10 prefixes
		firstBatch := make([]aliyunClient.IPSet, 10)
		for i := range firstBatch {
			firstBatch[i] = aliyunClient.IPSet{Prefix: aliyunClient.Prefix("10.0.0.0/28")}
		}
		// Second call returns 2 prefixes
		secondBatch := []aliyunClient.IPSet{
			{Prefix: "10.0.1.0/28"},
			{Prefix: "10.0.1.16/28"},
		}
		openAPI.On("AssignPrivateIPAddressV2", mock.Anything, mock.Anything).
			Return(firstBatch, nil).Once()
		openAPI.On("AssignPrivateIPAddressV2", mock.Anything, mock.Anything).
			Return(secondBatch, nil).Once()

		reconciler := NewReconcilerBuilder().
			WithAliyun(openAPI).
			WithDefaults().
			Build()

		ctx := newMetaCtx()
		err := reconciler.assignIPv4Prefix(ctx, node, eni, 12)
		require.NoError(t, err)

		assert.Len(t, eni.IPv4Prefix, 12)
		openAPI.AssertNumberOfCalls(t, "AssignPrivateIPAddressV2", 2)
	})

	t.Run("skips non-prefix results", func(t *testing.T) {
		mockHelper := NewMockAPIHelperWithT(t)
		openAPI, _, _ := mockHelper.GetMocks()

		eni := &networkv1beta1.Nic{ID: "eni-test"}
		node := NewNodeFactory("n").Build()

		openAPI.On("AssignPrivateIPAddressV2", mock.Anything, mock.Anything).
			Return([]aliyunClient.IPSet{
				{Prefix: "10.0.0.0/28"},
				{Prefix: ""},
				{Prefix: "10.0.0.16/28"},
			}, nil).Once()

		reconciler := NewReconcilerBuilder().
			WithAliyun(openAPI).
			WithDefaults().
			Build()

		ctx := newMetaCtx()
		err := reconciler.assignIPv4Prefix(ctx, node, eni, 3)
		require.NoError(t, err)

		assert.Len(t, eni.IPv4Prefix, 2, "only non-empty prefix entries should be appended")
	})
}

// TestAssignIPv6Prefix_Direct exercises assignIPv6Prefix directly.
func TestAssignIPv6Prefix_Direct(t *testing.T) {
	t.Run("single batch", func(t *testing.T) {
		mockHelper := NewMockAPIHelperWithT(t)
		openAPI, _, _ := mockHelper.GetMocks()

		eni := &networkv1beta1.Nic{ID: "eni-test"}
		node := NewNodeFactory("n").Build()

		openAPI.On("AssignIpv6AddressesV2", mock.Anything, mock.Anything).
			Return([]aliyunClient.IPSet{
				{Prefix: "fd00::/80"},
			}, nil).Once()

		reconciler := NewReconcilerBuilder().
			WithAliyun(openAPI).
			WithDefaults().
			Build()

		ctx := newMetaCtx()
		err := reconciler.assignIPv6Prefix(ctx, node, eni, 1)
		require.NoError(t, err)

		assert.Len(t, eni.IPv6Prefix, 1)
		assert.Equal(t, "fd00::/80", eni.IPv6Prefix[0].Prefix)
		assert.Equal(t, networkv1beta1.IPPrefixStatusValid, eni.IPv6Prefix[0].Status)
	})

	t.Run("error returned", func(t *testing.T) {
		mockHelper := NewMockAPIHelperWithT(t)
		openAPI, _, _ := mockHelper.GetMocks()

		eni := &networkv1beta1.Nic{ID: "eni-test"}
		node := NewNodeFactory("n").Build()

		openAPI.On("AssignIpv6AddressesV2", mock.Anything, mock.Anything).
			Return([]aliyunClient.IPSet(nil), errors.New("quota exceeded")).Once()

		reconciler := NewReconcilerBuilder().
			WithAliyun(openAPI).
			WithDefaults().
			Build()

		ctx := newMetaCtx()
		err := reconciler.assignIPv6Prefix(ctx, node, eni, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "quota exceeded")
	})
}

// TestConvertPrefixesToCR tests the helper function that converts API prefix results to CR format.
// This is critical for the fix where we record prefixes when creating an ENI so that
// subsequent reconciles can count them as "pending" prefixes on Attaching ENIs.
func TestConvertPrefixesToCR(t *testing.T) {
	tests := []struct {
		name           string
		input          []aliyunClient.Prefix
		expectedLen    int
		expectedFirst  string
		expectedStatus networkv1beta1.IPPrefixStatus
	}{
		{
			name:        "nil input returns nil",
			input:       nil,
			expectedLen: 0,
		},
		{
			name:        "empty input returns nil",
			input:       []aliyunClient.Prefix{},
			expectedLen: 0,
		},
		{
			name:           "single prefix converted with Valid status",
			input:          []aliyunClient.Prefix{"172.18.68.0/28"},
			expectedLen:    1,
			expectedFirst:  "172.18.68.0/28",
			expectedStatus: networkv1beta1.IPPrefixStatusValid,
		},
		{
			name: "multiple prefixes all converted with Valid status",
			input: []aliyunClient.Prefix{
				"172.18.68.0/28",
				"172.18.68.16/28",
				"172.18.68.32/28",
			},
			expectedLen:    3,
			expectedFirst:  "172.18.68.0/28",
			expectedStatus: networkv1beta1.IPPrefixStatusValid,
		},
		{
			name:           "IPv6 prefix converted correctly",
			input:          []aliyunClient.Prefix{"2408:4005:387:4c0c::/80"},
			expectedLen:    1,
			expectedFirst:  "2408:4005:387:4c0c::/80",
			expectedStatus: networkv1beta1.IPPrefixStatusValid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertPrefixesToCR(tt.input)

			if tt.expectedLen == 0 {
				assert.Nil(t, result)
				return
			}

			assert.Len(t, result, tt.expectedLen)
			assert.Equal(t, tt.expectedFirst, result[0].Prefix)
			for _, p := range result {
				assert.Equal(t, tt.expectedStatus, p.Status,
					"all prefixes should have Valid status when newly created")
			}
		})
	}
}
