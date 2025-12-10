//go:build e2e

package tests

import (
	"context"
	"fmt"
	"strings"
	"testing"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient"
)

// =============================================================================
// Node Capacity Requirements
// =============================================================================

const (
	// MinAdaptersForSharedENI is the minimum number of adapters required for shared ENI mode tests
	// Shared ENI mode requires at least 4 NICs
	MinAdaptersForSharedENI = 4

	// MinAdaptersForExclusiveENI is the minimum number of adapters required for exclusive ENI mode tests
	// Exclusive ENI mode requires at least 5 NICs
	MinAdaptersForExclusiveENI = 5

	// MinIPsPerAdapterForTest is the minimum IPs per adapter for reliable testing
	MinIPsPerAdapterForTest = 10
)

// NodeCapacityInfo contains capacity information for a single node
type NodeCapacityInfo struct {
	NodeName       string
	Adapters       int // Number of available adapters (NICs)
	TotalAdapters  int // Total adapters including primary
	IPv4PerAdapter int
	IPv6PerAdapter int
	NodeType       NodeType
	MeetsSharedENIRequirements    bool
	MeetsExclusiveENIRequirements bool
}

// NodeCapacityMap maps node names to their capacity info
type NodeCapacityMap map[string]*NodeCapacityInfo

// =============================================================================
// Node Type Definitions
// =============================================================================

// NodeType represents the type of node
type NodeType string

const (
	NodeTypeECSSharedENI        NodeType = "ecs-shared-eni"
	NodeTypeECSExclusiveENI     NodeType = "ecs-exclusive-eni"
	NodeTypeLingjunSharedENI    NodeType = "lingjun-shared-eni"
	NodeTypeLingjunExclusiveENI NodeType = "lingjun-exclusive-eni"
)

// ENIMode represents the ENI mode
type ENIMode string

const (
	ENIModeShared    ENIMode = "shared"
	ENIModeExclusive ENIMode = "exclusive"
)

// MachineType represents the machine type
type MachineType string

const (
	MachineTypeECS     MachineType = "ecs"
	MachineTypeLingjun MachineType = "lingjun"
)

// =============================================================================
// Node Type Info
// =============================================================================

// NodeTypeInfo contains information about available node types in the cluster
type NodeTypeInfo struct {
	ECSSharedENINodes        []corev1.Node
	ECSExclusiveENINodes     []corev1.Node
	LingjunSharedENINodes    []corev1.Node
	LingjunExclusiveENINodes []corev1.Node
	AllNodes                 []corev1.Node
}

// HasTrunkNodes checks if the cluster has nodes that support trunk mode
func (n *NodeTypeInfo) HasTrunkNodes() bool {
	// ECS nodes support trunk mode, Lingjun nodes do NOT support trunk mode
	return len(n.ECSSharedENINodes) > 0 || len(n.ECSExclusiveENINodes) > 0
}

// HasExclusiveENINodes checks if the cluster has exclusive ENI nodes
func (n *NodeTypeInfo) HasExclusiveENINodes() bool {
	return len(n.ECSExclusiveENINodes) > 0 || len(n.LingjunExclusiveENINodes) > 0
}

// HasLingjunNodes checks if the cluster has Lingjun nodes
func (n *NodeTypeInfo) HasLingjunNodes() bool {
	return len(n.LingjunSharedENINodes) > 0 || len(n.LingjunExclusiveENINodes) > 0
}

// GetNodesByType returns nodes of a specific type
func (n *NodeTypeInfo) GetNodesByType(nodeType NodeType) []corev1.Node {
	switch nodeType {
	case NodeTypeECSSharedENI:
		return n.ECSSharedENINodes
	case NodeTypeECSExclusiveENI:
		return n.ECSExclusiveENINodes
	case NodeTypeLingjunSharedENI:
		return n.LingjunSharedENINodes
	case NodeTypeLingjunExclusiveENI:
		return n.LingjunExclusiveENINodes
	default:
		return []corev1.Node{}
	}
}

