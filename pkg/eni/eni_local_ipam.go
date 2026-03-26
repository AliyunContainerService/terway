package eni

import (
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"sort"
	"sync"

	"github.com/bits-and-blooms/bitset"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/types"
)

var localIPAMLog = logf.Log.WithName("eni_local_ipam")

type PrefixInfo struct {
	Prefix    string
	bitmap    *bitset.BitSet
	allocated map[string]uint // PodID -> offset
	status    networkv1beta1.IPPrefixStatus
}

type ENILocalIPAM struct {
	lock        sync.RWMutex
	eniID       string
	eniMAC      string
	gatewayIP   types.IPSet
	vSwitchCIDR types.IPNetSet
	vSwitchID   string

	trafficMode networkv1beta1.NetworkInterfaceTrafficMode
	enableERDMA bool

	ipv4PrefixMap map[string]*PrefixInfo
	ipv6PrefixMap map[string]*PrefixInfo
	podToPrefixV4 map[string]string // PodID -> Prefix CIDR
	podToPrefixV6 map[string]string
}

// NewENILocalIPAMFromPrefix builds a Prefix-mode IPAM from Node CR's IPv4Prefix/IPv6Prefix.
func NewENILocalIPAMFromPrefix(eniID, mac string, eni *networkv1beta1.Nic, enableERDMA bool) *ENILocalIPAM {
	ipam := &ENILocalIPAM{
		eniID:         eniID,
		eniMAC:        mac,
		vSwitchID:     eni.VSwitchID,
		trafficMode:   eni.NetworkInterfaceTrafficMode,
		enableERDMA:   enableERDMA,
		ipv4PrefixMap: make(map[string]*PrefixInfo),
		ipv6PrefixMap: make(map[string]*PrefixInfo),
		podToPrefixV4: make(map[string]string),
		podToPrefixV6: make(map[string]string),
	}

	// Set gatewayIP and vSwitchCIDR
	if eni.IPv4CIDR != "" {
		gatewayIPv4 := terwayIP.DeriveGatewayIP(eni.IPv4CIDR)
		ipam.gatewayIP.SetIP(gatewayIPv4)
		ipam.vSwitchCIDR.SetIPNet(eni.IPv4CIDR)
	}
	if eni.IPv6CIDR != "" {
		gatewayIPv6 := terwayIP.DeriveGatewayIP(eni.IPv6CIDR)
		ipam.gatewayIP.SetIP(gatewayIPv6)
		ipam.vSwitchCIDR.SetIPNet(eni.IPv6CIDR)
	}

	// Process IPv4 Prefix
	for _, prefix := range eni.IPv4Prefix {
		if prefix.Status != networkv1beta1.IPPrefixStatusValid && prefix.Status != networkv1beta1.IPPrefixStatusFrozen {
			continue
		}
		size := prefixSize(prefix.Prefix)
		ipam.ipv4PrefixMap[prefix.Prefix] = &PrefixInfo{
			Prefix:    prefix.Prefix,
			bitmap:    bitset.New(size),
			allocated: make(map[string]uint),
			status:    prefix.Status,
		}
	}

	// Process IPv6 Prefix
	for _, prefix := range eni.IPv6Prefix {
		if prefix.Status != networkv1beta1.IPPrefixStatusValid && prefix.Status != networkv1beta1.IPPrefixStatusFrozen {
			continue
		}
		size := prefixSize(prefix.Prefix)
		ipam.ipv6PrefixMap[prefix.Prefix] = &PrefixInfo{
			Prefix:    prefix.Prefix,
			bitmap:    bitset.New(size),
			allocated: make(map[string]uint),
			status:    prefix.Status,
		}
	}

	return ipam
}

// IsERDMA returns true when both the node has ERDMA enabled and the NIC is in high-performance traffic mode.
func (e *ENILocalIPAM) IsERDMA() bool {
	return e.enableERDMA && e.trafficMode == networkv1beta1.NetworkInterfaceTrafficModeHighPerformance
}

// HasAllocations returns true if any pod is currently using an IP from this IPAM.
func (e *ENILocalIPAM) HasAllocations() bool {
	e.lock.RLock()
	defer e.lock.RUnlock()

	return len(e.podToPrefixV4) > 0 || len(e.podToPrefixV6) > 0
}

// AllocationCount returns the total number of pod allocations (IPv4 + IPv6) in this IPAM.
func (e *ENILocalIPAM) AllocationCount() int {
	e.lock.RLock()
	defer e.lock.RUnlock()

	return len(e.podToPrefixV4) + len(e.podToPrefixV6)
}

