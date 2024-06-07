package eni

import (
	"context"
	"fmt"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/AliyunContainerService/terway/pkg/aliyun/instance"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	"github.com/AliyunContainerService/terway/types/daemon"
)

var _ reconcile.Reconciler = &nodeReconcile{}

type nodeReconcile struct {
	client client.Client
	record record.EventRecorder
}

func (r *nodeReconcile) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	l := log.FromContext(ctx)
	l.Info("Reconcile node")
	node := &networkv1beta1.Node{}
	err := r.client.Get(ctx, request.NamespacedName, node)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if !node.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	eniConfig, err := daemon.ConfigFromConfigMap(ctx, r.client, node.Name)
	if err != nil {
		k8sNode := &corev1.Node{}
		innerErr := r.client.Get(ctx, request.NamespacedName, k8sNode)
		if innerErr == nil {
			r.record.Event(k8sNode, "Warning", "ConfigError", err.Error())
		} else {
			r.record.Event(node, "Warning", "ConfigError", err.Error())
		}
		return reconcile.Result{}, err
	}

	beforeStatus, err := runtime.DefaultUnstructuredConverter.ToUnstructured(node.DeepCopy())
	if err != nil {
		return reconcile.Result{}, err
	}

	if node.Spec.ENISpec == nil {
		// the initial setup
		ipv4 := false
		ipv6 := false
		switch eniConfig.IPStack {
		case "", "ipv4":
			ipv4 = true
		case "dual":
			ipv4, ipv6 = true, true
			if node.Spec.NodeCap.IPv6PerAdapter != node.Spec.NodeCap.IPv4PerAdapter {
				l.Info("unsupported dual stack instance")
				r.record.Eventf(node, "Warning", "ConfigError", "Instance not support k8s dual stack. ipv4 and ipv6 count is not equal.")
				ipv6 = false
			}
		case "ipv6":
			ipv6 = true
		default:
			return reconcile.Result{}, fmt.Errorf("unsupported ip stack %s", eniConfig.IPStack)
		}

		node.Spec.ENISpec = &networkv1beta1.ENISpec{
			EnableIPv4: ipv4,
			EnableIPv6: ipv6,
		}
	}

	vswitchOptions := []string{}
	for k, v := range eniConfig.VSwitches {
		if k == node.Spec.NodeMetadata.ZoneID {
			vswitchOptions = append(vswitchOptions, v...)
		}
	}
	if len(vswitchOptions) == 0 {
		// if user forget to set vsw , we still rely on metadata to get the actual one
		vswitchOptions = append(vswitchOptions, instance.GetInstanceMeta().VSwitchID)
	}

	// below fields allows to change

	// nb(l1b0k): only enable those feats for new nodes
	// if user change the instanceType , they have to re-add the node.
	if eniConfig.EnableERDMA {
		node.Spec.ENISpec.EnableERDMA = true
		if node.Spec.NodeCap.EriQuantity <= 0 {
			node.Spec.ENISpec.EnableERDMA = false
			l.Info("instance is not support erdma")
		}
		ok := nodecap.GetNodeCapabilities(nodecap.NodeCapabilityERDMA)
		if ok == "" {
			node.Spec.ENISpec.EnableERDMA = false
			l.Info("os is not support erdma")
		}
	}

	if eniConfig.EnableENITrunking {
		node.Spec.ENISpec.EnableTrunk = true
		if node.Spec.NodeCap.TotalAdapters-node.Spec.NodeCap.Adapters <= 0 {
			node.Spec.ENISpec.EnableTrunk = false
			l.Info("instance is not support trunk")
		}
	}

	policy := networkv1beta1.VSwitchSelectionPolicyRandom
	switch eniConfig.VSwitchSelectionPolicy {
	case "ordered":
		// keep the previous behave
		policy = networkv1beta1.VSwitchSelectionPolicyMost
	}

	node.Spec.ENISpec.VSwitchOptions = vswitchOptions
	node.Spec.ENISpec.VSwitchSelectPolicy = policy
	node.Spec.ENISpec.SecurityGroupIDs = eniConfig.GetSecurityGroups()
	node.Spec.ENISpec.Tag = eniConfig.ENITags
	node.Spec.ENISpec.TagFilter = eniConfig.ENITagFilter
	node.Spec.ENISpec.ResourceGroupID = eniConfig.ResourceGroupID

	node.Spec.Flavor = nil

	secondary := node.Spec.NodeCap.Adapters - 1
	if eniConfig.EnableENITrunking {
		node.Spec.Flavor = append(node.Spec.Flavor, networkv1beta1.Flavor{
			NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
			NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
			Count:                       1,
		})
		secondary--

		// node.Spec.ENISpec.EnableTrunk = eniConfig.EnableTrunk
	}
	if eniConfig.EnableERDMA {
		node.Spec.Flavor = append(node.Spec.Flavor, networkv1beta1.Flavor{
			NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
			NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeHighPerformance,
			Count:                       1,
		})
		secondary--
	}
	node.Spec.Flavor = append(node.Spec.Flavor, networkv1beta1.Flavor{
		NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
		NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
		Count:                       secondary,
	})

	node.Spec.Pool = &networkv1beta1.PoolSpec{
		MaxPoolSize: eniConfig.MaxPoolSize,
		MinPoolSize: eniConfig.MinPoolSize,
	}

	afterStatus, err := runtime.DefaultUnstructuredConverter.ToUnstructured(node.DeepCopy())
	if err != nil {
		return reconcile.Result{}, err
	}

	if !reflect.DeepEqual(beforeStatus, afterStatus) {
		err = r.client.Update(ctx, node)
		l.Info("update node spec")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *nodeReconcile) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkv1beta1.Node{}).
		Complete(r)
}
