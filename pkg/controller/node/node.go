package node

import (
	"context"
	"fmt"
	"reflect"
	"strconv"

	"github.com/go-logr/logr"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/AliyunContainerService/terway/deviceplugin"
	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/controller/multi-ip/node"
	"github.com/AliyunContainerService/terway/pkg/controller/status"
	"github.com/AliyunContainerService/terway/pkg/feature"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"
)

const (
	ControllerName = "node"

	finalizer = "network.alibabacloud.com/node-controller"
)

func init() {
	register.Add(ControllerName, func(mgr manager.Manager, ctrlCtx *register.ControllerCtx) error {
		ctrlCtx.RegisterResource = append(ctrlCtx.RegisterResource, &corev1.Node{}, &networkv1beta1.Node{}, &networkv1beta1.NodeRuntime{})

		err := mgr.GetFieldIndexer().IndexField(ctrlCtx.Context, &corev1.Pod{}, "spec.nodeName", func(object client.Object) []string {
			pod := object.(*corev1.Pod)
			return []string{pod.Spec.NodeName}
		})
		if err != nil {
			return err
		}

		nodePredicate := &predicateForNodeEvent{
			nodeLabelWhiteList: controlplane.GetConfig().NodeLabelWhiteList,
			supportEFLO:        utilfeature.DefaultMutableFeatureGate.Enabled(feature.EFLO),
		}
		return ctrl.NewControllerManagedBy(mgr).
			Named(ControllerName).
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
			For(&corev1.Node{}, builder.WithPredicates(nodePredicate)).
			Watches(&networkv1beta1.Node{}, &handler.EnqueueRequestForObject{}).
			Watches(&networkv1beta1.NodeRuntime{}, &handler.EnqueueRequestForObject{}).
			Complete(&ReconcileNode{
				client:          mgr.GetClient(),
				scheme:          mgr.GetScheme(),
				record:          mgr.GetEventRecorderFor(ControllerName),
				aliyun:          ctrlCtx.AliyunClient,
				supportEFLO:     utilfeature.DefaultMutableFeatureGate.Enabled(feature.EFLO),
				nodePredicate:   nodePredicate,
				nodeStatusCache: ctrlCtx.NodeStatusCache,
				centralizedIPAM: ctrlCtx.Config.CentralizedIPAM,
			})
	}, false)
}

var _ reconcile.Reconciler = &ReconcileNode{}

type ReconcileNode struct {
	client client.Client
	scheme *runtime.Scheme

	aliyun aliyunClient.OpenAPI
	record record.EventRecorder

	supportEFLO   bool
	nodePredicate *predicateForNodeEvent

	nodeStatusCache *status.Cache[status.NodeStatus]

	centralizedIPAM bool
}

