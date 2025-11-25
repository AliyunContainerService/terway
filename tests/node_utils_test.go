//go:build e2e

package tests

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient"
)

// NodeType represents the type of node
type NodeType string

const (
	NodeTypeECSSharedENI        NodeType = "ecs-shared-eni"
	NodeTypeECSExclusiveENI     NodeType = "ecs-exclusive-eni"
	NodeTypeLingjunSharedENI    NodeType = "lingjun-shared-eni"
	NodeTypeLingjunExclusiveENI NodeType = "lingjun-exclusive-eni"
)

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