// GetNodeZone returns the zone of a node from its labels
func GetNodeZone(node *corev1.Node) string {
	// Try standard Kubernetes topology label first
	if zone := node.Labels["topology.kubernetes.io/zone"]; zone != "" {
		return zone
	}
	// Fallback to legacy label
	if zone := node.Labels["failure-domain.beta.kubernetes.io/zone"]; zone != "" {
		return zone
	}
	return ""
}

// GetZonesForNodeType returns all unique zones for nodes of a specific type
func (n *NodeTypeInfo) GetZonesForNodeType(nodeType NodeType) []string {
	nodes := n.GetNodesByType(nodeType)
	zoneSet := make(map[string]bool)
	for _, node := range nodes {
		zone := GetNodeZone(&node)
		if zone != "" {
			zoneSet[zone] = true
		}
	}

	var zones []string
	for zone := range zoneSet {
		zones = append(zones, zone)
	}
	return zones
}

// HasMultipleZones checks if there are nodes of the specified type in multiple zones
func (n *NodeTypeInfo) HasMultipleZones(nodeType NodeType) bool {
	return len(n.GetZonesForNodeType(nodeType)) >= 2
}

// GetNodesInZone returns nodes of a specific type in a specific zone
func (n *NodeTypeInfo) GetNodesInZone(nodeType NodeType, zone string) []corev1.Node {
	nodes := n.GetNodesByType(nodeType)
	var result []corev1.Node
	for _, node := range nodes {
		if GetNodeZone(&node) == zone {
			result = append(result, node)
		}
	}
	return result
}

// GetNodePairInDifferentZones returns two nodes of the specified type in different zones
// Returns nil, nil if not enough nodes in different zones
func (n *NodeTypeInfo) GetNodePairInDifferentZones(nodeType NodeType) (*corev1.Node, *corev1.Node) {
	nodes := n.GetNodesByType(nodeType)
	if len(nodes) < 2 {
		return nil, nil
	}

	// Find two nodes in different zones
	for i := 0; i < len(nodes); i++ {
		zone1 := GetNodeZone(&nodes[i])
		for j := i + 1; j < len(nodes); j++ {
			zone2 := GetNodeZone(&nodes[j])
			if zone1 != zone2 && zone1 != "" && zone2 != "" {
				return &nodes[i], &nodes[j]
			}
		}
	}

	return nil, nil
}

// =============================================================================
// Node Type Discovery and Classification
// =============================================================================

// DiscoverNodeTypes discovers and classifies all nodes in the cluster
func DiscoverNodeTypes(ctx context.Context, client klient.Client) (*NodeTypeInfo, error) {
	nodes := &corev1.NodeList{}
	err := client.Resources().List(ctx, nodes)
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	info := &NodeTypeInfo{
		AllNodes: nodes.Items,
	}

	for _, node := range nodes.Items {
		nodeType := classifyNode(node)
		switch nodeType {
		case NodeTypeECSSharedENI:
			info.ECSSharedENINodes = append(info.ECSSharedENINodes, node)
		case NodeTypeECSExclusiveENI:
			info.ECSExclusiveENINodes = append(info.ECSExclusiveENINodes, node)
		case NodeTypeLingjunSharedENI:
			info.LingjunSharedENINodes = append(info.LingjunSharedENINodes, node)
		case NodeTypeLingjunExclusiveENI:
			info.LingjunExclusiveENINodes = append(info.LingjunExclusiveENINodes, node)
		}
	}

	return info, nil
}

// classifyNode determines the type of a node based on its labels and resources
func classifyNode(node corev1.Node) NodeType {
	// Check if it's a Lingjun exclusive ENI node first (both Lingjun and exclusive ENI)
	if isLingjunNode(&node) && isExclusiveENINode(&node) {
		return NodeTypeLingjunExclusiveENI
	}

	// Check if it's a Lingjun shared ENI node
	if isLingjunNode(&node) {
		return NodeTypeLingjunSharedENI
	}

	// Check if it's an ECS exclusive ENI node
	if isExclusiveENINode(&node) {
		return NodeTypeECSExclusiveENI
	}

	// Default to ECS shared ENI node
	return NodeTypeECSSharedENI
}

