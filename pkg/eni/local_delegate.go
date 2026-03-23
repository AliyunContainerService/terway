package eni

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/AliyunContainerService/terway/pkg/utils"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

var _ NetworkInterface = &LocalDelegate{}

var delegateLog = logf.Log.WithName("local-delegate")

type ENIID string
type LocalDelegate struct {
	sharedMgr   *SharedCRDManager
	client      client.Client
	nodeName    string
	lock        sync.RWMutex
	eniIPAMs    map[ENIID]*ENILocalIPAM
	notifier    *Notifier
	enableIPv4  bool
	enableIPv6  bool
	enableERDMA bool
}

func NewLocalDelegate(sharedMgr *SharedCRDManager, nodeName string, enableIPv4, enableIPv6 bool) *LocalDelegate {
	return &LocalDelegate{
		sharedMgr:  sharedMgr,
		client:     sharedMgr.Client(),
		nodeName:   nodeName,
		eniIPAMs:   make(map[ENIID]*ENILocalIPAM),
		notifier:   NewNotifier(),
		enableIPv4: enableIPv4,
		enableIPv6: enableIPv6,
	}
}

// Run implements NetworkInterface. It waits for the shared cache to sync,
// registers Node CR informer handlers, and performs synchronous initialization
// so the delegate is ready for allocation before Run returns.
func (l *LocalDelegate) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	delegateLog.Info("start local-delegate controller")

	<-l.sharedMgr.CacheSynced()
	delegateLog.Info("local-delegate cache synced")

	nodeInformer, err := l.sharedMgr.GetCache().GetInformer(ctx, &networkv1beta1.Node{})
	if err != nil {
		return err
	}
	_, err = nodeInformer.AddEventHandler(&toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			l.notifier.Notify()
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			newNode := newObj.(*networkv1beta1.Node)
			if newNode.Status.NetworkInterfaces == nil {
				return
			}
			l.notifier.Notify()
		},
		DeleteFunc: func(obj interface{}) {
			l.notifier.Notify()
		},
	})
	if err != nil {
		return err
	}

	if err := l.initWithRetry(ctx, podResources, wg); err != nil {
		return fmt.Errorf("local delegate init failed: %w", err)
	}

	return nil
}

func (l *LocalDelegate) Priority() int {
	return 200
}

func (l *LocalDelegate) Dispose(n int) int {
	return 0
}

// Allocate implements NetworkInterface. Returns nil channel when inactive so
// the Manager falls through to the next NetworkInterface (CRDV2).
func (l *LocalDelegate) Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	rt := request.ResourceType()
	if rt != ResourceTypeLocalIP && rt != ResourceTypeRDMA {
		return nil, []Trace{{Condition: ResourceTypeMismatch}}
	}

	// Return nil when inactive (no IPAMs configured) so Manager tries next NetworkInterface
	l.lock.RLock()
	hasIPAMs := len(l.eniIPAMs) > 0
	l.lock.RUnlock()
	if !hasIPAMs {
		return nil, nil
	}

	resp := make(chan *AllocResp, 1)

	go func() {
		defer close(resp)

		resource, err := l.tryAllocateLocal(ctx, cni.PodID)
		if err != nil {
			delegateLog.Error(err, "failed to allocate local IP immediately, waiting for Node CR update", "podID", cni.PodID)

			subCh := l.notifier.Subscribe()
			defer l.notifier.Unsubscribe(subCh)

			for {
				select {
				case <-ctx.Done():
					resp <- &AllocResp{Err: ctx.Err()}
					return
				case <-subCh:
					resource, err = l.tryAllocateLocal(ctx, cni.PodID)
					if err == nil {
						l.recordAllocMetrics(resource)
						resp <- &AllocResp{NetworkConfigs: NetworkResources{resource}}
						return
					}
					delegateLog.Info("retry allocation after Node CR update", "podID", cni.PodID, "error", err)
				}
			}
		}

		l.recordAllocMetrics(resource)
		resp <- &AllocResp{NetworkConfigs: NetworkResources{resource}}
	}()

	return resp, nil
}

