package testutil

import (
	"context"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"k8s.io/utils/ptr"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// K8sNodeBuilder builds corev1.Node objects for testing
type K8sNodeBuilder struct {
	node *corev1.Node
}

// NewK8sNodeBuilder creates a new K8sNodeBuilder with default values
func NewK8sNodeBuilder(name string) *K8sNodeBuilder {
	return &K8sNodeBuilder{
		node: &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					"topology.kubernetes.io/region":    "cn-hangzhou",
					"topology.kubernetes.io/zone":      "cn-hangzhou-a",
					"node.kubernetes.io/instance-type": "instanceType",
				},
				Annotations: map[string]string{},
			},
			Spec: corev1.NodeSpec{ProviderID: "cn-hangzhou.i-xxx"},
		},
	}
}

func (b *K8sNodeBuilder) WithRegion(region string) *K8sNodeBuilder {
	b.node.Labels["topology.kubernetes.io/region"] = region
	return b
}

func (b *K8sNodeBuilder) WithZone(zone string) *K8sNodeBuilder {
	b.node.Labels["topology.kubernetes.io/zone"] = zone
	return b
}

func (b *K8sNodeBuilder) WithInstanceType(instanceType string) *K8sNodeBuilder {
	b.node.Labels["node.kubernetes.io/instance-type"] = instanceType
	return b
}

func (b *K8sNodeBuilder) WithProviderID(providerID string) *K8sNodeBuilder {
	b.node.Spec.ProviderID = providerID
	return b
}

func (b *K8sNodeBuilder) WithLabel(key, value string) *K8sNodeBuilder {
	if b.node.Labels == nil {
		b.node.Labels = make(map[string]string)
	}
	b.node.Labels[key] = value
	return b
}

func (b *K8sNodeBuilder) WithAnnotation(key, value string) *K8sNodeBuilder {
	if b.node.Annotations == nil {
		b.node.Annotations = make(map[string]string)
	}
	b.node.Annotations[key] = value
	return b
}

func (b *K8sNodeBuilder) WithExclusiveENIMode() *K8sNodeBuilder {
	return b.WithLabel("k8s.aliyun.com/exclusive-mode-eni-type", "eniOnly")
}

func (b *K8sNodeBuilder) WithEFLO() *K8sNodeBuilder {
	return b.WithLabel("alibabacloud.com/lingjun-worker", "true")
}

func (b *K8sNodeBuilder) Build() *corev1.Node {
	return b.node
}

// NodeCRDBuilder builds networkv1beta1.Node objects for testing
type NodeCRDBuilder struct {
	node *networkv1beta1.Node
}

// NewNodeCRDBuilder creates a new NodeCRDBuilder with default values
func NewNodeCRDBuilder(name string) *NodeCRDBuilder {
	return &NodeCRDBuilder{
		node: &networkv1beta1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Labels:      map[string]string{},
				Annotations: map[string]string{},
			},
			Spec: networkv1beta1.NodeSpec{
				NodeMetadata: networkv1beta1.NodeMetadata{
					RegionID:     "cn-hangzhou",
					InstanceType: "instanceType",
					InstanceID:   "i-xxx",
					ZoneID:       "cn-hangzhou-a",
				},
				ENISpec: &networkv1beta1.ENISpec{
					VSwitchOptions:      []string{"vsw-xxx"},
					SecurityGroupIDs:    []string{"sg-xxx"},
					EnableIPv4:          true,
					VSwitchSelectPolicy: networkv1beta1.VSwitchSelectionPolicyMost,
				},
			},
			Status: networkv1beta1.NodeStatus{
				NetworkInterfaces: make(map[string]*networkv1beta1.Nic),
			},
		},
	}
}

func (b *NodeCRDBuilder) WithNodeMetadata(regionID, instanceType, instanceID, zoneID string) *NodeCRDBuilder {
	b.node.Spec.NodeMetadata = networkv1beta1.NodeMetadata{
		RegionID:     regionID,
		InstanceType: instanceType,
		InstanceID:   instanceID,
		ZoneID:       zoneID,
	}
	return b
}