// isLingjunNode checks if a node is a Lingjun node
func isLingjunNode(node metav1.Object) bool {
	return node.GetLabels()["alibabacloud.com/lingjun-worker"] == "true"
}

// isExclusiveENINode checks if a node is configured for exclusive ENI mode
func isExclusiveENINode(node metav1.Object) bool {
	return node.GetLabels()["k8s.aliyun.com/exclusive-mode-eni-type"] == "eniOnly"
}

// =============================================================================
// Node Affinity Helpers
// =============================================================================

// GetNodeAffinityForType returns node affinity labels for scheduling pods to specific node types
func GetNodeAffinityForType(nodeType NodeType) map[string]string {
	switch nodeType {
	case NodeTypeECSSharedENI:
		return map[string]string{}
	case NodeTypeECSExclusiveENI:
		return map[string]string{
			"k8s.aliyun.com/exclusive-mode-eni-type": "eniOnly",
		}
	case NodeTypeLingjunSharedENI:
		return map[string]string{
			"alibabacloud.com/lingjun-worker": "true",
		}
	case NodeTypeLingjunExclusiveENI:
		return map[string]string{
			"alibabacloud.com/lingjun-worker":        "true",
			"k8s.aliyun.com/exclusive-mode-eni-type": "eniOnly",
		}
	default:
		return map[string]string{}
	}
}

// GetNodeAffinityExcludeForType returns node affinity exclusion labels for specific node types
func GetNodeAffinityExcludeForType(nodeType NodeType) map[string]string {
	switch nodeType {
	case NodeTypeECSSharedENI:
		// For ECS shared ENI nodes, exclude all Lingjun nodes and ECS exclusive ENI nodes
		return map[string]string{
			"alibabacloud.com/lingjun-worker":        "true",
			"k8s.aliyun.com/exclusive-mode-eni-type": "eniOnly",
		}
	case NodeTypeECSExclusiveENI:
		// For ECS exclusive ENI nodes, exclude all Lingjun nodes
		return map[string]string{
			"alibabacloud.com/lingjun-worker": "true",
		}
	case NodeTypeLingjunSharedENI:
		// For Lingjun shared ENI nodes, exclude exclusive ENI nodes
		return map[string]string{
			"k8s.aliyun.com/exclusive-mode-eni-type": "eniOnly",
		}
	case NodeTypeLingjunExclusiveENI:
		// For Lingjun exclusive ENI nodes, no exclusions needed (already precisely matched)
		return map[string]string{}
	default:
		return map[string]string{}
	}
}

// =============================================================================
// ENI Mode and Machine Type Helpers
// =============================================================================

// GetNodeTypesForENIMode returns node types that match the specified ENI mode
func GetNodeTypesForENIMode(mode ENIMode) []NodeType {
	switch mode {
	case ENIModeShared:
		return []NodeType{NodeTypeECSSharedENI, NodeTypeLingjunSharedENI}
	case ENIModeExclusive:
		return []NodeType{NodeTypeECSExclusiveENI, NodeTypeLingjunExclusiveENI}
	default:
		return []NodeType{}
	}
}

// GetNodeTypesForMachineType returns node types that match the specified machine type
func GetNodeTypesForMachineType(machineType MachineType) []NodeType {
	switch machineType {
	case MachineTypeECS:
		return []NodeType{NodeTypeECSSharedENI, NodeTypeECSExclusiveENI}
	case MachineTypeLingjun:
		return []NodeType{NodeTypeLingjunSharedENI, NodeTypeLingjunExclusiveENI}
	default:
		return []NodeType{}
	}
}

// GetAllNodeTypes returns all available node types
func GetAllNodeTypes() []NodeType {
	return []NodeType{
		NodeTypeECSSharedENI,
		NodeTypeECSExclusiveENI,
		NodeTypeLingjunSharedENI,
		NodeTypeLingjunExclusiveENI,
	}
}