// Release implements NetworkInterface. Returns (false, nil) when inactive so
// the Manager falls through to the next NetworkInterface (CRDV2).
func (l *LocalDelegate) Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) (bool, error) {
	rt := request.ResourceType()
	if rt != ResourceTypeLocalIP && rt != ResourceTypeRDMA {
		return false, nil
	}

	localIPResource, ok := request.(*LocalIPResource)
	if !ok {
		return false, nil
	}

	l.lock.RLock()
	defer l.lock.RUnlock()

	for _, ipam := range l.eniIPAMs {
		if ipam.eniID == localIPResource.ENI.ID {
			if localIPResource.IP.GetIPv4() != "" {
				ipam.ReleaseIPv4(cni.PodID)
				metric.ResourcePoolIdle.WithLabelValues(metric.ResourcePoolTypeLocal, string(types.IPStackIPv4)).Inc()
			}
			if localIPResource.IP.GetIPv6() != "" {
				ipam.ReleaseIPv6(cni.PodID)
				metric.ResourcePoolIdle.WithLabelValues(metric.ResourcePoolTypeLocal, string(types.IPStackIPv6)).Inc()
			}
			return true, nil
		}
	}

	return false, nil
}

// tryAllocateLocal allocates IPs from the local IPAM.
// For dual-stack, both IPv4 and IPv6 are allocated from the same ENI atomically.
func (l *LocalDelegate) tryAllocateLocal(ctx context.Context, podID string) (*LocalIPResource, error) {
	l.lock.RLock()
	defer l.lock.RUnlock()

	var allocatedIPAM *ENILocalIPAM
	var ipv4, ipv6 netip.Addr

	if l.enableIPv4 && l.enableIPv6 {
		for _, ipam := range l.eniIPAMs {
			v4, err := ipam.AllocateIPv4(podID)
			if err != nil {
				continue
			}
			v6, err := ipam.AllocateIPv6(podID)
			if err != nil {
				ipam.ReleaseIPv4(podID)
				continue
			}
			ipv4, ipv6 = v4, v6
			allocatedIPAM = ipam
			break
		}
	} else if l.enableIPv4 {
		for _, ipam := range l.eniIPAMs {
			v4, err := ipam.AllocateIPv4(podID)
			if err == nil {
				ipv4 = v4
				allocatedIPAM = ipam
				break
			}
		}
	} else if l.enableIPv6 {
		for _, ipam := range l.eniIPAMs {
			v6, err := ipam.AllocateIPv6(podID)
			if err == nil {
				ipv6 = v6
				allocatedIPAM = ipam
				break
			}
		}
	}

	if allocatedIPAM == nil {
		return nil, fmt.Errorf("no available IPAM for pod %s", podID)
	}

	resource := &LocalIPResource{
		PodID: podID,
		ENI: daemon.ENI{
			ID:          allocatedIPAM.eniID,
			MAC:         allocatedIPAM.eniMAC,
			VSwitchID:   allocatedIPAM.vSwitchID,
			GatewayIP:   allocatedIPAM.gatewayIP,
			VSwitchCIDR: allocatedIPAM.vSwitchCIDR,
			ERdma:       allocatedIPAM.IsERDMA(),
		},
	}

	if ipv4.IsValid() {
		resource.IP.IPv4 = ipv4
	}
	if ipv6.IsValid() {
		resource.IP.IPv6 = ipv6
	}

	return resource, nil
}

func (l *LocalDelegate) initWithRetry(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	bo := wait.Backoff{
		Duration: 2 * time.Second,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    10,
		Cap:      60 * time.Second,
	}
	return wait.ExponentialBackoffWithContext(ctx, bo, func(ctx context.Context) (bool, error) {
		if err := l.doInit(ctx, podResources, wg); err != nil {
			delegateLog.Error(err, "local delegate init failed, will retry", "node", l.nodeName)
			return false, nil
		}
		return true, nil
	})
}

func (l *LocalDelegate) doInit(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	nodeCR := &networkv1beta1.Node{}
	err := l.client.Get(ctx, k8stypes.NamespacedName{Name: l.nodeName}, nodeCR)
	if err != nil {
		return fmt.Errorf("failed to get node CR %s: %w", l.nodeName, err)
	}

	delegateLog.Info("initializing local delegate with prefix mode", "node", l.nodeName)

	if nodeCR.Spec.ENISpec == nil {
		return fmt.Errorf("node not initialized")
	}
	l.enableERDMA = nodeCR.Spec.ENISpec.EnableERDMA

	l.lock.Lock()
	for eniID, eni := range nodeCR.Status.NetworkInterfaces {
		if eni.Status != "InUse" {
			continue
		}

		if ipam := NewENILocalIPAMFromPrefix(eniID, eni.MacAddress, eni, l.enableERDMA); ipam != nil {
			l.eniIPAMs[ENIID(eniID)] = ipam
		}
	}
	l.lock.Unlock()

	for _, podRes := range podResources {
		if podRes.PodInfo == nil {
			continue
		}
		podID := utils.PodInfoKey(podRes.PodInfo.Namespace, podRes.PodInfo.Name)

		for _, res := range podRes.Resources {
			if res.Type == daemon.ResourceTypeENIIP && res.ENIID != "" {
				l.lock.RLock()
				ipam, exists := l.eniIPAMs[ENIID(res.ENIID)]
				l.lock.RUnlock()

				if exists {
					ipam.RestorePod(podID, res.IPv4, res.IPv6, res.ENIID)
				}
			}
		}
	}

	wg.Add(1)
	go l.watchNodeCR(ctx, wg)

	l.notifier.Notify()
	l.syncPoolMetrics()
	delegateLog.Info("local delegate initialized successfully", "node", l.nodeName)

	return nil
}