func (b *NodeCRDBuilder) WithENISpec(vswitch, securityGroup string) *NodeCRDBuilder {
	if b.node.Spec.ENISpec == nil {
		b.node.Spec.ENISpec = &networkv1beta1.ENISpec{}
	}
	b.node.Spec.ENISpec.VSwitchOptions = []string{vswitch}
	b.node.Spec.ENISpec.SecurityGroupIDs = []string{securityGroup}
	b.node.Spec.ENISpec.EnableIPv4 = true
	b.node.Spec.ENISpec.VSwitchSelectPolicy = networkv1beta1.VSwitchSelectionPolicyMost
	return b
}

func (b *NodeCRDBuilder) WithEnableTrunk(enable bool) *NodeCRDBuilder {
	if b.node.Spec.ENISpec == nil {
		b.node.Spec.ENISpec = &networkv1beta1.ENISpec{}
	}
	b.node.Spec.ENISpec.EnableTrunk = enable
	return b
}

func (b *NodeCRDBuilder) WithVSwitchSelectPolicy(policy networkv1beta1.SelectionPolicy) *NodeCRDBuilder {
	if b.node.Spec.ENISpec == nil {
		b.node.Spec.ENISpec = &networkv1beta1.ENISpec{}
	}
	b.node.Spec.ENISpec.VSwitchSelectPolicy = policy
	return b
}

func (b *NodeCRDBuilder) WithFlavor(flavors ...networkv1beta1.Flavor) *NodeCRDBuilder {
	b.node.Spec.Flavor = append(b.node.Spec.Flavor, flavors...)
	return b
}

func (b *NodeCRDBuilder) WithNodeCap(adapters, totalAdapters, ipv4PerAdapter int) *NodeCRDBuilder {
	b.node.Spec.NodeCap.Adapters = adapters
	b.node.Spec.NodeCap.TotalAdapters = totalAdapters
	b.node.Spec.NodeCap.IPv4PerAdapter = ipv4PerAdapter
	return b
}

func (b *NodeCRDBuilder) WithNetworkCardsCount(count int) *NodeCRDBuilder {
	b.node.Spec.NodeCap.NetworkCardsCount = ptr.To(count)
	return b
}

func (b *NodeCRDBuilder) WithLabel(key, value string) *NodeCRDBuilder {
	if b.node.Labels == nil {
		b.node.Labels = make(map[string]string)
	}
	b.node.Labels[key] = value
	return b
}

func (b *NodeCRDBuilder) WithAnnotation(key, value string) *NodeCRDBuilder {
	if b.node.Annotations == nil {
		b.node.Annotations = make(map[string]string)
	}
	b.node.Annotations[key] = value
	return b
}

func (b *NodeCRDBuilder) WithNetworkInterface(eniID string, nic *networkv1beta1.Nic) *NodeCRDBuilder {
	if b.node.Status.NetworkInterfaces == nil {
		b.node.Status.NetworkInterfaces = make(map[string]*networkv1beta1.Nic)
	}
	b.node.Status.NetworkInterfaces[eniID] = nic
	return b
}

func (b *NodeCRDBuilder) WithExclusiveENIMode() *NodeCRDBuilder {
	return b.WithLabel("k8s.aliyun.com/exclusive-mode-eni-type", "eniOnly")
}

func (b *NodeCRDBuilder) WithEFLO() *NodeCRDBuilder {
	return b.WithLabel("alibabacloud.com/lingjun-worker", "true")
}

func (b *NodeCRDBuilder) Build() *networkv1beta1.Node {
	return b.node
}

// PodENIBuilder builds networkv1beta1.PodENI objects for testing
type PodENIBuilder struct {
	podENI *networkv1beta1.PodENI
}

// NewPodENIBuilder creates a new PodENIBuilder
func NewPodENIBuilder(name, namespace string) *PodENIBuilder {
	return &PodENIBuilder{
		podENI: &networkv1beta1.PodENI{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   namespace,
				Labels:      map[string]string{},
				Annotations: map[string]string{},
			},
			Spec:   networkv1beta1.PodENISpec{},
			Status: networkv1beta1.PodENIStatus{},
		},
	}
}

func (b *PodENIBuilder) WithAllocation(allocation networkv1beta1.Allocation) *PodENIBuilder {
	b.podENI.Spec.Allocations = append(b.podENI.Spec.Allocations, allocation)
	return b
}