func (r *ReconcileNode) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	if r.centralizedIPAM {
		defer node.Notify(ctx, request.Name)
	}

	k8sNode := &corev1.Node{}
	err := r.client.Get(ctx, request.NamespacedName, k8sNode)
	if err != nil {
		if k8sErr.IsNotFound(err) {
			return r.delete(ctx, request)
		}
		return reconcile.Result{}, err
	}
	if r.nodePredicate != nil && !r.nodePredicate.predicateNode(k8sNode) {
		return r.delete(ctx, request)
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
		return r.delete(ctx, request)
	}

	defer func() {
		if err != nil {
			r.record.Event(k8sNode, "Warning", "ConfigError", err.Error())
		}
	}()
	if utils.ISLingJunNode(k8sNode.Labels) {
		if !r.supportEFLO {
			return reconcile.Result{}, nil
		}
		var result reconcile.Result
		result, err = r.handleEFLO(ctx, k8sNode, node)
		return result, err
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

	prev := node.Labels[types.ExclusiveENIModeLabel]
	if prev == "" {
		node.Labels[types.ExclusiveENIModeLabel] = string(types.NodeExclusiveENIMode(k8sNode.Labels))
	} else if prev != string(types.NodeExclusiveENIMode(k8sNode.Labels)) {
		err = fmt.Errorf("node exclusive mode changed to %s, this is not allowd", types.NodeExclusiveENIMode(k8sNode.Labels))
		return err
	}

	if needUpdate(node, nodeInfo) {

		node.Spec.NodeMetadata = networkv1beta1.NodeMetadata{
			InstanceType: nodeInfo.InstanceType,
			InstanceID:   nodeInfo.InstanceID,
			ZoneID:       nodeInfo.ZoneID,
			RegionID:     nodeInfo.RegionID,
		}

		limit, err := aliyunClient.GetLimitProvider().GetLimit(r.aliyun.GetECS(), nodeInfo.InstanceType)
		if err != nil {
			return err
		}

		node.Spec.NodeCap = networkv1beta1.NodeCap{
			InstanceBandwidthTx:   limit.InstanceBandwidthTx,
			InstanceBandwidthRx:   limit.InstanceBandwidthRx,
			Adapters:              limit.Adapters,
			EriQuantity:           limit.ERDMARes(),
			TotalAdapters:         limit.TotalAdapters,
			IPv6PerAdapter:        limit.IPv6PerAdapter,
			MemberAdapterLimit:    limit.MemberAdapterLimit,
			IPv4PerAdapter:        limit.IPv4PerAdapter,
			MaxMemberAdapterLimit: limit.MaxMemberAdapterLimit,
			NetworkCards: lo.Map(limit.NetworkCards, func(item aliyunClient.NetworkCard, index int) networkv1beta1.NetworkCard {
				return networkv1beta1.NetworkCard{
					Index: item.Index,
				}
			}),
		}

		node.Spec.NodeCap.NetworkCardsCount = ptr.To(len(node.Spec.NodeCap.NetworkCards))
	}

	update := node.DeepCopy()
	_, err = controllerutil.CreateOrUpdate(ctx, r.client, update, func() error {
		update.Spec = node.Spec
		update.Labels = node.Labels
		return nil
	})
	if err != nil {
		return err
	}

	if !r.centralizedIPAM {
		return nil
	}

	err = r.k8sAnno(ctx, k8sNode, node)
	if err != nil {
		return err
	}

	err = r.patchNodeRes(ctx, k8sNode, node)
	return err
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

	secondaryIP := 0

	switch types.NodeExclusiveENIMode(node.Labels) {
	case types.ExclusiveENIOnly:
		lo.ForEach(node.Spec.Flavor, func(item networkv1beta1.Flavor, index int) {
			if item.NetworkInterfaceType == networkv1beta1.ENITypeSecondary &&
				item.NetworkInterfaceTrafficMode == networkv1beta1.NetworkInterfaceTrafficModeStandard {
				secondaryIP = item.Count
			}
		})
	default:
		lo.ForEach(node.Spec.Flavor, func(item networkv1beta1.Flavor, index int) {
			if item.NetworkInterfaceType == networkv1beta1.ENITypeSecondary &&
				item.NetworkInterfaceTrafficMode == networkv1beta1.NetworkInterfaceTrafficModeStandard {
				secondaryIP += item.Count * node.Spec.NodeCap.IPv4PerAdapter
			}
			if item.NetworkInterfaceType == networkv1beta1.ENITypeTrunk {
				secondaryIP += item.Count * node.Spec.NodeCap.IPv4PerAdapter
			}
		})

		// handle trunk
		if node.Spec.ENISpec.EnableTrunk {
			preferID := k8sNode.Annotations[types.TrunkOn]

			// verify eni is present
			_, found := lo.FindKeyBy(node.Status.NetworkInterfaces, func(key string, eni *networkv1beta1.Nic) bool {
				return eni.NetworkInterfaceType == networkv1beta1.ENITypeTrunk &&
					preferID == eni.ID
			})

			if !found {
				// either new node or trunk eni is missing

				// add one
				trunkID, _ := lo.FindKeyBy(node.Status.NetworkInterfaces, func(key string, eni *networkv1beta1.Nic) bool {
					return eni.NetworkInterfaceType == networkv1beta1.ENITypeTrunk && eni.Status == aliyunClient.ENIStatusInUse
				})

				if trunkID != "" {
					k8sNode.Annotations[types.TrunkOn] = trunkID
				}
			}
		}
	}
	if secondaryIP > 0 {
		k8sNode.Annotations[string(types.NormalIPTypeIPs)] = strconv.Itoa(secondaryIP)
	} else {
		delete(k8sNode.Annotations, string(types.NormalIPTypeIPs))
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

func (r *ReconcileNode) patchNodeRes(ctx context.Context, k8sNode *corev1.Node, node *networkv1beta1.Node) error {
	if node.Spec.ENISpec == nil {
		return nil
	}
	l := logf.FromContext(ctx)
	base := k8sNode.DeepCopy()

	var num *resource.Quantity
	resName := ""

	switch types.NodeExclusiveENIMode(node.Labels) {
	case types.ExclusiveENIOnly:
		secondary := 0
		lo.ForEach(node.Spec.Flavor, func(item networkv1beta1.Flavor, index int) {
			if item.NetworkInterfaceType == networkv1beta1.ENITypeSecondary &&
				item.NetworkInterfaceTrafficMode == networkv1beta1.NetworkInterfaceTrafficModeStandard {
				secondary = item.Count
			}
		})
		num = resource.NewQuantity(int64(secondary), resource.DecimalSI)
		resName = deviceplugin.ENIResName
	default:
		// report trunk if node has one
		if node.Spec.ENISpec.EnableTrunk {
			// report only when trunk is ready
			_, found := lo.FindKeyBy(node.Status.NetworkInterfaces, func(key string, eni *networkv1beta1.Nic) bool {
				return eni.NetworkInterfaceType == networkv1beta1.ENITypeTrunk && eni.Status == aliyunClient.ENIStatusInUse
			})

			if found {
				members := node.Spec.NodeCap.MemberAdapterLimit

				// report rse only trunk eni is ready
				num = resource.NewQuantity(int64(members), resource.DecimalSI)
				resName = deviceplugin.MemberENIResName
			}
		}
	}

	if num == nil {
		return nil
	}

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

		l.Info("report node cap", resName, *num, "prev", prev.String(), "rv", k8sNode.ResourceVersion)
		err := r.client.Status().Patch(ctx, k8sNode, patch)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *ReconcileNode) handleEFLO(ctx context.Context, k8sNode *corev1.Node, node *networkv1beta1.Node) (reconcile.Result, error) {
	if node.Labels == nil {
		node.Labels = make(map[string]string)
	}
	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}
	node.Labels["name"] = k8sNode.Name
	node.Labels[types.LingJunNodeLabelKey] = "true"

	prev := node.Labels[types.ExclusiveENIModeLabel]
	if prev == "" {
		node.Labels[types.ExclusiveENIModeLabel] = string(types.NodeExclusiveENIMode(k8sNode.Labels))
	} else if prev != string(types.NodeExclusiveENIMode(k8sNode.Labels)) {
		err := fmt.Errorf("node exclusive mode changed to %s, this is not allowd", types.NodeExclusiveENIMode(k8sNode.Labels))
		return reconcile.Result{}, err
	}

	nodeInfo, err := common.NewNodeInfo(k8sNode)
	if err != nil {
		return reconcile.Result{}, err
	}

	if needUpdate(node, nodeInfo) {

		node.Spec.NodeMetadata.InstanceID = nodeInfo.InstanceID
		node.Spec.NodeMetadata.InstanceType = nodeInfo.InstanceType
		node.Spec.NodeMetadata.ZoneID = nodeInfo.ZoneID
		node.Spec.NodeMetadata.RegionID = nodeInfo.RegionID

		node.Spec.NodeCap.NetworkCardsCount = ptr.To(0)
		node.Spec.NodeCap.NetworkCards = nil

		isEni, err := r.hasPrimaryENI(ctx, node.Spec.NodeMetadata.InstanceID)
		if err != nil {
			return reconcile.Result{}, err
		}

		if isEni {
			describeNodeReq := &aliyunClient.DescribeNodeRequestOptions{
				NodeID: &node.Spec.NodeMetadata.InstanceID,
			}

			if types.NodeExclusiveENIMode(node.Labels) != types.ExclusiveENIOnly {
				return reconcile.Result{}, fmt.Errorf("exclusive ENI mode must be enabled for EFLO nodes")
			}

			resp, err := r.aliyun.GetEFLOController().DescribeNode(ctx, describeNodeReq)
			if err != nil {
				return reconcile.Result{}, err
			}

			limit, err := aliyunClient.GetLimitProvider().GetLimit(r.aliyun.GetEFLOController(), resp.NodeType)
			if err != nil {
				return reconcile.Result{}, err
			}

			// this case use ecs api
			node.Spec.NodeCap.Adapters = limit.Adapters
			node.Spec.NodeCap.TotalAdapters = limit.Adapters
			node.Spec.NodeCap.IPv4PerAdapter = limit.IPv4PerAdapter

			if (node.Spec.NodeCap.Adapters <= 1 &&
				limit.HighDenseQuantity > 0) ||
				k8sNode.Annotations[types.ENOApi] == types.APIEcsHDeni { // check k8s config
				node.Spec.NodeCap.Adapters = limit.HighDenseQuantity
				node.Spec.NodeCap.TotalAdapters = limit.HighDenseQuantity

				if node.Annotations[types.ENOApi] != "" && node.Annotations[types.ENOApi] != types.APIEcsHDeni {
					return reconcile.Result{}, fmt.Errorf("cannot change ENOApi from %s to ecs-hdeni", node.Annotations[types.ENOApi])
				}

				node.Annotations[types.ENOApi] = types.APIEcsHDeni
			} else {
				if node.Annotations[types.ENOApi] != "" && node.Annotations[types.ENOApi] != types.APIEcs {
					return reconcile.Result{}, fmt.Errorf("cannot change ENOApi from %s to ecs", node.Annotations[types.ENOApi])
				}
				node.Annotations[types.ENOApi] = types.APIEcs
			}
		} else {
			resp, err := r.aliyun.GetEFLO().GetNodeInfoForPod(ctx, node.Spec.NodeMetadata.InstanceID)
			if err != nil {
				return reconcile.Result{}, err
			}
			node.Spec.NodeCap.Adapters = resp.LeniQuota
			node.Spec.NodeCap.TotalAdapters = resp.LeniQuota
			node.Spec.NodeCap.IPv4PerAdapter = resp.LeniSipQuota

			// fallback to hdeni if leni not available
			if (node.Spec.NodeCap.Adapters <= 1 &&
				resp.HdeniQuota > 0 &&
				types.NodeExclusiveENIMode(node.Labels) == types.ExclusiveENIOnly) ||
				k8sNode.Annotations[types.ENOApi] == types.APIEnoHDeni { // check k8s config
				node.Spec.NodeCap.Adapters = resp.HdeniQuota
				node.Spec.NodeCap.TotalAdapters = resp.HdeniQuota
				node.Spec.NodeCap.IPv4PerAdapter = 1

				node.Annotations[types.ENOApi] = types.APIEnoHDeni
			}
		}
	}

	update := node.DeepCopy()
	_, err = controllerutil.CreateOrUpdate(ctx, r.client, update, func() error {
		update.Spec = node.Spec
		update.Labels = node.Labels
		update.Annotations = node.Annotations
		return nil
	})
	return reconcile.Result{}, err
}

func (r *ReconcileNode) hasPrimaryENI(ctx context.Context, instanceID string) (bool, error) {
	req := &aliyunClient.DescribeNetworkInterfaceOptions{
		InstanceID:   &instanceID,
		InstanceType: ptr.To(aliyunClient.ENITypePrimary),
	}
	resp, err := r.aliyun.DescribeNetworkInterfaceV2(ctx, req)
	if err != nil {
		return false, err
	}

	return len(resp) == 1, nil
}

func (r *ReconcileNode) delete(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	l := logr.FromContextOrDiscard(ctx)
	l.Info("begin delete node")

	r.nodeStatusCache.Delete(request.Name)

	if r.centralizedIPAM {
		// nb(l1b0k): at centralizedIPAM , multi-ip controller will remove finalizer
		return reconcile.Result{}, nil
	}
	node := &networkv1beta1.Node{}
	err := r.client.Get(ctx, request.NamespacedName, node)
	if err != nil {
		if !k8sErr.IsNotFound(err) {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}

	update := node.DeepCopy()
	controllerutil.RemoveFinalizer(update, finalizer)
	err = r.client.Patch(ctx, update, client.MergeFrom(node))

	l.Info("delete node finished")
	return reconcile.Result{}, err
}

func needUpdate(node *networkv1beta1.Node, nodeInfo *common.NodeInfo) bool {
	return node.Spec.NodeMetadata.InstanceType != nodeInfo.InstanceType ||
		node.Spec.NodeMetadata.InstanceID != nodeInfo.InstanceID ||
		node.Spec.NodeMetadata.ZoneID != nodeInfo.ZoneID ||
		node.Spec.NodeMetadata.RegionID != nodeInfo.RegionID ||
		node.Spec.NodeCap.NetworkCardsCount == nil
}