func (l *LocalDelegate) syncPoolMetrics() {
	l.lock.RLock()
	defer l.lock.RUnlock()

	var totalV4, idleV4, totalV6, idleV6 float64
	for _, ipam := range l.eniIPAMs {
		t4, i4, t6, i6 := ipam.PoolStats()
		totalV4 += float64(t4)
		idleV4 += float64(i4)
		totalV6 += float64(t6)
		idleV6 += float64(i6)
	}

	metric.ResourcePoolTotal.WithLabelValues(metric.ResourcePoolTypeLocal, string(types.IPStackIPv4)).Set(totalV4)
	metric.ResourcePoolIdle.WithLabelValues(metric.ResourcePoolTypeLocal, string(types.IPStackIPv4)).Set(idleV4)
	metric.ResourcePoolTotal.WithLabelValues(metric.ResourcePoolTypeLocal, string(types.IPStackIPv6)).Set(totalV6)
	metric.ResourcePoolIdle.WithLabelValues(metric.ResourcePoolTypeLocal, string(types.IPStackIPv6)).Set(idleV6)
}

func (l *LocalDelegate) watchNodeCR(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	subCh := l.notifier.Subscribe()
	defer l.notifier.Unsubscribe(subCh)

	for {
		select {
		case <-ctx.Done():
			return
		case <-subCh:
			l.syncNodeCR(ctx)
			l.syncPoolMetrics()
		}
	}
}

func (l *LocalDelegate) syncNodeCR(ctx context.Context) {
	nodeCR := &networkv1beta1.Node{}
	err := l.client.Get(ctx, k8stypes.NamespacedName{Name: l.nodeName}, nodeCR)
	if err != nil {
		delegateLog.Error(err, "failed to get node CR for sync", "node", l.nodeName)
		return
	}

	l.lock.Lock()
	defer l.lock.Unlock()

	for eniID, eni := range nodeCR.Status.NetworkInterfaces {
		if eni.Status != "InUse" {
			continue
		}

		ipam, exists := l.eniIPAMs[ENIID(eniID)]
		if !exists {
			if newIPAM := NewENILocalIPAMFromPrefix(eniID, eni.MacAddress, eni, l.enableERDMA); newIPAM != nil {
				l.eniIPAMs[ENIID(eniID)] = newIPAM
			}
			continue
		}

		ipam.UpdatePrefixes(eni.IPv4Prefix, false)
		ipam.UpdatePrefixes(eni.IPv6Prefix, true)
	}

	for eniID, ipam := range l.eniIPAMs {
		if _, exists := nodeCR.Status.NetworkInterfaces[string(eniID)]; !exists {
			if ipam.HasAllocations() {
				delegateLog.Info("ENI removed from Node CR but still has allocations, keeping IPAM for graceful drain",
					"eniID", eniID, "allocations", ipam.AllocationCount())
				continue
			}
			delete(l.eniIPAMs, eniID)
		}
	}
}

func (l *LocalDelegate) recordAllocMetrics(resource *LocalIPResource) {
	metric.ResourcePoolAllocated.WithLabelValues(metric.ResourcePoolTypeLocal).Inc()
	if resource.IP.IPv4.IsValid() {
		metric.ResourcePoolIdle.WithLabelValues(metric.ResourcePoolTypeLocal, string(types.IPStackIPv4)).Dec()
	}
	if resource.IP.IPv6.IsValid() {
		metric.ResourcePoolIdle.WithLabelValues(metric.ResourcePoolTypeLocal, string(types.IPStackIPv6)).Dec()
	}
}
