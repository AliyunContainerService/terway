//go:build e2e

package tests

import (
	"context"
	"fmt"
	"strings"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/e2e-framework/klient"
)

// NodeType represents the type of node
type NodeType string

const (
	NodeTypeNormal       NodeType = "normal"
	NodeTypeExclusiveENI NodeType = "exclusive-eni"
	NodeTypeLingjun      NodeType = "lingjun"
)

// NodeTypeInfo contains information about available node types in the cluster
type NodeTypeInfo struct {
	NormalNodes       []corev1.Node
	ExclusiveENINodes []corev1.Node
	LingjunNodes      []corev1.Node
	AllNodes          []corev1.Node
}

// HasTrunkNodes checks if the cluster has nodes that support trunk mode
func (n *NodeTypeInfo) HasTrunkNodes() bool {
	// Normal nodes and exclusive ENI nodes support trunk mode
	// Lingjun nodes do NOT support trunk mode
	return len(n.NormalNodes) > 0 || len(n.ExclusiveENINodes) > 0
}

// HasExclusiveENINodes checks if the cluster has exclusive ENI nodes
func (n *NodeTypeInfo) HasExclusiveENINodes() bool {
	return len(n.ExclusiveENINodes) > 0
}

// HasLingjunNodes checks if the cluster has Lingjun nodes
func (n *NodeTypeInfo) HasLingjunNodes() bool {
	return len(n.LingjunNodes) > 0
}

// GetNodesByType returns nodes of a specific type
func (n *NodeTypeInfo) GetNodesByType(nodeType NodeType) []corev1.Node {
	switch nodeType {
	case NodeTypeNormal:
		return n.NormalNodes
	case NodeTypeExclusiveENI:
		return n.ExclusiveENINodes
	case NodeTypeLingjun:
		return n.LingjunNodes
	default:
		return []corev1.Node{}
	}
}

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
		case NodeTypeNormal:
			info.NormalNodes = append(info.NormalNodes, node)
		case NodeTypeExclusiveENI:
			info.ExclusiveENINodes = append(info.ExclusiveENINodes, node)
		case NodeTypeLingjun:
			info.LingjunNodes = append(info.LingjunNodes, node)
		}
	}

	return info, nil
}

// classifyNode determines the type of a node based on its labels and resources
func classifyNode(node corev1.Node) NodeType {
	// Check if it's a Lingjun node first
	if isLingjunNode(node) {
		return NodeTypeLingjun
	}

	// Check if it's an exclusive ENI node
	if isExclusiveENINode(node) {
		return NodeTypeExclusiveENI
	}

	// Default to normal node
	return NodeTypeNormal
}

// isLingjunNode checks if a node is a Lingjun node
func isLingjunNode(node corev1.Node) bool {
	value, exists := node.Labels["alibabacloud.com/lingjun-worker"]
	return exists && value == "true"
}

// isExclusiveENINode checks if a node is configured for exclusive ENI mode
func isExclusiveENINode(node corev1.Node) bool {
	value, exists := node.Labels["k8s.aliyun.com/exclusive-mode-eni-type"]
	return exists && value == "eniOnly"
}

// GetNodeAffinityForType returns node affinity labels for scheduling pods to specific node types
func GetNodeAffinityForType(nodeType NodeType) map[string]string {
	switch nodeType {
	case NodeTypeLingjun:
		return map[string]string{
			"alibabacloud.com/lingjun-worker": "true",
		}
	case NodeTypeExclusiveENI:
		return map[string]string{
			"k8s.aliyun.com/exclusive-mode-eni-type": "eniOnly",
		}
	default:
		return map[string]string{}
	}
}

// GetNodeAffinityExcludeForType returns node affinity exclusion labels for specific node types
func GetNodeAffinityExcludeForType(nodeType NodeType) map[string]string {
	switch nodeType {
	case NodeTypeNormal:
		// For normal nodes, exclude Lingjun and exclusive ENI nodes
		return map[string]string{
			"alibabacloud.com/lingjun-worker":        "true",
			"k8s.aliyun.com/exclusive-mode-eni-type": "eniOnly",
		}
	default:
		return map[string]string{}
	}
}

// CheckNodeSupportsExclusiveENI checks if a node supports exclusive ENI resource
func CheckNodeSupportsExclusiveENI(node corev1.Node) bool {
	// Check if the node has aliyun/eni resource
	r := node.Status.Allocatable.Name("aliyun/eni", resource.DecimalSI)
	return r != nil && !r.IsZero()
}

// CheckNodeSupportsTrunk checks if a node supports trunk mode
func CheckNodeSupportsTrunk(node corev1.Node) bool {
	// Lingjun nodes do not support trunk mode
	if isLingjunNode(node) {
		return false
	}
	// All other nodes support trunk mode
	return true
}

// ShouldExcludeNodeForIdleIPCheck checks if a node should be excluded from idle IP count checks
func ShouldExcludeNodeForIdleIPCheck(node *networkv1beta1.Node) bool {
	// Exclude Lingjun nodes
	if val, ok := node.Labels["alibabacloud.com/lingjun-worker"]; ok && val == "true" {
		return true
	}
	// Exclude exclusive ENI nodes
	if val, ok := node.Labels["k8s.aliyun.com/exclusive-mode-eni-type"]; ok && val == "eniOnly" {
		return true
	}
	return false
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

// GetSupportedNodeTypesForTrunk returns node types that support trunk mode
func (n *NodeTypeInfo) GetSupportedNodeTypesForTrunk() []NodeType {
	var supported []NodeType

	if len(n.NormalNodes) > 0 {
		supported = append(supported, NodeTypeNormal)
	}
	if len(n.ExclusiveENINodes) > 0 {
		supported = append(supported, NodeTypeExclusiveENI)
	}
	// Lingjun nodes do NOT support trunk mode

	return supported
}

// GetSupportedNodeTypesForExclusiveENI returns node types that support exclusive ENI
func (n *NodeTypeInfo) GetSupportedNodeTypesForExclusiveENI() []NodeType {
	var supported []NodeType

	// Check normal nodes for aliyun/eni resource
	for _, node := range n.NormalNodes {
		if CheckNodeSupportsExclusiveENI(node) {
			supported = append(supported, NodeTypeNormal)
			break
		}
	}

	// Check exclusive ENI nodes
	if len(n.ExclusiveENINodes) > 0 {
		supported = append(supported, NodeTypeExclusiveENI)
	}

	// Lingjun nodes support exclusive ENI but don't expose aliyun/eni resource
	if len(n.LingjunNodes) > 0 {
		supported = append(supported, NodeTypeLingjun)
	}

	return supported
}

// GetSupportedNodeTypesForMultiNetwork returns node types that support multi-network
func (n *NodeTypeInfo) GetSupportedNodeTypesForMultiNetwork() []NodeType {
	var supported []NodeType

	// Multi-network does not support Lingjun nodes
	if len(n.NormalNodes) > 0 {
		supported = append(supported, NodeTypeNormal)
	}
	if len(n.ExclusiveENINodes) > 0 {
		supported = append(supported, NodeTypeExclusiveENI)
	}

	return supported
}
