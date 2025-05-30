package common

import (
	"context"
	"fmt"
	"time"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/types"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CreateOption struct {
	Trunk            bool
	TrafficMode      string
	VSwitchID        string
	SecurityGroupIDs []string
	ResourceGroupID  string
	RDMAQPCount      int
	IPCount          int
	IPv6Count        int
	Tags             map[string]string

	PodName      string
	PodNamespace string
}

func FromPodENI(podENI *v1beta1.PodENI) []*v1beta1.NetworkInterface {
	var result []*v1beta1.NetworkInterface

	for _, alloc := range podENI.Spec.Allocations {
		eniID := alloc.ENI.ID
		if eniID == "" {
			continue
		}

		// create it
		networkInterface := &v1beta1.NetworkInterface{
			ObjectMeta: metav1.ObjectMeta{
				Name:   eniID,
				Labels: map[string]string{},
			},
		}

		if podENI.Labels[types.ENIRelatedNodeName] != "" {
			networkInterface.Labels[types.ENIRelatedNodeName] = podENI.Labels[types.ENIRelatedNodeName]
		}

		networkInterface.Spec.ENI = alloc.ENI
		networkInterface.Spec.IPv4 = alloc.IPv4
		networkInterface.Spec.IPv6 = alloc.IPv6
		// leave cidr to empty
		networkInterface.Spec.ExtraConfig = alloc.ExtraConfig

		networkInterface.Spec.ManagePolicy = v1beta1.ManagePolicy{
			Cache:     false,
			UnManaged: false,
		}

		if _, ok := podENI.Annotations[types.ENIAllocFromPool]; ok {
			networkInterface.Spec.ManagePolicy.Cache = true
		}

		networkInterface.Spec.PodENIRef = &corev1.ObjectReference{
			Kind:      "Pod",
			Name:      podENI.Name,
			Namespace: podENI.Namespace,
		}

		networkInterface.Status.Phase = podENI.Status.Phase

		networkInterface.Status.NodeName = podENI.Labels[types.ENIRelatedNodeName]
		networkInterface.Status.InstanceID = podENI.Status.InstanceID
		networkInterface.Status.TrunkENIID = podENI.Status.TrunkENIID
		networkInterface.Status.ENIInfo = podENI.Status.ENIInfos[eniID]
		// CardIndex is not stored
	}
	return result
}

func ToNetworkInterfaceCR(eni *aliyunClient.NetworkInterface) *v1beta1.NetworkInterface {
	networkInterface := &v1beta1.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Name: eni.NetworkInterfaceID,
		},
		Spec: v1beta1.NetworkInterfaceSpec{
			ENI: v1beta1.ENI{
				ID:               eni.NetworkInterfaceID,
				MAC:              eni.MacAddress,
				VPCID:            eni.VPCID,
				Zone:             eni.ZoneID,
				VSwitchID:        eni.VSwitchID,
				ResourceGroupID:  eni.ResourceGroupID,
				SecurityGroupIDs: eni.SecurityGroupIDs,
			},
			IPv4: eni.PrivateIPAddress,
			IPv6: func() string {
				for _, ip := range eni.IPv6Set {
					return ip.IPAddress
				}
				return ""
			}(),
			// will not used
			//IPv4CIDR:
			//IPv6CIDR:
			ExtraConfig: map[string]string{},
		},
	}
	return networkInterface
}

type AttachOption struct {
	InstanceID         string
	NetworkInterfaceID string
	TrunkENIID         string
	NetworkCardIndex   *int
	NodeName           string
}

func Attach(ctx context.Context, c client.Client, option *AttachOption) error {

	if option.InstanceID == "" {
		return fmt.Errorf("instance id is empty")
	}
	if option.NetworkInterfaceID == "" {
		return fmt.Errorf("network interface id is empty")
	}
	if option.NodeName == "" {
		return fmt.Errorf("node name is empty")
	}

	networkInterface := &v1beta1.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{},
	}
	err := c.Get(ctx, k8stypes.NamespacedName{
		Name: option.NetworkInterfaceID,
	}, networkInterface)
	if err != nil {
		return err
	}

	switch networkInterface.Status.Phase {
	case v1beta1.ENIPhaseInitial, v1beta1.ENIPhaseUnbind:
	case v1beta1.ENIPhaseBind, v1beta1.ENIPhaseBinding:
		return nil
	case v1beta1.ENIPhaseDetaching, v1beta1.ENIPhaseDeleting:
		return fmt.Errorf("eni cr phase %s ", networkInterface.Status.Phase)
	}

	// update to binding
	networkInterface.Status.Phase = v1beta1.ENIPhaseBinding
	networkInterface.Status.InstanceID = option.InstanceID
	networkInterface.Status.TrunkENIID = option.TrunkENIID
	networkInterface.Status.NodeName = option.NodeName
	networkInterface.Status.CardIndex = option.NetworkCardIndex

	err = c.Status().Update(ctx, networkInterface)
	if err != nil {
		return fmt.Errorf("failed to update network interface status: %w", err)
	}

	return nil
}

type DescribeOption struct {
	NetworkInterfaceID string
	IgnoreNotExist     bool

	ExpectPhase *v1beta1.Phase
	BackOff     wait.Backoff
}

