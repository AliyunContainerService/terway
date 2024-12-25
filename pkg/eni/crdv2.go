package eni

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

var _ NetworkInterface = &CRDV2{}

type started struct {
	ch chan struct{}
}

func (s *started) Start(context.Context) error {
	close(s.ch)
	return nil
}

type CRDV2 struct {
	scheme *runtime.Scheme
	mgr    ctrl.Manager

	client client.Client

	nodeName string

	lock sync.Mutex
	// record pods cni del is called, it is indexed by pod uid
	deletedPods   map[string]*networkv1beta1.RuntimePodStatus
	cacheSyncedCh chan struct{}
}

func NewCRDV2(nodeName, namespace string) *CRDV2 {
	restConfig := ctrl.GetConfigOrDie()

	options := ctrl.Options{
		Scheme:                 types.Scheme,
		HealthProbeBindAddress: "0",
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
		WebhookServer: nil,
		Cache: cache.Options{
			HTTPClient:           nil,
			Scheme:               nil,
			Mapper:               types.NewRESTMapper(),
			SyncPeriod:           nil,
			DefaultLabelSelector: nil,
			DefaultFieldSelector: nil,
			DefaultTransform:     nil,
			ByObject: map[client.Object]cache.ByObject{
				&corev1.Node{}: {
					Field: fields.SelectorFromSet(map[string]string{
						"metadata.name": nodeName,
					}),
					Transform: nil,
				},
				&networkv1beta1.Node{}: {
					Field: fields.SelectorFromSet(map[string]string{
						"metadata.name": nodeName,
					}),
					Transform: nil,
				},
				&networkv1beta1.NodeRuntime{}: {
					Field: fields.SelectorFromSet(map[string]string{
						"metadata.name": nodeName,
					}),
					Transform: nil,
				},
				&corev1.Pod{}: {
					Field: client.MatchingFieldsSelector{
						Selector: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}),
					},
					Transform: func(i interface{}) (interface{}, error) {
						if pod, ok := i.(*corev1.Pod); ok {
							pod.Spec.Volumes = nil
							pod.Spec.EphemeralContainers = nil
							pod.Spec.SecurityContext = nil
							pod.Spec.ImagePullSecrets = nil
							pod.Spec.Tolerations = nil
							pod.Spec.ReadinessGates = nil
							pod.Spec.PreemptionPolicy = nil
							pod.Status.InitContainerStatuses = nil
							pod.Status.ContainerStatuses = nil
							pod.Status.EphemeralContainerStatuses = nil
							return pod, nil
						}
						return nil, fmt.Errorf("unexpected type %T", i)
					},
				},
				&corev1.ConfigMap{}: {
					Field: fields.SelectorFromSet(map[string]string{
						"metadata.namespace": namespace,
					}),
				},
			},
		},
	}

	mgr, err := ctrl.NewManager(restConfig, options)
	if err != nil {
		panic(err)
	}
	if err = (&nodeReconcile{
		nodeName: nodeName,
		client:   mgr.GetClient(),
		record:   mgr.GetEventRecorderFor("terway-daemon"),
	}).SetupWithManager(mgr); err != nil {
		panic(err)
	}

	cacheSyncedCh := make(chan struct{})
	err = mgr.Add(&started{ch: cacheSyncedCh})
	if err != nil {
		panic(err)
	}

	return &CRDV2{
		scheme:        mgr.GetScheme(),
		mgr:           mgr,
		client:        mgr.GetClient(),
		nodeName:      nodeName,
		deletedPods:   make(map[string]*networkv1beta1.RuntimePodStatus),
		cacheSyncedCh: cacheSyncedCh,
	}
}

