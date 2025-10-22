package testutil

import (
	"context"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Test helpers and utilities
func CreateTestNode(name string) *corev1.Node {
	return &corev1.Node{
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
	}
}

func CreateEFLONode(name string) *corev1.Node {
	node := CreateTestNode(name)
	node.Labels["alibabacloud.com/lingjun-worker"] = "true"
	return node
}

func CreateTestNodeCRD(name string) *networkv1beta1.Node {
	return &networkv1beta1.Node{
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
	}
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