// GetAvailableNodeTypes returns node types that have at least one available node
func (n *NodeTypeInfo) GetAvailableNodeTypes() []NodeType {
	var available []NodeType
	if len(n.ECSSharedENINodes) > 0 {
		available = append(available, NodeTypeECSSharedENI)
	}
	if len(n.ECSExclusiveENINodes) > 0 {
		available = append(available, NodeTypeECSExclusiveENI)
	}
	if len(n.LingjunSharedENINodes) > 0 {
		available = append(available, NodeTypeLingjunSharedENI)
	}
	if len(n.LingjunExclusiveENINodes) > 0 {
		available = append(available, NodeTypeLingjunExclusiveENI)
	}
	return available
}

// GetAvailableNodeTypesForENIMode returns available node types that match the specified ENI mode
func (n *NodeTypeInfo) GetAvailableNodeTypesForENIMode(mode ENIMode) []NodeType {
	targetTypes := GetNodeTypesForENIMode(mode)
	var available []NodeType
	for _, nodeType := range targetTypes {
		if len(n.GetNodesByType(nodeType)) > 0 {
			available = append(available, nodeType)
		}
	}
	return available
}

// =============================================================================
// Test Iteration Helpers
// =============================================================================

// NodeTypeTestFunc is a function type for tests that run on specific node types
type NodeTypeTestFunc func(t *testing.T, nodeType NodeType)

// RunTestForNodeTypes runs a test function for each specified node type
// It skips the test for node types that are not available in the cluster
func RunTestForNodeTypes(t *testing.T, nodeTypes []NodeType, nodeInfo *NodeTypeInfo, testFunc NodeTypeTestFunc) {
	for _, nodeType := range nodeTypes {
		nodes := nodeInfo.GetNodesByType(nodeType)
		if len(nodes) == 0 {
			t.Logf("Skipping %s: no nodes of this type available", nodeType)
			continue
		}

		t.Run(string(nodeType), func(t *testing.T) {
			testFunc(t, nodeType)
		})
	}
}

// RunTestForENIMode runs a test function for all node types in the specified ENI mode
func RunTestForENIMode(t *testing.T, mode ENIMode, nodeInfo *NodeTypeInfo, testFunc NodeTypeTestFunc) {
	nodeTypes := GetNodeTypesForENIMode(mode)
	RunTestForNodeTypes(t, nodeTypes, nodeInfo, testFunc)
}

// =============================================================================
// Node Support Checks
// =============================================================================

// CheckNodeSupportsExclusiveENI checks if a node supports exclusive ENI resource
func CheckNodeSupportsExclusiveENI(node corev1.Node) bool {
	// Check if the node has aliyun/eni resource
	r := node.Status.Allocatable.Name("aliyun/eni", resource.DecimalSI)
	return r != nil && !r.IsZero()
}

// GetRandomNodeOfType returns a random node of the specified type
func (n *NodeTypeInfo) GetRandomNodeOfType(nodeType NodeType) (*corev1.Node, error) {
	nodes := n.GetNodesByType(nodeType)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes of type %s found", nodeType)
	}
	// Return the first node (in a real implementation, you might want to randomize)
	return &nodes[0], nil
}

// ValidateNodeTypeRequirements checks if the cluster meets the requirements for a test scenario
func ValidateNodeTypeRequirements(nodeInfo *NodeTypeInfo, requiredTypes []NodeType) error {
	var missingTypes []string

	for _, nodeType := range requiredTypes {
		nodes := nodeInfo.GetNodesByType(nodeType)
		if len(nodes) == 0 {
			missingTypes = append(missingTypes, string(nodeType))
		}
	}

	if len(missingTypes) > 0 {
		return fmt.Errorf("required node types not available: %s", strings.Join(missingTypes, ", "))
	}

	return nil
}

// =============================================================================
// Feature Support Helpers
// =============================================================================

// GetSupportedNodeTypesForTrunk returns node types that support trunk mode
func (n *NodeTypeInfo) GetSupportedNodeTypesForTrunk() []NodeType {
	var supported []NodeType

	if len(n.ECSSharedENINodes) > 0 {
		supported = append(supported, NodeTypeECSSharedENI)
	}
	if len(n.ECSExclusiveENINodes) > 0 {
		supported = append(supported, NodeTypeECSExclusiveENI)
	}
	// Lingjun nodes do NOT support trunk mode

	return supported
}

