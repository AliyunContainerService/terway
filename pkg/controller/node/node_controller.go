package node

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	eni_pool "github.com/AliyunContainerService/terway/pkg/controller/pool"
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"
	"github.com/AliyunContainerService/terway/types/daemon"
	"k8s.io/client-go/util/retry"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const controllerName = "node"

func init() {
	register.Add(controllerName, func(mgr manager.Manager, ctrlCtx *register.ControllerCtx) error {
		_, err := aliyun.GetLimit(ctrlCtx.AliyunClient, "")
		if err != nil {
			return err
		}

		c, err := controller.NewUnmanaged(controllerName, mgr, controller.Options{
			Reconciler:              NewReconcileNode(mgr, ctrlCtx.AliyunClient, ctrlCtx.VSwitchPool),
			MaxConcurrentReconciles: ctrlCtx.Config.NodeMaxConcurrent,
		})
		if err != nil {
			return err
		}

		w := &Wrapper{ctrl: c, client: mgr.GetClient()}
		err = mgr.Add(w)
		if err != nil {
			return err
		}

		return c.Watch(
			source.Kind(mgr.GetCache(), &corev1.Node{}),
			&handler.EnqueueRequestForObject{},
			&predicate.ResourceVersionChangedPredicate{},
			&predicateForNodeEvent{},
		)
	}, false)
}

type Wrapper struct {
	ctrl   controller.Controller
	client client.Client
}

// Start the controller
func (w *Wrapper) Start(ctx context.Context) error {
	nodes := &corev1.NodeList{}

	err := retry.OnError(retry.DefaultBackoff, func(err error) bool {
		return true
	}, func() error {
		return w.client.List(ctx, nodes)
	})
	if err != nil {
		return fmt.Errorf("failed to list nodes, %w", err)
	}

	for _, node := range nodes.Items {
		nodePool.Store(node.Name, &Client{})
	}

	err = w.ctrl.Start(ctx)
	if err != nil {
		return err
	}

	<-ctx.Done()
	return nil
}

// ReconcilePod implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileNode{}

// ReconcileNode reconciles node
type ReconcileNode struct {
	client client.Client
	aliyun register.Interface
	swPool *vswitch.SwitchPool

	//record event recorder
	record record.EventRecorder
}

func NewReconcileNode(mgr manager.Manager, aliyunClient register.Interface, swPool *vswitch.SwitchPool) *ReconcileNode {
	r := &ReconcileNode{
		client: mgr.GetClient(),
		record: mgr.GetEventRecorderFor("Node"),
		aliyun: aliyunClient,
		swPool: swPool,
	}
	return r
}

func (m *ReconcileNode) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	l := log.FromContext(ctx)
	l.V(5).Info("Reconcile")

	in := &corev1.Node{}
	err := m.client.Get(ctx, request.NamespacedName, in)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return m.delete(ctx, request)
		}
		return reconcile.Result{}, err
	}

	// for node is deleting we will wait it terminated
	if !in.DeletionTimestamp.IsZero() {
		return reconcile.Result{RequeueAfter: 2 * time.Second}, nil
	}

	return m.createOrUpdate(ctx, in)
}

func (m *ReconcileNode) createOrUpdate(ctx context.Context, node *corev1.Node) (reconcile.Result, error) {
	nodeInfo, err := common.NewNodeInfo(node)
	if err != nil {
		return reconcile.Result{}, err
	}

	if *controlplane.GetConfig().EnableTrunk && nodeInfo.TrunkENIID == "" {
		return m.ensureTrunkENI(ctx, node)
	}

	if controlplane.GetConfig().EnableENIPool {
		needInit := false
		v, ok := nodePool.Load(node.Name)
		if !ok {
			needInit = true
		} else {
			if _, err = v.(*Client).GetClient(); err != nil {
				needInit = true
			}
		}
		if needInit {
			err = m.initENIManagerForNode(ctx, node, nodeInfo)
			if err != nil {
				return reconcile.Result{}, err
			}
		}
	}
	if controlplane.GetConfig().EnableDevicePlugin {
		return reconcile.Result{}, m.ensureResourceLimit(ctx, node)
	}

	return reconcile.Result{}, nil
}

