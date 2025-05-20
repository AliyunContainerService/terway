package node

import (
	"sort"

	"github.com/go-logr/logr"
	"github.com/samber/lo"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

type Condition string

const (
	ConditionInsufficientIP = "InsufficientIP"
	ConditionOperationErr   = "OperationErr"
)

type eniTypeKey struct {
	networkv1beta1.ENIType
	networkv1beta1.NetworkInterfaceTrafficMode
}

var secondaryKey = eniTypeKey{
	ENIType:                     networkv1beta1.ENITypeSecondary,
	NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
}

var trunkKey = eniTypeKey{
	ENIType:                     networkv1beta1.ENITypeTrunk,
	NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
}

var rdmaKey = eniTypeKey{
	ENIType:                     networkv1beta1.ENITypeSecondary,
	NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeHighPerformance,
}

type eniOptions struct {
	eniTypeKey eniTypeKey

	// if eniRef is nil , we use options to create the eni
	eniRef *networkv1beta1.Nic

	addIPv4N int
	addIPv6N int

	isFull bool
	errors []error
}

var EniOptions = map[eniTypeKey]*aliyunClient.CreateNetworkInterfaceOptions{
	secondaryKey: {
		NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
			Trunk: false,
			ERDMA: false,
		},
	},
	trunkKey: {
		NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
			Trunk: true,
			ERDMA: false,
		},
	},
	rdmaKey: {
		NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
			Trunk: false,
			ERDMA: true,
		},
	},
}

// releaseUnUsedIP toDel is the number of idle ip need to del
func releaseUnUsedIP(log logr.Logger, eni *networkv1beta1.Nic, toDel int) int {
	_, inUse := IPUsage(eni.IPv4)
	_, inUseV6 := IPUsage(eni.IPv6)
	// try delete eni, only if no one use it
	if inUse == 0 && inUseV6 == 0 &&
		len(eni.IPv4) < toDel && len(eni.IPv6) < toDel &&
		eni.NetworkInterfaceType == networkv1beta1.ENITypeSecondary &&
		eni.NetworkInterfaceTrafficMode == networkv1beta1.NetworkInterfaceTrafficModeStandard {

		eni.Status = aliyunClient.ENIStatusDeleting

		log.Info("release eni", "eni", eni.ID)

		return max(len(eni.IPv4), len(eni.IPv6))
	}

	// balance ip , in case of unnecessary ip release
	idleV4 := IdlesWithAvailable(eni.IPv4)
	idleV6 := IdlesWithAvailable(eni.IPv6)

	leftover := max(idleV4, idleV6) - toDel

	toDelIPv4 := idleV4 - leftover
	toDelIPv6 := idleV6 - leftover
	releasedV4, releasedV6 := 0, 0

	for _, v := range eni.IPv4 {
		if v.PodID == "" && !v.Primary && toDelIPv4 > 0 && v.Status != networkv1beta1.IPStatusDeleting {
			v.Status = networkv1beta1.IPStatusDeleting
			toDelIPv4--
			releasedV4++
		}
	}

	for _, v := range eni.IPv6 {
		if v.PodID == "" && !v.Primary && toDelIPv6 > 0 && v.Status != networkv1beta1.IPStatusDeleting {
			v.Status = networkv1beta1.IPStatusDeleting
			toDelIPv6--
			releasedV6++
		}
	}
	log.Info("released ip", "v4", releasedV4, "v6", releasedV6)

	return max(releasedV4, releasedV6)
}

func newENIFromAPI(eni *aliyunClient.NetworkInterface) *networkv1beta1.Nic {

	return &networkv1beta1.Nic{
		ID:                          eni.NetworkInterfaceID,
		Status:                      eni.Status,
		MacAddress:                  eni.MacAddress,
		VSwitchID:                   eni.VSwitchID,
		SecurityGroupIDs:            eni.SecurityGroupIDs,
		PrimaryIPAddress:            eni.PrivateIPAddress,
		NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficMode(eni.NetworkInterfaceTrafficMode),
		NetworkInterfaceType:        networkv1beta1.ENIType(eni.Type),
		IPv4:                        convertIPSet(eni.PrivateIPSets),
		IPv6:                        convertIPSet(eni.IPv6Set),
	}
}

