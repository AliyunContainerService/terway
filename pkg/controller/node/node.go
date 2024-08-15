package node

import (
	"context"
	"reflect"
	"strconv"

	"github.com/go-logr/logr"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/controller/multi-ip/node"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"
)

const (
	ControllerName = "node"

	finalizer = "network.alibabacloud.com/node-controller"
)

func init() {
	register.Add(ControllerName, func(mgr manager.Manager, ctrlCtx *register.ControllerCtx) error {
		ctrlCtx.RegisterResource = append(ctrlCtx.RegisterResource, &corev1.Node{}, &networkv1beta1.Node{})

		err := mgr.GetFieldIndexer().IndexField(ctrlCtx.Context, &corev1.Pod{}, "spec.nodeName", func(object client.Object) []string {
			pod := object.(*corev1.Pod)
			return []string{pod.Spec.NodeName}
		})
		if err != nil {
			return err
		}

		return ctrl.NewControllerManagedBy(mgr).
			WithOptions(controller.Options{
				MaxConcurrentReconciles: controlplane.GetConfig().NodeMaxConcurrent,
				LogConstructor: func(request *reconcile.Request) logr.Logger {
					log := mgr.GetLogger()
					if request != nil {
						log = log.WithValues("name", request.Name)
					}
					return log
				},
			}).
			For(&corev1.Node{}, builder.WithPredicates(&predicateForNodeEvent{})).
			Watches(&networkv1beta1.Node{}, &handler.EnqueueRequestForObject{}).Complete(&ReconcileNode{
			client: mgr.GetClient(),
			scheme: mgr.GetScheme(),
			record: mgr.GetEventRecorderFor(ControllerName),
			aliyun: ctrlCtx.AliyunClient,
		})
	}, false)
}

var _ reconcile.Reconciler = &ReconcileNode{}

type ReconcileNode struct {
	client client.Client
	scheme *runtime.Scheme

	aliyun register.Interface
	record record.EventRecorder
}

func (r *ReconcileNode) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	r.notify(ctx, request.Name)

	k8sNode := &corev1.Node{}
	err := r.client.Get(ctx, request.NamespacedName, k8sNode)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if !isECSNode(k8sNode) {
		return reconcile.Result{}, nil
	}
	if !k8sNode.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	node := &networkv1beta1.Node{}
	err = r.client.Get(ctx, request.NamespacedName, node)
	if err != nil {
		if !k8sErr.IsNotFound(err) {
			return reconcile.Result{}, err
		}
		node.Name = request.Name
		err = controllerutil.SetControllerReference(k8sNode, node, r.scheme)
		if err != nil {
			return ctrl.Result{}, err
		}
		_ = controllerutil.AddFinalizer(node, finalizer)
	}
	if !node.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	// create or update the crdNode
	err = r.createOrUpdate(ctx, k8sNode, node)
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileNode) createOrUpdate(ctx context.Context, k8sNode *corev1.Node, node *networkv1beta1.Node) error {
	nodeInfo, err := common.NewNodeInfo(k8sNode)
	if err != nil {
		return err
	}

	if node.Labels == nil {
		node.Labels = make(map[string]string)
	}
	node.Labels["name"] = k8sNode.Name

	if node.Spec.NodeMetadata.InstanceType != nodeInfo.InstanceType ||
		node.Spec.NodeMetadata.InstanceID != nodeInfo.InstanceID {

		node.Spec.NodeMetadata = networkv1beta1.NodeMetadata{
			InstanceType: nodeInfo.InstanceType,
			InstanceID:   nodeInfo.InstanceID,
			ZoneID:       nodeInfo.ZoneID,
			RegionID:     nodeInfo.RegionID,
		}

		limit, err := aliyunClient.LimitProviders["ecs"].GetLimit(r.aliyun, nodeInfo.InstanceType)
		if err != nil {
			return err
		}

		node.Spec.NodeCap = networkv1beta1.NodeCap{
			InstanceBandwidthTx:   limit.InstanceBandwidthTx,
			InstanceBandwidthRx:   limit.InstanceBandwidthRx,
			Adapters:              limit.Adapters,
			EriQuantity:           limit.ERdmaAdapters,
			TotalAdapters:         limit.TotalAdapters,
			IPv6PerAdapter:        limit.IPv6PerAdapter,
			MemberAdapterLimit:    limit.MemberAdapterLimit,
			IPv4PerAdapter:        limit.IPv4PerAdapter,
			MaxMemberAdapterLimit: limit.MaxMemberAdapterLimit,
		}
	}

	update := node.DeepCopy()
	_, err = controllerutil.CreateOrPatch(ctx, r.client, update, func() error {
		update.Status = node.Status
		update.Spec = node.Spec
		update.Labels = node.Labels
		return nil
	})
	if err != nil {
		return err
	}
	err = r.k8sAnno(ctx, k8sNode, node)
	return err
}