// PoolStats returns (totalIPv4, idleIPv4, totalIPv6, idleIPv6).
func (e *ENILocalIPAM) PoolStats() (int, int, int, int) {
	e.lock.RLock()
	defer e.lock.RUnlock()

	var totalV4, allocV4, totalV6, allocV6 int
	for _, info := range e.ipv4PrefixMap {
		totalV4 += int(info.bitmap.Len())
		allocV4 += len(info.allocated)
	}
	for _, info := range e.ipv6PrefixMap {
		totalV6 += int(info.bitmap.Len())
		allocV6 += len(info.allocated)
	}
	return totalV4, totalV4 - allocV4, totalV6, totalV6 - allocV6
}

// StatusSnapshot exports a point-in-time snapshot of ENI identity and per-prefix allocations.
func (e *ENILocalIPAM) StatusSnapshot() (eniID, mac string, ipv4 []PrefixStatus, ipv6 []PrefixStatus) {
	e.lock.RLock()
	defer e.lock.RUnlock()

	eniID = e.eniID
	mac = e.eniMAC
	ipv4 = snapshotPrefixMap(e.ipv4PrefixMap)
	ipv6 = snapshotPrefixMap(e.ipv6PrefixMap)
	return
}

func snapshotPrefixMap(prefixMap map[string]*PrefixInfo) []PrefixStatus {
	result := make([]PrefixStatus, 0, len(prefixMap))
	for _, info := range prefixMap {
		total := int(info.bitmap.Len())
		used := len(info.allocated)

		allocs := make([]PrefixAllocation, 0, used)
		for podID, offset := range info.allocated {
			ip, err := calculateIP(info.Prefix, offset)
			if err != nil {
				continue
			}
			allocs = append(allocs, PrefixAllocation{IP: ip.String(), PodID: podID})
		}

		result = append(result, PrefixStatus{
			Prefix:      info.Prefix,
			Status:      string(info.status),
			Total:       total,
			Used:        used,
			Available:   total - used,
			Allocations: allocs,
		})
	}
	return result
}

type prefixCandidate struct {
	cidr string
	info *PrefixInfo
}

// sortedValidPrefixes returns Valid prefixes sorted by remaining capacity ascending
// (fewest remaining first = most-full first). This packs allocations into fewer
// prefixes, enabling earlier release of empty prefixes.
func sortedValidPrefixes(prefixMap map[string]*PrefixInfo) []prefixCandidate {
	candidates := make([]prefixCandidate, 0, len(prefixMap))
	for cidr, info := range prefixMap {
		if info.status != networkv1beta1.IPPrefixStatusValid {
			continue
		}
		candidates = append(candidates, prefixCandidate{cidr: cidr, info: info})
	}
	sort.Slice(candidates, func(i, j int) bool {
		remainI := candidates[i].info.bitmap.Len() - uint(len(candidates[i].info.allocated))
		remainJ := candidates[j].info.bitmap.Len() - uint(len(candidates[j].info.allocated))
		if remainI != remainJ {
			return remainI < remainJ
		}
		return candidates[i].cidr < candidates[j].cidr
	})
	return candidates
}

func (e *ENILocalIPAM) AllocateIPv4(podID string) (netip.Addr, error) {
	e.lock.Lock()
	defer e.lock.Unlock()

	if prefix, ok := e.podToPrefixV4[podID]; ok {
		if info, exists := e.ipv4PrefixMap[prefix]; exists {
			if offset, found := info.allocated[podID]; found {
				ip, err := calculateIP(prefix, offset)
				if err == nil {
					return ip, nil
				}
			}
		}
	}
	for _, c := range sortedValidPrefixes(e.ipv4PrefixMap) {
		nextClear, ok := c.info.bitmap.NextClear(0)
		if !ok || nextClear >= c.info.bitmap.Len() {
			continue
		}
		c.info.bitmap.Set(nextClear)
		c.info.allocated[podID] = nextClear
		e.podToPrefixV4[podID] = c.cidr
		ip, err := calculateIP(c.cidr, nextClear)
		if err != nil {
			localIPAMLog.Error(err, "failed to calculate IPv4 address", "prefix", c.cidr, "offset", nextClear)
			c.info.bitmap.Clear(nextClear)
			delete(c.info.allocated, podID)
			delete(e.podToPrefixV4, podID)
			continue
		}
		return ip, nil
	}
	return netip.Addr{}, fmt.Errorf("no available IPv4 address in prefixes")
}