func (r *CRDV2) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	klog.Info("start CRDV2 controller")

	wg.Add(1)
	go func() {
		defer wg.Done()

		err := r.mgr.Start(ctx)
		if err != nil {
			if ctx.Err() == nil {
				klog.Fatalf("manager failed: %v", err)
			}
		}
	}()

	// block until cache ready, workaround for now
	<-r.cacheSyncedCh
	klog.Info("crd v2 controller cache synced")

	go wait.UntilWithContext(ctx, func(ctx context.Context) {
		err := r.syncNodeRuntime(ctx)
		if err != nil {
			logf.Log.Error(err, "failed to mark delete")
		}
	}, 3*time.Second)

	go wait.UntilWithContext(ctx, func(ctx context.Context) {
		err := r.syncDeletedPods(ctx)
		if err != nil {
			logf.Log.Error(err, "sync deleted pods")
		}
	}, 5*time.Minute)

	return nil
}

func (r *CRDV2) Priority() int {
	return 100
}

func (r *CRDV2) Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	switch request.ResourceType() {
	case ResourceTypeLocalIP, ResourceTypeRDMA:
		return r.multiIP(ctx, cni, request)
	case ResourceTypeRemoteIP:
		return r.remote(ctx, cni, request)
	default:
		return nil, []Trace{{Condition: ResourceTypeMismatch}}
	}
}

func (r *CRDV2) Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) (bool, error) {
	switch request.ResourceType() {
	case ResourceTypeLocalIP, ResourceTypeRDMA:
		// handle the del req
		// 1. daemon already verified the request is valid
		r.lock.Lock()
		defer r.lock.Unlock()

		// it happens when sync to k8s failed , if local has no pod , need to mark it delete
		r.deletedPods[cni.PodUID] = &networkv1beta1.RuntimePodStatus{
			PodID: cni.PodID,
		}
	}
	return false, nil
}

func (r *CRDV2) Dispose(n int) int {
	return 0
}

func (r *CRDV2) multiIP(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	resp := make(chan *AllocResp)

	go func() {
		l := logf.FromContext(ctx, "ipam", "crd")

		node := &networkv1beta1.Node{}
		allocResp := &AllocResp{}

		var err error
		err = wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.WaitPodENIStatus), func(ctx context.Context) (bool, error) {
			err = r.client.Get(ctx, client.ObjectKey{Name: r.nodeName}, node)
			if err != nil {
				l.Error(err, "get node failed")
				return false, nil
			}
			// cni.PodName
			var ipv4, ipv6 netip.Addr
			var eniInfo *networkv1beta1.NetworkInterface
			for _, eni := range node.Status.NetworkInterfaces {
				if eni.Status != aliyunClient.ENIStatusInUse {
					continue
				}
				for _, ip := range eni.IPv4 {
					if ip.Status != networkv1beta1.IPStatusValid ||
						ip.PodID != cni.PodID {
						continue
					}
					if ip.PodUID != "" && ip.PodUID != cni.PodUID {
						continue
					}
					addr, err := netip.ParseAddr(ip.IP)
					if err != nil {
						return false, err
					}
					ipv4 = addr
					eniInfo = eni
				}
				for _, ip := range eni.IPv6 {
					if ip.Status != networkv1beta1.IPStatusValid ||
						ip.PodID != cni.PodID {
						continue
					}
					if ip.PodUID != "" && ip.PodUID != cni.PodUID {
						continue
					}
					addr, err := netip.ParseAddr(ip.IP)
					if err != nil {
						return false, err
					}
					ipv6 = addr
					eniInfo = eni
				}
			}
			if (!ipv4.IsValid() && !ipv6.IsValid()) || eniInfo == nil {
				l.V(2).Info("no valid ip found")
				return false, nil
			}

			var ip types.IPSet2

			ip.IPv4 = ipv4
			ip.IPv6 = ipv6
			gw := types.IPSet{}
			vsw := types.IPNetSet{}
			if ipv4.IsValid() {
				gw.IPv4 = net.ParseIP(terwayIP.DeriveGatewayIP(eniInfo.IPv4CIDR))

				_, cidr, err := net.ParseCIDR(eniInfo.IPv4CIDR)
				if err != nil {
					return false, err
				}
				vsw.IPv4 = cidr
			}
			if ipv6.IsValid() {
				gw.IPv6 = net.ParseIP(terwayIP.DeriveGatewayIP(eniInfo.IPv6CIDR))

				_, cidr, err := net.ParseCIDR(eniInfo.IPv6CIDR)
				if err != nil {
					return false, err
				}
				vsw.IPv6 = cidr
			}

			allocResp.NetworkConfigs = append(allocResp.NetworkConfigs, &LocalIPResource{
				ENI: daemon.ENI{
					ID:               eniInfo.ID,
					MAC:              eniInfo.MacAddress,
					SecurityGroupIDs: eniInfo.SecurityGroupIDs,
					Trunk:            false,
					ERdma: node.Spec.ENISpec.EnableERDMA &&
						eniInfo.NetworkInterfaceTrafficMode == networkv1beta1.NetworkInterfaceTrafficModeHighPerformance,
					GatewayIP:   gw,
					VSwitchCIDR: vsw,
					VSwitchID:   eniInfo.VSwitchID,
				},
				IP: ip,
			})
			l.Info("get valid ip from crd", "cfg", allocResp.NetworkConfigs)

			return true, nil
		})

		if err != nil {
			if wait.Interrupted(err) {
				allocResp.Err = &types.Error{
					Code: types.ErrIPNotAllocated,
					Msg:  fmt.Sprintf("timed out waiting for ip allocated. Use 'kubectl describe nodes.network.alibabacloud.com %s' to see more detail", r.nodeName),
					R:    err,
				}
			} else {
				allocResp.Err = err
			}
		}

		select {
		case <-ctx.Done():
			l.Error(ctx.Err(), "parent ctx done")
		case resp <- allocResp:
			r.lock.Lock()
			delete(r.deletedPods, cni.PodUID)
			r.lock.Unlock()
		}
	}()

	return resp, nil
}

