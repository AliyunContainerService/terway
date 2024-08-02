package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/samber/lo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/AliyunContainerService/terway/deviceplugin"
	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types"
)

const (
	ControllerName = "multi-ip-node"

	finalizer = "network.alibabacloud.com/node-controller"

	batchSize = 10
)

var EventCh = make(chan event.GenericEvent, 1)

func init() {
	register.Add(ControllerName, func(mgr manager.Manager, ctrlCtx *register.ControllerCtx) error {
		fullSyncPeriod, err := time.ParseDuration(ctrlCtx.Config.MultiIPNodeSyncPeriod)
		if err != nil {
			return err
		}

		gcPeriod, err := time.ParseDuration(ctrlCtx.Config.MultiIPGCPeriod)
		if err != nil {
			return err
		}

		minSyncPeriod, err := time.ParseDuration(ctrlCtx.Config.MultiIPMinSyncPeriodOnFailure)
		if err != nil {
			return err
		}

		maxSyncPeriod, err := time.ParseDuration(ctrlCtx.Config.MultiIPMaxSyncPeriodOnFailure)
		if err != nil {
			return err
		}

		// metric and tracer

		metrics.Registry.MustRegister(ResourcePoolTotal)
		tracer := ctrlCtx.TracerProvider.Tracer(ControllerName)

		ctrl, err := controller.New(ControllerName, mgr, controller.Options{
			MaxConcurrentReconciles: ctrlCtx.Config.MultiIPNodeMaxConcurrent,
			Reconciler: &ReconcileNode{
				client:             mgr.GetClient(),
				scheme:             mgr.GetScheme(),
				record:             mgr.GetEventRecorderFor(ControllerName),
				aliyun:             ctrlCtx.AliyunClient,
				vswpool:            ctrlCtx.VSwitchPool,
				fullSyncNodePeriod: fullSyncPeriod,
				gcPeriod:           gcPeriod,
				tracer:             tracer,
			},
			RateLimiter: workqueue.NewMaxOfRateLimiter(
				workqueue.NewItemExponentialFailureRateLimiter(minSyncPeriod, maxSyncPeriod),
				&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(500), 1000)}),
			LogConstructor: func(request *reconcile.Request) logr.Logger {
				log := mgr.GetLogger()
				if request != nil {
					log = log.WithValues("name", request.Name)
				}
				return log
			},
		})
		if err != nil {
			return err
		}

		return ctrl.Watch(
			&source.Channel{Source: EventCh},
			&handler.EnqueueRequestForObject{},
		)
	}, false)
}

var _ reconcile.Reconciler = &ReconcileNode{}

type NodeStatus struct {
	NeedSyncOpenAPI   *atomic.Bool
	StatusChanged     *atomic.Bool
	LastGCTime        time.Time
	LastReconcileTime time.Time
}

type ReconcileNode struct {
	client client.Client
	scheme *runtime.Scheme
	record record.EventRecorder

	aliyun  register.Interface
	vswpool *vswitch.SwitchPool

	cache sync.Map

	fullSyncNodePeriod time.Duration
	gcPeriod           time.Duration

	tracer trace.Tracer
}

type ctxMetaKey struct{}

func MetaCtx(ctx context.Context) *NodeStatus {
	if metadata := ctx.Value(ctxMetaKey{}); metadata != nil {
		if m, ok := metadata.(*NodeStatus); ok {
			return m
		}
	}
	// return nil to avoid mistake
	return nil
}