// GetSupportedNodeTypesForExclusiveENI returns node types that support exclusive ENI
func (n *NodeTypeInfo) GetSupportedNodeTypesForExclusiveENI() []NodeType {
	var supported []NodeType

	// Check ECS shared ENI nodes for aliyun/eni resource
	for _, node := range n.ECSSharedENINodes {
		if CheckNodeSupportsExclusiveENI(node) {
			supported = append(supported, NodeTypeECSSharedENI)
			break
		}
	}

	// Check ECS exclusive ENI nodes
	if len(n.ECSExclusiveENINodes) > 0 {
		supported = append(supported, NodeTypeECSExclusiveENI)
	}

	// Lingjun nodes support exclusive ENI but don't expose aliyun/eni resource
	if len(n.LingjunSharedENINodes) > 0 {
		supported = append(supported, NodeTypeLingjunSharedENI)
	}
	if len(n.LingjunExclusiveENINodes) > 0 {
		supported = append(supported, NodeTypeLingjunExclusiveENI)
	}

	return supported
}

// GetSupportedNodeTypesForMultiNetwork returns node types that support multi-network
func (n *NodeTypeInfo) GetSupportedNodeTypesForMultiNetwork() []NodeType {
	var supported []NodeType

	// Multi-network does not support Lingjun nodes
	if len(n.ECSSharedENINodes) > 0 {
		supported = append(supported, NodeTypeECSSharedENI)
	}
	if len(n.ECSExclusiveENINodes) > 0 {
		supported = append(supported, NodeTypeECSExclusiveENI)
	}

	return supported
}

// =============================================================================
// Kube-Proxy and Datapath Helpers
// =============================================================================

// HasNoKubeProxyLabel checks if any node has the no-kube-proxy label
// indicating kube-proxy replacement is enabled (Datapath V2)
func (n *NodeTypeInfo) HasNoKubeProxyLabel() bool {
	for _, node := range n.AllNodes {
		if node.Labels["k8s.aliyun.com/no-kube-proxy"] == "true" {
			return true
		}
	}
	return false
}

// CheckNoKubeProxyLabel checks if the cluster has nodes with kube-proxy replacement enabled
func CheckNoKubeProxyLabel(ctx context.Context, client klient.Client) (bool, error) {
	nodeInfo, err := DiscoverNodeTypes(ctx, client)
	if err != nil {
		return false, err
	}
	return nodeInfo.HasNoKubeProxyLabel(), nil
}

// =============================================================================
// ENI Resource Info
// =============================================================================

// ENIResourceInfo contains information about ENI resources across all nodes
type ENIResourceInfo struct {
	// TotalExclusiveENI is the total allocatable aliyun/eni resources across all nodes
	TotalExclusiveENI int64
	// TotalMemberENI is the total allocatable aliyun/member-eni resources across all nodes
	TotalMemberENI int64
	// NodesWithExclusiveENI contains nodes that have aliyun/eni > 0
	NodesWithExclusiveENI []corev1.Node
	// NodesWithMemberENI contains nodes that have aliyun/member-eni > 0
	NodesWithMemberENI []corev1.Node
}

// HasExclusiveENIResource returns true if any node has aliyun/eni resource
func (e *ENIResourceInfo) HasExclusiveENIResource() bool {
	return e.TotalExclusiveENI > 0
}

// HasMemberENIResource returns true if any node has aliyun/member-eni resource
func (e *ENIResourceInfo) HasMemberENIResource() bool {
	return e.TotalMemberENI > 0
}

// HasENIResource returns true if any node has aliyun/eni resource
func (e *ENIResourceInfo) HasENIResource() bool {
	return e.TotalExclusiveENI > 0
}