func (m *ReconcileNode) delete(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	v, ok := nodePool.LoadAndDelete(request.Name)
	if ok {
		mgr, err := v.(*Client).GetClient()
		if err == nil {
			mgr.Stop()
		}
	}
	return reconcile.Result{}, nil
}

func (m *ReconcileNode) ensureTrunkENI(ctx context.Context, node *corev1.Node) (reconcile.Result, error) {
	l := log.FromContext(ctx)
	nodeInfo, err := common.NewNodeInfo(node)
	if err != nil {
		return reconcile.Result{}, err
	}
	tags := map[string]string{
		types.TagKeyClusterID:               controlplane.GetConfig().ClusterID,
		types.TagK8SNodeName:                node.Name,
		"node-uid":                          string(node.UID),
		types.NetworkInterfaceTagCreatorKey: types.TagTerwayController,
	}

	enis, err := m.aliyun.DescribeNetworkInterface(ctx, "", nil, "", aliyunClient.ENITypeTrunk, "", tags)
	if err != nil {
		return reconcile.Result{}, err
	}

	var trunkENI *aliyunClient.NetworkInterface
	if len(enis) == 0 {
		l.Info("no eni found, will create trunk eni")

		// create and attach eni
		eniConfig, err := daemon.ConfigFromConfigMap(ctx, m.client, node.Name)
		if err != nil {
			return reconcile.Result{}, err
		}

		vsw, err := m.swPool.GetOne(ctx, m.aliyun, node.Labels[corev1.LabelTopologyZone], eniConfig.GetVSwitchIDs())
		if err != nil {
			return reconcile.Result{}, err
		}

		resp, err := m.aliyun.CreateNetworkInterface(ctx, true, vsw.ID, eniConfig.GetSecurityGroups(), "", 1, 0, tags)
		if err != nil {
			return reconcile.Result{}, err
		}
		trunkENI = resp
	} else {
		trunkENI = enis[0]
	}
	if trunkENI.Status != aliyunClient.ENIStatusInUse {
		l.WithValues("eni", trunkENI.NetworkInterfaceID, "instance-id", nodeInfo.InstanceID).Info("attach eni to node")

		err = m.aliyun.AttachNetworkInterface(ctx, trunkENI.NetworkInterfaceID, nodeInfo.InstanceID, "")
		return reconcile.Result{RequeueAfter: 2 * time.Second}, err
	}

	update := node.DeepCopy()
	update.Annotations[types.TrunkOn] = trunkENI.NetworkInterfaceID
	l.Info("patch node")

	return reconcile.Result{}, m.client.Patch(ctx, update, client.MergeFrom(node))
}

func (m *ReconcileNode) ensureResourceLimit(ctx context.Context, node *corev1.Node) error {
	// node level config
	// 1. get node level config
	// 2. update quota
	instanceType := node.Labels[corev1.LabelInstanceTypeStable]
	if instanceType == "" {
		return fmt.Errorf("get label %s is empty", corev1.LabelInstanceTypeStable)
	}
	l, err := aliyun.GetLimit(m.aliyun, instanceType)
	if err != nil {
		return err
	}
	update := node.DeepCopy()

	if *controlplane.GetConfig().EnableTrunk {
		// TODO respect ENICapPolicy in eni_config
		num := resource.NewQuantity(int64(l.MaxMemberAdapterLimit), resource.DecimalSI)
		update.Status.Allocatable["aliyun/member-eni"] = *num
		update.Status.Capacity["aliyun/member-eni"] = *num
	} else {
		num := resource.NewQuantity(int64(l.MemberAdapterLimit), resource.DecimalSI)
		update.Status.Allocatable["aliyun/eni"] = *num
		update.Status.Capacity["aliyun/eni"] = *num
	}
	if reflect.DeepEqual(node.Status, update.Status) {
		return nil
	}

	return m.client.Status().Patch(ctx, update, client.MergeFrom(node))
}