func (r *ReconcileNode) notify(ctx context.Context, name string) bool {
	select {
	case node.EventCh <- event.GenericEvent{
		Object: &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}},
	}:
		if logf.FromContext(ctx).V(4).Enabled() {
			logf.FromContext(ctx).Info("notify node event")
		}
	default:
		logf.FromContext(ctx).Info("event chan is full")
		return false
	}
	return true
}

func (r *ReconcileNode) k8sAnno(ctx context.Context, k8sNode *corev1.Node, node *networkv1beta1.Node) error {
	if node.Spec.ENISpec == nil {
		return nil
	}
	if k8sNode.Annotations == nil {
		k8sNode.Annotations = make(map[string]string)
	}
	l := logf.FromContext(ctx)
	base := k8sNode.DeepCopy()

	// nb(l1b0k): those anno should be deprecated after we move to controlpalne
	secondaryIP := 0
	lo.ForEach(node.Spec.Flavor, func(item networkv1beta1.Flavor, index int) {
		if item.NetworkInterfaceType == networkv1beta1.ENITypeSecondary &&
			item.NetworkInterfaceTrafficMode == networkv1beta1.NetworkInterfaceTrafficModeStandard {
			secondaryIP = item.Count * node.Spec.NodeCap.IPv4PerAdapter
		}
	})
	if secondaryIP > 0 {
		k8sNode.Annotations[string(types.NormalIPTypeIPs)] = strconv.Itoa(secondaryIP)
	} else {
		delete(k8sNode.Annotations, string(types.NormalIPTypeIPs))
	}

	// report trunk if node has one
	if node.Spec.ENISpec.EnableTrunk {
		// reconcile with local
		members := node.Spec.NodeCap.TotalAdapters - node.Spec.NodeCap.Adapters

		preferID := k8sNode.Annotations[types.TrunkOn]
		if preferID == "" {
			_, found := lo.FindKeyBy(node.Status.NetworkInterfaces, func(key string, eni *networkv1beta1.NetworkInterface) bool {
				return eni.NetworkInterfaceType == networkv1beta1.ENITypeTrunk &&
					preferID == eni.ID
			})

			if !found {
				trunkID, _ := lo.FindKeyBy(node.Status.NetworkInterfaces, func(key string, eni *networkv1beta1.NetworkInterface) bool {
					return eni.NetworkInterfaceType == networkv1beta1.ENITypeTrunk
				})

				if trunkID != "" {
					k8sNode.Annotations[types.TrunkOn] = trunkID
				}
			}
		}

		// report rse only trunk eni is ready
		num := resource.NewQuantity(int64(members), resource.DecimalSI)
		resName := "aliyun/member-eni"

		prev := k8sNode.Status.Allocatable[corev1.ResourceName(resName)]
		if !prev.Equal(*num) {
			patch := client.MergeFrom(base)

			if k8sNode.Status.Allocatable == nil {
				k8sNode.Status.Allocatable = make(corev1.ResourceList)
			}
			if k8sNode.Status.Capacity == nil {
				k8sNode.Status.Capacity = make(corev1.ResourceList)
			}

			k8sNode.Status.Allocatable[corev1.ResourceName(resName)] = *num
			k8sNode.Status.Capacity[corev1.ResourceName(resName)] = *num

			l.Info("report node member cap", "cap", members, "num", *num)
			err := r.client.Status().Patch(ctx, k8sNode, patch)
			if err != nil {
				return err
			}
		}
	}

	// update node annotation
	if !reflect.DeepEqual(k8sNode.Annotations, base.Annotations) {
		patch := client.MergeFrom(base)

		l.Info("new node annotations", "annotations", k8sNode.Annotations)
		err := r.client.Patch(ctx, k8sNode, patch)
		if err != nil {
			return err
		}
	}

	return nil
}
