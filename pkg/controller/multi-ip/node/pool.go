package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/lo"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/mod/semver"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"
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
	"github.com/AliyunContainerService/terway/pkg/eni/ops"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/controlplane"
)

const (
	ControllerName = "multi-ip-node"

	finalizer = "network.alibabacloud.com/node-controller"

	ecsBatchSize  = 10
	efloBatchSize = 1

	// Maximum concurrent ENI attach operations per instance
	// ECS API limits concurrent attach operations to 5
	ecsMaxConcurrentAttach = 5
	// EFLO API limits concurrent attach operations to 2
	efloMaxConcurrentAttach = 2

	// maxPrefixPerAPICall is the maximum number of prefixes that can be assigned per API call.
	// Both CreateNetworkInterface and AssignPrivateIpAddresses APIs limit IPv4/IPv6 prefix count to 1-10.
	maxPrefixPerAPICall = 10

	// Event reasons
	EventAllocIPFailed      = "AllocIPFailed"
	EventSyncOpenAPISuccess = "SyncOpenAPISuccess"
	EventSyncOpenAPIFailed  = "SyncOpenAPIFailed"
)

var EventCh = make(chan event.GenericEvent, 1000)

func Notify(ctx context.Context, name string) {
	EventCh <- event.GenericEvent{
		Object: &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}},
	}
	if logf.FromContext(ctx).V(4).Enabled() {
		logf.FromContext(ctx).Info("notify node event")
	}
}

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
		metrics.Registry.MustRegister(SyncOpenAPITotal)
		metrics.Registry.MustRegister(ReconcileLatency)
		metrics.Registry.MustRegister(ENITaskQueueSize)
		metrics.Registry.MustRegister(ENIAttachDuration)
		tracer := ctrlCtx.TracerProvider.Tracer(ControllerName)

		// Create ENI task queue for async attach operations
		eniNotifyCh := make(chan string, ctrlCtx.Config.ENIMaxConcurrent)
		executor := ops.NewExecutor(ctrlCtx.AliyunClient, tracer)
		eniTaskQueue := NewENITaskQueue(ctrlCtx.Context, executor, eniNotifyCh)

		// Start a goroutine to forward eniNotifyCh to EventCh
		// This ensures Reconcile is triggered when async ENI attach tasks complete
		go func() {
			for {
				select {
				case <-ctrlCtx.Done():
					return
				case name := <-eniNotifyCh:
					Notify(ctrlCtx, name)
				}
			}
		}()

		ctrl, err := controller.New(ControllerName, mgr, controller.Options{
			MaxConcurrentReconciles: ctrlCtx.Config.MultiIPNodeMaxConcurrent,
			Reconciler: &ReconcileNode{
				client:             mgr.GetClient(),
				scheme:             mgr.GetScheme(),
				record:             mgr.GetEventRecorderFor(utils.EventName(ControllerName)),
				aliyun:             ctrlCtx.AliyunClient,
				vswpool:            ctrlCtx.VSwitchPool,
				fullSyncNodePeriod: fullSyncPeriod,
				gcPeriod:           gcPeriod,
				tracer:             tracer,
				eniBatchSize:       ecsMaxConcurrentAttach, // operate eni on one reconcile
				v:                  controlplane.GetViper(),
				eniTaskQueue:       eniTaskQueue,
			},
			RateLimiter: workqueue.NewTypedMaxOfRateLimiter(
				workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](minSyncPeriod, maxSyncPeriod),
				&workqueue.TypedBucketRateLimiter[reconcile.Request]{Limiter: rate.NewLimiter(rate.Limit(500), 1000)}),
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

		return ctrl.Watch(source.Channel(EventCh, &handler.EnqueueRequestForObject{}))
	}, false)
}

var _ reconcile.Reconciler = &ReconcileNode{}

type NodeStatus struct {
	NeedSyncOpenAPI   *atomic.Bool
	StatusChanged     *atomic.Bool
	LastGCTime        time.Time
	LastReconcileTime time.Time

	ProcessedTaskIDs []string
	Mutex            sync.Mutex
}

type ReconcileNode struct {
	client client.Client
	scheme *runtime.Scheme
	record record.EventRecorder

	aliyun  aliyunClient.OpenAPI
	vswpool *vswitch.SwitchPool

	cache sync.Map

	fullSyncNodePeriod time.Duration
	gcPeriod           time.Duration

	tracer trace.Tracer

	eniBatchSize int

	v *viper.Viper

	// eniTaskQueue manages async ENI attach operations
	eniTaskQueue *ENITaskQueue
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

	now := time.Now()
	defer func() {
		took := time.Since(now)
		if took > 5*time.Second {
			l.Info("slow reconcile", "took", took)
		}
	}()

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

		ResourcePoolTotal.Delete(prometheus.Labels{"node": request.Name})
		SyncOpenAPITotal.Delete(prometheus.Labels{"node": request.Name})
		ReconcileLatency.Delete(prometheus.Labels{"node": request.Name})

		// Clean up async ENI tasks
		if n.eniTaskQueue != nil {
			n.eniTaskQueue.RemoveTasks(request.Name)
		}

		if changed {
			err = n.client.Patch(ctx, node, patch)
		}
		return reconcile.Result{}, err
	}

	// check if daemon has ready
	if node.Spec.NodeMetadata.InstanceID == "" ||
		node.Spec.NodeMetadata.InstanceType == "" ||
		node.Spec.NodeMetadata.RegionID == "" ||
		node.Spec.NodeMetadata.ZoneID == "" ||
		node.Spec.ENISpec == nil ||
		node.Spec.Pool == nil ||
		node.Spec.NodeCap.Adapters == 0 {
		return reconcile.Result{}, nil
	}

	if utils.ISLingJunNode(node.Labels) {
		backend := aliyunClient.BackendAPIEFLO
		switch node.Annotations[types.ENOApi] {
		case types.APIEcs:
			backend = aliyunClient.BackendAPIECS
		case types.APIEcsHDeni, types.APIEnoHDeni:
			return reconcile.Result{}, fmt.Errorf("not support high dense %s", node.Annotations[types.ENOApi])
		}
		ctx = aliyunClient.SetBackendAPI(ctx, backend)
	} else {
		ctx = aliyunClient.SetBackendAPI(ctx, aliyunClient.BackendAPIECS)
	}

	if types.NodeExclusiveENIMode(node.Labels) == types.ExclusiveENIOnly {
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
	// Reset processed tasks for this reconciliation loop
	nodeStatus.ProcessedTaskIDs = nil

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

	var errorList []error

	// initialize warm-up if needed (for new nodes or existing nodes without warm-up status)
	n.initializeWarmUp(node)

	// ensure async tasks are running for Attaching ENIs (recovery)
	n.ensureAsyncTasks(ctx, node)

	// do not block ipam

	// do not block ipam
	err = n.syncWithAPI(ctx, node)
	if err != nil {
		errorList = append(errorList, err)
		l.Error(err, "syncWithAPI error")
	}

	err = n.syncPods(ctx, podRequests, node)
	if err != nil {
		errorList = append(errorList, err)
		l.Error(err, "syncPods error")
	}

	afterStatus, err := runtime.DefaultUnstructuredConverter.ToUnstructured(node.Status.DeepCopy())
	if err != nil {
		return reconcile.Result{}, err
	}

	requeueAfter := n.requeueAfter(node)

	if !reflect.DeepEqual(beforeStatus, afterStatus) {
		span.AddEvent("update node cr")

		err = n.client.Status().Update(ctx, node)

		if err == nil && n.eniTaskQueue != nil && len(nodeStatus.ProcessedTaskIDs) > 0 {
			n.eniTaskQueue.DeleteTasks(nodeStatus.ProcessedTaskIDs)
		}

		if err != nil && nodeStatus.StatusChanged.CompareAndSwap(true, false) {
			nodeStatus.NeedSyncOpenAPI.Store(true)
		}
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{RequeueAfter: requeueAfter}, nil
	}

	return reconcile.Result{RequeueAfter: requeueAfter}, utilerrors.NewAggregate(errorList)
}