func WaitStatus(ctx context.Context, c client.Client, option *DescribeOption) (*v1beta1.NetworkInterface, error) {
	networkInterface := &v1beta1.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Name: option.NetworkInterfaceID,
		},
	}
	if option.BackOff.Steps == 0 {
		option.BackOff.Steps = 1
	}
	var innerErr error
	var notFound bool
	err := wait.ExponentialBackoffWithContext(ctx, option.BackOff, func(ctx context.Context) (done bool, err error) {
		innerErr = c.Get(ctx, client.ObjectKeyFromObject(networkInterface), networkInterface)
		if innerErr != nil {
			if k8sErr.IsNotFound(innerErr) && option.IgnoreNotExist {
				notFound = true
				return true, nil
			}
			return false, nil
		}

		if option.ExpectPhase != nil &&
			*option.ExpectPhase != networkInterface.Status.Phase {
			innerErr = fmt.Errorf("eni cr phase %s not match %s", networkInterface.Status.Phase, *option.ExpectPhase)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		if innerErr != nil {
			return nil, innerErr
		}
		return nil, err
	}
	if notFound {
		return nil, nil
	}

	return networkInterface, nil
}

type DetachOption struct {
	NetworkInterfaceID string
	IgnoreCache        bool
}

func Detach(ctx context.Context, c client.Client, option *DetachOption) error {
	networkInterface := &v1beta1.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Name: option.NetworkInterfaceID,
		},
	}
	err := c.Get(ctx, client.ObjectKeyFromObject(networkInterface), networkInterface)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return nil
		}
		return err
	}

	if networkInterface.Spec.ManagePolicy.UnManaged {
		return nil
	}
	if !option.IgnoreCache && networkInterface.Spec.ManagePolicy.Cache {
		return nil
	}

	switch networkInterface.Status.Phase {
	case v1beta1.ENIPhaseUnbind, v1beta1.ENIPhaseDetaching:
		return nil
	case v1beta1.ENIPhaseInitial, v1beta1.ENIPhaseBind:
	case v1beta1.ENIPhaseBinding, v1beta1.ENIPhaseDeleting:
		return fmt.Errorf("eni cr phase %s ", networkInterface.Status.Phase)
	}

	networkInterface.Status.Phase = v1beta1.ENIPhaseDetaching

	err = c.Status().Update(ctx, networkInterface)
	if err != nil {
		return fmt.Errorf("failed to update network interface status to %s: %w", v1beta1.ENIPhaseDetaching, err)
	}
	return nil
}

type DeleteOption struct {
	NetworkInterfaceID string
	IgnoreCache        bool

	Obj *v1beta1.NetworkInterface
}

func Delete(ctx context.Context, c client.Client, option *DeleteOption) error {
	var err error
	networkInterface := option.Obj
	if networkInterface == nil {
		networkInterface = &v1beta1.NetworkInterface{
			ObjectMeta: metav1.ObjectMeta{
				Name: option.NetworkInterfaceID,
			},
		}
		err = c.Get(ctx, client.ObjectKeyFromObject(networkInterface), networkInterface)
		if err != nil {
			if k8sErr.IsNotFound(err) {
				return nil
			}
			return err
		}
	}

	if networkInterface.Spec.ManagePolicy.UnManaged {
		return nil
	}
	if !option.IgnoreCache && networkInterface.Spec.ManagePolicy.Cache {
		networkInterface.Spec.PodENIRef = nil
		err = c.Update(ctx, networkInterface)
		if err != nil {
			return fmt.Errorf("failed to update interface status, %w", err)
		}
		return nil
	}

	switch networkInterface.Status.Phase {
	case v1beta1.ENIPhaseDeleting:
		return nil
	}

	networkInterface.Status.Phase = v1beta1.ENIPhaseDeleting

	err = c.Status().Update(ctx, networkInterface)
	if err != nil {
		return fmt.Errorf("failed to update network interface status to %s: %w", v1beta1.ENIPhaseDeleting, err)
	}
	return nil
}

func WaitCreated[T client.Object](ctx context.Context, c client.Client, obj T, namespace, name string) {
	_ = wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 2*time.Second, true, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, k8stypes.NamespacedName{
			Namespace: namespace,
			Name:      name,
		}, obj)
		if err != nil {
			return false, nil
		}
		return true, nil
	})
}

func WaitDeleted[T client.Object](ctx context.Context, c client.Client, obj T, namespace, name string) {
	_ = wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 2*time.Second, true, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, k8stypes.NamespacedName{
			Namespace: namespace,
			Name:      name,
		}, obj)
		if err != nil {
			if k8sErr.IsNotFound(err) {
				return true, nil
			}
		}
		return false, nil
	})
}

func WaitRVChanged[T client.Object](ctx context.Context, c client.Client, obj T, namespace, name string, currentRV string) error {
	err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 2*time.Second, true, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, k8stypes.NamespacedName{
			Namespace: namespace,
			Name:      name,
		}, obj)
		if err != nil {
			return false, nil
		}
		return obj.GetResourceVersion() != currentRV, nil
	})
	return err
}