func (n *ReconcileNode) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	ctx, span := n.tracer.Start(ctx, "reconcile", trace.WithAttributes(attribute.String("node", request.Name)))
	defer span.End()

	// write trace
	var l logr.Logger
	if span.SpanContext().IsValid() {
		traceID := span.SpanContext().TraceID().String()
		spanID := span.SpanContext().SpanID().String()

		l = logf.FromContext(ctx, "traceID", traceID, "spanID", spanID)
		ctx = logf.IntoContext(ctx, l)
	} else {
		l = logf.FromContext(ctx)
	}

	l.V(2).Info("reconcile node")

	node := &networkv1beta1.Node{}
	err := n.client.Get(ctx, client.ObjectKey{Name: request.Name}, node)

	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, "")
		} else {
			span.SetStatus(codes.Ok, "")
		}
	}()

	if err != nil {
		if k8sErr.IsNotFound(err) {
			// special for vk nodes, we don't need to do anything
			n.cache.Delete(request.Name)
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if !node.DeletionTimestamp.IsZero() {
		patch := client.MergeFrom(node.DeepCopy())
		changed := controllerutil.RemoveFinalizer(node, finalizer)
		n.cache.Delete(request.Name)
		if changed {
			err = n.client.Patch(ctx, node, patch)
		}
		return reconcile.Result{}, err
	}

	// check if daemon has ready
	if node.Spec.ENISpec == nil ||
		node.Spec.Pool == nil {
		return reconcile.Result{}, nil
	}

	ctx = logf.IntoContext(ctx, l.WithValues("rv", node.ResourceVersion))

	var nodeStatus *NodeStatus
	prev, ok := n.cache.Load(node.Name)
	if !ok {
		nodeStatus = &NodeStatus{
			NeedSyncOpenAPI: &atomic.Bool{},
			StatusChanged:   &atomic.Bool{},
		}
	} else {
		nodeStatus = prev.(*NodeStatus)
	}

	if nodeStatus.LastReconcileTime.Add(1 * time.Second).After(time.Now()) {
		return reconcile.Result{RequeueAfter: 1 * time.Second}, nil
	}

	// write into ctx for latter use
	// this should be thread safe to access NodeStatus
	ctx = context.WithValue(ctx, ctxMetaKey{}, nodeStatus)

	defer func() {
		nodeStatus.LastReconcileTime = time.Now()
		n.cache.Store(node.Name, nodeStatus)
	}()

	beforeStatus, err := runtime.DefaultUnstructuredConverter.ToUnstructured(node.Status.DeepCopy())
	if err != nil {
		return reconcile.Result{}, err
	}

	podRequests, err := n.getPods(ctx, node)
	if err != nil {
		return reconcile.Result{}, err
	}

	// on first startup, only node with pods on it, need to do a full sync
	// for legacy , we need to handle trunk eni
	if len(podRequests) != 0 && len(node.Status.NetworkInterfaces) == 0 {
		span.AddEvent("newNodeForceSyncOpenAPI")

		nodeStatus.NeedSyncOpenAPI.Store(true)
	} else {
		now := metav1.Now()
		if node.Status.NextSyncOpenAPITime.Before(&now) {
			span.AddEvent("ttlReachedSyncOpenAPI")

			nodeStatus.NeedSyncOpenAPI.Store(true)
		}
	}

	err = n.syncWithAPI(ctx, node)
	if err != nil {
		return reconcile.Result{}, err
	}

	syncErr := n.syncPods(ctx, podRequests, node)
	if syncErr != nil {
		l.Error(syncErr, "syncPods error")
	}

	afterStatus, err := runtime.DefaultUnstructuredConverter.ToUnstructured(node.Status.DeepCopy())
	if err != nil {
		return reconcile.Result{}, err
	}

	if !reflect.DeepEqual(beforeStatus, afterStatus) {
		span.AddEvent("update node cr")

		err = n.client.Status().Update(ctx, node)

		if err != nil && nodeStatus.StatusChanged.CompareAndSwap(true, false) {
			nodeStatus.NeedSyncOpenAPI.Store(true)
		}

		return reconcile.Result{RequeueAfter: 1 * time.Second}, err
	}

	return reconcile.Result{}, syncErr
}

// syncWithAPI will sync all eni from openAPI. Need to re-sync with local pods.
func (n *ReconcileNode) syncWithAPI(ctx context.Context, node *networkv1beta1.Node) error {
	if !MetaCtx(ctx).NeedSyncOpenAPI.Load() {
		return nil
	}

	ctx, span := n.tracer.Start(ctx, "syncWithAPI")
	defer span.End()

	l := logf.FromContext(ctx).WithName("syncWithAPI")
	SyncOpenAPITotal.WithLabelValues(node.Name).Inc()

	// all eni attached to this instance is take into count. (exclude member eni)
	enis, err := n.aliyun.DescribeNetworkInterface(ctx, "", nil, node.Spec.NodeMetadata.InstanceID, "", "", node.Spec.ENISpec.TagFilter)
	if err != nil {
		return err
	}

	// ignore primary eni
	enis = lo.Filter(enis, func(item *aliyunClient.NetworkInterface, index int) bool {
		return item.Type != aliyunClient.ENITypePrimary
	})

	eniIDMap := map[string]struct{}{}
	// add ip

	for _, item := range enis {
		log := l.WithValues("eni", item.NetworkInterfaceID, "status", item.Status)

		eniIDMap[item.NetworkInterfaceID] = struct{}{}
		remote := newENIFromAPI(item)

		crENI, ok := node.Status.NetworkInterfaces[item.NetworkInterfaceID]
		if !ok {
			log.Info("sync eni with remote, new eni added")

			// new eni, need add to local
			if node.Status.NetworkInterfaces == nil {
				node.Status.NetworkInterfaces = make(map[string]*networkv1beta1.NetworkInterface)
			}
			// we need cidr info
			vsw, err := n.vswpool.GetByID(ctx, n.aliyun, remote.VSwitchID)
			if err != nil {
				return err
			}
			remote.IPv4CIDR = vsw.IPv4CIDR
			remote.IPv6CIDR = vsw.IPv6CIDR
			node.Status.NetworkInterfaces[item.NetworkInterfaceID] = remote
		} else {
			// exist record
			// only ip is updated
			mergeIPMap(log, remote.IPv4, crENI.IPv4)
			mergeIPMap(log, remote.IPv6, crENI.IPv6)

			// nb(l1b0k): use Deleting status in cr for eni we don't wanted
			if crENI.Status != aliyunClient.ENIStatusDeleting {
				crENI.Status = remote.Status
			}
		}
	}

	// del eni (those eni is not attached on ecs)
	for id := range node.Status.NetworkInterfaces {
		if _, ok := eniIDMap[id]; !ok {
			// as the eni is not attached, so just delete it
			if node.Status.NetworkInterfaces[id].NetworkInterfaceType == networkv1beta1.ENITypeSecondary {
				l.Info("delete eni not found in remote, but in cr", "eni", id)
				err = n.aliyun.DeleteNetworkInterface(ctx, id)
			}
			if err != nil {
				l.Error(err, "eni not found on remote, delete eni error", "eni", id)
			} else {
				delete(node.Status.NetworkInterfaces, id)
			}
		}
	}

	// change the ts
	node.Status.NextSyncOpenAPITime = metav1.NewTime(time.Now().Add(wait.Jitter(n.fullSyncNodePeriod, 1)))
	node.Status.LastSyncOpenAPITime = metav1.Now()

	MetaCtx(ctx).NeedSyncOpenAPI.Store(false)
	return nil
}

func (n *ReconcileNode) getPods(ctx context.Context, node *networkv1beta1.Node) (map[string]*PodRequest, error) {
	ctx, span := n.tracer.Start(ctx, "getPods")
	defer span.End()

	pods := &corev1.PodList{}
	err := n.client.List(ctx, pods, client.MatchingFields{
		"spec.nodeName": node.Name,
	})
	if err != nil {
		return nil, err
	}

	podsMapper := make(map[string]*PodRequest, len(pods.Items))
	for _, pod := range pods.Items {
		if pod.Spec.HostNetwork ||
			types.PodUseENI(&pod) ||
			utils.PodSandboxExited(&pod) {
			continue
		}

		requireERDMA := false
		if node.Spec.ENISpec.EnableERDMA {
			lo.ForEach(append(pod.Spec.InitContainers, pod.Spec.Containers...), func(item corev1.Container, index int) {
				if res, ok := item.Resources.Limits[deviceplugin.ERDMAResName]; ok && !res.IsZero() {
					requireERDMA = true
				}
			})
		}
		ips := []string{pod.Status.PodIP}
		for _, v := range pod.Status.PodIPs {
			ips = append(ips, v.IP)
		}
		ipv4, ipv6, err := podIPs(ips)
		if err != nil {
			return nil, err
		}

		podsMapper[pod.Namespace+"/"+pod.Name] = &PodRequest{
			RequireIPv4:  node.Spec.ENISpec.EnableIPv4,
			RequireIPv6:  node.Spec.ENISpec.EnableIPv6,
			RequireERDMA: requireERDMA,
			IPv4:         ipv4,
			IPv6:         ipv6,
		}
	}
	return podsMapper, nil
}

// syncPods build a resource mapping to each pod
func (n *ReconcileNode) syncPods(ctx context.Context, podsMapper map[string]*PodRequest, node *networkv1beta1.Node) error {
	ctx, span := n.tracer.Start(ctx, "syncPods")
	defer span.End()

	l := logf.FromContext(ctx)

	ipv4Map, ipv6Map := buildIPMap(podsMapper, node.Status.NetworkInterfaces)

	// 1. delete unwanted
	releasePodNotFound(l, podsMapper, ipv4Map, ipv6Map)

	// 2. assign ip from local pool
	unSucceedPods := assignIPFromLocalPool(l, podsMapper, ipv4Map, ipv6Map)

	// 3. if there is no enough ip, try to allocate from api
	err := n.addIP(ctx, unSucceedPods, node)

	// 4. after all is assigned , we can re-allocate ip
	ipv4Map, ipv6Map = buildIPMap(podsMapper, node.Status.NetworkInterfaces)
	_ = assignIPFromLocalPool(l, podsMapper, ipv4Map, ipv6Map)

	if err == nil {
		err = n.gc(ctx, node)
	}

	return err
}

// releasePodNotFound release ip if there is no pod found
func releasePodNotFound(log logr.Logger, podsMapper map[string]*PodRequest, ipMapper ...map[string]*EniIP) {
	for _, ipMap := range ipMapper {
		for _, v := range ipMap {
			if v.IP.PodID == "" {
				continue
			}
			_, ok := podsMapper[v.IP.PodID]
			if ok {
				continue
			}
			log.Info("pod released", "pod", v.IP.PodID, "ip", v.IP.IP)
			v.IP.PodID = ""
		}
	}
}

// DO NOT assign pod ip if pod already has one
func assignIPFromLocalPool(log logr.Logger, podsMapper map[string]*PodRequest, ipv4Map, ipv6Map map[string]*EniIP) map[string]*PodRequest {
	pendingPods := lo.PickBy(podsMapper, func(key string, value *PodRequest) bool {
		if value.RequireIPv4 && value.ipv4Ref == nil {
			return true
		}
		if value.RequireIPv6 && value.ipv6Ref == nil {
			return true
		}
		return value.IPv6 == "" && value.IPv4 == ""
	})

	unSucceedPods := map[string]*PodRequest{}

	// only pending pods is handled
	for podID, info := range pendingPods {
		// choose eni first ...
		if info.RequireIPv4 && info.ipv4Ref == nil {
			if info.IPv4 != "" {
				// for take over case , pod has ip already, we can only assign to previous eni
				eniIP, ok := ipv4Map[info.IPv4]
				if ok && (eniIP.IP.PodID == "" || eniIP.IP.PodID == podID) {
					info.ipv4Ref = eniIP
					eniIP.IP.PodID = podID
					log.Info("assign ip", "pod", podID, "ip", eniIP.IP, "eni", eniIP.NetworkInterface.ID)
				}
			} else {
				for _, v := range ipv4Map {
					if v.NetworkInterface.Status != aliyunClient.ENIStatusInUse {
						continue
					}

					if info.RequireERDMA &&
						v.NetworkInterface.NetworkInterfaceTrafficMode != networkv1beta1.NetworkInterfaceTrafficModeHighPerformance {
						continue
					}

					if v.IP.Status == networkv1beta1.IPStatusValid && v.IP.PodID == "" {
						info.ipv4Ref = &EniIP{
							NetworkInterface: v.NetworkInterface,
							IP:               v.IP,
						}
						v.IP.PodID = podID

						log.Info("assign ip", "pod", podID, "ip", v.IP.IP, "eni", v.NetworkInterface.ID)
						break
					}
				}
			}

			// not found
			if info.ipv4Ref == nil {
				unSucceedPods[podID] = info

				continue
			}
		}

		if info.RequireIPv6 && info.ipv6Ref == nil {
			if info.IPv6 != "" {
				// for take over case , pod has ip already, we can only assign to previous eni
				eniIP, ok := ipv6Map[info.IPv6]
				if ok && (eniIP.IP.PodID == "" || eniIP.IP.PodID == podID) {
					info.ipv6Ref = eniIP
					eniIP.IP.PodID = podID
					log.Info("assign ip", "pod", podID, "ip", eniIP.IP, "eni", eniIP.NetworkInterface.ID)
				}
			} else {
				for _, v := range ipv6Map {
					if v.NetworkInterface.Status != aliyunClient.ENIStatusInUse {
						continue
					}

					if info.RequireERDMA &&
						v.NetworkInterface.NetworkInterfaceTrafficMode != networkv1beta1.NetworkInterfaceTrafficModeHighPerformance {
						continue
					}

					if info.ipv4Ref != nil && v.NetworkInterface.ID != info.ipv4Ref.NetworkInterface.ID {
						// we have chosen the eni
						continue
					}

					if v.IP.Status == networkv1beta1.IPStatusValid && v.IP.PodID == "" {
						info.ipv6Ref = &EniIP{
							NetworkInterface: v.NetworkInterface,
							IP:               v.IP,
						}
						v.IP.PodID = podID
						log.Info("assign ip", "pod", podID, "ip", v.IP.IP, "eni", v.NetworkInterface.ID)

						break
					}
				}
			}

			if info.ipv6Ref == nil {
				if info.IPv4 == "" && info.ipv4Ref != nil {
					log.Info("failed to get ipv6 addr, roll back ipv4", "pod", podID, "ip", info.ipv4Ref.IP)

					info.ipv4Ref.IP.PodID = ""
					info.ipv4Ref = nil
				}
				unSucceedPods[podID] = info

				continue
			}
		}
	}

	if len(unSucceedPods) > 0 {
		log.Info("unSucceedPods pods", "pods", unSucceedPods)
	}
	return unSucceedPods
}

// addIP is called when there is no enough ip for current pods
// for cases, eni is attaching, we need to wait
func (n *ReconcileNode) addIP(ctx context.Context, unSucceedPods map[string]*PodRequest, node *networkv1beta1.Node) error {
	ctx, span := n.tracer.Start(ctx, "addIP")
	defer span.End()

	normalPods := lo.PickBy(unSucceedPods, func(key string, value *PodRequest) bool {
		return !value.RequireERDMA
	})
	rdmaPods := lo.PickBy(unSucceedPods, func(key string, value *PodRequest) bool {
		return value.RequireERDMA
	})

	// before create eni , we need to check the quota
	options := getEniOptions(node)

	// handle trunk/secondary eni
	assignEniWithOptions(node, len(normalPods)+node.Spec.Pool.MinPoolSize, options, func(option *eniOptions) bool {
		return n.validateENI(ctx, option, []eniTypeKey{secondaryKey, trunkKey})
	})
	assignEniWithOptions(node, len(rdmaPods), options, func(option *eniOptions) bool {
		return n.validateENI(ctx, option, []eniTypeKey{rdmaKey})
	})

	err := n.allocateFromOptions(ctx, node, options)

	// update node condition based on eni status
	updateNodeCondition(ctx, n.client, node.Name, options)

	updateCrCondition(options)

	// the err is kept
	return err
}

// updateCrCondition record openAPI error to cr
func updateCrCondition(options []*eniOptions) {
	lo.ForEach(options, func(item *eniOptions, index int) {
		if item.eniRef == nil {
			return
		}
		if len(item.errors) == 0 {
			item.eniRef.Conditions = nil
			return
		}

		if item.eniRef.Conditions == nil {
			item.eniRef.Conditions = make(map[string]networkv1beta1.Condition)
		}

		var ipNotEnoughErr []error
		var otherErr []error
		for _, err := range item.errors {
			if isIPNotEnough(err) {
				ipNotEnoughErr = append(ipNotEnoughErr, err)
			} else {
				otherErr = append(otherErr, err)
			}
		}

		if len(ipNotEnoughErr) > 0 {
			prev, ok := item.eniRef.Conditions[ConditionInsufficientIP]
			if !ok {
				item.eniRef.Conditions[ConditionInsufficientIP] = networkv1beta1.Condition{
					ObservedTime: metav1.Now(),
					Message:      fmt.Sprintf("%s ip is not enough", item.eniRef.VSwitchID),
				}
			} else {
				if prev.ObservedTime.Add(5 * time.Minute).Before(time.Now()) {
					item.eniRef.Conditions[ConditionInsufficientIP] = networkv1beta1.Condition{
						ObservedTime: metav1.Now(),
						Message:      fmt.Sprintf("%s ip is not enough", item.eniRef.VSwitchID)}
				}
			}
		}
		if len(otherErr) > 0 {
			str := utilerrors.NewAggregate(otherErr).Error()
			item.eniRef.Conditions[ConditionOperationErr] = networkv1beta1.Condition{
				ObservedTime: metav1.Now(),
				Message:      str[:min(len(str), 256)],
			}
		}
	})
}

func updateNodeCondition(ctx context.Context, c client.Client, nodeName string, options []*eniOptions) {
	l := logf.FromContext(ctx)
	k8sNode := &corev1.Node{}
	err := c.Get(ctx, client.ObjectKey{Name: nodeName}, k8sNode)
	if err != nil {
		l.Error(err, "get node failed", "node", nodeName)
		return
	}

	hasIPLeft := false
	hasAllocatableEND := false

	// here we don't check the pod type is met
	lo.ForEach(options, func(item *eniOptions, index int) {
		if hasIPLeft || hasAllocatableEND {
			return
		}

		if item.eniRef != nil {
			for _, v := range item.eniRef.IPv4 {
				if v != nil {
					if v.Status == networkv1beta1.IPStatusValid && v.PodID == "" {
						hasIPLeft = true
						break
					}
				}
			}
		}
		// 1. vsw is blocked or eni is full
		if item.insufficientIP || item.isFull {
			return
		}

		// 2. call openAPI failed
		if lo.ContainsBy(item.errors, func(err error) bool {
			return isIPNotEnough(err)
		}) {
			return
		}

		hasAllocatableEND = true
	})

	status := corev1.ConditionTrue
	reason := types.IPResSufficientReason
	message := "node has sufficient IP"
	if !hasIPLeft && !hasAllocatableEND {
		status = corev1.ConditionFalse
		reason = types.IPResInsufficientReason
		message = "node has insufficient IP"
	}

	transitionTime := metav1.Now()
	for _, cond := range k8sNode.Status.Conditions {
		if cond.Type == types.SufficientIPCondition && cond.Status == status &&
			cond.Reason == reason && cond.Message == message {
			if cond.LastHeartbeatTime.Add(5 * time.Minute).After(time.Now()) {
				// refresh condition period 5min
				return
			}
			transitionTime = cond.LastTransitionTime
		}
	}
	now := metav1.Now()
	ipResCondition := corev1.NodeCondition{
		Type:               types.SufficientIPCondition,
		Status:             status,
		LastHeartbeatTime:  now,
		LastTransitionTime: transitionTime,
		Reason:             reason,
		Message:            message,
	}

	raw, err := json.Marshal(&[]corev1.NodeCondition{ipResCondition})
	if err != nil {
		l.Error(err, "marshal failed")
		return
	}

	patch := []byte(fmt.Sprintf(`{"status":{"conditions":%s}}`, raw))
	err = c.Status().Patch(ctx, k8sNode, client.RawPatch(k8stypes.StrategicMergePatchType, patch))
	if err != nil {
		l.Error(err, "patch node failed", "node", nodeName, "patch", patch)
	}
}

func (n *ReconcileNode) validateENI(ctx context.Context, option *eniOptions, eniTypes []eniTypeKey) bool {
	if !lo.Contains(eniTypes, option.eniTypeKey) {
		return false
	}

	if option.eniRef != nil {
		vsw, err := n.vswpool.GetByID(ctx, n.aliyun, option.eniRef.VSwitchID)
		if err != nil {
			logf.FromContext(ctx).Error(err, "failed to get vsw")
			return false
		}

		if vsw.AvailableIPCount <= 0 {
			option.insufficientIP = true
			return false
		}

		return true
	}

	return true
}

// assignEniWithOptions determine how many ip should be added to this eni.
// In dual stack, ip on eni is automatically balanced.
func assignEniWithOptions(node *networkv1beta1.Node, toAdd int, options []*eniOptions, filterFunc func(option *eniOptions) bool) {
	eniSpec := node.Spec.ENISpec

	toAddIPv4, toAddIPv6 := 0, 0
	if eniSpec.EnableIPv4 {
		toAddIPv4 = toAdd
	}
	if eniSpec.EnableIPv6 {
		toAddIPv6 = toAdd
	}

	// already ordered the eni
	for _, option := range options {
		if !filterFunc(option) {
			continue
		}

		if option.eniRef != nil {
			// exist eni
			if toAddIPv4 > 0 {
				toAddIPv4 -= len(getAllocatable(option.eniRef.IPv4))

				if toAddIPv4 > 0 {
					leftQuota := node.Spec.NodeCap.IPv4PerAdapter - len(option.eniRef.IPv4)
					if leftQuota > 0 {
						option.addIPv4N = min(leftQuota, toAddIPv4, batchSize)
						toAddIPv4 -= option.addIPv4N
					} else {
						option.isFull = true
					}
				}
			}

			if toAddIPv6 > 0 {
				// exclude the already assigned count, call this before quota check
				toAddIPv6 -= len(getAllocatable(option.eniRef.IPv6))

				if toAddIPv6 > 0 {
					leftQuota := node.Spec.NodeCap.IPv6PerAdapter - len(option.eniRef.IPv6)
					if leftQuota > 0 {
						option.addIPv6N = min(leftQuota, toAddIPv6, batchSize)
						toAddIPv6 -= option.addIPv6N
					} else {
						option.isFull = true
					}
				}
			}
		} else {
			// for new eni

			// for trunk , just create it
			if option.eniTypeKey == trunkKey {
				if toAddIPv4 <= 0 {
					toAddIPv4 = 1
				}
				if eniSpec.EnableIPv6 && toAddIPv6 <= 0 {
					toAddIPv6 = 1
				}
			}

			if toAddIPv4 > 0 {
				option.addIPv4N = min(node.Spec.NodeCap.IPv4PerAdapter, toAddIPv4, batchSize)
				toAddIPv4 -= option.addIPv4N
			}
			if toAddIPv6 > 0 {
				option.addIPv6N = min(node.Spec.NodeCap.IPv6PerAdapter, toAddIPv6, batchSize)
				toAddIPv6 -= option.addIPv6N
			}
		}
	}
}

// allocateFromOptions add ip or create eni, based on the options
func (n *ReconcileNode) allocateFromOptions(ctx context.Context, node *networkv1beta1.Node, options []*eniOptions) error {
	ctx, span := n.tracer.Start(ctx, "allocateFromOptions")
	defer span.End()

	wg := wait.Group{}
	lock := sync.Mutex{}
	var errs []error

	for _, option := range options {
		if option.addIPv6N <= 0 && option.addIPv4N <= 0 {
			continue
		}

		opt := option
		// start a gr for each eni
		wg.StartWithContext(ctx, func(ctx context.Context) {
			var err error
			if opt.eniRef == nil {
				err = n.createENI(ctx, node, opt)
			} else {
				// for exists enis
				err = n.assignIP(ctx, opt)
			}

			if err != nil {
				opt.errors = append(opt.errors, err)

				if apiErr.ErrorCodeIs(err, apiErr.ErrEniPerInstanceLimitExceeded, apiErr.ErrIPv4CountExceeded, apiErr.ErrIPv6CountExceeded) {
					MetaCtx(ctx).NeedSyncOpenAPI.Store(true)
				}

				lock.Lock()
				errs = append(errs, err)
				lock.Unlock()
			}
		})
	}

	wg.Wait()

	return utilerrors.NewAggregate(errs)
}

// gc follow those steps:
// 1. clean up deleting status eni and ip
// 2. mark unwanted eni/ip status to deleting
func (n *ReconcileNode) gc(ctx context.Context, node *networkv1beta1.Node) error {
	ctx, span := n.tracer.Start(ctx, "gc")
	defer span.End()

	// 1. clean up deleting status eni and ip
	err := n.handleStatus(ctx, node)
	if err != nil {
		return err
	}

	// 2. sort eni and find the victim
	return n.adjustPool(ctx, node)
}

func (n *ReconcileNode) handleStatus(ctx context.Context, node *networkv1beta1.Node) error {
	ctx, span := n.tracer.Start(ctx, "handleStatus")
	defer span.End()

	l := logf.FromContext(ctx).WithName("handleStatus")
	// 1. clean up deleting status eni and ip
	for _, eni := range node.Status.NetworkInterfaces {
		// we use ENIStatusDeleting as the eni is not needed, so we always need to detach it and then delete it
		log := l.WithValues("eni", eni.ID, "status", eni.Status)

		switch eni.Status {
		case aliyunClient.ENIStatusDeleting, aliyunClient.ENIStatusDetaching:
			err := n.aliyun.DetachNetworkInterface(ctx, eni.ID, node.Spec.NodeMetadata.InstanceID, "")
			if err != nil {
				log.Error(err, "run gc failed")
				continue
			}
			_, err = n.aliyun.WaitForNetworkInterface(ctx, eni.ID, aliyunClient.ENIStatusAvailable, backoff.Backoff(backoff.WaitENIStatus), true)
			if err != nil {
				if !errors.Is(err, apiErr.ErrNotFound) {
					log.Error(err, "run gc failed")
					continue
				}
			}

			// wait eni detached
			err = n.aliyun.DeleteNetworkInterface(ctx, eni.ID)
			if err != nil {
				log.Error(err, "run gc failed")
				continue
			}
			MetaCtx(ctx).StatusChanged.Store(true)

			log.Info("run gc succeed, eni removed", "eni", eni.ID)
			// remove from status
			delete(node.Status.NetworkInterfaces, eni.ID)
		case aliyunClient.ENIStatusInUse:
			var waitTime time.Duration
			ips := make([]netip.Addr, 0)
			for _, ip := range eni.IPv4 {
				if ip.Status == networkv1beta1.IPStatusDeleting {
					addr, err := netip.ParseAddr(ip.IP)
					if err == nil {
						ips = append(ips, addr)
					}
				}
			}
			ips = lo.Subset(ips, 0, batchSize)
			if len(ips) > 0 {
				err := n.aliyun.UnAssignPrivateIPAddresses(ctx, eni.ID, ips)
				if err != nil {
					continue
				}

				MetaCtx(ctx).StatusChanged.Store(true)

				lo.ForEach(ips, func(ip netip.Addr, _ int) {
					delete(eni.IPv4, ip.String())
				})

				waitTime = 1 * time.Second
			}

			ips = ips[:0]
			for _, ip := range eni.IPv6 {
				if ip.Status == networkv1beta1.IPStatusDeleting {
					addr, err := netip.ParseAddr(ip.IP)
					if err == nil {
						ips = append(ips, addr)
					}
				}
			}
			ips = lo.Subset(ips, 0, batchSize)
			if len(ips) > 0 {
				if waitTime > 0 {
					time.Sleep(waitTime)
				}

				err := n.aliyun.UnAssignIpv6Addresses(ctx, eni.ID, ips)
				if err != nil {
					continue
				}
				MetaCtx(ctx).StatusChanged.Store(true)

				lo.ForEach(ips, func(ip netip.Addr, _ int) {
					delete(eni.IPv6, ip.String())
				})
			}
		default:
			// nb(l1b0k): there is noway to reach here
			log.Info("unexpected status, skip eni", "eni", eni.ID, "status", eni.Status)
		}
	}
	return nil
}

func (n *ReconcileNode) adjustPool(ctx context.Context, node *networkv1beta1.Node) error {
	if MetaCtx(ctx).LastGCTime.Add(n.gcPeriod).After(time.Now()) {
		return nil
	}

	ctx, span := n.tracer.Start(ctx, "adjustPool")
	defer span.End()

	ResourcePoolTotal.WithLabelValues(node.Name).Inc()

	MetaCtx(ctx).LastGCTime = time.Now()

	l := logf.FromContext(ctx).WithName("adjustPool")

	keepN := node.Spec.Pool.MaxPoolSize

	idles := 0
	for _, eni := range node.Status.NetworkInterfaces {
		if eni.Status != aliyunClient.ENIStatusInUse {
			continue
		}
		if node.Spec.ENISpec.EnableIPv4 {
			idles += IdlesWithAvailable(eni.IPv4)
		} else {
			idles += IdlesWithAvailable(eni.IPv6)
		}
	}

	toDel := idles - keepN

	if toDel > 0 {
		l.Info("to delete ip", "idle", idles, "toDel", toDel)
		sorted := sortNetworkInterface(node)

		for i := len(sorted) - 1; i >= 0; i-- {
			// we unAssigned the ip
			count := releaseUnUsedIP(l, sorted[i], toDel)
			toDel -= count
			if toDel <= 0 {
				break
			}
		}
	}
	return nil
}

func (n *ReconcileNode) createENI(ctx context.Context, node *networkv1beta1.Node, opt *eniOptions) error {
	ctx, span := n.tracer.Start(ctx, "createENI", trace.WithAttributes(attribute.String("eniType", string(opt.eniTypeKey.ENIType))))
	defer span.End()

	// for new eni
	typeOption, ok := EniOptions[opt.eniTypeKey]
	if !ok {
		return fmt.Errorf("failed to find eni type, %v", opt.eniTypeKey)
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	vsw, err := n.vswpool.GetOne(ctx, n.aliyun, node.Spec.NodeMetadata.ZoneID, node.Spec.ENISpec.VSwitchOptions, &vswitch.SelectOptions{
		VSwitchSelectPolicy: vswitch.SelectionPolicy(node.Spec.ENISpec.VSwitchSelectPolicy),
	})
	if err != nil {
		return err
	}
	bo := backoff.Backoff(backoff.ENICreate)

	createOpts := &aliyunClient.CreateNetworkInterfaceOptions{
		NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
			VSwitchID:        vsw.ID,
			SecurityGroupIDs: node.Spec.ENISpec.SecurityGroupIDs,
			ResourceGroupID:  node.Spec.ENISpec.ResourceGroupID,
			Tags:             node.Spec.ENISpec.Tag,
			IPCount:          opt.addIPv4N,
			IPv6Count:        opt.addIPv6N,
		},
		Backoff: &bo,
	}

	result, err := n.aliyun.CreateNetworkInterface(ctx, typeOption, createOpts)
	if err != nil {
		if apiErr.ErrorCodeIs(err, apiErr.InvalidVSwitchIDIPNotEnough) {
			// block
			n.vswpool.Block(createOpts.NetworkInterfaceOptions.VSwitchID)
		}
		return err
	}

	if node.Status.NetworkInterfaces == nil {
		node.Status.NetworkInterfaces = make(map[string]*networkv1beta1.NetworkInterface)
	}

	defer func() {
		if err != nil {
			rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer rollbackCancel()

			innerErr := n.aliyun.DeleteNetworkInterface(rollbackCtx, result.NetworkInterfaceID)
			if innerErr == nil {
				return
			}
			logf.FromContext(ctx).Error(innerErr, "failed to delete eni, this may result leak", "eni", result.NetworkInterfaceID)
			// if we failed to delete the eni , we need to store the eni
			node.Status.NetworkInterfaces[result.NetworkInterfaceID] = &networkv1beta1.NetworkInterface{
				ID:     result.NetworkInterfaceID,
				Status: aliyunClient.ENIStatusDeleting,
			}
			MetaCtx(ctx).StatusChanged.Store(true)
		}
	}()
	err = n.aliyun.AttachNetworkInterface(ctx, result.NetworkInterfaceID, node.Spec.NodeMetadata.InstanceID, "")
	if err != nil {
		return err
	}

	time.Sleep(3 * time.Second)
	eni, err := n.aliyun.WaitForNetworkInterface(ctx, result.NetworkInterfaceID, aliyunClient.ENIStatusInUse, backoff.Backoff(backoff.WaitENIStatus), false)
	if err != nil {
		return err
	}

	networkInterface := newENIFromAPI(eni)
	// update vsw
	networkInterface.IPv4CIDR = vsw.IPv4CIDR
	networkInterface.IPv6CIDR = vsw.IPv6CIDR

	node.Status.NetworkInterfaces[eni.NetworkInterfaceID] = networkInterface
	// if changed , but we update failed , that case ,need to sync openAPI...

	MetaCtx(ctx).StatusChanged.Store(true)
	return nil
}

func (n *ReconcileNode) assignIP(ctx context.Context, opt *eniOptions) error {
	ctx, span := n.tracer.Start(ctx, "assignIP", trace.WithAttributes(attribute.String("eni", opt.eniRef.ID), attribute.Int("ipv4", opt.addIPv4N), attribute.Int("ipv6", opt.addIPv6N)))
	defer span.End()

	// nb(l1b0k): ENi does not support assigning both IPv4 and IPv6 simultaneously.
	if opt.addIPv4N > 0 {
		bo := backoff.Backoff(backoff.ENIIPOps)
		result, err := n.aliyun.AssignPrivateIPAddress(ctx, &aliyunClient.AssignPrivateIPAddressOptions{
			NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
				NetworkInterfaceID: opt.eniRef.ID,
				IPCount:            opt.addIPv4N,
			},
			Backoff: &bo,
		})
		if err != nil {
			if apiErr.ErrorCodeIs(err, apiErr.InvalidVSwitchIDIPNotEnough) {
				// block
				n.vswpool.Block(opt.eniRef.VSwitchID)
			}

			return err
		} else {
			MetaCtx(ctx).StatusChanged.Store(true)
			if opt.eniRef.IPv4 == nil {
				opt.eniRef.IPv4 = make(map[string]*networkv1beta1.IP)
			}
			for _, ip := range result {
				addIPToMap(opt.eniRef.IPv4, &networkv1beta1.IP{
					IP:     ip.String(),
					Status: networkv1beta1.IPStatusValid,
				})
			}
		}
	}
	if opt.addIPv6N > 0 {
		bo := backoff.Backoff(backoff.ENIIPOps)
		result, err := n.aliyun.AssignIpv6Addresses(ctx, &aliyunClient.AssignIPv6AddressesOptions{
			NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
				NetworkInterfaceID: opt.eniRef.ID,
				IPv6Count:          opt.addIPv6N,
			},
			Backoff: &bo,
		})
		if err != nil {
			if apiErr.ErrorCodeIs(err, apiErr.InvalidVSwitchIDIPNotEnough) {
				// block
				n.vswpool.Block(opt.eniRef.VSwitchID)
			}
			return err
		} else {
			MetaCtx(ctx).StatusChanged.Store(true)

			if opt.eniRef.IPv6 == nil {
				opt.eniRef.IPv6 = make(map[string]*networkv1beta1.IP)
			}
			for _, ip := range result {
				addIPToMap(opt.eniRef.IPv6, &networkv1beta1.IP{
					IP:     ip.String(),
					Status: networkv1beta1.IPStatusValid,
				})
			}
		}
	}
	return nil
}