// DiscoverENIResources discovers ENI resources across all nodes in the cluster
func DiscoverENIResources(ctx context.Context, client klient.Client) (*ENIResourceInfo, error) {
	nodes := &corev1.NodeList{}
	err := client.Resources().List(ctx, nodes)
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	info := &ENIResourceInfo{}

	for _, node := range nodes.Items {
		// Check aliyun/eni resource (exclusive ENI mode)
		exclusiveENI := node.Status.Allocatable.Name("aliyun/eni", resource.DecimalSI)
		if exclusiveENI != nil && !exclusiveENI.IsZero() {
			info.TotalExclusiveENI += exclusiveENI.Value()
			info.NodesWithExclusiveENI = append(info.NodesWithExclusiveENI, node)
		}

		// Check aliyun/member-eni resource (trunk mode)
		memberENI := node.Status.Allocatable.Name("aliyun/member-eni", resource.DecimalSI)
		if memberENI != nil && !memberENI.IsZero() {
			info.TotalMemberENI += memberENI.Value()
			info.NodesWithMemberENI = append(info.NodesWithMemberENI, node)
		}
	}

	return info, nil
}

// CheckENIResourcesForPodNetworking checks if the cluster has sufficient ENI resources
// for PodNetworking tests. Returns true if at least one of the resources is available.
func CheckENIResourcesForPodNetworking(ctx context.Context, client klient.Client) (*ENIResourceInfo, error) {
	return DiscoverENIResources(ctx, client)
}

// =============================================================================
// Node Capacity Discovery from nodes.network.alibabacloud.com CRD
// =============================================================================

// DiscoverNodeCapacities discovers node capacities from nodes.network.alibabacloud.com CRD
func DiscoverNodeCapacities(ctx context.Context, client klient.Client) (NodeCapacityMap, error) {
	nodes := &networkv1beta1.NodeList{}
	err := client.Resources().List(ctx, nodes)
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes.network.alibabacloud.com: %w", err)
	}

	capacityMap := make(NodeCapacityMap)

	for _, node := range nodes.Items {
		nodeType := classifyNodeCR(&node)
		cap := &NodeCapacityInfo{
			NodeName:       node.Name,
			Adapters:       node.Spec.NodeCap.Adapters,
			TotalAdapters:  node.Spec.NodeCap.TotalAdapters,
			IPv4PerAdapter: node.Spec.NodeCap.IPv4PerAdapter,
			IPv6PerAdapter: node.Spec.NodeCap.IPv6PerAdapter,
			NodeType:       nodeType,
		}

		// Check if node meets test requirements
		cap.MeetsSharedENIRequirements = cap.Adapters >= MinAdaptersForSharedENI
		cap.MeetsExclusiveENIRequirements = cap.Adapters >= MinAdaptersForExclusiveENI

		capacityMap[node.Name] = cap
	}

	return capacityMap, nil
}

// classifyNodeCR classifies a node based on its network CRD labels
func classifyNodeCR(node *networkv1beta1.Node) NodeType {
	isLingjun := node.Labels["alibabacloud.com/lingjun-worker"] == "true"
	isExclusive := node.Labels["k8s.aliyun.com/exclusive-mode-eni-type"] == "eniOnly"

	if isLingjun && isExclusive {
		return NodeTypeLingjunExclusiveENI
	}
	if isLingjun {
		return NodeTypeLingjunSharedENI
	}
	if isExclusive {
		return NodeTypeECSExclusiveENI
	}
	return NodeTypeECSSharedENI
}

// GetQualifiedNodesForTest returns nodes that meet the capacity requirements for testing
func (m NodeCapacityMap) GetQualifiedNodesForTest(nodeType NodeType) []string {
	var qualified []string

	for nodeName, cap := range m {
		if cap.NodeType != nodeType {
			continue
		}

		// Check capacity requirements based on ENI mode
		switch nodeType {
		case NodeTypeECSSharedENI, NodeTypeLingjunSharedENI:
			if cap.MeetsSharedENIRequirements {
				qualified = append(qualified, nodeName)
			}
		case NodeTypeECSExclusiveENI, NodeTypeLingjunExclusiveENI:
			if cap.MeetsExclusiveENIRequirements {
				qualified = append(qualified, nodeName)
			}
		}
	}

	return qualified
}

