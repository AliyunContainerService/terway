package node

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"

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
		c, err := controller.New(controllerName, mgr, controller.Options{
			Reconciler:              NewReconcileNode(mgr, ctrlCtx.AliyunClient, ctrlCtx.VSwitchPool),
			MaxConcurrentReconciles: ctrlCtx.Config.NodeMaxConcurrent,
		})
		if err != nil {
			return err
		}

		return c.Watch(
			&source.Kind{
				Type: &corev1.Node{},
			},
			&handler.EnqueueRequestForObject{},
			&predicate.ResourceVersionChangedPredicate{},
		)
	}, false)
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
	_, err := aliyun.GetLimit(aliyunClient, "")
	if err != nil {
		panic(err)
	}
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
	if *controlplane.GetConfig().EnableTrunk && node.Annotations[types.TrunkOn] == "" {
		return m.ensureTrunkENI(ctx, node)
	}

	return reconcile.Result{}, m.ensureResourceLimit(ctx, node)
}

func (m *ReconcileNode) delete(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
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
		"node-name":                         node.Name,
		"node-uid":                          string(node.UID),
		types.NetworkInterfaceTagCreatorKey: types.TagTerwayController,
	}

	enis, err := m.aliyun.DescribeNetworkInterface(ctx, "", nil, "", aliyunClient.ENITypeTrunk, "", tags)
	if err != nil {
		return reconcile.Result{}, err
	}

	var trunkENI ecs.NetworkInterfaceSet
	if len(enis) == 0 {
		l.Info("no eni found, will create trunk eni")

		// create and attach eni
		eniConfig, err := common.ConfigFromConfigMConfigFromConfigMap(ctx, m.client, node.Name)
		if err != nil {
			return reconcile.Result{}, err
		}

		vsw, err := m.swPool.GetOne(ctx, m.aliyun, node.Labels[corev1.LabelTopologyZone], eniConfig.GetVSwitchIDs())
		if err != nil {
			return reconcile.Result{}, err
		}

		resp, err := m.aliyun.CreateNetworkInterface(ctx, aliyunClient.ENITypeTrunk, vsw.ID, eniConfig.GetSecurityGroups(), 1, 0, tags)
		if err != nil {
			return reconcile.Result{}, err
		}
		trunkENI = ecs.NetworkInterfaceSet{
			Status:             resp.Status,
			NetworkInterfaceId: resp.NetworkInterfaceId,
		}
	} else {
		trunkENI = enis[0]
	}
	if trunkENI.Status != "InUse" {
		l.WithValues("eni", trunkENI.NetworkInterfaceId, "instance-id", nodeInfo.InstanceID).Info("attach eni to node")

		err = m.aliyun.AttachNetworkInterface(ctx, trunkENI.NetworkInterfaceId, nodeInfo.InstanceID, "")
		return reconcile.Result{RequeueAfter: 2 * time.Second}, err
	}

	update := node.DeepCopy()
	update.Annotations[types.TrunkOn] = trunkENI.NetworkInterfaceId
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