func (e *ENILocalIPAM) AllocateIPv6(podID string) (netip.Addr, error) {
	e.lock.Lock()
	defer e.lock.Unlock()

	if prefix, ok := e.podToPrefixV6[podID]; ok {
		if info, exists := e.ipv6PrefixMap[prefix]; exists {
			if offset, found := info.allocated[podID]; found {
				ip, err := calculateIP(prefix, offset)
				if err == nil {
					return ip, nil
				}
			}
		}
	}
	for _, c := range sortedValidPrefixes(e.ipv6PrefixMap) {
		nextClear, ok := c.info.bitmap.NextClear(0)
		if !ok || nextClear >= c.info.bitmap.Len() {
			continue
		}
		c.info.bitmap.Set(nextClear)
		c.info.allocated[podID] = nextClear
		e.podToPrefixV6[podID] = c.cidr
		ip, err := calculateIP(c.cidr, nextClear)
		if err != nil {
			localIPAMLog.Error(err, "failed to calculate IPv6 address", "prefix", c.cidr, "offset", nextClear)
			c.info.bitmap.Clear(nextClear)
			delete(c.info.allocated, podID)
			delete(e.podToPrefixV6, podID)
			continue
		}
		return ip, nil
	}
	return netip.Addr{}, fmt.Errorf("no available IPv6 address in prefixes")
}

// ReleaseIPv4 releases an IPv4 address.
func (e *ENILocalIPAM) ReleaseIPv4(podID string) {
	e.lock.Lock()
	defer e.lock.Unlock()

	prefix, ok := e.podToPrefixV4[podID]
	if !ok {
		return
	}
	info, ok := e.ipv4PrefixMap[prefix]
	if !ok {
		return
	}
	offset, ok := info.allocated[podID]
	if !ok {
		return
	}
	info.bitmap.Clear(offset)
	delete(info.allocated, podID)
	delete(e.podToPrefixV4, podID)
}

// ReleaseIPv6 releases an IPv6 address.
func (e *ENILocalIPAM) ReleaseIPv6(podID string) {
	e.lock.Lock()
	defer e.lock.Unlock()

	prefix, ok := e.podToPrefixV6[podID]
	if !ok {
		return
	}
	info, ok := e.ipv6PrefixMap[prefix]
	if !ok {
		return
	}
	offset, ok := info.allocated[podID]
	if !ok {
		return
	}
	info.bitmap.Clear(offset)
	delete(info.allocated, podID)
	delete(e.podToPrefixV6, podID)
}

// RestorePod restores the IP allocation state for a Pod.
func (e *ENILocalIPAM) RestorePod(podID string, ipv4, ipv6 string, eniID string) {
	if eniID != e.eniID {
		return
	}

	e.lock.Lock()
	defer e.lock.Unlock()

	if ipv4 != "" {
		ip, err := netip.ParseAddr(ipv4)
		if err != nil {
			localIPAMLog.Error(err, "failed to parse IPv4 address for restore", "ip", ipv4, "podID", podID)
		} else {
			prefix, offset, found := findPrefixContainingIP(ip, e.ipv4PrefixMap)
			if found {
				info := e.ipv4PrefixMap[prefix]
				info.bitmap.Set(offset)
				info.allocated[podID] = offset
				e.podToPrefixV4[podID] = prefix
			}
		}
	}

	if ipv6 != "" {
		ip, err := netip.ParseAddr(ipv6)
		if err != nil {
			localIPAMLog.Error(err, "failed to parse IPv6 address for restore", "ip", ipv6, "podID", podID)
		} else {
			prefix, offset, found := findPrefixContainingIP(ip, e.ipv6PrefixMap)
			if found {
				info := e.ipv6PrefixMap[prefix]
				info.bitmap.Set(offset)
				info.allocated[podID] = offset
				e.podToPrefixV6[podID] = prefix
			}
		}
	}
}