// buildIPMap put the relating from node.Status.NetworkInterfaces to ipv4Map and ipv6Map
func buildIPMap(podsMapper map[string]*PodRequest, enis map[string]*networkv1beta1.NetworkInterface) (map[string]*EniIP, map[string]*EniIP) {
	// build a allocated ip map
	ipv4Map := make(map[string]*EniIP)
	ipv6Map := make(map[string]*EniIP)

	for _, item := range enis {
		for k, v := range item.IPv4 {
			eniIP := &EniIP{
				NetworkInterface: item,
				IP:               v,
			}
			// 1. index eniip by ip
			ipv4Map[k] = eniIP

			podReq, ok := podsMapper[v.PodID]
			if ok {
				// 2. link eniip to pod
				podReq.ipv4Ref = eniIP
			}
		}
		for k, v := range item.IPv6 {
			eniIP := &EniIP{
				NetworkInterface: item,
				IP:               v,
			}
			ipv6Map[k] = eniIP

			podReq, ok := podsMapper[v.PodID]
			if ok {
				podReq.ipv6Ref = eniIP
			}
		}
	}
	return ipv4Map, ipv6Map
}

func getAllocatable(in map[string]*networkv1beta1.IP) map[string]*networkv1beta1.IP {
	return lo.PickBy(in, func(key string, value *networkv1beta1.IP) bool {
		return value.Status == networkv1beta1.IPStatusValid && value.PodID == ""
	})
}

