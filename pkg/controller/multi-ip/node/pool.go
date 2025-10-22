package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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

	// Event reasons
	EventAllocIPFailed      = "AllocIPFailed"
	EventSyncOpenAPISuccess = "SyncOpenAPISuccess"
	EventSyncOpenAPIFailed  = "SyncOpenAPIFailed"
	EventENICreated         = "ENICreated"
	EventENICreateFailed    = "ENICreateFailed"
	EventENIDeleted         = "ENIDeleted"
	EventENIDeleteFailed    = "ENIDeleteFailed"
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
				eniBatchSize:       5, // operate eni on one reconcile
				v:                  controlplane.GetViper(),
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

	Mutex sync.Mutex
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

		ResourcePoolTotal.Delete(prometheus.Labels{"node": request.Name})
		SyncOpenAPITotal.Delete(prometheus.Labels{"node": request.Name})
		ReconcileLatency.Delete(prometheus.Labels{"node": request.Name})

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
		ctx = aliyunClient.SetBackendAPI(ctx, aliyunClient.BackendAPIEFLO)
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

	if !reflect.DeepEqual(beforeStatus, afterStatus) {
		span.AddEvent("update node cr")

		err = n.client.Status().Update(ctx, node)

		if err != nil && nodeStatus.StatusChanged.CompareAndSwap(true, false) {
			nodeStatus.NeedSyncOpenAPI.Store(true)
		}

		return reconcile.Result{RequeueAfter: 1 * time.Second}, err
	}

	return reconcile.Result{}, utilerrors.NewAggregate(errorList)
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

		if item.Status == aliyunClient.LENIStatusExecuting ||
			item.Status == aliyunClient.LENIStatusDeleting ||
			item.Status == aliyunClient.ENIStatusAttaching ||
			item.Status == aliyunClient.ENIStatusDetaching {
			hasMiddleStatus = true
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

	if utilerrors.NewAggregate(errList) != nil {
		return utilerrors.NewAggregate(errList)
	}
	// 6. pool management sort eni and find the victim

	return n.adjustPool(ctx, node)
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

	// handle exist pod ip
	for podID, info := range pendingPods {
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
	for podID, info := range pendingPods {
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
func (n *ReconcileNode) addIP(ctx context.Context, unSucceedPods map[string]*PodRequest, node *networkv1beta1.Node) error {
	ctx, span := n.tracer.Start(ctx, "addIP")
	defer span.End()

	// Record method latency
	startTime := time.Now()
	defer func() {
		ReconcileLatency.WithLabelValues("addIP", node.Name).Observe(float64(time.Since(startTime).Milliseconds()))
	}()

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

	// handle trunk/secondary eni
	assignEniWithOptions(ctx, node, len(normalPods)+node.Spec.Pool.MinPoolSize, options, func(option *eniOptions) bool {
		return n.validateENI(ctx, option, []eniTypeKey{secondaryKey, trunkKey})
	})
	assignEniWithOptions(ctx, node, len(rdmaPods), options, func(option *eniOptions) bool {
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
		if option.eniRef.Status != aliyunClient.ENIStatusInUse {
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
func assignEniWithOptions(ctx context.Context, node *networkv1beta1.Node, toAdd int, options []*eniOptions, filterFunc func(option *eniOptions) bool) {
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
						option.addIPv4N = min(leftQuota, toAddIPv4, batchSize(ctx))
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
						option.addIPv6N = min(leftQuota, toAddIPv6, batchSize(ctx))
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

// allocateFromOptions add ip or create eni, based on the options
func (n *ReconcileNode) allocateFromOptions(ctx context.Context, node *networkv1beta1.Node, options []*eniOptions) error {
	ctx, span := n.tracer.Start(ctx, "allocateFromOptions")
	defer span.End()

	wg := wait.Group{}
	lock := sync.Mutex{}
	var errs []error

	inFlight := 0

	createBatchSize := min(n.eniBatchSize, 1)
	if isEFLO(ctx) {
		createBatchSize = 2
	}
	for _, option := range options {
		if option.addIPv6N <= 0 && option.addIPv4N <= 0 {
			continue
		}
		if inFlight >= createBatchSize {
			continue
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
				n.record.Event(node, corev1.EventTypeWarning, EventENIDeleteFailed,
					fmt.Sprintf("Failed to delete ENI %s: %v", eni.ID, err))
				log.Error(err, "run gc failed")
				continue
			}
			MetaCtx(ctx).StatusChanged.Store(true)

			n.record.Event(node, corev1.EventTypeNormal, EventENIDeleted,
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

	gcPeriod := n.gcPeriod
	if node.Spec.Pool.PoolSyncPeriod != "" {
		period, err := time.ParseDuration(node.Spec.Pool.PoolSyncPeriod)
		if err != nil {
			l.Error(err, "parse pool sync period failed, use default config")
		} else {
			gcPeriod = period
		}
	}

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
	createOpts := &aliyunClient.CreateNetworkInterfaceOptions{
		NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
			VSwitchID:        vsw.ID,
			SecurityGroupIDs: node.Spec.ENISpec.SecurityGroupIDs,
			ResourceGroupID:  node.Spec.ENISpec.ResourceGroupID,
			Tags:             tags,
			IPCount:          opt.addIPv4N,
			IPv6Count:        opt.addIPv6N,
			SourceDestCheck:  ptr.To(false),
			ZoneID:           node.Spec.NodeMetadata.ZoneID, // eflo
			InstanceID:       node.Spec.NodeMetadata.InstanceID,
		},

		Backoff: &bo.Backoff,
	}

	result, err := n.aliyun.CreateNetworkInterfaceV2(ctx, typeOption, createOpts)
	if err != nil {
		if apiErr.ErrorCodeIs(err, apiErr.InvalidVSwitchIDIPNotEnough, apiErr.QuotaExceededPrivateIPAddress) {
			// block
			n.vswpool.Block(createOpts.NetworkInterfaceOptions.VSwitchID)
		}
		n.record.Event(node, corev1.EventTypeWarning, EventENICreateFailed,
			fmt.Sprintf("Failed to create ENI type=%s vsw=%s: %v", opt.eniTypeKey.ENIType, vsw.ID, err))
		return err
	}

	MetaCtx(ctx).Mutex.Lock()
	if node.Status.NetworkInterfaces == nil {
		node.Status.NetworkInterfaces = make(map[string]*networkv1beta1.Nic)
	}
	MetaCtx(ctx).Mutex.Unlock()

	defer func() {
		if err != nil {
			rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer rollbackCancel()

			if isEFLO(ctx) {
				rollbackCtx = aliyunClient.SetBackendAPI(rollbackCtx, aliyunClient.BackendAPIEFLO)
			}

			innerErr := n.aliyun.DeleteNetworkInterfaceV2(rollbackCtx, result.NetworkInterfaceID)
			if innerErr == nil {
				return
			}
			logf.FromContext(ctx).Error(innerErr, "failed to delete eni, this may result leak", "eni", result.NetworkInterfaceID)
			// if we failed to delete the eni , we need to store the eni
			MetaCtx(ctx).Mutex.Lock()
			node.Status.NetworkInterfaces[result.NetworkInterfaceID] = &networkv1beta1.Nic{
				ID:                          result.NetworkInterfaceID,
				Status:                      aliyunClient.ENIStatusDeleting,
				NetworkInterfaceType:        networkv1beta1.ENIType(result.Type),
				NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficMode(result.NetworkInterfaceTrafficMode),
			}
			MetaCtx(ctx).Mutex.Unlock()

			MetaCtx(ctx).StatusChanged.Store(true)
		}
	}()

	if !isEFLO(ctx) {
		err = n.aliyun.GetECS().AttachNetworkInterface(ctx, &aliyunClient.AttachNetworkInterfaceOptions{
			NetworkInterfaceID:     &result.NetworkInterfaceID,
			InstanceID:             &node.Spec.NodeMetadata.InstanceID,
			TrunkNetworkInstanceID: nil,
			NetworkCardIndex:       nil,
			Backoff:                nil,
		})
		if err != nil {
			n.record.Event(node, corev1.EventTypeWarning, EventENICreateFailed,
				fmt.Sprintf("Failed to attach ENI %s: %v", result.NetworkInterfaceID, err))
			return err
		}

		time.Sleep(3 * time.Second)
	}

	eni, err := n.aliyun.WaitForNetworkInterfaceV2(ctx, result.NetworkInterfaceID, aliyunClient.ENIStatusInUse, backoff.Backoff(backoff.WaitENIStatus).Backoff, false)
	if err != nil {
		n.record.Event(node, corev1.EventTypeWarning, EventENICreateFailed,
			fmt.Sprintf("Failed to wait ENI %s ready: %v", result.NetworkInterfaceID, err))
		return err
	}

	networkInterface := newENIFromAPI(eni)
	// update vsw
	networkInterface.IPv4CIDR = vsw.IPv4CIDR
	networkInterface.IPv6CIDR = vsw.IPv6CIDR

	MetaCtx(ctx).Mutex.Lock()
	node.Status.NetworkInterfaces[eni.NetworkInterfaceID] = networkInterface
	// if changed , but we update failed , that case ,need to sync openAPI...
	MetaCtx(ctx).Mutex.Unlock()

	MetaCtx(ctx).StatusChanged.Store(true)

	n.record.Event(node, corev1.EventTypeNormal, EventENICreated,
		fmt.Sprintf("Successfully created ENI %s type=%s with %d IPv4 and %d IPv6 addresses",
			eni.NetworkInterfaceID, opt.eniTypeKey.ENIType, opt.addIPv4N, opt.addIPv6N))
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