// syncWithAPI will sync all eni from openAPI. Need to re-sync with local pods.
func (n *ReconcileNode) syncWithAPI(ctx context.Context, node *networkv1beta1.Node) error {
	if !MetaCtx(ctx).NeedSyncOpenAPI.Load() {
		return nil
	}

	l := logf.FromContext(ctx).WithName("syncWithAPI")

	switch n.getDegradation() {
	case controlplane.DegradationL0, controlplane.DegradationL1:
		l.Info("degradationL0 or L1, skip syncWithAPI")
		return nil
	}

	ctx, span := n.tracer.Start(ctx, "syncWithAPI")
	defer span.End()

	// Record method latency
	startTime := time.Now()
	defer func() {
		ReconcileLatency.WithLabelValues("syncWithAPI", node.Name).Observe(float64(time.Since(startTime).Milliseconds()))
	}()

	SyncOpenAPITotal.WithLabelValues(node.Name).Inc()

	var err error
	var enis []*aliyunClient.NetworkInterface
	// all eni attached to this instance is take into count. (exclude member eni)
	opts := &aliyunClient.DescribeNetworkInterfaceOptions{
		InstanceID: &node.Spec.NodeMetadata.InstanceID,
	}
	if node.Spec.ENISpec.TagFilter != nil {
		opts.Tags = &node.Spec.ENISpec.TagFilter
	}
	enis, err = n.aliyun.DescribeNetworkInterfaceV2(ctx, opts)
	if err != nil {
		n.record.Event(node, corev1.EventTypeWarning, EventSyncOpenAPIFailed, fmt.Sprintf("Failed to describe network interfaces: %v", err))
		return err
	}

	// ignore primary eni
	enis = lo.Filter(enis, func(item *aliyunClient.NetworkInterface, index int) bool {
		return item.Type != aliyunClient.ENITypePrimary
	})

	eniIDMap := map[string]struct{}{}
	// add ip

	hasMiddleStatus := false
	for _, item := range enis {
		log := l.WithValues("eni", item.NetworkInterfaceID, "status", item.Status)

		eniIDMap[item.NetworkInterfaceID] = struct{}{}
		remote := newENIFromAPI(item)

		// Handle different ENI statuses
		switch item.Status {
		case aliyunClient.LENIStatusExecuting,
			aliyunClient.LENIStatusDeleting,
			aliyunClient.ENIStatusAttaching,
			aliyunClient.ENIStatusDetaching:
			// Middle statuses that need faster re-sync
			// Note: LENIStatusAttaching/LENIStatusDetaching have the same value as ENIStatusAttaching/ENIStatusDetaching
			hasMiddleStatus = true

		case aliyunClient.LENIStatusCreateFailed,
			aliyunClient.LENIStatusAttachFailed,
			aliyunClient.LENIStatusDetachFailed,
			aliyunClient.LENIStatusDeleteFailed:
			// EFLO terminal failure statuses - mark for deletion
			log.Info("EFLO ENI in failed state, marking for deletion")
			hasMiddleStatus = true
			remote.Status = aliyunClient.ENIStatusDeleting
		}

		crENI, ok := node.Status.NetworkInterfaces[item.NetworkInterfaceID]
		if !ok {
			log.Info("sync eni with remote, new eni added")

			// new eni, need add to local
			if node.Status.NetworkInterfaces == nil {
				node.Status.NetworkInterfaces = make(map[string]*networkv1beta1.Nic)
			}
			// we need cidr info
			vsw, err := n.vswpool.GetByID(ctx, n.aliyun.GetVPC(), remote.VSwitchID)
			if err != nil {
				return err
			}
			remote.IPv4CIDR = vsw.IPv4CIDR
			remote.IPv6CIDR = vsw.IPv6CIDR
			node.Status.NetworkInterfaces[item.NetworkInterfaceID] = remote
		} else {
			log.Info("sync eni with remote, old eni merged")
			// exist record
			// only ip is updated
			mergeIPMap(log, remote.IPv4, crENI.IPv4)
			mergeIPMap(log, remote.IPv6, crENI.IPv6)
			crENI.IPv4Prefix = mergeIPPrefixes(log, item.IPv4PrefixSets, crENI.IPv4Prefix)
			crENI.IPv6Prefix = mergeIPPrefixes(log, item.IPv6PrefixSets, crENI.IPv6Prefix)

			// nb(l1b0k): use Deleting status in cr for eni we don't wanted
			// Note: Don't overwrite Attaching status if we're managing it via queue
			// The queue will update the status when attach completes
			if crENI.Status != aliyunClient.ENIStatusDeleting &&
				crENI.Status != aliyunClient.ENIStatusAttaching {
				crENI.Status = remote.Status
			}
		}
	}

	// del eni (those eni is not attached on ecs)
	for id := range node.Status.NetworkInterfaces {
		if _, ok := eniIDMap[id]; !ok {
			// as the eni is not attached, so just delete it
			if node.Status.NetworkInterfaces[id].NetworkInterfaceType == networkv1beta1.ENITypeSecondary {
				var remote []*aliyunClient.NetworkInterface

				opts = &aliyunClient.DescribeNetworkInterfaceOptions{
					InstanceID:          &node.Spec.NodeMetadata.InstanceID,
					NetworkInterfaceIDs: &[]string{id},
				}
				if node.Spec.ENISpec.TagFilter != nil {
					opts.Tags = &node.Spec.ENISpec.TagFilter
				}
				remote, err = n.aliyun.DescribeNetworkInterfaceV2(ctx, opts)
				if err != nil {
					l.Error(err, "error get eni", "eni", id)
					continue
				}
				// ignore eni , either be attached to other instance or be deleted or ignored by tag filter
				if len(remote) > 0 {
					switch remote[0].Status {
					case aliyunClient.ENIStatusAvailable:
						l.Info("delete eni not found in remote, but in cr", "eni", id)
						err = n.aliyun.DeleteNetworkInterfaceV2(ctx, id)
					case aliyunClient.ENIStatusInUse:
						// ignore eni used by other instance
					default:
						// some middle status, wait next time
						continue
					}
				}
			}
			if err != nil {
				l.Error(err, "eni not found on remote, delete eni error", "eni", id)
			} else {
				delete(node.Status.NetworkInterfaces, id)
			}
		}
	}

	// change the ts
	if hasMiddleStatus {
		node.Status.NextSyncOpenAPITime = metav1.NewTime(time.Now().Add(wait.Jitter(30*time.Second, 1)))
		return nil
	} else {
		node.Status.NextSyncOpenAPITime = metav1.NewTime(time.Now().Add(wait.Jitter(n.fullSyncNodePeriod, 1)))
	}
	node.Status.LastSyncOpenAPITime = metav1.Now()

	MetaCtx(ctx).NeedSyncOpenAPI.Store(false)
	n.record.Event(node, corev1.EventTypeNormal, EventSyncOpenAPISuccess, fmt.Sprintf("Successfully synced %d ENIs from OpenAPI", len(enis)))
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
			PodUID:       string(pod.UID),
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

	// Record method latency
	startTime := time.Now()
	defer func() {
		ReconcileLatency.WithLabelValues("syncPods", node.Name).Observe(float64(time.Since(startTime).Milliseconds()))
	}()

	var errList []error
	l := logf.FromContext(ctx)

	// Prefix mode: the controller only ensures the desired number of IP prefixes exist on each ENI.
	// Pod↔IP allocation records are NOT stored in the Node CR (owned by daemon).
	// Regular pool management (addIP, adjustPool) is skipped entirely.
	if isPrefixMode(node) {
		l.V(2).Info("prefix mode active, skipping regular IPAM")

		// Still release stale pod references in CR (guard against unexpected leftover)
		ipv4Map, ipv6Map := buildIPMap(podsMapper, node.Status.NetworkInterfaces)
		releasePodNotFound(ctx, n.client, node.Name, podsMapper, ipv4Map, ipv6Map)

		// Ensure the right number of ENIs exist and each new ENI is created with prefix counts.
		options := getEniOptions(node)
		assignEniPrefixWithOptions(ctx, node, options, n.eniTaskQueue)
		if err := n.allocateFromOptions(ctx, node, options); err != nil {
			errList = append(errList, err)
		}

		// Sync task queue status (attach completions update the CR)
		n.syncTaskQueueStatus(ctx, node)

		// Revert expired Frozen prefixes back to Valid.
		if processFrozenExpireAt(l, node) {
			MetaCtx(ctx).StatusChanged.Store(true)
		}

		// Ensure prefix counts on already-InUse ENIs meet the desired target.
		if err := n.syncPrefixAllocation(ctx, node); err != nil {
			errList = append(errList, err)
		}
		if err := n.handleStatus(ctx, node); err != nil {
			errList = append(errList, err)
		}
		return utilerrors.NewAggregate(errList)
	}

	ipv4Map, ipv6Map := buildIPMap(podsMapper, node.Status.NetworkInterfaces)

	// 1. delete unwanted
	releasePodNotFound(ctx, n.client, node.Name, podsMapper, ipv4Map, ipv6Map)

	// 2. assign ip from local pool
	unSucceedPods := assignIPFromLocalPool(l, podsMapper, ipv4Map, ipv6Map, node.Spec.ENISpec.EnableERDMA, node)

	// 3. if there is no enough ip, try to allocate from api
	err := n.addIP(ctx, unSucceedPods, node)
	if err != nil {
		errList = append(errList, err)
	}
	// 4. after all is assigned , we can re-allocate ip
	ipv4Map, ipv6Map = buildIPMap(podsMapper, node.Status.NetworkInterfaces)
	_ = assignIPFromLocalPool(l, podsMapper, ipv4Map, ipv6Map, node.Spec.ENISpec.EnableERDMA, node)

	// 5. clean up deleting status eni and ip
	err = n.handleStatus(ctx, node)
	if err != nil {
		errList = append(errList, err)
	}

	// 6. check and mark warm-up completion
	n.checkWarmUpCompletion(node)

	// 7. pool management sort eni and find the victim
	// Always run adjustPool regardless of addIP/handleStatus errors,
	// pool scale-down and cleanup should not be blocked by allocation failures.
	err = n.adjustPool(ctx, node)
	if err != nil {
		errList = append(errList, err)
	}

	return utilerrors.NewAggregate(errList)
}

// releasePodNotFound release ip if there is no pod found
func releasePodNotFound(ctx context.Context, c client.Client, nodeName string, podsMapper map[string]*PodRequest, ipMapper ...map[string]*EniIP) {
	l := logf.FromContext(ctx)

	// NodeRuntime is created by daemon, so unless it is supported, we can't get node runtime
	daemonSupport := true
	nodeRuntime := &networkv1beta1.NodeRuntime{}
	err := c.Get(ctx, client.ObjectKey{Name: nodeName}, nodeRuntime)
	if err != nil {
		if !k8sErr.IsNotFound(err) {
			l.Error(err, "failed to get node runtime, ignore ipam release ip")
			return
		}
		// not found, try to determine by pod version
		daemonSupport = isDaemonSupportNodeRuntime(ctx, c, nodeName)
		if daemonSupport {
			l.Info("daemon support node runtime, but not found, try next time")
			return
		}
		l.Info("daemon not support node runtime")
	}
	for _, ipMap := range ipMapper {
		for _, v := range ipMap {
			if v.IP.PodID == "" {
				continue
			}
			info, ok := podsMapper[v.IP.PodID]
			if ok {
				v.IP.PodUID = info.PodUID
				continue
			}

			if v.IP.PodUID != "" && daemonSupport {
				// check cni has finished
				runtimePodStatus, ok := nodeRuntime.Status.Pods[v.IP.PodUID]
				if !ok {
					continue
				}

				status, _, ok := utils.RuntimeFinalStatus(runtimePodStatus.Status)
				if !ok || status != networkv1beta1.CNIStatusDeleted {
					continue
				}
				// we are certain ip is released
			}
			l.Info("pod released", "pod", v.IP.PodID, "ip", v.IP.IP)
			v.IP.PodID = ""
			v.IP.PodUID = ""
			if v.IP.Status == networkv1beta1.IPStatusInvalid && !v.IP.Primary {
				v.IP.Status = networkv1beta1.IPStatusDeleting
			}
		}
	}
}