func (b *PodENIBuilder) WithENI(eniID string, trunk bool) *PodENIBuilder {
	allocation := networkv1beta1.Allocation{
		ENI: networkv1beta1.ENI{
			ID: eniID,
			AttachmentOptions: networkv1beta1.AttachmentOptions{
				Trunk: ptr.To(trunk),
			},
		},
	}
	return b.WithAllocation(allocation)
}

func (b *PodENIBuilder) WithZone(zone string) *PodENIBuilder {
	b.podENI.Spec.Zone = zone
	return b
}

func (b *PodENIBuilder) WithPhase(phase networkv1beta1.Phase) *PodENIBuilder {
	b.podENI.Status.Phase = phase
	return b
}

func (b *PodENIBuilder) WithInstanceID(instanceID string) *PodENIBuilder {
	b.podENI.Status.InstanceID = instanceID
	return b
}

func (b *PodENIBuilder) WithTrunkENIID(trunkID string) *PodENIBuilder {
	b.podENI.Status.TrunkENIID = trunkID
	return b
}

func (b *PodENIBuilder) WithFinalizer(finalizer string) *PodENIBuilder {
	b.podENI.Finalizers = append(b.podENI.Finalizers, finalizer)
	return b
}

func (b *PodENIBuilder) WithLabel(key, value string) *PodENIBuilder {
	if b.podENI.Labels == nil {
		b.podENI.Labels = make(map[string]string)
	}
	b.podENI.Labels[key] = value
	return b
}

func (b *PodENIBuilder) WithAnnotation(key, value string) *PodENIBuilder {
	if b.podENI.Annotations == nil {
		b.podENI.Annotations = make(map[string]string)
	}
	b.podENI.Annotations[key] = value
	return b
}

func (b *PodENIBuilder) Build() *networkv1beta1.PodENI {
	return b.podENI
}

func CreateTestPod(name, namespace, nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
			Containers: []corev1.Container{
				{
					Name:  "pause",
					Image: "pause",
				},
			},
		},
	}
}

// CreateResource updates the status of a resource.
// It first gets a fresh copy of the resource, then deep copies the status, and finally updates it.
func CreateResource(ctx context.Context, cli client.Client, obj client.Object) error {
	// Deep copy the status before updating
	var statusCopy interface{}

	switch v := obj.(type) {
	case *networkv1beta1.PodENI:
		statusCopy = v.Status.DeepCopy()

		err := cli.Create(ctx, obj)
		if err != nil {
			return err
		}

		v.Status = *statusCopy.(*networkv1beta1.PodENIStatus)
		return cli.Status().Update(ctx, v)
	case *networkv1beta1.NetworkInterface:
		statusCopy = v.Status.DeepCopy()

		err := cli.Create(ctx, obj)
		if err != nil {
			return err
		}

		v.Status = *statusCopy.(*networkv1beta1.NetworkInterfaceStatus)
		return cli.Status().Update(ctx, v)
	case *networkv1beta1.Node:
		statusCopy = v.Status.DeepCopy()

		err := cli.Create(ctx, obj)
		if err != nil {
			return err
		}

		v.Status = *statusCopy.(*networkv1beta1.NodeStatus)
		return cli.Status().Update(ctx, v)
	case *corev1.Pod:
		statusCopy = v.Status.DeepCopy()

		err := cli.Create(ctx, obj)
		if err != nil {
			return err
		}

		v.Status = *statusCopy.(*corev1.PodStatus)
		return cli.Status().Update(ctx, v)
	case *corev1.Node:
		statusCopy = v.Status.DeepCopy()

		err := cli.Create(ctx, obj)
		if err != nil {
			return err
		}

		v.Status = *statusCopy.(*corev1.NodeStatus)
		return cli.Status().Update(ctx, v)
	case *networkv1beta1.PodNetworking:
		statusCopy = v.Status.DeepCopy()

		err := cli.Create(ctx, obj)
		if err != nil {
			return err
		}

		v.Status = *statusCopy.(*networkv1beta1.PodNetworkingStatus)
		return cli.Status().Update(ctx, v)
	default:
		err := cli.Create(ctx, obj)
		if err != nil {
			return err
		}
		// For other types, we just try to update the status
		return cli.Status().Update(ctx, obj)
	}
}