func (r *CRDV2) remote(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	remote := &Remote{
		client: r.client,
	}
	trunk, err := r.getTrunkENI(ctx)
	if err != nil {
		resp := make(chan *AllocResp)

		go func() {
			select {
			case <-ctx.Done():
				return
			case resp <- &AllocResp{Err: err}:
			}
		}()
		return resp, nil
	}

	remote.trunkENI = trunk

	return remote.Allocate(ctx, cni, request)
}

func (r *CRDV2) getTrunkENI(ctx context.Context) (*daemon.ENI, error) {
	var node *networkv1beta1.Node

	var trunkENI *daemon.ENI

	var innerErr error
	err := wait.ExponentialBackoffWithContext(ctx, backoff.Backoff(backoff.WaitPodENIStatus), func(ctx context.Context) (bool, error) {
		node = &networkv1beta1.Node{}
		innerErr = r.client.Get(ctx, client.ObjectKey{Name: r.nodeName}, node)
		if innerErr != nil {
			return false, nil
		}
		if node.Spec.ENISpec == nil {
			// cr not ready
			innerErr = fmt.Errorf("nodes.network.alibabacloud.com %s has not been initialized", r.nodeName)
			return false, nil
		}
		if !node.Spec.ENISpec.EnableTrunk {
			// trunk is not enabled
			return true, nil
		}
		// nb(l1b0k): we need to deprecate the trunk-on anno on node

		k8sNode := &corev1.Node{}
		innerErr = r.client.Get(ctx, client.ObjectKey{Name: r.nodeName}, k8sNode)
		if innerErr != nil {
			return false, nil
		}
		trunkID := k8sNode.Annotations[types.TrunkOn]

		trunk, ok := node.Status.NetworkInterfaces[trunkID]
		if !ok {
			innerErr = fmt.Errorf("trunk %s has not been initialized", trunkID)
			return false, nil
		}

		trunkENI = &daemon.ENI{
			ID:               trunk.ID,
			MAC:              trunk.MacAddress,
			SecurityGroupIDs: trunk.SecurityGroupIDs,
			Trunk:            true,
			ERdma:            false,
			PrimaryIP:        types.IPSet{},
			GatewayIP:        types.IPSet{},
			VSwitchCIDR:      types.IPNetSet{},
			VSwitchID:        trunk.VSwitchID,
		}
		trunkENI.PrimaryIP.SetIP(trunk.PrimaryIPAddress)
		if node.Spec.ENISpec.EnableIPv4 {
			trunkENI.GatewayIP.SetIP(terwayIP.DeriveGatewayIP(trunk.IPv4CIDR))
			trunkENI.VSwitchCIDR.SetIPNet(trunk.IPv4CIDR)
		}
		if node.Spec.ENISpec.EnableIPv6 {
			trunkENI.GatewayIP.SetIP(terwayIP.DeriveGatewayIP(trunk.IPv6CIDR))
			trunkENI.VSwitchCIDR.SetIPNet(trunk.IPv6CIDR)
		}

		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("error get trunk eni %w, innerErr %s", err, innerErr)
	}

	return trunkENI, err
}

// syncNodeRuntime run a cron job to update delete pods
func (r *CRDV2) syncNodeRuntime(ctx context.Context) error {
	r.lock.Lock()
	defer r.lock.Unlock()

	if len(r.deletedPods) == 0 {
		return nil
	}

	nodeRuntime, err := r.getRuntimeNode(ctx)
	if err != nil {
		return err
	}

	if !nodeRuntime.DeletionTimestamp.IsZero() {
		// ignore deleting
		return nil
	}

	for k, pod := range r.deletedPods {
		v, ok := nodeRuntime.Status.Pods[k]
		if !ok {
			v = &networkv1beta1.RuntimePodStatus{
				PodID: pod.PodID,
			}
			if nodeRuntime.Status.Pods == nil {
				nodeRuntime.Status.Pods = make(map[string]*networkv1beta1.RuntimePodStatus)
			}
			nodeRuntime.Status.Pods[k] = v
		}
		// cni del is called
		if v.Status == nil {
			v.Status = map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{}
		}
		v.Status[networkv1beta1.CNIStatusDeleted] = &networkv1beta1.CNIStatusInfo{
			LastUpdateTime: metav1.Now(),
		}

		logf.Log.Info("report pod deleted", "pod", v)
	}

	err = saveStatus(ctx, r.client, nodeRuntime)
	if err != nil {
		return err
	}

	r.deletedPods = make(map[string]*networkv1beta1.RuntimePodStatus)
	return nil
}

func saveStatus(ctx context.Context, c client.Client, nodeRuntime *networkv1beta1.NodeRuntime) error {
	update := nodeRuntime.DeepCopy()
	changed, err := controllerutil.CreateOrPatch(ctx, c, update, func() error {
		update.Status = nodeRuntime.Status
		update.Spec = nodeRuntime.Spec
		update.Labels = nodeRuntime.Labels
		update.Name = nodeRuntime.Name
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to save node runtime status %w", err)
	}
	if changed != controllerutil.OperationResultNone {
		logf.Log.Info("changed node runtime status", "pods", nodeRuntime.Status.Pods)
	}
	return nil
}

func (r *CRDV2) syncDeletedPods(ctx context.Context) error {
	l := logf.FromContext(ctx)

	nodeRuntime, err := r.getRuntimeNode(ctx)
	if err != nil {
		return err
	}

	if !nodeRuntime.DeletionTimestamp.IsZero() {
		return nil
	}

	// get inUsed Pod UIDs in ipam
	inUsed, err := r.inUsedPodUIDs(ctx)
	if err != nil {
		return err
	}

	removeDeleted(l, nodeRuntime, inUsed)

	syncBack(l, nodeRuntime, inUsed)

	err = saveStatus(ctx, r.client, nodeRuntime)
	return err
}

func removeDeleted(l logr.Logger, nodeRuntime *networkv1beta1.NodeRuntime, inUsed map[string]networkv1beta1.RuntimePodStatus) {
	// clean exist record if not expected
	for uid, v := range nodeRuntime.Status.Pods {
		_, found := inUsed[uid]
		// only clean when ipam is forget this pod
		if !found {
			status, _, ok := utils.RuntimeFinalStatus(v.Status)
			if !ok || status != networkv1beta1.CNIStatusDeleted {
				continue
			}

			delete(nodeRuntime.Status.Pods, uid)
			l.Info("pod uid no longer exist in nodes cr, remove this pod ", "pod", v, "uid", uid)
		}
	}
}

func syncBack(l logr.Logger, nodeRuntime *networkv1beta1.NodeRuntime, inUsed map[string]networkv1beta1.RuntimePodStatus) {
	// for uid found in ipam , but not on runtimeStatus, sync back
	// so gc could clean up those records
	if nodeRuntime.Status.Pods == nil {
		nodeRuntime.Status.Pods = make(map[string]*networkv1beta1.RuntimePodStatus)
	}
	for uid := range inUsed {
		_, ok := nodeRuntime.Status.Pods[uid]
		if !ok {
			newStatus := inUsed[uid]
			nodeRuntime.Status.Pods[uid] = &newStatus
			l.Info("sync back pod", "pod", newStatus, "uid", uid)
		}
	}
}

// inUsedPodUIDs return the pod uid record in the ipam
func (r *CRDV2) inUsedPodUIDs(ctx context.Context) (map[string]networkv1beta1.RuntimePodStatus, error) {
	node := &networkv1beta1.Node{}
	err := r.client.Get(ctx, client.ObjectKey{Name: r.nodeName}, node)
	if err != nil {
		return nil, err
	}

	pendingUID := map[string]networkv1beta1.RuntimePodStatus{}

	for _, v := range node.Status.NetworkInterfaces {
		for _, ipam := range v.IPv4 {
			if ipam.PodUID != "" {
				pendingUID[ipam.PodUID] = networkv1beta1.RuntimePodStatus{
					PodID: ipam.PodID,
					Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
						networkv1beta1.CNIStatusInitial: {
							LastUpdateTime: metav1.Now(),
						},
					},
				}
			}
		}
		for _, ipam := range v.IPv6 {
			if ipam.PodUID != "" {
				pendingUID[ipam.PodUID] = networkv1beta1.RuntimePodStatus{
					PodID: ipam.PodID,
					Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
						networkv1beta1.CNIStatusInitial: {
							LastUpdateTime: metav1.Now(),
						},
					},
				}
			}
		}
	}
	return pendingUID, nil
}

// getRuntimeNode create if not present
func (r *CRDV2) getRuntimeNode(ctx context.Context) (*networkv1beta1.NodeRuntime, error) {
	nodeRuntime := &networkv1beta1.NodeRuntime{}

	err := r.client.Get(ctx, client.ObjectKey{Name: r.nodeName}, nodeRuntime)
	if err != nil {
		if !k8sErr.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get node runtime %w", err)
		}
		// not found
		node := &corev1.Node{}
		err = r.client.Get(ctx, client.ObjectKey{Name: r.nodeName}, node)
		if err != nil {
			return nil, fmt.Errorf("failed to get node %s: %w", r.nodeName, err)
		}
		nodeRuntime.Name = r.nodeName
		if nodeRuntime.Labels == nil {
			nodeRuntime.Labels = map[string]string{}
		}
		nodeRuntime.Labels["name"] = r.nodeName
		err = controllerutil.SetOwnerReference(node, nodeRuntime, r.scheme)
		if err != nil {
			return nil, err
		}
	}
	return nodeRuntime, nil
}