// GetQualifiedNodeCount returns the count of nodes that meet capacity requirements
func (m NodeCapacityMap) GetQualifiedNodeCount(nodeType NodeType) int {
	return len(m.GetQualifiedNodesForTest(nodeType))
}

// HasQualifiedNodes returns true if there are any qualified nodes for the specified type
func (m NodeCapacityMap) HasQualifiedNodes(nodeType NodeType) bool {
	return m.GetQualifiedNodeCount(nodeType) > 0
}

// GetDisqualifiedNodes returns nodes that don't meet capacity requirements with reasons
func (m NodeCapacityMap) GetDisqualifiedNodes(nodeType NodeType) map[string]string {
	disqualified := make(map[string]string)

	for nodeName, cap := range m {
		if cap.NodeType != nodeType {
			continue
		}

		var required int
		switch nodeType {
		case NodeTypeECSSharedENI, NodeTypeLingjunSharedENI:
			required = MinAdaptersForSharedENI
			if !cap.MeetsSharedENIRequirements {
				disqualified[nodeName] = fmt.Sprintf("adapters=%d, required>=%d", cap.Adapters, required)
			}
		case NodeTypeECSExclusiveENI, NodeTypeLingjunExclusiveENI:
			required = MinAdaptersForExclusiveENI
			if !cap.MeetsExclusiveENIRequirements {
				disqualified[nodeName] = fmt.Sprintf("adapters=%d, required>=%d", cap.Adapters, required)
			}
		}
	}

	return disqualified
}

// PrintCapacitySummary logs a summary of node capacities
func (m NodeCapacityMap) PrintCapacitySummary(t *testing.T) {
	t.Log("=== Node Capacity Summary ===")

	for _, nodeType := range GetAllNodeTypes() {
		qualified := m.GetQualifiedNodesForTest(nodeType)
		disqualified := m.GetDisqualifiedNodes(nodeType)

		if len(qualified) > 0 || len(disqualified) > 0 {
			t.Logf("%s: %d qualified, %d disqualified", nodeType, len(qualified), len(disqualified))

			for _, nodeName := range qualified {
				cap := m[nodeName]
				t.Logf("  ✓ %s: adapters=%d, ipv4PerAdapter=%d",
					nodeName, cap.Adapters, cap.IPv4PerAdapter)
			}

			for nodeName, reason := range disqualified {
				t.Logf("  ✗ %s: %s", nodeName, reason)
			}
		}
	}

	t.Log("=============================")
}

// =============================================================================
// Extended NodeTypeInfo with Capacity Filtering
// =============================================================================

// NodeTypeInfoWithCapacity extends NodeTypeInfo with capacity filtering
type NodeTypeInfoWithCapacity struct {
	*NodeTypeInfo
	Capacities NodeCapacityMap
}

// DiscoverNodeTypesWithCapacity discovers and classifies all nodes with capacity info
func DiscoverNodeTypesWithCapacity(ctx context.Context, client klient.Client) (*NodeTypeInfoWithCapacity, error) {
	nodeInfo, err := DiscoverNodeTypes(ctx, client)
	if err != nil {
		return nil, err
	}

	capacities, err := DiscoverNodeCapacities(ctx, client)
	if err != nil {
		return nil, err
	}

	return &NodeTypeInfoWithCapacity{
		NodeTypeInfo: nodeInfo,
		Capacities:   capacities,
	}, nil
}

// GetQualifiedNodesByType returns nodes of a specific type that meet capacity requirements
func (n *NodeTypeInfoWithCapacity) GetQualifiedNodesByType(nodeType NodeType) []corev1.Node {
	qualifiedNames := n.Capacities.GetQualifiedNodesForTest(nodeType)
	qualifiedSet := make(map[string]bool)
	for _, name := range qualifiedNames {
		qualifiedSet[name] = true
	}

	allNodes := n.NodeTypeInfo.GetNodesByType(nodeType)
	var qualified []corev1.Node
	for _, node := range allNodes {
		if qualifiedSet[node.Name] {
			qualified = append(qualified, node)
		}
	}

	return qualified
}