// convertIPSet convert aliyunClient.IPSet to networkv1beta1.IP
// for valid ip , IPAddress is always be set
func convertIPSet(in []aliyunClient.IPSet) map[string]*networkv1beta1.IP {
	return lo.SliceToMap(in, func(item aliyunClient.IPSet) (string, *networkv1beta1.IP) {
		if item.IPName != "" && item.IPStatus != aliyunClient.LENIIPStatusAvailable && !item.Primary {
			if item.IPAddress == item.IPName {
				return item.IPAddress, &networkv1beta1.IP{
					IP:      item.IPAddress,
					IPName:  item.IPName,
					Status:  networkv1beta1.IPStatusDeleting,
					Primary: item.Primary,
				}
			}
			return item.IPAddress, &networkv1beta1.IP{
				IP:      item.IPAddress,
				IPName:  item.IPName,
				Status:  networkv1beta1.IPStatusDeleting,
				Primary: item.Primary,
			}
		}
		return item.IPAddress, &networkv1beta1.IP{
			IP:      item.IPAddress,
			IPName:  item.IPName,
			Status:  networkv1beta1.IPStatusValid,
			Primary: item.Primary,
		}
	})
}

func mergeIPMap(log logr.Logger, remote, current map[string]*networkv1beta1.IP) {
	// delete remote not in current
	for k := range current {
		_, ok := remote[k]
		if !ok {
			log.Info("sync eni with remote, delete ip from local", "ip", k)
			delete(current, k)
		}
	}

	// merge remote to current
	for k, v := range remote {
		_, ok := current[k]
		if !ok {
			if current == nil {
				current = make(map[string]*networkv1beta1.IP)
			}
			current[k] = v
			log.Info("sync eni with remote, add ip to local", "ip", k)
		}
	}
}

// sortNetworkInterface by eni's ip desc. We won't delete trunk or rdma card ,we should use those first.
func sortNetworkInterface(node *networkv1beta1.Node) []*networkv1beta1.Nic {
	sorted := lo.Values(node.Status.NetworkInterfaces)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].NetworkInterfaceType == networkv1beta1.ENITypeTrunk && sorted[j].NetworkInterfaceType != networkv1beta1.ENITypeTrunk {
			return true
		}
		if sorted[i].NetworkInterfaceType != networkv1beta1.ENITypeTrunk && sorted[j].NetworkInterfaceType == networkv1beta1.ENITypeTrunk {
			return false
		}

		if sorted[i].NetworkInterfaceTrafficMode == networkv1beta1.NetworkInterfaceTrafficModeHighPerformance && sorted[j].NetworkInterfaceTrafficMode != networkv1beta1.NetworkInterfaceTrafficModeHighPerformance {
			return true
		}
		if sorted[i].NetworkInterfaceTrafficMode != networkv1beta1.NetworkInterfaceTrafficModeHighPerformance && sorted[j].NetworkInterfaceTrafficMode == networkv1beta1.NetworkInterfaceTrafficModeHighPerformance {
			return false
		}

		if node.Spec.ENISpec.EnableIPv4 {
			return len(sorted[i].IPv4) > len(sorted[j].IPv4)
		}
		return len(sorted[i].IPv6) > len(sorted[j].IPv6)
	})
	return sorted
}

func IPUsage(eniIP map[string]*networkv1beta1.IP) (int, int) {
	idle, inUse := 0, 0
	for _, v := range eniIP {
		if v.PodID == "" {
			idle++
		} else {
			inUse++
		}
	}

	return idle, inUse
}

func IdlesWithAvailable(eniIP map[string]*networkv1beta1.IP) (count int) {
	for _, v := range eniIP {
		if v.PodID == "" && v.Status == networkv1beta1.IPStatusValid {
			count++
		}
	}
	return
}
