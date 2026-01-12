package eni

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/AliyunContainerService/terway/deviceplugin"
	"github.com/AliyunContainerService/terway/pkg/aliyun/instance"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

var _ reconcile.Reconciler = &nodeReconcile{}

type nodeReconcile struct {
	client client.Client
	record record.EventRecorder

	once     sync.Once
	nodeName string
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

	k8sNode := &corev1.Node{}
	err = r.client.Get(ctx, request.NamespacedName, k8sNode)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if utils.ISLingJunNode(k8sNode.Labels) {
		return r.handleEFLO(ctx, k8sNode, node)
	}

	eniConfig, err := daemon.ConfigFromConfigMap(ctx, r.client, node.Name)
	if err != nil {
		r.record.Event(k8sNode, "Warning", types.EventConfigError, err.Error())
		return reconcile.Result{}, err
	}

	eniConfig.Populate()

	beforeStatus, err := runtime.DefaultUnstructuredConverter.ToUnstructured(node.DeepCopy())
	if err != nil {
		return reconcile.Result{}, err
	}

	node.Spec.ENISpec = nil

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
			r.record.Eventf(node, "Warning", types.EventConfigError, "Instance not support k8s dual stack. ipv4 and ipv6 count is not equal.")
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

	vswitchOptions := []string{}
	for k, v := range eniConfig.VSwitches {
		if k == node.Spec.NodeMetadata.ZoneID {
			vswitchOptions = append(vswitchOptions, v...)
		}
	}
	if len(vswitchOptions) == 0 {
		// if user forget to set vsw , we still rely on metadata to get the actual one

		switchID, err := instance.GetInstanceMeta().GetVSwitchID()
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to get vsw from metadata, %w", err)
		}
		vswitchOptions = append(vswitchOptions, switchID)
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
		if node.Spec.NodeCap.MemberAdapterLimit <= 0 {
			node.Spec.ENISpec.EnableTrunk = false
			l.Info("instance is not support trunk")
		}
		if node.Spec.ENISpec.EnableTrunk && types.NodeExclusiveENIMode(node.Labels) == types.ExclusiveENIOnly {
			node.Spec.ENISpec.EnableTrunk = false
			l.Info("instance is at exclusive eni mode, trunk is disabled")
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
	if node.Spec.ENISpec.EnableTrunk && secondary > 0 {
		node.Spec.Flavor = append(node.Spec.Flavor, networkv1beta1.Flavor{
			NetworkInterfaceType:        networkv1beta1.ENITypeTrunk,
			NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
			Count:                       1,
		})
		secondary--
	}
	if node.Spec.ENISpec.EnableERDMA && secondary > 0 {
		node.Spec.Flavor = append(node.Spec.Flavor, networkv1beta1.Flavor{
			NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
			NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeHighPerformance,
			Count:                       1,
		})
		secondary--
		r.runERDMADevicePlugin(node.Spec.NodeCap.EriQuantity * node.Spec.NodeCap.IPv4PerAdapter)
	}
	node.Spec.Flavor = append(node.Spec.Flavor, networkv1beta1.Flavor{
		NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
		NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
		Count:                       secondary,
	})

	node.Spec.Pool = &networkv1beta1.PoolSpec{
		MaxPoolSize:    eniConfig.MaxPoolSize,
		MinPoolSize:    eniConfig.MinPoolSize,
		PoolSyncPeriod: eniConfig.IPPoolSyncPeriod,
	}

	if eniConfig.IPWarmUpSize != nil {
		node.Spec.Pool.WarmUpSize = *eniConfig.IPWarmUpSize
	}

	if eniConfig.IdleIPReclaimAfter != nil {
		reclaim := &networkv1beta1.IPReclaimPolicy{}

		duration, err := time.ParseDuration(*eniConfig.IdleIPReclaimAfter)
		if err != nil {
			return reconcile.Result{}, err
		}
		reclaim.After = &metav1.Duration{Duration: duration}

		if *eniConfig.IdleIPReclaimInterval != "" {
			interval, err := time.ParseDuration(*eniConfig.IdleIPReclaimInterval)
			if err != nil {
				return reconcile.Result{}, err
			}
			reclaim.Interval = &metav1.Duration{Duration: interval}
		}
		reclaim.BatchSize = eniConfig.IdleIPReclaimBatchSize

		if eniConfig.IdleIPReclaimJitterFactor != nil {
			reclaim.JitterFactor = *eniConfig.IdleIPReclaimJitterFactor
		}

		node.Spec.Pool.Reclaim = reclaim
	}

	node.Spec.Datapath = &networkv1beta1.Datapath{
		Type: networkv1beta1.DatapathType(getDatapath()),
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

func (r *nodeReconcile) handleEFLO(ctx context.Context, k8sNode *corev1.Node, node *networkv1beta1.Node) (reconcile.Result, error) {
	l := log.FromContext(ctx)

	eniConfig, err := daemon.ConfigFromConfigMap(ctx, r.client, node.Name)
	if err != nil {
		r.record.Event(k8sNode, "Warning", types.EventConfigError, err.Error())
		return reconcile.Result{}, err
	}

	beforeStatus, err := runtime.DefaultUnstructuredConverter.ToUnstructured(node.DeepCopy())
	if err != nil {
		return reconcile.Result{}, err
	}
	node.Spec.ENISpec = nil

	node.Spec.ENISpec = &networkv1beta1.ENISpec{
		EnableIPv4: true,
		EnableIPv6: false,
	}

	if node.Labels == nil {
		node.Labels = map[string]string{}
	}

	vswitchOptions := []string{}
	for k, v := range eniConfig.VSwitches {
		if k == node.Spec.NodeMetadata.ZoneID {
			vswitchOptions = append(vswitchOptions, v...)
		}
	}
	if len(vswitchOptions) == 0 {
		err = fmt.Errorf("failed to get vsw for zone %s", node.Spec.NodeMetadata.ZoneID)
		r.record.Event(k8sNode, "Warning", types.EventConfigError, err.Error())
		return reconcile.Result{}, err
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

	if node.Spec.NodeCap.Adapters > 0 {
		node.Spec.Flavor = nil

		if node.Annotations[types.ENOApi] == "hdeni" {
			node.Spec.Flavor = append(node.Spec.Flavor, networkv1beta1.Flavor{
				NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
				NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
				Count:                       node.Spec.NodeCap.Adapters,
			})
		} else {
			node.Spec.Flavor = append(node.Spec.Flavor, networkv1beta1.Flavor{
				NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
				NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
				Count:                       node.Spec.NodeCap.Adapters - 1,
			})
		}
	}

	node.Spec.Pool = &networkv1beta1.PoolSpec{
		MaxPoolSize:    eniConfig.MaxPoolSize,
		MinPoolSize:    eniConfig.MinPoolSize,
		PoolSyncPeriod: eniConfig.IPPoolSyncPeriod,
	}
	if eniConfig.IPWarmUpSize != nil {
		node.Spec.Pool.WarmUpSize = *eniConfig.IPWarmUpSize
	}

	if eniConfig.IdleIPReclaimAfter != nil {
		reclaim := &networkv1beta1.IPReclaimPolicy{}

		duration, err := time.ParseDuration(*eniConfig.IdleIPReclaimAfter)
		if err != nil {
			return reconcile.Result{}, err
		}
		reclaim.After = &metav1.Duration{Duration: duration}

		if *eniConfig.IdleIPReclaimInterval != "" {
			interval, err := time.ParseDuration(*eniConfig.IdleIPReclaimInterval)
			if err != nil {
				return reconcile.Result{}, err
			}
			reclaim.Interval = &metav1.Duration{Duration: interval}
		}
		reclaim.BatchSize = eniConfig.IdleIPReclaimBatchSize

		if eniConfig.IdleIPReclaimJitterFactor != nil {
			reclaim.JitterFactor = *eniConfig.IdleIPReclaimJitterFactor
		}

		node.Spec.Pool.Reclaim = reclaim
	}

	node.Spec.Datapath = &networkv1beta1.Datapath{
		Type: networkv1beta1.DatapathType(getDatapath()),
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

func (r *nodeReconcile) runERDMADevicePlugin(count int) {
	r.once.Do(func() {
		log.Log.Info("start erdma device plugin")
		dp := deviceplugin.NewENIDevicePlugin(count, deviceplugin.ENITypeERDMA)
		go dp.Serve()
	})
}

func getDatapath() string {
	dp := nodecap.GetNodeCapabilities(nodecap.NodeCapabilityDataPath)
	if dp == "" {
		return "veth"
	}
	return dp
}