// getEniOptions get the expected eni type and count based on flavor
func getEniOptions(node *networkv1beta1.Node) []*eniOptions {
	total := 0
	flavor := lo.SliceToMap(node.Spec.Flavor, func(item networkv1beta1.Flavor) (eniTypeKey, int) {
		total += item.Count
		return eniTypeKey{
			ENIType:                     item.NetworkInterfaceType,
			NetworkInterfaceTrafficMode: item.NetworkInterfaceTrafficMode,
		}, item.Count
	})

	// cal exists eni
	sorted := sortNetworkInterface(node)
	lo.ForEach(sorted, func(item *networkv1beta1.NetworkInterface, index int) {
		key := eniTypeKey{
			ENIType:                     item.NetworkInterfaceType,
			NetworkInterfaceTrafficMode: item.NetworkInterfaceTrafficMode,
		}
		v, ok := flavor[key]
		if ok {
			flavor[key] = v - 1
		}
	})

	result := make([]*eniOptions, 0, total)

	newENILimit := max(total-len(sorted), 0)
	// range all eni
	// 1. if we require trunk/erdma, create it
	if node.Spec.ENISpec.EnableTrunk {
		cnt := min(flavor[trunkKey], newENILimit)
		for i := 0; i < cnt; i++ {
			result = append(result, &eniOptions{
				eniTypeKey: trunkKey,
				eniRef:     nil,
			})
		}
		newENILimit -= cnt
	}

	if node.Spec.ENISpec.EnableERDMA {
		cnt := min(flavor[rdmaKey], newENILimit)
		for i := 0; i < cnt; i++ {
			result = append(result, &eniOptions{
				eniTypeKey: rdmaKey,
				eniRef:     nil,
			})
		}
	}

	// 2. put current eni to result
	lo.ForEach(sorted, func(item *networkv1beta1.NetworkInterface, index int) {
		result = append(result, &eniOptions{
			eniTypeKey: eniTypeKey{
				ENIType:                     item.NetworkInterfaceType,
				NetworkInterfaceTrafficMode: item.NetworkInterfaceTrafficMode,
			},
			eniRef: item,
		})
	})

	toAdd := min(total-len(result), flavor[secondaryKey])

	// 3. put the rest empty slot for secondary eni
	for i := 0; i < toAdd; i++ {
		result = append(result, &eniOptions{
			eniTypeKey: secondaryKey,
			eniRef:     nil,
		})
	}

	return result
}

func addIPToMap(in map[string]*networkv1beta1.IP, ip *networkv1beta1.IP) {
	v, ok := in[ip.IP]
	if ok {
		v.Status = ip.Status
		v.Primary = ip.Primary
		if v.PodID == "" {
			v.PodID = ip.PodID
		}
	} else {
		in[ip.IP] = ip
	}
}

func isIPNotEnough(err error) bool {
	if apiErr.ErrorCodeIs(err, apiErr.InvalidVSwitchIDIPNotEnough) {
		return true
	}
	// can not find vsw
	if errors.Is(err, vswitch.ErrNoAvailableVSwitch) {
		return true
	}
	return false
}