// HasQualifiedNodesForType returns true if there are qualified nodes for the type
func (n *NodeTypeInfoWithCapacity) HasQualifiedNodesForType(nodeType NodeType) bool {
	return len(n.GetQualifiedNodesByType(nodeType)) > 0
}

// GetAvailableQualifiedNodeTypes returns node types that have qualified nodes
func (n *NodeTypeInfoWithCapacity) GetAvailableQualifiedNodeTypes() []NodeType {
	var available []NodeType
	for _, nodeType := range GetAllNodeTypes() {
		if n.HasQualifiedNodesForType(nodeType) {
			available = append(available, nodeType)
		}
	}
	return available
}

// GetAvailableQualifiedNodeTypesForENIMode returns qualified node types for ENI mode
func (n *NodeTypeInfoWithCapacity) GetAvailableQualifiedNodeTypesForENIMode(mode ENIMode) []NodeType {
	targetTypes := GetNodeTypesForENIMode(mode)
	var available []NodeType
	for _, nodeType := range targetTypes {
		if n.HasQualifiedNodesForType(nodeType) {
			available = append(available, nodeType)
		}
	}
	return available
}

// =============================================================================
// Test Iteration Helpers with Capacity Filtering
// =============================================================================

// RunTestForQualifiedNodeTypes runs tests only on nodes that meet capacity requirements
func RunTestForQualifiedNodeTypes(t *testing.T, nodeTypes []NodeType, nodeInfo *NodeTypeInfoWithCapacity, testFunc NodeTypeTestFunc) {
	for _, nodeType := range nodeTypes {
		qualifiedNodes := nodeInfo.GetQualifiedNodesByType(nodeType)
		if len(qualifiedNodes) == 0 {
			disqualified := nodeInfo.Capacities.GetDisqualifiedNodes(nodeType)
			if len(disqualified) > 0 {
				t.Logf("Skipping %s: nodes don't meet capacity requirements:", nodeType)
				for nodeName, reason := range disqualified {
					t.Logf("  - %s: %s", nodeName, reason)
				}
			} else {
				t.Logf("Skipping %s: no nodes of this type available", nodeType)
			}
			continue
		}

		t.Run(string(nodeType), func(t *testing.T) {
			testFunc(t, nodeType)
		})
	}
}

// RunTestForQualifiedENIMode runs tests for all qualified node types in the specified ENI mode
func RunTestForQualifiedENIMode(t *testing.T, mode ENIMode, nodeInfo *NodeTypeInfoWithCapacity, testFunc NodeTypeTestFunc) {
	nodeTypes := GetNodeTypesForENIMode(mode)
	RunTestForQualifiedNodeTypes(t, nodeTypes, nodeInfo, testFunc)
}

// =============================================================================
// Affinity Helpers for Qualified Nodes
// =============================================================================

// GetNodeAffinityForQualifiedNodes returns node affinity that targets only qualified nodes
func GetNodeAffinityForQualifiedNodes(qualifiedNodeNames []string) *corev1.NodeAffinity {
	if len(qualifiedNodeNames) == 0 {
		return nil
	}

	return &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Key:      "kubernetes.io/hostname",
							Operator: corev1.NodeSelectorOpIn,
							Values:   qualifiedNodeNames,
						},
					},
				},
			},
		},
	}
}

// GetPodAffinityForQualifiedNodes creates pod spec with affinity for qualified nodes only
func GetPodAffinityForQualifiedNodes(nodeInfo *NodeTypeInfoWithCapacity, nodeType NodeType) *corev1.Affinity {
	qualifiedNodes := nodeInfo.GetQualifiedNodesByType(nodeType)
	if len(qualifiedNodes) == 0 {
		return nil
	}

	qualifiedNames := make([]string, 0, len(qualifiedNodes))
	for _, node := range qualifiedNodes {
		qualifiedNames = append(qualifiedNames, node.Name)
	}

	return &corev1.Affinity{
		NodeAffinity: GetNodeAffinityForQualifiedNodes(qualifiedNames),
	}
}