// DO NOT assign pod ip if pod already has one
func assignIPFromLocalPool(log logr.Logger, podsMapper map[string]*PodRequest, ipv4Map, ipv6Map map[string]*EniIP, enableEDRMA bool, node *networkv1beta1.Node) map[string]*PodRequest {
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

	// Sort pod IDs for deterministic iteration order
	podIDs := make([]string, 0, len(pendingPods))
	for podID := range pendingPods {
		podIDs = append(podIDs, podID)
	}
	sort.Strings(podIDs)

	// handle exist pod ip
	for _, podID := range podIDs {
		info := pendingPods[podID]
		if info.RequireIPv4 && info.ipv4Ref == nil {
			if info.IPv4 != "" {
				// for take over case , pod has ip already, we can only assign to previous eni
				eniIP, ok := ipv4Map[info.IPv4]
				if ok && (eniIP.IP.PodID == "" || eniIP.IP.PodID == podID) {
					info.ipv4Ref = eniIP
					eniIP.IP.PodID = podID
					eniIP.IP.PodUID = info.PodUID
					log.Info("assign ip (from pod status)", "pod", podID, "ip", eniIP.IP, "eni", eniIP.NetworkInterface.ID)
				}
			}
		}

		if info.RequireIPv6 && info.ipv6Ref == nil {
			if info.IPv6 != "" {
				// for take over case , pod has ip already, we can only assign to previous eni
				eniIP, ok := ipv6Map[info.IPv6]
				if ok && (eniIP.IP.PodID == "" || eniIP.IP.PodID == podID) {
					info.ipv6Ref = eniIP
					eniIP.IP.PodID = podID
					eniIP.IP.PodUID = info.PodUID
					log.Info("assign ip (from pod status)", "pod", podID, "ip", eniIP.IP, "eni", eniIP.NetworkInterface.ID)
				}
			}
		}
	}

	// only pending pods is handled
	for _, podID := range podIDs {
		info := pendingPods[podID]
		// choose eni first ...
		if info.RequireIPv4 && info.ipv4Ref == nil {
			if info.IPv4 == "" {
				for _, v := range ipv4Map {
					if v.NetworkInterface.Status != aliyunClient.ENIStatusInUse {
						continue
					}

					// schedule to erdma card
					if info.RequireERDMA &&
						v.NetworkInterface.NetworkInterfaceTrafficMode != networkv1beta1.NetworkInterfaceTrafficModeHighPerformance {
						continue
					}

					// do not schedule to erdma if node has erdma enable
					if !info.RequireERDMA &&
						enableEDRMA &&
						v.NetworkInterface.NetworkInterfaceTrafficMode == networkv1beta1.NetworkInterfaceTrafficModeHighPerformance {
						continue
					}

					if v.IP.Status == networkv1beta1.IPStatusValid && v.IP.PodID == "" {
						info.ipv4Ref = &EniIP{
							NetworkInterface: v.NetworkInterface,
							IP:               v.IP,
						}
						v.IP.PodID = podID
						v.IP.PodUID = info.PodUID
						log.Info("assign ip", "pod", podID, "ip", v.IP.IP, "eni", v.NetworkInterface.ID)
						node.Status.LastModifiedTime = metav1.Now()
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
			if info.IPv6 == "" {
				for _, v := range ipv6Map {
					if v.NetworkInterface.Status != aliyunClient.ENIStatusInUse {
						continue
					}

					if info.RequireERDMA &&
						v.NetworkInterface.NetworkInterfaceTrafficMode != networkv1beta1.NetworkInterfaceTrafficModeHighPerformance {
						continue
					}

					if !info.RequireERDMA &&
						enableEDRMA &&
						v.NetworkInterface.NetworkInterfaceTrafficMode == networkv1beta1.NetworkInterfaceTrafficModeHighPerformance {
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
						v.IP.PodUID = info.PodUID
						log.Info("assign ip", "pod", podID, "ip", v.IP.IP, "eni", v.NetworkInterface.ID)
						node.Status.LastModifiedTime = metav1.Now()
						break
					}
				}
			}

			if info.ipv6Ref == nil {
				if info.IPv4 == "" && info.ipv4Ref != nil {
					log.Info("failed to get ipv6 addr, roll back ipv4", "pod", podID, "ip", info.ipv4Ref.IP)

					info.ipv4Ref.IP.PodID = ""
					info.ipv4Ref.IP.PodUID = ""
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
// syncTaskQueueStatus syncs completed tasks from queue to Node CR
func (n *ReconcileNode) syncTaskQueueStatus(ctx context.Context, node *networkv1beta1.Node) {
	if n.eniTaskQueue == nil {
		return
	}

	l := logf.FromContext(ctx).WithName("syncTaskQueueStatus")

	// Process completed tasks
	completedTasks := n.eniTaskQueue.PeekCompletedTasks(node.Name)
	if len(completedTasks) == 0 {
		return
	}

	l.Info("processing completed tasks", "count", len(completedTasks))

	for _, task := range completedTasks {
		nic, ok := node.Status.NetworkInterfaces[task.ENIID]

		if !ok {
			l.Info("ENI not found in Node CR, skip", "eni", task.ENIID)
			continue
		}

		switch task.Status {
		case TaskStatusCompleted:
			// Update ENI info from task result
			if task.ENIInfo != nil {
				nic.Status = aliyunClient.ENIStatusInUse
				nic.MacAddress = task.ENIInfo.MacAddress
				nic.SecurityGroupIDs = task.ENIInfo.SecurityGroupIDs
				nic.PrimaryIPAddress = task.ENIInfo.PrivateIPAddress
				nic.NetworkInterfaceTrafficMode = networkv1beta1.NetworkInterfaceTrafficMode(task.ENIInfo.NetworkInterfaceTrafficMode)

				// Convert IP sets using existing helper function
				nic.IPv4 = convertIPSet(task.ENIInfo.PrivateIPSets)
				nic.IPv6 = convertIPSet(task.ENIInfo.IPv6Set)

				// IMPORTANT: Update prefixes from DescribeNetworkInterface response.
				// This ensures prefixes are correctly recorded even if the CreateNetworkInterface
				// response didn't include them (API behavior can vary).
				// Only update if task.ENIInfo has prefixes; otherwise preserve existing CR data.
				if len(task.ENIInfo.IPv4PrefixSets) > 0 {
					nic.IPv4Prefix = convertPrefixesToCR(task.ENIInfo.IPv4PrefixSets)
				}
				if len(task.ENIInfo.IPv6PrefixSets) > 0 {
					nic.IPv6Prefix = convertPrefixesToCR(task.ENIInfo.IPv6PrefixSets)
				}

				// Track OpenAPI allocations for warm-up
				if !node.Status.WarmUpCompleted && node.Status.WarmUpTarget > 0 {
					node.Status.WarmUpAllocatedCount += max(len(nic.IPv4), len(nic.IPv6))
				}

				l.Info("ENI attach completed", "eni", task.ENIID,
					"ipv4Count", len(nic.IPv4), "ipv6Count", len(nic.IPv6),
					"ipv4PrefixCount", len(nic.IPv4Prefix), "ipv6PrefixCount", len(nic.IPv6Prefix))
				n.record.Event(node, corev1.EventTypeNormal, "ENIAttachSuccess",
					fmt.Sprintf("ENI %s is now ready with %d IPv4, %d IPv6, %d IPv4Prefixes, %d IPv6Prefixes",
						task.ENIID, len(nic.IPv4), len(nic.IPv6), len(nic.IPv4Prefix), len(nic.IPv6Prefix)))
			} else {
				// should not happen
				nic.Status = aliyunClient.ENIStatusDeleting
			}
		case TaskStatusFailed, TaskStatusTimeout:
			// Mark for deletion
			nic.Status = aliyunClient.ENIStatusDeleting

			errMsg := "unknown error"
			if task.Error != nil {
				errMsg = task.Error.Error()
			}

			l.Error(task.Error, "ENI attach failed", "eni", task.ENIID, "status", task.Status)
			n.record.Event(node, corev1.EventTypeWarning, "ENIAttachFailed",
				fmt.Sprintf("ENI %s attach failed: %s", task.ENIID, errMsg))
		}

		MetaCtx(ctx).StatusChanged.Store(true)
		MetaCtx(ctx).ProcessedTaskIDs = append(MetaCtx(ctx).ProcessedTaskIDs, task.ENIID)
	}
}

// staleTaskThreshold defines how long a completed task can stay in queue before being cleaned up
const staleTaskThreshold = 30 * time.Minute

// ensureAsyncTasks checks for Attaching ENIs that are not in the queue and submits them (recovery)
// It also cleans up orphaned and stale tasks to prevent task leakage
func (n *ReconcileNode) ensureAsyncTasks(ctx context.Context, node *networkv1beta1.Node) {
	if n.eniTaskQueue == nil {
		return
	}

	l := logf.FromContext(ctx)

	// Build set of valid ENI IDs from current CR status
	validENIIDs := make(map[string]struct{}, len(node.Status.NetworkInterfaces))
	for eniID := range node.Status.NetworkInterfaces {
		validENIIDs[eniID] = struct{}{}
	}

	// Clean up orphaned and stale tasks
	// - Orphaned: ENI no longer exists in CR (was deleted externally or by syncWithAPI)
	// - Stale: Completed tasks sitting in queue for more than 30 minutes without being consumed
	removedTasks := n.eniTaskQueue.CleanupStaleTasks(node.Name, validENIIDs, staleTaskThreshold)
	if len(removedTasks) > 0 {
		l.Info("cleaned up stale/orphaned tasks", "count", len(removedTasks), "enis", removedTasks)
	}

	// Submit recovery tasks for Attaching ENIs that are not in the queue
	for eniID, nic := range node.Status.NetworkInterfaces {
		if nic.Status == aliyunClient.ENIStatusAttaching {
			// Check if task exists
			if _, ok := n.eniTaskQueue.GetTaskStatus(eniID); !ok {
				// Task missing, submit recovery task
				// We don't know the original requested counts, so we start with 1 IP and 0 prefixes.
				// The task will update the ENI info from DescribeNetworkInterface after attach.
				n.eniTaskQueue.SubmitAttach(ctx, eniID, node.Spec.NodeMetadata.InstanceID, "", node.Name, 1, 1, 0, 0)
				l.Info("submitted recovery attach task", "eni", eniID)
			}
		}
	}
}

func (n *ReconcileNode) addIP(ctx context.Context, unSucceedPods map[string]*PodRequest, node *networkv1beta1.Node) error {
	ctx, span := n.tracer.Start(ctx, "addIP")
	defer span.End()

	// Record method latency
	startTime := time.Now()
	defer func() {
		ReconcileLatency.WithLabelValues("addIP", node.Name).Observe(float64(time.Since(startTime).Milliseconds()))
	}()

	// 1. Sync completed tasks from queue to Node CR
	n.syncTaskQueueStatus(ctx, node)

	if n.getDegradation() == controlplane.DegradationL0 {
		logr.FromContextOrDiscard(ctx).Info("degradation to L0, skip addIP")
		return nil
	}

	normalPods := lo.PickBy(unSucceedPods, func(key string, value *PodRequest) bool {
		return !value.RequireERDMA
	})
	rdmaPods := lo.PickBy(unSucceedPods, func(key string, value *PodRequest) bool {
		return value.RequireERDMA
	})

	// before create eni , we need to check the quota
	options := getEniOptions(node)

	// Step 1: Count total idle IPs in ALL valid ENIs (regardless of vSwitch status)
	// This is used for MinPoolSize check - we check the final pool state
	totalIdleIPs := countTotalIdleIPs(node)

	// Step 2: Pod demand (IPs needed by pods that couldn't get from local pool)
	podDemand := len(normalPods)

	// Step 3: Calculate MinPoolSize demand
	// MinPoolSize = minimum idle IPs to maintain in pool AFTER serving pods
	// After serving pods, remaining idle = max(0, totalIdleIPs - podDemand)
	// Additional needed = max(0, MinPoolSize - (totalIdleIPs - podDemand))
	//                   = max(0, MinPoolSize + podDemand - totalIdleIPs)
	minPoolDemand := max(0, node.Spec.Pool.MinPoolSize+podDemand-totalIdleIPs)

	// Step 4: Calculate WarmUp demand (one-time, tracked by allocation count)
	// WarmUpTarget is independent of MinPoolSize, tracked via WarmUpAllocatedCount
	warmUpDemand := 0
	if n.shouldPerformWarmUp(node) {
		warmUpDemand = max(0, node.Status.WarmUpTarget-node.Status.WarmUpAllocatedCount)
		logr.FromContextOrDiscard(ctx).Info("warm up calculation",
			"warmUpTarget", node.Status.WarmUpTarget,
			"warmUpAllocatedCount", node.Status.WarmUpAllocatedCount,
			"warmUpDemand", warmUpDemand)
	}

	// Step 5: Total allocation demand
	// - minPoolDemand ensures steady-state pool size
	// - warmUpDemand is one-time initial allocation
	// Take max because they serve similar purposes (pre-allocation)
	// Add podDemand for immediate pod needs
	allocationDemand := podDemand + max(minPoolDemand, warmUpDemand)

	logr.FromContextOrDiscard(ctx).Info("addIP demand calculation",
		"podDemand", podDemand,
		"totalIdleIPs", totalIdleIPs,
		"minPoolSize", node.Spec.Pool.MinPoolSize,
		"minPoolDemand", minPoolDemand,
		"warmUpDemand", warmUpDemand,
		"allocationDemand", allocationDemand)

	// handle trunk/secondary eni
	assignEniWithOptions(ctx, node, allocationDemand, options, n.eniTaskQueue, func(option *eniOptions) bool {
		return n.validateENI(ctx, option, []eniTypeKey{secondaryKey, trunkKey})
	})
	assignEniWithOptions(ctx, node, len(rdmaPods), options, n.eniTaskQueue, func(option *eniOptions) bool {
		return n.validateENI(ctx, option, []eniTypeKey{rdmaKey})
	})

	err := n.allocateFromOptions(ctx, node, options)

	// update node condition based on eni status
	updateNodeCondition(ctx, n.client, node.Name, options)

	updateCrCondition(options)

	if err != nil {
		n.record.Event(node, corev1.EventTypeWarning, EventAllocIPFailed, err.Error())
	}

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
						Message:      fmt.Sprintf("%s ip is not enough", item.eniRef.VSwitchID),
					}
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
		if k8sErr.IsNotFound(err) {
			return
		}
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
		// 1. eni is full
		if item.isFull {
			return
		}

		// 2. ip not enough
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
		switch option.eniRef.Status {
		case aliyunClient.ENIStatusInUse, aliyunClient.ENIStatusAttaching:
		default:
			return false
		}

		vsw, err := n.vswpool.GetByID(ctx, n.aliyun.GetVPC(), option.eniRef.VSwitchID)
		if err != nil {
			logf.FromContext(ctx).Error(err, "failed to get vsw")
			return false
		}

		if vsw.AvailableIPCount <= 0 {
			option.errors = append(option.errors, vswitch.ErrNoAvailableVSwitch)
			return false
		}

		return true
	}

	return true
}

// assignEniWithOptions determine how many ip should be added to this eni.
// In dual stack, ip on eni is automatically balanced.
func assignEniWithOptions(ctx context.Context, node *networkv1beta1.Node, toAdd int, options []*eniOptions, taskQueue *ENITaskQueue, filterFunc func(option *eniOptions) bool) {
	l := logf.FromContext(ctx)
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
			// For Attaching status ENI, check requested IP count from task queue
			if option.eniRef.Status == aliyunClient.ENIStatusAttaching {
				task, found := taskQueue.GetTaskStatus(option.eniRef.ID)
				if found {
					// Requested IP counts from the task
					requestedIPv4 := task.RequestedIPv4Count
					requestedIPv6 := task.RequestedIPv6Count

					l.V(4).Info("found Attaching ENI in task queue",
						"eni", option.eniRef.ID,
						"requestedIPv4", requestedIPv4,
						"requestedIPv6", requestedIPv6,
						"toAddIPv4", toAddIPv4,
						"toAddIPv6", toAddIPv6)

					// If requested count >= needed count, no need to request more
					if toAddIPv4 > 0 && requestedIPv4 >= toAddIPv4 {
						toAddIPv4 = 0
					} else if toAddIPv4 > 0 {
						// Partially satisfied, subtract already requested
						toAddIPv4 -= requestedIPv4
					}

					if toAddIPv6 > 0 && requestedIPv6 >= toAddIPv6 {
						toAddIPv6 = 0
					} else if toAddIPv6 > 0 {
						toAddIPv6 -= requestedIPv6
					}
					// for not found , this may happen when controller is restarted ,
				}
				// Clear addIP request for Attaching ENI (can't add IPs to Attaching ENI)
				option.addIPv4N = 0
				option.addIPv6N = 0
				continue // Skip Attaching status ENI
			}

			// For InUse status ENI, allocate IPs without subtracting existing idle IPs
			// Note: The demand calculation in addIP already accounts for ALL idle IPs,
			// so we should NOT subtract allocatable IPs here to avoid double-counting
			// IMPORTANT: ECS limits the combined count of (IPs + prefixes) to IPv4PerAdapter,
			// so we must account for existing prefixes in the quota calculation.
			if toAddIPv4 > 0 {
				leftQuota := node.Spec.NodeCap.IPv4PerAdapter - len(option.eniRef.IPv4) - len(option.eniRef.IPv4Prefix)
				if leftQuota > 0 {
					option.addIPv4N = min(leftQuota, toAddIPv4, batchSize(ctx))
					toAddIPv4 -= option.addIPv4N
				} else {
					option.isFull = true
				}
			}

			if toAddIPv6 > 0 {
				leftQuota := node.Spec.NodeCap.IPv6PerAdapter - len(option.eniRef.IPv6) - len(option.eniRef.IPv6Prefix)
				if leftQuota > 0 {
					option.addIPv6N = min(leftQuota, toAddIPv6, batchSize(ctx))
					toAddIPv6 -= option.addIPv6N
				} else {
					option.isFull = true
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
				option.addIPv4N = min(node.Spec.NodeCap.IPv4PerAdapter, toAddIPv4, batchSize(ctx))
				toAddIPv4 -= option.addIPv4N
			}
			if toAddIPv6 > 0 {
				option.addIPv6N = min(node.Spec.NodeCap.IPv6PerAdapter, toAddIPv6, batchSize(ctx))
				toAddIPv6 -= option.addIPv6N
			}
		}
	}
}

// assignEniPrefixWithOptions sets the prefix counts on new (not-yet-created) ENI options for prefix mode.
// For existing InUse ENIs, syncPrefixAllocation handles prefix allocation separately.
// ECS allows mixing prefix and secondary IP on the same ENI; the only constraint is that
// the combined count (primary IP + secondary IPs + prefixes) must not exceed IPv4PerAdapter.
//
// IMPORTANT: This function checks if existing InUse ENIs have capacity to accommodate the
// remaining prefix demand. If they do, we skip creating new ENIs and let syncPrefixAllocation
// add prefixes to existing ENIs. This prevents unnecessary ENI creation when prefixes can be
// added to existing ENIs (e.g., when prefix mode is enabled on a node that already has ENIs).
//
// IPv6 prefix allocation:
//   - Dual-stack: each ENI automatically gets exactly 1 IPv6 prefix. The user does NOT
//     configure ipv6_prefix_count; the controller manages this implicitly.
//   - IPv6-only: uses IPv6PrefixCount (valid values: 0 or 1).
func assignEniPrefixWithOptions(ctx context.Context, node *networkv1beta1.Node, options []*eniOptions, taskQueue *ENITaskQueue) {
	l := logf.FromContext(ctx)
	eniSpec := node.Spec.ENISpec

	isDualStack := eniSpec.EnableIPv4 && eniSpec.EnableIPv6

	// Get IPv4 desired prefix count
	totalDesiredV4Prefixes := eniSpec.IPv4PrefixCount

	// Calculate prefix capacity for new ENI:
	// - IPv4 ENI always has a primary IP which occupies 1 slot
	// - So the effective prefix capacity is IPv4PerAdapter - 1
	prefixCapForNewENI := node.Spec.NodeCap.IPv4PerAdapter - 1
	if prefixCapForNewENI <= 0 {
		prefixCapForNewENI = 1 // Safety: ensure at least 1 prefix can be allocated
	}

	// Count existing ENIs and their state
	existingENICount := 0
	for _, option := range options {
		if option.eniRef != nil && option.eniRef.Status == aliyunClient.ENIStatusInUse {
			existingENICount++
		}
	}

	// Calculate how many prefixes already exist on InUse ENIs and their available capacity
	existingV4Prefixes := 0
	existingV6Prefixes := 0
	existingV4Capacity := 0 // Available slots on existing InUse ENIs for adding more prefixes
	// Count how many InUse ENIs already have at least one IPv6 prefix
	inUseENIsWithV6Prefix := 0
	for _, option := range options {
		if option.eniRef == nil {
			continue
		}
		if option.eniRef.Status == aliyunClient.ENIStatusInUse {
			existingV4Prefixes += countValidPrefixes(option.eniRef.IPv4Prefix)
			v6PrefixCount := countValidPrefixes(option.eniRef.IPv6Prefix)
			existingV6Prefixes += v6PrefixCount
			if v6PrefixCount > 0 {
				inUseENIsWithV6Prefix++
			}
			// Calculate available capacity: IPv4PerAdapter - (IPs + prefixes)
			// Each ENI has limited slots shared between IPs and prefixes
			v4UsedSlots := len(option.eniRef.IPv4) + len(option.eniRef.IPv4Prefix)
			existingV4Capacity += max(0, node.Spec.NodeCap.IPv4PerAdapter-v4UsedSlots)
		}
	}

	// Calculate how many prefixes are pending in Attaching ENIs.
	// Priority: Use task queue's requested prefix count, as the CreateNetworkInterface API
	// might not return the actual prefixes in its response (they're only available after attach).
	// Fall back to ENI's IPv4Prefix field if task doesn't exist.
	pendingV4Prefixes := 0
	pendingV6Prefixes := 0
	attachingENIsWithV6Prefix := 0
	for _, option := range options {
		if option.eniRef == nil {
			continue
		}
		if option.eniRef.Status == aliyunClient.ENIStatusAttaching {
			// First, try to get the requested prefix count from the task queue
			if taskQueue != nil {
				if task, ok := taskQueue.GetTaskStatus(option.eniRef.ID); ok {
					pendingV4Prefixes += task.RequestedIPv4PrefixCount
					pendingV6Prefixes += task.RequestedIPv6PrefixCount
					if task.RequestedIPv6PrefixCount > 0 {
						attachingENIsWithV6Prefix++
					}
					continue
				}
			}
			// Fall back to counting prefixes from the ENI's CR data
			pendingV4Prefixes += countValidPrefixes(option.eniRef.IPv4Prefix)
			v6PrefixCount := countValidPrefixes(option.eniRef.IPv6Prefix)
			pendingV6Prefixes += v6PrefixCount
			if v6PrefixCount > 0 {
				attachingENIsWithV6Prefix++
			}
		}
	}

	// Calculate remaining IPv4 demand
	remainingV4Demand := max(0, totalDesiredV4Prefixes-existingV4Prefixes-pendingV4Prefixes)

	// IPv6 demand depends on the stack mode:
	//   - Dual-stack: not driven by IPv6PrefixCount; handled as "1 per ENI" below.
	//   - IPv6-only:  uses IPv6PrefixCount (0 or 1).
	totalDesiredV6Prefixes := 0
	if !isDualStack {
		totalDesiredV6Prefixes = eniSpec.IPv6PrefixCount
	}
	remainingV6Demand := max(0, totalDesiredV6Prefixes-existingV6Prefixes-pendingV6Prefixes)

	l.V(4).Info("prefix mode: calculating prefix demand",
		"dualStack", isDualStack,
		"totalDesiredV4", totalDesiredV4Prefixes, "totalDesiredV6", totalDesiredV6Prefixes,
		"existingV4", existingV4Prefixes, "pendingV4", pendingV4Prefixes, "remainingV4", remainingV4Demand,
		"existingV6", existingV6Prefixes, "pendingV6", pendingV6Prefixes, "remainingV6", remainingV6Demand,
		"existingV4Capacity", existingV4Capacity)

	// Check if existing InUse ENIs have enough capacity for remaining demand.
	// If so, skip creating new ENIs - syncPrefixAllocation will add prefixes to existing ENIs.
	// This prevents creating unnecessary ENIs when prefix mode is enabled on nodes that
	// already have ENIs (e.g., migrating from non-prefix to prefix mode).
	v4CanUseExisting := remainingV4Demand <= existingV4Capacity || !eniSpec.EnableIPv4
	// For IPv6 in dual-stack, there is no count-based demand; syncPrefixAllocation will
	// ensure each InUse ENI has exactly 1 IPv6 prefix, so we don't block new ENI creation.
	v6CanUseExisting := true
	if !isDualStack && eniSpec.EnableIPv6 {
		// IPv6-only: check if enough ENI slots exist for the remaining prefix demand.
		enisSlotsForV6 := existingENICount - inUseENIsWithV6Prefix - attachingENIsWithV6Prefix
		v6CanUseExisting = remainingV6Demand <= enisSlotsForV6
	}
	if v4CanUseExisting && v6CanUseExisting {
		l.V(4).Info("prefix mode: existing ENIs have enough capacity, skipping new ENI creation",
			"remainingV4Demand", remainingV4Demand, "existingV4Capacity", existingV4Capacity,
			"remainingV6Demand", remainingV6Demand)
		return
	}

	// Existing ENIs don't have enough capacity. Calculate how much additional capacity we need
	// from new ENIs, accounting for what existing ENIs can provide.
	v4DemandForNewENIs := max(0, remainingV4Demand-existingV4Capacity)
	v6DemandForNewENIs := 0
	if !isDualStack && eniSpec.EnableIPv6 {
		enisSlotsForV6 := existingENICount - inUseENIsWithV6Prefix - attachingENIsWithV6Prefix
		v6DemandForNewENIs = max(0, remainingV6Demand-enisSlotsForV6)
	}

	l.V(4).Info("prefix mode: need new ENIs for additional capacity",
		"v4DemandForNewENIs", v4DemandForNewENIs, "v6DemandForNewENIs", v6DemandForNewENIs)

	// Assign prefixes to new ENI slots only for the demand that exceeds existing capacity
	for _, option := range options {
		if option.eniRef != nil {
			// Existing ENI: syncPrefixAllocation handles it; skip here.
			continue
		}

		if v4DemandForNewENIs <= 0 && v6DemandForNewENIs <= 0 {
			break
		}

		// Calculate how many prefixes this new ENI should request
		// Note: For new ENI, 1 slot is reserved for primary IP (IPv4)
		// Also respect the API limit of maxPrefixPerAPICall (10) per CreateNetworkInterface call
		if eniSpec.EnableIPv4 && v4DemandForNewENIs > 0 {
			option.addIPv4PrefixN = min(v4DemandForNewENIs, prefixCapForNewENI, maxPrefixPerAPICall)
			v4DemandForNewENIs -= option.addIPv4PrefixN
		}

		if isDualStack {
			// Dual-stack: every new ENI that carries IPv4 prefixes gets exactly 1 IPv6 prefix.
			if option.addIPv4PrefixN > 0 {
				option.addIPv6PrefixN = 1
			}
		} else if eniSpec.EnableIPv6 && v6DemandForNewENIs > 0 {
			// IPv6-only: each ENI gets at most 1 prefix
			option.addIPv6PrefixN = 1
			v6DemandForNewENIs--
		}

		l.V(4).Info("prefix mode: new ENI will be created with prefix counts",
			"ipv4PrefixCount", option.addIPv4PrefixN, "ipv6PrefixCount", option.addIPv6PrefixN,
			"v4DemandForNewENIs", v4DemandForNewENIs, "v6DemandForNewENIs", v6DemandForNewENIs)
	}
}

// allocateFromOptions add ip or create eni, based on the options
func (n *ReconcileNode) allocateFromOptions(ctx context.Context, node *networkv1beta1.Node, options []*eniOptions) error {
	ctx, span := n.tracer.Start(ctx, "allocateFromOptions")
	defer span.End()

	l := logf.FromContext(ctx).WithName("allocateFromOptions")

	wg := wait.Group{}
	lock := sync.Mutex{}
	var errs []error

	inFlight := 0

	createBatchSize := min(n.eniBatchSize, 1)
	if isEFLO(ctx) {
		createBatchSize = efloMaxConcurrentAttach
	}

	// Get current attaching count from task queue
	currentAttachingCount := 0
	if n.eniTaskQueue != nil {
		currentAttachingCount = n.eniTaskQueue.GetAttachingCount(node.Name)
	}

	// Calculate available slots for new ENI attach operations
	availableSlots := createBatchSize - currentAttachingCount
	if availableSlots <= 0 {
		l.Info("max concurrent attach limit reached, skip creating new ENIs",
			"maxConcurrent", createBatchSize, "currentAttaching", currentAttachingCount)
		availableSlots = 0
	}

	// Track how many new ENI create operations we've submitted
	newENICreateCount := 0

	for _, option := range options {
		if option.addIPv6N <= 0 && option.addIPv4N <= 0 && option.addIPv4PrefixN <= 0 && option.addIPv6PrefixN <= 0 {
			continue
		}
		if inFlight >= createBatchSize {
			continue
		}

		// For new ENI creation (async attach), check concurrent limit
		if option.eniRef == nil {
			if newENICreateCount >= availableSlots {
				l.V(4).Info("skip creating new ENI due to concurrent attach limit",
					"maxConcurrent", createBatchSize, "currentAttaching", currentAttachingCount,
					"newENICreateCount", newENICreateCount)
				continue
			}
			newENICreateCount++
		}

		node.Status.LastModifiedTime = metav1.Now()

		inFlight++
		opt := option
		// start a gr for each eni
		wg.StartWithContext(ctx, func(ctx context.Context) {
			var err error
			if opt.eniRef == nil {
				err = n.createENI(ctx, node, opt)
			} else {
				// for exists enis
				err = n.assignIP(ctx, node, opt)
			}

			if err != nil {
				opt.errors = append(opt.errors, err)

				if apiErr.ErrorCodeIs(err, apiErr.ErrEniPerInstanceLimitExceeded, apiErr.ErrIPv4CountExceeded, apiErr.ErrIPv6CountExceeded) ||
					apiErr.IsEfloCode(err, apiErr.ErrEfloPrivateIPQuotaExecuted) {
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

func (n *ReconcileNode) handleStatus(ctx context.Context, node *networkv1beta1.Node) error {
	ctx, span := n.tracer.Start(ctx, "handleStatus")
	defer span.End()

	// Record method latency
	startTime := time.Now()
	defer func() {
		ReconcileLatency.WithLabelValues("handleStatus", node.Name).Observe(float64(time.Since(startTime).Milliseconds()))
	}()

	l := logf.FromContext(ctx).WithName("handleStatus")
	// 1. clean up deleting status eni and ip
	for _, eni := range node.Status.NetworkInterfaces {
		// we use ENIStatusDeleting as the eni is not needed, so we always need to detach it and then delete it
		log := l.WithValues("eni", eni.ID, "status", eni.Status)

		switch eni.Status {
		case aliyunClient.LENIStatusCreateFailed,
			aliyunClient.LENIStatusAttachFailed,
			aliyunClient.LENIStatusDetachFailed,
			aliyunClient.LENIStatusDeleteFailed:
			// EFLO failed statuses - directly delete
			if !isEFLO(ctx) {
				log.Info("non-EFLO ENI with LENI failed status, skipping")
				continue
			}

			log.Info("cleaning up EFLO ENI in failed state")
			//	 mark as deleting
			eni.Status = aliyunClient.ENIStatusDeleting

		case aliyunClient.ENIStatusDeleting, aliyunClient.ENIStatusDetaching:
			if !isEFLO(ctx) {
				err := n.aliyun.GetECS().DetachNetworkInterface(ctx, eni.ID, node.Spec.NodeMetadata.InstanceID, "")
				if err != nil {
					log.Error(err, "run gc failed")
					continue
				}
				_, err = n.aliyun.WaitForNetworkInterfaceV2(ctx, eni.ID, aliyunClient.ENIStatusAvailable, backoff.Backoff(backoff.WaitENIStatus).Backoff, true)
				if err != nil {
					if !errors.Is(err, apiErr.ErrNotFound) {
						log.Error(err, "run gc failed")
						continue
					}
				}
			}

			// wait eni detached
			err := n.aliyun.DeleteNetworkInterfaceV2(ctx, eni.ID)
			if err != nil {
				n.record.Event(node, corev1.EventTypeWarning, types.EventDeleteENIFailed,
					fmt.Sprintf("Failed to delete ENI %s: %v", eni.ID, err))
				log.Error(err, "run gc failed")
				continue
			}
			MetaCtx(ctx).StatusChanged.Store(true)

			n.record.Event(node, corev1.EventTypeNormal, types.EventDeleteENISucceed,
				fmt.Sprintf("Successfully deleted ENI %s", eni.ID))
			log.Info("run gc succeed, eni removed", "eni", eni.ID)
			// remove from status
			delete(node.Status.NetworkInterfaces, eni.ID)
		case aliyunClient.ENIStatusInUse:
			var waitTime time.Duration

			ipv4s := make([]aliyunClient.IPSet, 0)
			for _, ip := range eni.IPv4 {
				if ip.Status == networkv1beta1.IPStatusDeleting {
					ipv4s = append(ipv4s, aliyunClient.IPSet{
						Primary:   false,
						IPAddress: ip.IP,
						IPName:    ip.IPName,
					})
				}
			}
			ipv4s = lo.Subset(ipv4s, 0, uint(batchSize(ctx)))
			if len(ipv4s) > 0 {
				err := n.aliyun.UnAssignPrivateIPAddressesV2(ctx, eni.ID, ipv4s)
				if err != nil {
					continue
				}

				MetaCtx(ctx).StatusChanged.Store(true)

				waitTime = 1 * time.Second
			}

			ipv6s := make([]aliyunClient.IPSet, 0)
			for _, ip := range eni.IPv6 {
				if ip.Status == networkv1beta1.IPStatusDeleting {
					ipv6s = append(ipv6s, aliyunClient.IPSet{
						Primary:   false,
						IPAddress: ip.IP,
						IPName:    ip.IPName,
					})
				}
			}
			ipv6s = lo.Subset(ipv6s, 0, uint(batchSize(ctx)))
			if len(ipv6s) > 0 {
				if waitTime > 0 {
					time.Sleep(waitTime)
				}

				err := n.aliyun.UnAssignIpv6AddressesV2(ctx, eni.ID, ipv6s)
				if err != nil {
					continue
				}

				MetaCtx(ctx).StatusChanged.Store(true)
			}

			// Unassign IPv4 prefixes marked for deletion.
			ipv4Prefixes := make([]aliyunClient.IPSet, 0)
			for i := range eni.IPv4Prefix {
				if eni.IPv4Prefix[i].Status == networkv1beta1.IPPrefixStatusDeleting {
					ipv4Prefixes = append(ipv4Prefixes, aliyunClient.IPSet{
						Prefix: aliyunClient.Prefix(eni.IPv4Prefix[i].Prefix),
					})
				}
			}
			ipv4Prefixes = lo.Subset(ipv4Prefixes, 0, uint(batchSize(ctx)))
			if len(ipv4Prefixes) > 0 {
				if waitTime > 0 {
					time.Sleep(waitTime)
				}
				err := n.aliyun.UnAssignPrivateIPAddressesV2(ctx, eni.ID, ipv4Prefixes)
				if err != nil {
					log.Error(err, "failed to unassign IPv4 prefixes", "eni", eni.ID)
					continue
				}
				MetaCtx(ctx).StatusChanged.Store(true)
				// Force a full API sync on next reconcile so the Node CR reflects
				// the released prefixes and avoids IpPrefixNotEnough errors.
				MetaCtx(ctx).NeedSyncOpenAPI.Store(true)
				waitTime = 1 * time.Second
			}

			// Unassign IPv6 prefixes marked for deletion.
			ipv6Prefixes := make([]aliyunClient.IPSet, 0)
			for i := range eni.IPv6Prefix {
				if eni.IPv6Prefix[i].Status == networkv1beta1.IPPrefixStatusDeleting {
					ipv6Prefixes = append(ipv6Prefixes, aliyunClient.IPSet{
						Prefix: aliyunClient.Prefix(eni.IPv6Prefix[i].Prefix),
					})
				}
			}
			ipv6Prefixes = lo.Subset(ipv6Prefixes, 0, uint(batchSize(ctx)))
			if len(ipv6Prefixes) > 0 {
				if waitTime > 0 {
					time.Sleep(waitTime)
				}
				err := n.aliyun.UnAssignIpv6AddressesV2(ctx, eni.ID, ipv6Prefixes)
				if err != nil {
					log.Error(err, "failed to unassign IPv6 prefixes", "eni", eni.ID)
					continue
				}
				MetaCtx(ctx).StatusChanged.Store(true)
				// Force a full API sync on next reconcile so the Node CR reflects
				// the released prefixes and avoids IpPrefixNotEnough errors.
				MetaCtx(ctx).NeedSyncOpenAPI.Store(true)
				waitTime = 1 * time.Second
			}

			err := n.waitIPGone(ctx, eni, ipv4s, ipv6s)
			if err != nil {
				log.Error(err, "run gc failed")
				continue
			}
		default:
			// nb(l1b0k): there is noway to reach here
			log.Info("unexpected status, skip eni", "eni", eni.ID, "status", eni.Status)
		}
	}

	return nil
}

func (n *ReconcileNode) adjustPool(ctx context.Context, node *networkv1beta1.Node) error {
	l := logf.FromContext(ctx).WithName("adjustPool")

	gcPeriod := n.poolSyncPeriod(node.Spec.Pool.PoolSyncPeriod)

	if MetaCtx(ctx).LastGCTime.Add(gcPeriod).After(time.Now()) {
		l.V(4).Info("skip adjustPool", "lastGCTime", MetaCtx(ctx).LastGCTime)
		return nil
	}

	switch n.getDegradation() {
	case controlplane.DegradationL0, controlplane.DegradationL1:
		l.Info("degradationL0 or L1, skip adjustPool")
		return nil
	}

	ctx, span := n.tracer.Start(ctx, "adjustPool")
	defer span.End()

	// Record method latency
	startTime := time.Now()
	defer func() {
		ReconcileLatency.WithLabelValues("adjustPool", node.Name).Observe(float64(time.Since(startTime).Milliseconds()))
	}()

	ResourcePoolTotal.WithLabelValues(node.Name).Inc()

	MetaCtx(ctx).LastGCTime = time.Now()

	toDel := calculateToDel(ctx, node)

	if toDel > 0 {
		l.Info("to delete ip", "toDel", toDel)
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

	// Record method latency
	startTime := time.Now()
	defer func() {
		ReconcileLatency.WithLabelValues("createENI", node.Name).Observe(float64(time.Since(startTime).Milliseconds()))
	}()

	l := logf.FromContext(ctx)

	// for new eni
	typeOption, ok := EniOptions[opt.eniTypeKey]
	if !ok {
		return fmt.Errorf("failed to find eni type, %v", opt.eniTypeKey)
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	vsw, err := n.vswpool.GetOne(ctx, n.aliyun.GetVPC(), node.Spec.NodeMetadata.ZoneID, node.Spec.ENISpec.VSwitchOptions, &vswitch.SelectOptions{
		VSwitchSelectPolicy: vswitch.SelectionPolicy(node.Spec.ENISpec.VSwitchSelectPolicy),
	})
	if err != nil {
		return err
	}
	bo := backoff.Backoff(backoff.ENICreate)

	tags := make(map[string]string, len(node.Spec.ENISpec.Tag))
	for k, v := range node.Spec.ENISpec.Tag {
		tags[k] = v
	}

	// keep the ds behave
	tags[types.NetworkInterfaceTagCreatorKey] = types.NetworkInterfaceTagCreatorValue

	// Prefix count and private IP count are mutually exclusive per the ECS API.
	// Use prefix counts when in prefix mode; otherwise use regular IP counts.
	ipv4Count := opt.addIPv4N
	ipv6Count := opt.addIPv6N
	ipv4PrefixCount := opt.addIPv4PrefixN
	ipv6PrefixCount := opt.addIPv6PrefixN
	if ipv4PrefixCount > 0 {
		ipv4Count = 0
	}
	if ipv6PrefixCount > 0 {
		ipv6Count = 0
	}

	createOpts := &aliyunClient.CreateNetworkInterfaceOptions{
		NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
			VSwitchID:        vsw.ID,
			SecurityGroupIDs: node.Spec.ENISpec.SecurityGroupIDs,
			ResourceGroupID:  node.Spec.ENISpec.ResourceGroupID,
			Tags:             tags,
			IPCount:          ipv4Count,
			IPv6Count:        ipv6Count,
			IPv4PrefixCount:  ipv4PrefixCount,
			IPv6PrefixCount:  ipv6PrefixCount,
			SourceDestCheck:  ptr.To(false),
			ZoneID:           node.Spec.NodeMetadata.ZoneID, // eflo
			InstanceID:       node.Spec.NodeMetadata.InstanceID,
		},

		Backoff: &bo.Backoff,
	}

	// 1. Create ENI via OpenAPI
	result, err := n.aliyun.CreateNetworkInterfaceV2(ctx, typeOption, createOpts)
	if err != nil {
		if apiErr.ErrorCodeIs(err, apiErr.InvalidVSwitchIDIPNotEnough, apiErr.QuotaExceededPrivateIPAddress) {
			// block
			n.vswpool.Block(createOpts.NetworkInterfaceOptions.VSwitchID)
		}
		n.record.Event(node, corev1.EventTypeWarning, types.EventCreateENIFailed,
			fmt.Sprintf("Failed to create ENI type=%s vsw=%s: %v", opt.eniTypeKey.ENIType, vsw.ID, err))
		return err
	}

	l.Info("ENI created, submitting async attach task", "eni", result.NetworkInterfaceID,
		"requestedIPv4", ipv4Count, "requestedIPv6", ipv6Count,
		"requestedIPv4Prefix", ipv4PrefixCount, "requestedIPv6Prefix", ipv6PrefixCount)

	// 2. Submit attach task to queue (async, non-blocking)
	// This never fails - it only adds a task to in-memory queue
	n.eniTaskQueue.SubmitAttach(ctx,
		result.NetworkInterfaceID,
		node.Spec.NodeMetadata.InstanceID,
		"", // trunkENIID - not used for secondary ENI
		node.Name,
		ipv4Count,
		ipv6Count,
		ipv4PrefixCount,
		ipv6PrefixCount)

	// 3. Mark ENI as Attaching in Node CR
	// IMPORTANT: Record prefixes from the API response so that subsequent reconciles
	// can count them as "pending" and avoid creating duplicate ENIs.
	networkInterface := &networkv1beta1.Nic{
		ID:                          result.NetworkInterfaceID,
		Status:                      aliyunClient.ENIStatusAttaching,
		VSwitchID:                   vsw.ID,
		IPv4CIDR:                    vsw.IPv4CIDR,
		IPv6CIDR:                    vsw.IPv6CIDR,
		NetworkInterfaceType:        networkv1beta1.ENIType(result.Type),
		NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficMode(result.NetworkInterfaceTrafficMode),
		IPv4Prefix:                  convertPrefixesToCR(result.IPv4PrefixSets),
		IPv6Prefix:                  convertPrefixesToCR(result.IPv6PrefixSets),
	}

	MetaCtx(ctx).Mutex.Lock()
	if node.Status.NetworkInterfaces == nil {
		node.Status.NetworkInterfaces = make(map[string]*networkv1beta1.Nic)
	}
	node.Status.NetworkInterfaces[result.NetworkInterfaceID] = networkInterface
	MetaCtx(ctx).Mutex.Unlock()

	MetaCtx(ctx).StatusChanged.Store(true)

	n.record.Event(node, corev1.EventTypeNormal, types.EventCreateENISucceed,
		fmt.Sprintf("ENI %s created (IPv4=%d IPv4Prefix=%d), attach in progress",
			result.NetworkInterfaceID, ipv4Count, ipv4PrefixCount))

	// Return immediately, don't block waiting for attach
	return nil
}

func (n *ReconcileNode) assignIP(ctx context.Context, node *networkv1beta1.Node, opt *eniOptions) error {
	ctx, span := n.tracer.Start(ctx, "assignIP", trace.WithAttributes(attribute.String("eni", opt.eniRef.ID), attribute.Int("ipv4", opt.addIPv4N), attribute.Int("ipv6", opt.addIPv6N)))
	defer span.End()

	// Record method latency
	startTime := time.Now()
	defer func() {
		ReconcileLatency.WithLabelValues("assignIP", node.Name).Observe(float64(time.Since(startTime).Milliseconds()))
	}()

	// nb(l1b0k): ENi does not support assigning both IPv4 and IPv6 simultaneously.
	if opt.addIPv4N > 0 {
		MetaCtx(ctx).Mutex.Lock()
		if opt.eniRef.IPv4 == nil {
			opt.eniRef.IPv4 = make(map[string]*networkv1beta1.IP)
		}
		MetaCtx(ctx).Mutex.Unlock()

		bo := backoff.Backoff(backoff.ENIIPOps)
		result, err := n.aliyun.AssignPrivateIPAddressV2(ctx, &aliyunClient.AssignPrivateIPAddressOptions{
			NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
				NetworkInterfaceID: opt.eniRef.ID,
				IPCount:            opt.addIPv4N,
			},
			Backoff: &bo.Backoff,
		})

		if err != nil {
			if apiErr.ErrorCodeIs(err, apiErr.InvalidVSwitchIDIPNotEnough, apiErr.QuotaExceededPrivateIPAddress) {
				// block
				n.vswpool.Block(opt.eniRef.VSwitchID)
			}

			MetaCtx(ctx).Mutex.Lock()
			lo.ForEach(result, func(item aliyunClient.IPSet, index int) {
				if item.IPName != "" {
					addIPToMap(opt.eniRef.IPv4, &networkv1beta1.IP{
						IPName: item.IPName,
						Status: networkv1beta1.IPStatusDeleting,
					})
				}
			})

			MetaCtx(ctx).Mutex.Unlock()

			return err
		} else {
			MetaCtx(ctx).StatusChanged.Store(true)

			MetaCtx(ctx).Mutex.Lock()
			for _, ip := range result {
				addIPToMap(opt.eniRef.IPv4, &networkv1beta1.IP{
					IPName: ip.IPName,
					IP:     ip.IPAddress,
					Status: networkv1beta1.IPStatusValid,
				})
			}

			// Track OpenAPI allocations for warm-up
			if !node.Status.WarmUpCompleted && node.Status.WarmUpTarget > 0 {
				node.Status.WarmUpAllocatedCount += len(result)
			}

			MetaCtx(ctx).Mutex.Unlock()
		}
	}
	if opt.addIPv6N > 0 {
		MetaCtx(ctx).Mutex.Lock()
		if opt.eniRef.IPv6 == nil {
			opt.eniRef.IPv6 = make(map[string]*networkv1beta1.IP)
		}
		MetaCtx(ctx).Mutex.Unlock()

		bo := backoff.Backoff(backoff.ENIIPOps)
		result, err := n.aliyun.AssignIpv6AddressesV2(ctx, &aliyunClient.AssignIPv6AddressesOptions{
			NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
				NetworkInterfaceID: opt.eniRef.ID,
				IPv6Count:          opt.addIPv6N,
			},
			Backoff: &bo.Backoff,
		})
		if err != nil {
			if apiErr.ErrorCodeIs(err, apiErr.InvalidVSwitchIDIPNotEnough, apiErr.QuotaExceededPrivateIPAddress) {
				// block
				n.vswpool.Block(opt.eniRef.VSwitchID)
			}

			MetaCtx(ctx).Mutex.Lock()
			lo.ForEach(result, func(item aliyunClient.IPSet, index int) {
				if item.IPName != "" {
					addIPToMap(opt.eniRef.IPv6, &networkv1beta1.IP{
						IPName: item.IPName,
						Status: networkv1beta1.IPStatusDeleting,
					})
				}
			})

			MetaCtx(ctx).Mutex.Unlock()

			return err
		} else {
			MetaCtx(ctx).StatusChanged.Store(true)

			MetaCtx(ctx).Mutex.Lock()

			for _, ip := range result {
				addIPToMap(opt.eniRef.IPv6, &networkv1beta1.IP{
					IPName: ip.IPName,
					IP:     ip.IPAddress,
					Status: networkv1beta1.IPStatusValid,
				})
			}

			// Track OpenAPI allocations for warm-up
			if !node.Spec.ENISpec.EnableIPv4 && !node.Status.WarmUpCompleted && node.Status.WarmUpTarget > 0 {
				node.Status.WarmUpAllocatedCount += len(result)
			}
			MetaCtx(ctx).Mutex.Unlock()
		}
	}
	return nil
}

// buildIPMap put the relating from node.Status.NetworkInterfaces to ipv4Map and ipv6Map
func buildIPMap(podsMapper map[string]*PodRequest, enis map[string]*networkv1beta1.Nic) (map[string]*EniIP, map[string]*EniIP) {
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

// countTotalIdleIPs counts idle IPs across ALL valid ENIs (InUse status)
// This is used for MinPoolSize check, regardless of vSwitch availability
func countTotalIdleIPs(node *networkv1beta1.Node) int {
	count := 0
	for _, eni := range node.Status.NetworkInterfaces {
		if eni.Status != aliyunClient.ENIStatusInUse {
			continue
		}
		if node.Spec.ENISpec.EnableIPv4 {
			count += len(getAllocatable(eni.IPv4))
		} else if node.Spec.ENISpec.EnableIPv6 {
			count += len(getAllocatable(eni.IPv6))
		}
	}
	return count
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
	lo.ForEach(sorted, func(item *networkv1beta1.Nic, index int) {
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
	lo.ForEach(sorted, func(item *networkv1beta1.Nic, index int) {
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
			v.PodUID = ip.PodUID
		}
	} else {
		in[ip.IP] = ip
	}
}

func isIPNotEnough(err error) bool {
	if apiErr.ErrorCodeIs(err, apiErr.InvalidVSwitchIDIPNotEnough, apiErr.QuotaExceededPrivateIPAddress) {
		return true
	}
	// can not find vsw
	if errors.Is(err, vswitch.ErrNoAvailableVSwitch) {
		return true
	}
	return false
}

func isEFLO(ctx context.Context) bool {
	return aliyunClient.GetBackendAPI(ctx) == aliyunClient.BackendAPIEFLO
}

func batchSize(ctx context.Context) int {
	if isEFLO(ctx) {
		return efloBatchSize
	}
	return ecsBatchSize
}

func (n *ReconcileNode) getDegradation() controlplane.Degradation {
	if n.v == nil {
		return ""
	}
	return controlplane.Degradation(n.v.GetString("degradation"))
}

func isDaemonSupportNodeRuntime(ctx context.Context, c client.Client, nodeName string) bool {
	l := logr.FromContextOrDiscard(ctx)
	podList := &corev1.PodList{}
	// Define the list options
	listOpts := &client.ListOptions{
		Namespace: "kube-system",
		LabelSelector: labels.SelectorFromSet(labels.Set{
			"app": "terway-eniip",
		}),
		FieldSelector: fields.SelectorFromSet(fields.Set{
			"spec.nodeName": nodeName,
		}),
	}

	// List the pods
	err := c.List(ctx, podList, listOpts)
	if err != nil {
		// nb(l1b0k): if daemon not exist, or error happen, we assume it is support
		l.Error(err, "failed to list terway-eniip pod, assume it is support")
		return true
	}

	if len(podList.Items) == 0 {
		l.Info("terway-eniip pod not exist, assume it is support")
		return true
	}

	if len(podList.Items[0].Spec.Containers) == 0 {
		l.Info("terway-eniip pod not exist, assume it is support")
		return true
	}

	image := podList.Items[0].Spec.Containers[0].Image
	tag := getTagFromImage(image)

	if !semver.IsValid(tag) {
		l.Info("terway version is not valid, assume it is support")
		return true
	}

	if semver.Compare(tag, "v1.11.3") < 0 {
		l.Info("terway version is less than v1.11.3, it is not support node runtime")
		return false
	}
	l.Info("terway version is greater than v1.11.3, it is support node runtime")

	return true
}

func getTagFromImage(image string) string {
	parts := strings.Split(image, "@")
	imageWithoutDigest := parts[0]

	lastColon := strings.LastIndex(imageWithoutDigest, ":")
	if lastColon == -1 {
		return "latest"
	}

	tag := imageWithoutDigest[lastColon+1:]

	if !strings.Contains(tag, "/") {
		return tag
	}

	return "latest"
}

func calculateToDel(ctx context.Context, node *networkv1beta1.Node) int {
	l := logr.FromContextOrDiscard(ctx)
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

	if node.Spec.Pool.Reclaim == nil {
		return toDel
	}

	after := node.Spec.Pool.Reclaim.After
	interval := node.Spec.Pool.Reclaim.Interval
	reclaimBatchSize := node.Spec.Pool.Reclaim.BatchSize
	jitterFactorStr := node.Spec.Pool.Reclaim.JitterFactor

	if reclaimBatchSize <= 0 {
		return toDel
	}

	// check the last pool used time
	if node.Status.LastModifiedTime.Add(after.Duration).After(time.Now()) {
		node.Status.NextIdleIPReclaimTime = metav1.Time{}

		l.V(4).Info("pool modified, reset next idle ip reclaim time")
		return toDel
	}

	maxFactor := 0.1
	if jitterFactorStr != "" {
		jitterFactor, _ := strconv.ParseFloat(jitterFactorStr, 64)
		if jitterFactor > 0 {
			maxFactor = jitterFactor
		}
	}

	// calculate the reclaim time
	if node.Status.NextIdleIPReclaimTime.IsZero() {
		jitteredInterval := wait.Jitter(interval.Duration, maxFactor)
		node.Status.NextIdleIPReclaimTime = metav1.Time{Time: time.Now().Add(jitteredInterval)}
		l.V(4).Info("set next idle ip reclaim time", "time", node.Status.NextIdleIPReclaimTime)
		return toDel
	}

	if node.Status.NextIdleIPReclaimTime.After(time.Now()) {
		l.V(4).Info("next idle ip reclaim time not reached", "time", node.Status.NextIdleIPReclaimTime)
		return toDel
	}
	toDel = max(0, toDel)

	// reset the next check time with jitter
	jitteredInterval := wait.Jitter(interval.Duration, maxFactor)
	node.Status.NextIdleIPReclaimTime = metav1.Time{Time: time.Now().Add(jitteredInterval)}
	extraDel := min(reclaimBatchSize, max(0, idles-toDel-node.Spec.Pool.MinPoolSize))

	l.V(4).Info("next idle ip reclaim time reached, increase to del", "extraDel", extraDel)
	toDel += extraDel

	return toDel
}

// waitIPGone checks if IP is removed from ENI, if timeout rely on next GC to do the clean up
func (n *ReconcileNode) waitIPGone(ctx context.Context, eni *networkv1beta1.Nic, ipv4, ipv6 []aliyunClient.IPSet) error {
	if len(ipv4) == 0 && len(ipv6) == 0 {
		return nil
	}

	return backoff.ExponentialBackoffWithInitialDelay(ctx, backoff.Backoff(backoff.WaitENIIPRemoved), func(ctx context.Context) (bool, error) {
		var err error
		var enis []*aliyunClient.NetworkInterface
		// all eni attached to this instance is taken into account. (exclude member eni)
		opts := &aliyunClient.DescribeNetworkInterfaceOptions{
			NetworkInterfaceIDs: &[]string{eni.ID},
		}

		enis, err = n.aliyun.DescribeNetworkInterfaceV2(ctx, opts)
		if err != nil {
			return false, err
		}

		if len(enis) == 0 {
			// we assume eni is exist here
			return false, fmt.Errorf("eni not found, eni: %s", eni.ID)
		}

		ipv4Set := convertIPSet(enis[0].PrivateIPSets)
		ipv6Set := convertIPSet(enis[0].IPv6Set)

		unfinished := false
		lo.ForEach(ipv4, func(ip aliyunClient.IPSet, _ int) {
			_, found := ipv4Set[ip.IPAddress]
			if !found {
				delete(eni.IPv4, ip.IPAddress)
			} else {
				unfinished = true
			}
		})

		lo.ForEach(ipv6, func(ip aliyunClient.IPSet, _ int) {
			_, found := ipv6Set[ip.IPAddress]
			if !found {
				delete(eni.IPv6, ip.IPAddress)
			} else {
				unfinished = true
			}
		})
		return !unfinished, nil
	})
}

// initializeWarmUp initializes warm-up status for nodes
// For new nodes with warm-up configured: set up tracking
// For existing nodes without warm-up config or already initialized: mark as completed
func (n *ReconcileNode) initializeWarmUp(node *networkv1beta1.Node) {
	if node.Spec.Pool == nil {
		return
	}

	warmUpSize := node.Spec.Pool.WarmUpSize

	// For existing nodes that already have warm-up status initialized, do nothing
	if node.Status.WarmUpTarget > 0 || node.Status.WarmUpCompleted {
		return
	}

	// If no warm-up configured, mark as completed immediately
	if warmUpSize <= 0 {
		node.Status.WarmUpCompleted = true
		return
	}

	// New node with warm-up configured
	node.Status.WarmUpTarget = warmUpSize
	node.Status.WarmUpAllocatedCount = 0
	node.Status.WarmUpCompleted = false
}

// shouldPerformWarmUp checks if warm-up should be performed
func (n *ReconcileNode) shouldPerformWarmUp(node *networkv1beta1.Node) bool {
	if node.Status.WarmUpCompleted {
		return false
	}
	if node.Status.WarmUpTarget <= 0 {
		return false
	}
	return true
}

// checkWarmUpCompletion checks if warm-up has been completed and marks it
func (n *ReconcileNode) checkWarmUpCompletion(node *networkv1beta1.Node) {
	if node.Status.WarmUpCompleted {
		return
	}
	if node.Status.WarmUpTarget <= 0 {
		return
	}

	if node.Status.WarmUpAllocatedCount >= node.Status.WarmUpTarget {
		node.Status.WarmUpCompleted = true
	}
}

func (n *ReconcileNode) poolSyncPeriod(user string) time.Duration {
	system := n.gcPeriod
	if user != "" {
		period, err := time.ParseDuration(user)
		if err == nil {
			system = period
		}
	}
	return system
}

func (n *ReconcileNode) requeueAfter(node *networkv1beta1.Node) time.Duration {
	poolPeriod := n.poolSyncPeriod(node.Spec.Pool.PoolSyncPeriod)

	now := time.Now()
	result := poolPeriod

	if !node.Status.NextSyncOpenAPITime.IsZero() {
		nextSyncTime := node.Status.NextSyncOpenAPITime.Time
		if nextSyncTime.After(now) {
			nextSyncDuration := nextSyncTime.Sub(now)
			// Take the minimum of poolPeriod and nextSyncDuration (priority processing principle)
			if nextSyncDuration < poolPeriod {
				result = nextSyncDuration
			}
		}
	}

	// Ensure minimum 1s
	if result < 1*time.Second {
		return 1 * time.Second
	}

	return result
}

// isPrefixMode returns true when the node should use prefix-based IPAM.
// Reads the EnableIPPrefix field from the Node CR's ENISpec.
// LingJun (EFLO) nodes are explicitly unsupported and always return false.
func isPrefixMode(node *networkv1beta1.Node) bool {
	if utils.ISLingJunNode(node.Labels) {
		return false
	}
	if node.Spec.ENISpec == nil {
		return false
	}
	return node.Spec.ENISpec.EnableIPPrefix
}

// processFrozenExpireAt checks all Frozen prefixes for FrozenExpireAt timeout.
// If a Frozen prefix has expired, it is reverted to Valid so it can be used again.
func processFrozenExpireAt(log logr.Logger, node *networkv1beta1.Node) bool {
	now := time.Now()
	changed := false

	for eniID, eni := range node.Status.NetworkInterfaces {
		for i := range eni.IPv4Prefix {
			if revertExpiredFrozenPrefix(log, &eni.IPv4Prefix[i], now) {
				changed = true
			}
		}
		for i := range eni.IPv6Prefix {
			if revertExpiredFrozenPrefix(log, &eni.IPv6Prefix[i], now) {
				changed = true
			}
		}
		node.Status.NetworkInterfaces[eniID] = eni
	}
	return changed
}

// revertExpiredFrozenPrefix reverts a single Frozen prefix to Valid if its FrozenExpireAt has passed.
func revertExpiredFrozenPrefix(log logr.Logger, prefix *networkv1beta1.IPPrefix, now time.Time) bool {
	if prefix.Status != networkv1beta1.IPPrefixStatusFrozen {
		return false
	}
	if prefix.FrozenExpireAt.IsZero() {
		return false
	}
	if now.Before(prefix.FrozenExpireAt.Time) {
		return false
	}
	log.Info("frozen prefix expired, reverting to Valid",
		"prefix", prefix.Prefix, "frozenExpireAt", prefix.FrozenExpireAt.Time)
	prefix.Status = networkv1beta1.IPPrefixStatusValid
	prefix.FrozenExpireAt = metav1.Time{}
	return true
}

// countValidPrefixes returns the number of prefixes with Valid status.
func countValidPrefixes(prefixes []networkv1beta1.IPPrefix) int {
	count := 0
	for _, p := range prefixes {
		if p.Status == networkv1beta1.IPPrefixStatusValid {
			count++
		}
	}
	return count
}

// convertPrefixesToCR converts API prefix results to CR format with Valid status.
// This is used when recording newly created ENI prefixes to the Node CR,
// so that subsequent reconciles can count them as "pending" prefixes on Attaching ENIs.
func convertPrefixesToCR(prefixes []aliyunClient.Prefix) []networkv1beta1.IPPrefix {
	if len(prefixes) == 0 {
		return nil
	}
	result := make([]networkv1beta1.IPPrefix, 0, len(prefixes))
	for _, p := range prefixes {
		result = append(result, networkv1beta1.IPPrefix{
			Prefix: string(p),
			Status: networkv1beta1.IPPrefixStatusValid,
		})
	}
	return result
}

// syncPrefixAllocation ensures the node has the desired total number of IPv4 (and optionally IPv6) prefixes.
// It distributes prefixes across InUse ENIs to meet the total target.
//
// IPv4: uses IPv4PrefixCount (effective in IPv4 single-stack and dual-stack).
// IPv6 dual-stack: each InUse ENI with IPv4 prefixes automatically gets exactly 1 IPv6 prefix
//
//	(the user does NOT configure ipv6_prefix_count in dual-stack mode).
//
// IPv6-only: uses IPv6PrefixCount (valid values: 0 or 1).
// Pod↔IP binding is NOT stored in the Node CR; that is owned by the daemon.
func (n *ReconcileNode) syncPrefixAllocation(ctx context.Context, node *networkv1beta1.Node) error {
	isDualStack := node.Spec.ENISpec.EnableIPv4 && node.Spec.ENISpec.EnableIPv6

	// Get desired prefix counts
	totalDesiredV4Prefixes := node.Spec.ENISpec.IPv4PrefixCount
	totalDesiredV6Prefixes := 0
	if !isDualStack {
		totalDesiredV6Prefixes = node.Spec.ENISpec.IPv6PrefixCount
	}

	// Calculate total existing prefixes across all InUse ENIs,
	// and also count prefixes on Attaching (in-flight) ENIs to avoid over-allocation.
	// Attaching ENIs were created with prefixes via CreateNetworkInterface; they will
	// become InUse once attached, so their prefixes must be counted as already-pending.
	totalV4Prefixes := 0
	totalV6Prefixes := 0
	for _, eni := range node.Status.NetworkInterfaces {
		switch eni.Status {
		case aliyunClient.ENIStatusInUse:
			totalV4Prefixes += countValidPrefixes(eni.IPv4Prefix)
			totalV6Prefixes += countValidPrefixes(eni.IPv6Prefix)
		case aliyunClient.ENIStatusAttaching:
			totalV4Prefixes += countValidPrefixes(eni.IPv4Prefix)
			totalV6Prefixes += countValidPrefixes(eni.IPv6Prefix)
		}
	}

	// Calculate remaining demand
	remainingV4Demand := max(0, totalDesiredV4Prefixes-totalV4Prefixes)
	remainingV6Demand := max(0, totalDesiredV6Prefixes-totalV6Prefixes)

	var errList []error
	for _, eni := range node.Status.NetworkInterfaces {
		if eni.Status != aliyunClient.ENIStatusInUse {
			continue
		}

		// Slot accounting: eni.IPv4 includes primary IP (1 slot) + secondary IPs.
		// Each prefix also occupies 1 slot. Total = len(eni.IPv4) + len(eni.IPv4Prefix).
		// ECS allows mixing secondary IPs and prefixes on the same ENI; the only constraint
		// is that the combined count must not exceed IPv4PerAdapter.
		usedSlots := len(eni.IPv4) + len(eni.IPv4Prefix)
		freeSlots := node.Spec.NodeCap.IPv4PerAdapter - usedSlots

		if remainingV4Demand > 0 && freeSlots > 0 {
			toAddV4 := min(remainingV4Demand, freeSlots)
			if err := n.assignIPv4Prefix(ctx, node, eni, toAddV4); err != nil {
				errList = append(errList, err)
			} else {
				remainingV4Demand -= toAddV4
			}
		}

		if isDualStack {
			// Dual-stack: ensure this ENI has exactly 1 IPv6 prefix.
			// Only assign if the ENI has IPv4 prefixes and is missing an IPv6 prefix.
			if countValidPrefixes(eni.IPv6Prefix) == 0 && countValidPrefixes(eni.IPv4Prefix) > 0 {
				if err := n.assignIPv6Prefix(ctx, node, eni, 1); err != nil {
					errList = append(errList, err)
				}
			}
		} else if node.Spec.ENISpec.EnableIPv6 && remainingV6Demand > 0 {
			// IPv6-only: assign prefix based on IPv6PrefixCount demand.
			if countValidPrefixes(eni.IPv6Prefix) == 0 {
				if err := n.assignIPv6Prefix(ctx, node, eni, 1); err != nil {
					errList = append(errList, err)
				} else {
					remainingV6Demand--
				}
			}
		}
	}
	return utilerrors.NewAggregate(errList)
}

// assignIPv4Prefix allocates IPv4 prefixes on an ENI via the OpenAPI and appends them to the CR.
// If count exceeds maxPrefixPerAPICall (10), multiple API calls will be made.
func (n *ReconcileNode) assignIPv4Prefix(ctx context.Context, _ *networkv1beta1.Node, eni *networkv1beta1.Nic, count int) error {
	remaining := count
	for remaining > 0 {
		batch := min(remaining, maxPrefixPerAPICall)
		bo := backoff.Backoff(backoff.ENIIPOps)
		result, err := n.aliyun.AssignPrivateIPAddressV2(ctx, &aliyunClient.AssignPrivateIPAddressOptions{
			NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
				NetworkInterfaceID: eni.ID,
				IPv4PrefixCount:    batch,
			},
			Backoff: &bo.Backoff,
		})
		if err != nil {
			return err
		}
		MetaCtx(ctx).StatusChanged.Store(true)
		for _, ip := range result {
			if ip.Prefix != "" {
				eni.IPv4Prefix = append(eni.IPv4Prefix, networkv1beta1.IPPrefix{
					Prefix: string(ip.Prefix),
					Status: networkv1beta1.IPPrefixStatusValid,
				})
			}
		}
		remaining -= batch
	}
	return nil
}

// assignIPv6Prefix allocates IPv6 prefixes on an ENI via the OpenAPI and appends them to the CR.
// If count exceeds maxPrefixPerAPICall (10), multiple API calls will be made.
func (n *ReconcileNode) assignIPv6Prefix(ctx context.Context, _ *networkv1beta1.Node, eni *networkv1beta1.Nic, count int) error {
	remaining := count
	for remaining > 0 {
		batch := min(remaining, maxPrefixPerAPICall)
		bo := backoff.Backoff(backoff.ENIIPOps)
		result, err := n.aliyun.AssignIpv6AddressesV2(ctx, &aliyunClient.AssignIPv6AddressesOptions{
			NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
				NetworkInterfaceID: eni.ID,
				IPv6PrefixCount:    batch,
			},
			Backoff: &bo.Backoff,
		})
		if err != nil {
			return err
		}
		MetaCtx(ctx).StatusChanged.Store(true)
		for _, ip := range result {
			if ip.Prefix != "" {
				eni.IPv6Prefix = append(eni.IPv6Prefix, networkv1beta1.IPPrefix{
					Prefix: string(ip.Prefix),
					Status: networkv1beta1.IPPrefixStatusValid,
				})
			}
		}
		remaining -= batch
	}
	return nil
}