// UpdatePrefixes syncs the local prefix state with the Node CR's prefix list.
// Prefixes removed from the CR (or marked Deleting) are set to Deleting locally,
// which blocks new allocations via sortedValidPrefixes. Once all Pods drain from
// a Deleting prefix, it is cleaned up on the next call.
func (e *ENILocalIPAM) UpdatePrefixes(prefixes []networkv1beta1.IPPrefix, isIPv6 bool) {
	e.lock.Lock()
	defer e.lock.Unlock()

	var currentPrefixMap map[string]*PrefixInfo
	if isIPv6 {
		currentPrefixMap = e.ipv6PrefixMap
	} else {
		currentPrefixMap = e.ipv4PrefixMap
	}

	newPrefixMap := make(map[string]*networkv1beta1.IPPrefix)
	for i := range prefixes {
		newPrefixMap[prefixes[i].Prefix] = &prefixes[i]
	}

	// Prefixes removed from the CR: delete if drained, otherwise mark Deleting.
	for prefix, info := range currentPrefixMap {
		if _, exists := newPrefixMap[prefix]; !exists {
			if len(info.allocated) == 0 {
				delete(currentPrefixMap, prefix)
			} else {
				info.status = networkv1beta1.IPPrefixStatusDeleting
			}
		}
	}

	// Prefixes present in the CR: update status or create.
	for prefix, newPrefix := range newPrefixMap {
		if info, exists := currentPrefixMap[prefix]; exists {
			info.status = newPrefix.Status
			// Drained Deleting prefix can be removed immediately.
			if info.status == networkv1beta1.IPPrefixStatusDeleting && len(info.allocated) == 0 {
				delete(currentPrefixMap, prefix)
			}
		} else {
			if newPrefix.Status == networkv1beta1.IPPrefixStatusValid || newPrefix.Status == networkv1beta1.IPPrefixStatusFrozen {
				size := prefixSize(prefix)
				currentPrefixMap[prefix] = &PrefixInfo{
					Prefix:    prefix,
					bitmap:    bitset.New(size),
					allocated: make(map[string]uint),
					status:    newPrefix.Status,
				}
			}
		}
	}
}

// IsEmpty returns true when the IPAM has no prefixes left (all drained and cleaned up).
func (e *ENILocalIPAM) IsEmpty() bool {
	e.lock.RLock()
	defer e.lock.RUnlock()

	return len(e.ipv4PrefixMap) == 0 && len(e.ipv6PrefixMap) == 0
}

// calculateIP calculates the IP address from CIDR and offset.
func calculateIP(cidr string, offset uint) (netip.Addr, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("failed to parse CIDR %s: %w", cidr, err)
	}

	// Convert network address to big.Int
	networkIP := ipNet.IP
	if networkIP.To4() != nil {
		networkIP = networkIP.To4()
	}
	ipInt := big.NewInt(0).SetBytes(networkIP)

	// Add offset
	offsetInt := big.NewInt(0).SetUint64(uint64(offset))
	resultInt := big.NewInt(0).Add(ipInt, offsetInt)

	// Convert back to []byte, maintaining original IP length
	resultBytes := resultInt.Bytes()
	expectedLen := len(networkIP)
	if len(resultBytes) < expectedLen {
		padded := make([]byte, expectedLen)
		copy(padded[expectedLen-len(resultBytes):], resultBytes)
		resultBytes = padded
	} else if len(resultBytes) > expectedLen {
		resultBytes = resultBytes[len(resultBytes)-expectedLen:]
	}

	addr, ok := netip.AddrFromSlice(resultBytes)
	if !ok {
		return netip.Addr{}, fmt.Errorf("failed to convert result bytes to netip.Addr")
	}
	return addr, nil
}

// findPrefixContainingIP finds the prefix containing the specified IP.
// For capped IPv6 prefixes (e.g. /80), the offset must fall within the
// bitmap range; IPs with non-zero middle bits are rejected.
func findPrefixContainingIP(ip netip.Addr, prefixMap map[string]*PrefixInfo) (string, uint, bool) {
	for prefix, info := range prefixMap {
		_, ipNet, err := net.ParseCIDR(prefix)
		if err != nil {
			continue
		}
		if ipNet.Contains(ip.AsSlice()) {
			networkIP := ipNet.IP
			if networkIP.To4() != nil {
				networkIP = networkIP.To4()
			}
			ipBytes := ip.AsSlice()
			ipInt := big.NewInt(0).SetBytes(ipBytes)
			networkInt := big.NewInt(0).SetBytes(networkIP)
			offset := big.NewInt(0).Sub(ipInt, networkInt).Uint64()
			if offset >= uint64(info.bitmap.Len()) {
				continue
			}
			return prefix, uint(offset), true
		}
	}
	return "", 0, false
}

// IPv6PrefixMaxAddresses caps the managed address range for large IPv6 prefixes
// (e.g. /80 with 2^48 host bits). Addresses are formed as:
//
//	[prefix bits][zeros][16-bit offset]
//
// so only the lowest 16 bits are used, giving 65536 usable addresses per prefix.
const IPv6PrefixMaxAddresses uint = 65536

// prefixSize calculates the number of IPs in a prefix (package-level function for use by constructors).
func prefixSize(cidr string) uint {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0
	}

	ones, bits := ipNet.Mask.Size()
	if ones >= bits {
		return 1
	}

	hostBits := bits - ones
	if bits == 128 && hostBits > 16 {
		return IPv6PrefixMaxAddresses
	}

	return 1 << hostBits
}