func (m *ReconcileNode) initENIManagerForNode(ctx context.Context, node *corev1.Node, nodeInfo *common.NodeInfo) error {
	l := log.FromContext(ctx)

	instanceType := node.Labels[corev1.LabelInstanceTypeStable]
	if instanceType == "" {
		return fmt.Errorf("get label %s is empty", corev1.LabelInstanceTypeStable)
	}
	limit, err := aliyun.GetLimit(m.aliyun, instanceType)
	if err != nil {
		return err
	}

	eniConfig, err := daemon.ConfigFromConfigMap(ctx, m.client, node.Name)
	if err != nil {
		return err
	}

	all, err := enisFromAPI(ctx, m.aliyun, nodeInfo.InstanceID)
	if err != nil {
		return err
	}
	inUse, err := podENIsByNode(ctx, m.client, nodeInfo.InstanceID)
	if err != nil {
		return err
	}
	for k, v := range inUse {
		all[k] = v
	}
	for _, v := range all {
		if v.GetStatus() == "" {
			v.SetStatus(eni_pool.StatusIdle)
		}
	}

	maxENI := limit.Adapters
	if *controlplane.GetConfig().EnableTrunk {
		maxENI = limit.TotalAdapters
	}

	ipv6Enable := false
	switch controlplane.GetConfig().IPStack {
	case "ipv6", "dual":
		ipv6Enable = true
	}

	maxIdle := eniConfig.MaxPoolSize
	minIdle := eniConfig.MinPoolSize
	if minIdle > maxIdle {
		minIdle = 0
	}
	if maxIdle > maxENI {
		maxIdle = maxENI
	}
	nodeENIMgr := eni_pool.NewManager(&eni_pool.Config{
		IPv4Enable:       true,
		IPv6Enable:       ipv6Enable,
		NodeName:         node.Name,
		InstanceID:       nodeInfo.InstanceID,
		ZoneID:           nodeInfo.ZoneID,
		TrunkENIID:       nodeInfo.TrunkENIID,
		VSwitchIDs:       eniConfig.GetVSwitchIDs(),
		SecurityGroupIDs: eniConfig.GetSecurityGroups(),
		ENITags: map[string]string{
			types.TagKeyClusterID:               controlplane.GetConfig().ClusterID,
			types.NetworkInterfaceTagCreatorKey: types.TagTerwayController,
			types.TagENIAllocPolicy:             "pool",
			types.TagK8SNodeName:                node.Name,
		},
		MaxENI:  maxENI,
		MaxIdle: maxIdle,
		MinIdle: minIdle,
	}, all, m.swPool, m.aliyun)

	l.Info("add node to pool", "maxENI", maxENI, "node", node.Name, "preveni", len(all))

	v, ok := nodePool.LoadOrStore(node.Name, &Client{
		client: nodeENIMgr,
	})
	if !ok {
		go func() {
			nodeENIMgr.Run()
		}()

		return nil
	}

	// for old one
	nodeClient := v.(*Client)
	if _, err = nodeClient.GetClient(); err == nil {
		// already working
		return nil
	}
	// update old one
	nodeClient.SetClient(nodeENIMgr)
	go func() {
		nodeENIMgr.Run()
	}()
	return nil
}

func enisFromAPI(ctx context.Context, aliyun register.Interface, instanceID string) (map[string]*eni_pool.Allocation, error) {
	var networkInterfaces []*aliyunClient.NetworkInterface
	var err error

	if *controlplane.GetConfig().EnableTrunk {
		networkInterfaces, err = aliyun.DescribeNetworkInterface(ctx, controlplane.GetConfig().VPCID, nil, "", aliyunClient.ENITypeMember, "", nil)
		if err != nil {
			return nil, err
		}
	}

	attachedENIs, err := aliyun.DescribeNetworkInterface(ctx, "", nil, instanceID, "", "", nil)
	if err != nil {
		return nil, err
	}
	networkInterfaces = append(networkInterfaces, attachedENIs...)

	all := make(map[string]*eni_pool.Allocation)

	for _, networkInterface := range networkInterfaces {
		// ignore eni not related to this instance
		if networkInterface.InstanceID != instanceID {
			continue
		}

		switch networkInterface.Status {
		case aliyunClient.ENIStatusAvailable, aliyunClient.ENIStatusInUse:
		default:
			return nil, fmt.Errorf("eni is processing, %s", networkInterface.Status)
		}

		alloc := &eni_pool.Allocation{
			NetworkInterface: networkInterface,
		}

		// 1. ignore eni not created by control plane
		if !eniFilter(networkInterface, map[string]string{
			types.TagKeyClusterID:               controlplane.GetConfig().ClusterID,
			types.NetworkInterfaceTagCreatorKey: types.TagTerwayController,
		}) {
			alloc.Status = eni_pool.StatusUnManaged
		} else {
			// 2. ignore Trunk type eni
			if networkInterface.Type == "Trunk" {
				alloc.Status = eni_pool.StatusUnManaged
			}
			if eniFilter(networkInterface, map[string]string{
				types.TagENIAllocPolicy: "pool",
			}) {
				alloc.AllocType = eni_pool.AllocPolicyPreferPool
			} else {
				alloc.AllocType = eni_pool.AllocPolicyDirect
			}
		}
		all[networkInterface.NetworkInterfaceID] = alloc
	}

	return all, nil
}

func podENIsByNode(ctx context.Context, c client.Client, instanceID string) (map[string]*eni_pool.Allocation, error) {
	podENIs := &v1beta1.PodENIList{}

	err := c.List(ctx, podENIs)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*eni_pool.Allocation)

	for _, podENI := range podENIs.Items {
		switch podENI.Status.Phase {
		case v1beta1.ENIPhaseBind, v1beta1.ENIPhaseBinding, v1beta1.ENIPhaseDetaching:
		default:
			continue
		}
		if podENI.Status.InstanceID != instanceID {
			continue
		}
		allocType := eni_pool.AllocPolicyDirect
		if _, ok := podENI.Annotations[types.ENIAllocFromPool]; ok {
			allocType = eni_pool.AllocPolicyPreferPool
		}
		for _, networkInterface := range fromPodENI(&podENI) {
			alloc := &eni_pool.Allocation{
				NetworkInterface: networkInterface,
				Status:           eni_pool.StatusInUse,
				AllocType:        allocType,
			}
			result[alloc.GetNetworkInterface().NetworkInterfaceID] = alloc
		}
	}
	return result, nil
}

// not every field is required
func fromPodENI(in *v1beta1.PodENI) []*aliyunClient.NetworkInterface {
	var result []*aliyunClient.NetworkInterface

	for _, podENI := range in.Spec.Allocations {
		var v6Set []ecs.Ipv6Set
		if podENI.IPv6 != "" {
			v6Set = append(v6Set, ecs.Ipv6Set{Ipv6Address: podENI.IPv6})
		}

		networkInterface := &aliyunClient.NetworkInterface{
			MacAddress:              podENI.ENI.MAC,
			NetworkInterfaceID:      podENI.ENI.ID,
			VSwitchID:               podENI.ENI.VSwitchID,
			PrivateIPAddress:        podENI.IPv4,
			ZoneID:                  in.Spec.Zone,
			SecurityGroupIDs:        podENI.ENI.SecurityGroupIDs,
			IPv6Set:                 v6Set,
			InstanceID:              in.Status.InstanceID,
			TrunkNetworkInterfaceID: in.Status.TrunkENIID,
			DeviceIndex:             0,
		}
		result = append(result, networkInterface)
		eniInfo, ok := in.Status.ENIInfos[podENI.ENI.ID]
		if !ok {
			continue
		}
		networkInterface.Status = string(eniInfo.Status)
		networkInterface.DeviceIndex = eniInfo.Vid
		networkInterface.Type = string(eniInfo.Type)
	}
	return result
}

// eniFilter will compare eni tags with filter, if all filter match return true
func eniFilter(eni *aliyunClient.NetworkInterface, filter map[string]string) bool {
	for k, v := range filter {
		found := false
		for _, tag := range eni.Tags {
			if tag.TagKey != k {
				continue
			}
			if tag.TagValue != v {
				return false
			}
			found = true
			break
		}
		if !found {
			return false
		}
	}
	return true
}
