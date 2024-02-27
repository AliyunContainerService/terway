package eni

//go:generate stringer -type=status -trimprefix=status

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"time"

	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/factory"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/rpc"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

var _ NetworkInterface = &Local{}
var _ Usage = &Local{}
var _ ReportStatus = &Trunk{}

type eniStatus int

const (
	statusInit eniStatus = iota
	statusCreating
	statusInUse
	statusDeleting
)

const (
	LocalIPTypeERDMA = "ERDMA"
)

var rateLimit = rate.Every(1 * time.Minute / 10)

var _ ResourceRequest = &LocalIPRequest{}

type LocalIPRequest struct {
	NetworkInterfaceID string
	LocalIPType        string
	IPv4               netip.Addr
	IPv6               netip.Addr

	NoCache bool // do not use cached ip
}

func (l *LocalIPRequest) ResourceType() ResourceType {
	return ResourceTypeLocalIP
}

var _ NetworkResource = &LocalIPResource{}

type LocalIPResource struct {
	PodID string

	ENI daemon.ENI

	IP types.IPSet2
}

func (l *LocalIPResource) ResourceType() ResourceType {
	return ResourceTypeLocalIP
}

func (l *LocalIPResource) ToStore() []daemon.ResourceItem {
	if l == nil {
		return nil
	}
	r := daemon.ResourceItem{
		Type:   daemon.ResourceTypeENIIP,
		ID:     fmt.Sprintf("%s.%s", l.ENI.MAC, l.IP.String()),
		ENIID:  l.ENI.ID,
		ENIMAC: l.ENI.MAC,
		IPv4:   l.IP.GetIPv4(),
		IPv6:   l.IP.GetIPv6(),
	}

	return []daemon.ResourceItem{r}
}

func (l *LocalIPResource) ToRPC() []*rpc.NetConf {
	cfg := &rpc.NetConf{
		BasicInfo: &rpc.BasicInfo{
			PodIP:       l.IP.ToRPC(),
			PodCIDR:     l.ENI.VSwitchCIDR.ToRPC(),
			GatewayIP:   l.ENI.GatewayIP.ToRPC(),
			ServiceCIDR: nil,
		},
		ENIInfo: &rpc.ENIInfo{
			MAC:       l.ENI.MAC,
			Trunk:     false,
			Vid:       0,
			GatewayIP: l.ENI.GatewayIP.ToRPC(),
		},
		Pod:          nil,
		IfName:       "",
		ExtraRoutes:  nil,
		DefaultRoute: true,
	}

	return []*rpc.NetConf{cfg}
}

type Local struct {
	batchSize int

	cap                        int
	allocatingV4, allocatingV6 int

	eni                    *daemon.ENI
	ipAllocInhibitExpireAt time.Time

	eniType string

	enableIPv4, enableIPv6                 bool
	ipv4, ipv6                             Set
	rateLimitEni, rateLimitv4, rateLimitv6 *rate.Limiter

	cond *sync.Cond

	status eniStatus

	factory factory.Factory
}

func NewLocal(eni *daemon.ENI, eniType string, factory factory.Factory, poolConfig *types.PoolConfig) *Local {
	l := &Local{
		eni:        eni,
		batchSize:  poolConfig.BatchSize,
		cap:        poolConfig.MaxIPPerENI,
		cond:       sync.NewCond(&sync.Mutex{}),
		ipv4:       make(Set),
		ipv6:       make(Set),
		eniType:    eniType,
		enableIPv4: poolConfig.EnableIPv4,
		enableIPv6: poolConfig.EnableIPv6,
		factory:    factory,

		rateLimitEni: rate.NewLimiter(rateLimit, 2),
		rateLimitv4:  rate.NewLimiter(rateLimit, 2),
		rateLimitv6:  rate.NewLimiter(rateLimit, 2),
	}

	return l
}

// Run initialize the local eni
func (l *Local) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	err := l.load(podResources)
	if err != nil {
		return err
	}

	wg.Add(2)

	go func() {
		defer wg.Done()
		l.factoryAllocWorker(ctx)
	}()
	go func() {
		defer wg.Done()
		l.factoryDisposeWorker(ctx)
	}()

	go l.notify(ctx)

	go wait.JitterUntil(l.sync, 1*time.Minute, 1.0, true, ctx.Done())

	return nil
}

func (l *Local) notify(ctx context.Context) {
	<-ctx.Done()
	l.cond.Broadcast()
}

func (l *Local) load(podResources []daemon.PodResources) error {
	if l.eni == nil {
		return nil
	}

	// sync ips
	ipv4, ipv6, err := l.factory.LoadNetworkInterface(l.eni.MAC)
	if err != nil {
		return err
	}

	logf.Log.Info("load eni", "eni", l.eni.ID, "mac", l.eni.MAC, "ipv4", ipv4, "ipv6", ipv6)

	primary, err := netip.ParseAddr(l.eni.PrimaryIP.IPv4.String())
	if err != nil {
		return err
	}

	for _, v := range ipv4 {
		l.ipv4.Add(NewValidIP(v, netip.MustParseAddr(v.String()) == primary))
	}
	l.ipv6.PutValid(ipv6...)
	l.status = statusInUse

	// allocate to previous pods
	for _, podResource := range podResources {
		podID := podResource.PodInfo.Namespace + "/" + podResource.PodInfo.Name

		for _, res := range podResource.Resources {
			switch res.Type {
			case daemon.ResourceTypeENIIP, daemon.ResourceTypeENI:
			default:
				continue
			}

			if res.ENIID != l.eni.ID {
				continue
			}

			logf.Log.Info("existed pod", "pod", podID, "ipv4", res.IPv4, "ipv6", res.IPv6, "eni", l.eni.ID)

			// belong to this eni
			if res.IPv4 != "" {
				ip, err := netip.ParseAddr(res.IPv4)
				if err != nil {
					return &types.Error{
						Code: types.ErrInvalidDataType,
						Msg:  err.Error(),
						R:    err,
					}
				}
				v, ok := l.ipv4[ip]
				if !ok {
					continue
				}
				v.Allocate(podID)
			}
			if res.IPv6 != "" {
				ip, err := netip.ParseAddr(res.IPv6)
				if err != nil {
					return &types.Error{
						Code: types.ErrInvalidDataType,
						Msg:  err.Error(),
						R:    err,
					}
				}
				v, ok := l.ipv6[ip]
				if !ok {
					continue
				}
				v.Allocate(podID)
			}
		}
	}

	// adjust resource with cap
	// the only case here is for switch from eni multi ip to eni only mod
	if len(l.ipv4) > l.cap {
		logf.Log.Info("dispose the ipv4 as current ip is more then cap", "idles", len(l.ipv4.Idles()))
		for _, ip := range l.ipv4.Idles() {
			ip.Dispose()
		}
	}
	if len(l.ipv6) > l.cap {
		logf.Log.Info("dispose the ipv6 as current ip is more then cap", "idles", len(l.ipv4.Idles()))
		for _, ip := range l.ipv6.Idles() {
			ip.Dispose()
		}
	}

	return nil
}

// sync periodically sync the ip from remote
func (l *Local) sync() {
	l.cond.L.Lock()
	defer l.cond.L.Unlock()

	if l.eni == nil || l.status != statusInUse {
		return
	}

	ipv4, ipv6, err := l.factory.LoadNetworkInterface(l.eni.MAC)
	if err != nil {
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning, string(types.ErrOpenAPIErr), fmt.Sprintf("failed to sync eni, %s", err.Error()))
		return
	}

	syncIPLocked(l.ipv4, ipv4)
	syncIPLocked(l.ipv6, ipv6)

	l.cond.Broadcast()
}

func (l *Local) Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {

	if request.ResourceType() != ResourceTypeLocalIP {
		return nil, []Trace{{Condition: ResourceTypeMismatch}}
	}
	if request.(*LocalIPRequest).LocalIPType == LocalIPTypeERDMA && l.eniType != "erdma" {
		return nil, []Trace{{Condition: ResourceTypeMismatch}}
	}

	l.cond.L.Lock()
	defer l.cond.L.Unlock()

	switch l.status {
	case statusDeleting:
		return nil, nil
	}

	lo, ok := request.(*LocalIPRequest)
	if !ok {
		return nil, []Trace{{Condition: ResourceTypeMismatch}}
	}

	if lo.NetworkInterfaceID != "" && l.eni.ID != lo.NetworkInterfaceID {
		return nil, []Trace{{Condition: NetworkInterfaceMismatch}}
	}

	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("local request %v", lo))

	expectV4 := 0
	expectV6 := 0

	if l.enableIPv4 {
		if lo.NoCache {
			expectV4 = 1
		} else {
			ipv4 := l.ipv4.PeekAvailable(cni.PodID)
			if ipv4 == nil && len(l.ipv4)+l.allocatingV4 >= l.cap {
				return nil, []Trace{{Condition: Full}}
			} else if ipv4 == nil {
				expectV4 = 1
			}
		}
	}

	if l.enableIPv6 {
		if lo.NoCache {
			expectV6 = 1
		} else {
			ipv6 := l.ipv6.PeekAvailable(cni.PodID)
			if ipv6 == nil && len(l.ipv6)+l.allocatingV6 >= l.cap {
				return nil, []Trace{{Condition: Full}}
			} else if ipv6 == nil {
				expectV6 = 1
			}
		}
	}

	if expectV4 > 0 && expectV6 > 0 && l.ipAllocInhibitExpireAt.After(time.Now()) {
		log.Info("eni alloc inhibit", "expire", l.ipAllocInhibitExpireAt.String())
		return nil, []Trace{{Condition: InsufficientVSwitchIP, Reason: fmt.Sprintf("alloc inhibit, expire at %s", l.ipAllocInhibitExpireAt.String())}}
	}

	l.allocatingV4 += expectV4
	l.allocatingV6 += expectV6

	l.cond.Broadcast()

	respCh := make(chan *AllocResp)

	go l.allocWorker(ctx, cni, lo, respCh, func() {
		// current roll back ip at same time
		l.allocatingV4 -= expectV4
		l.allocatingV4 = max(l.allocatingV4, 0)
		l.allocatingV6 -= expectV6
		l.allocatingV6 = max(l.allocatingV6, 0)
		log.Info("rollback ipv4", "ipv4", expectV4)
	})

	return respCh, nil
}

// Release take the cni Del request and release resource to pool
func (l *Local) Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) bool {
	if request.ResourceType() != ResourceTypeLocalIP {
		return false
	}

	l.cond.L.Lock()
	defer l.cond.L.Unlock()

	res := request.(*LocalIPResource)

	if l.eni == nil || l.eni.ID != res.ENI.ID {
		return false
	}

	log := logf.FromContext(ctx)

	if res.IP.IPv4.IsValid() {
		l.ipv4.Release(cni.PodID, res.IP.IPv4)

		log.Info("release ipv4", "ipv4", res.IP.IPv4)
	}
	if res.IP.IPv6.IsValid() {
		l.ipv6.Release(cni.PodID, res.IP.IPv6)

		log.Info("release ipv6", "ipv6", res.IP.IPv6)
	}

	return true
}

// Priority for local resource only
func (l *Local) Priority() int {
	l.cond.L.Lock()
	defer l.cond.L.Unlock()

	// unInitiated eni has the lower priority
	prio := 0
	switch l.status {
	case statusDeleting:
		return -100
	case statusInUse:
		prio = 50
	case statusCreating:
		prio = 10
	case statusInit:
		prio = 0
	}

	if l.enableIPv4 {
		prio += len(l.ipv4)
	}
	if l.enableIPv6 {
		prio += len(l.ipv6)
	}
	return prio
}

// allocWorker started with each Allocate call
func (l *Local) allocWorker(ctx context.Context, cni *daemon.CNI, request *LocalIPRequest, respCh chan *AllocResp, onErrLocked func()) {
	done := make(chan struct{})
	defer close(done)

	l.cond.L.Lock()
	defer l.cond.L.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			l.cond.L.Lock()
			l.cond.Broadcast()
			l.cond.L.Unlock()
		case <-done:
		}
	}()

	log := logf.FromContext(ctx)
	for {
		select {
		case <-ctx.Done():
			// parent cancel the context, so close the ch
			onErrLocked()

			close(respCh)
			return
		default:
		}

		resp := &AllocResp{}

		var ip types.IPSet2
		var ipv4, ipv6 *IP
		if l.enableIPv4 {
			ipv4 = l.ipv4.PeekAvailable(cni.PodID)
			if ipv4 == nil {
				l.cond.Wait()
				continue
			}
			ip.IPv4 = ipv4.ip
		}
		if l.enableIPv6 {
			ipv6 = l.ipv6.PeekAvailable(cni.PodID)
			if ipv6 == nil {
				l.cond.Wait()
				continue
			}
			ip.IPv6 = ipv6.ip
		}

		resp.NetworkConfigs = append(resp.NetworkConfigs, &LocalIPResource{
			ENI: *l.eni,
			IP:  ip,
		})

		log.Info("allocWorker got ip", "eni", l.eni.ID, "ipv4", ip.IPv4.String(), "ipv6", ip.IPv6.String())

		select {
		case <-ctx.Done():
			continue
		case respCh <- resp:
			// mark the ip as allocated
			if ipv4 != nil {
				ipv4.Allocate(cni.PodID)
			}
			if ipv6 != nil {
				ipv6.Allocate(cni.PodID)
			}
		}

		return
	}
}

func (l *Local) factoryAllocWorker(ctx context.Context) {
	l.cond.L.Lock()

	log := logf.FromContext(ctx)
	for {
		if log.V(4).Enabled() {
			log.V(4).Info("call allocWorker")
		}

		select {
		case <-ctx.Done():
			l.cond.L.Unlock()
			return
		default:
		}

		if l.allocatingV4 <= 0 && l.allocatingV6 <= 0 {
			l.cond.Wait()
			continue
		}

		switch l.status {
		case statusCreating, statusDeleting:
			l.cond.Wait()
			continue
		case statusInit, statusInUse:
		}

		if l.ipAllocInhibitExpireAt.After(time.Now()) {
			l.cond.Wait()
			continue
		}

		// wait a small period
		l.cond.L.Unlock()
		time.Sleep(300 * time.Millisecond)
		l.cond.L.Lock()

		if l.eni == nil {
			// create eni
			v4Count := min(l.batchSize, max(l.allocatingV4, 1))
			v6Count := min(l.batchSize, l.allocatingV6)

			l.status = statusCreating
			l.cond.L.Unlock()

			err := l.rateLimitEni.Wait(ctx)
			if err != nil {
				log.Error(err, "wait for rate limit failed")
				l.cond.L.Lock()
				continue
			}
			eni, ipv4Set, ipv6Set, err := l.factory.CreateNetworkInterface(v4Count, v6Count, l.eniType)
			if err == nil {
				err = setupENICompartment(eni)
			}

			if err != nil {
				log.Error(err, "create eni failed")

				l.cond.L.Lock()
				l.errorHandleLocked(err)

				l.eni = eni

				// if create failed, mark eni as deleting
				if eni != nil {
					log.Info("mark eni as deleting", "eni", eni.ID)
					l.status = statusDeleting
					l.cond.Broadcast()
				} else {
					l.status = statusInit
				}
				continue
			}

			l.cond.L.Lock()

			l.eni = eni

			l.allocatingV4 -= v4Count
			l.allocatingV6 -= v6Count

			l.allocatingV4 = max(l.allocatingV4, 0)
			l.allocatingV6 = max(l.allocatingV6, 0)

			primary, err := netip.ParseAddr(eni.PrimaryIP.IPv4.String())
			if err == nil {
				for _, v := range ipv4Set {
					l.ipv4.Add(NewValidIP(v, netip.MustParseAddr(v.String()) == primary))
				}
			}

			l.ipv6.PutValid(ipv6Set...)

			l.status = statusInUse
		} else {
			eniID := l.eni.ID
			v4Count := min(l.batchSize, l.allocatingV4)
			v6Count := min(l.batchSize, l.allocatingV6)

			if v4Count > 0 {
				l.cond.L.Unlock()

				err := l.rateLimitv4.Wait(ctx)
				if err != nil {
					log.Error(err, "wait for rate limit failed")
					l.cond.L.Lock()
					continue
				}
				ipv4Set, err := l.factory.AssignNIPv4(eniID, v4Count, l.eni.MAC)

				l.cond.L.Lock()

				if err != nil {
					log.Error(err, "assign ipv4 failed", "eni", eniID)
					l.ipv4.PutDeleting(ipv4Set...)

					l.errorHandleLocked(err)

					continue
				}

				l.allocatingV4 -= len(ipv4Set)
				l.allocatingV4 = max(l.allocatingV4, 0)

				l.ipv4.PutValid(ipv4Set...)
			}

			if v6Count > 0 {
				l.cond.L.Unlock()

				err := l.rateLimitv6.Wait(ctx)
				if err != nil {
					log.Error(err, "wait for rate limit failed")
					l.cond.L.Lock()
					continue
				}
				ipv6Set, err := l.factory.AssignNIPv6(eniID, v6Count, l.eni.MAC)

				l.cond.L.Lock()

				if err != nil {
					log.Error(err, "assign ipv6 failed", "eni", eniID)

					l.ipv6.PutDeleting(ipv6Set...)

					l.errorHandleLocked(err)

					continue
				}

				l.allocatingV6 -= len(ipv6Set)
				l.allocatingV6 = max(l.allocatingV6, 0)

				l.ipv6.PutValid(ipv6Set...)
			}
		}

		l.cond.Broadcast()
	}
}

func (l *Local) Dispose(n int) int {
	l.cond.L.Lock()
	defer l.cond.L.Unlock()

	if l.eni == nil || l.status != statusInUse {
		return 0
	}

	log := logf.Log.WithValues("eni", l.eni.ID, "mac", l.eni.MAC)

	defer l.cond.Broadcast()

	// 1. check if can dispose the eni
	if n >= max(len(l.ipv4), len(l.ipv6)) {
		if l.eni.Type != "trunk" && len(l.ipv4.InUse()) == 0 && len(l.ipv6.InUse()) == 0 {
			log.Info("dispose eni")
			l.status = statusDeleting
			return max(len(l.ipv4), len(l.ipv6))
		}
	}

	// 2. dispose invalid first
	// it is not take into account
	for _, v := range l.ipv4 {
		if v.InUse() || v.Valid() {
			continue
		}
		log.Info("dispose invalid ipv4", "ip", v.ip.String())
		v.Dispose()
	}
	for _, v := range l.ipv6 {
		if v.InUse() || v.Valid() {
			continue
		}
		log.Info("dispose invalid ipv6", "ip", v.ip.String())
		v.Dispose()
	}

	// 3. dispose idle
	left := min(len(l.ipv4.Idles()), n)

	for i := 0; i < left; i++ {
		for _, v := range l.ipv4 {
			if v.InUse() || v.Deleting() {
				continue
			}
			v.Dispose() // small problem for primary ip
			break
		}
	}

	left6 := min(len(l.ipv6.Idles()), n)

	for i := 0; i < left6; i++ {
		for _, v := range l.ipv6 {
			if v.InUse() || v.Deleting() {
				continue
			}
			v.Dispose()
			break
		}
	}

	return max(left, left6)
}

func (l *Local) factoryDisposeWorker(ctx context.Context) {
	l.cond.L.Lock()

	log := logf.FromContext(ctx)
	for {
		select {
		case <-ctx.Done():
			l.cond.L.Unlock()
			return
		default:
		}

		if l.eni == nil {
			l.cond.Wait()
			continue
		}

		if l.status == statusDeleting {
			// remove the eni

			l.cond.L.Unlock()

			err := l.rateLimitEni.Wait(ctx)
			if err != nil {
				log.Error(err, "wait for rate limit failed")
				l.cond.L.Lock()
				continue
			}
			err = l.factory.DeleteNetworkInterface(l.eni.ID)
			if err == nil {
				err = destroyENICompartment(l.eni)
			}

			l.cond.L.Lock()

			if err != nil {
				continue
			}

			l.eni = nil
			l.ipv4 = make(Set)
			l.ipv6 = make(Set)
			l.status = statusInit
			l.ipAllocInhibitExpireAt = time.Time{}

			l.cond.Broadcast()
			continue
		}

		toDelete4 := l.ipv4.Deleting()
		toDelete6 := l.ipv6.Deleting()

		if toDelete4 == nil && toDelete6 == nil {
			l.cond.Wait()
			continue
		}

		if len(toDelete4) > l.batchSize {
			toDelete4 = toDelete4[:l.batchSize]
		}
		if len(toDelete6) > l.batchSize {
			toDelete6 = toDelete6[:l.batchSize]
		}

		if len(toDelete4) > 0 {
			l.cond.L.Unlock()
			err := l.factory.UnAssignNIPv4(l.eni.ID, toDelete4, l.eni.MAC)
			l.cond.L.Lock()

			if err == nil {
				l.ipv4.Delete(toDelete4...)
			}
		}

		if len(toDelete6) > 0 {
			l.cond.L.Unlock()
			err := l.factory.UnAssignNIPv6(l.eni.ID, toDelete6, l.eni.MAC)
			l.cond.L.Lock()

			if err == nil {
				l.ipv6.Delete(toDelete6...)
			}
		}
	}
}

func (l *Local) errorHandleLocked(err error) {
	if err == nil {
		return
	}

	if apiErr.ErrAssert(apiErr.ErrEniPerInstanceLimitExceeded, err) {
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning, string(types.ErrIPOutOfSyncErr), err.Error())
		next := time.Now().Add(1 * time.Minute)
		if next.After(l.ipAllocInhibitExpireAt) {
			l.ipAllocInhibitExpireAt = next
		}
	}

	if apiErr.ErrAssert(apiErr.InvalidVSwitchIDIPNotEnough, err) {
		next := time.Now().Add(10 * time.Minute)
		if next.After(l.ipAllocInhibitExpireAt) {
			l.ipAllocInhibitExpireAt = next
		}
	}
}

func (l *Local) Usage() (int, int, error) {
	// return idle and inUse resource
	l.cond.L.Lock()
	defer l.cond.L.Unlock()

	if l.eni == nil || l.status != statusInUse {
		return 0, 0, nil
	}

	var idles, inUse int
	if l.enableIPv4 {
		idles = len(l.ipv4.Idles())
		inUse = len(l.ipv4.InUse())
	} else if l.enableIPv6 {
		idles = len(l.ipv6.Idles())
		inUse = len(l.ipv6.InUse())
	}
	return idles, inUse, nil
}

func (l *Local) Status() Status {
	l.cond.L.Lock()
	defer l.cond.L.Unlock()

	s := Status{
		Status:               l.status.String(),
		Type:                 l.eniType,
		AllocInhibitExpireAt: l.ipAllocInhibitExpireAt.String(),
	}
	if l.eni == nil {
		return s
	}

	s.MAC = l.eni.MAC
	s.NetworkInterfaceID = l.eni.ID

	usage := make([][]string, 0, len(l.ipv4)+len(l.ipv6))
	for _, v := range l.ipv4 {
		usage = append(usage, []string{v.ip.String(), v.podID, v.status.String()})
	}
	for _, v := range l.ipv6 {
		usage = append(usage, []string{v.ip.String(), v.podID, v.status.String()})
	}

	sort.Slice(usage, func(i, j int) bool {
		return usage[i][0] > usage[j][0]
	})
	s.Usage = usage
	return s
}

// syncIPLocked will mark ip as invalid , if not found in remote
func syncIPLocked(lo Set, remote []netip.Addr) {
	s := sets.New[netip.Addr](remote...)
	for _, v := range lo {
		if !s.Has(v.ip) {
			logf.Log.Info("remote ip gone, mark as invalid", "ip", v.ip.String())
			_ = tracing.RecordNodeEvent(corev1.EventTypeWarning, string(types.ErrResourceInvalid), fmt.Sprintf("Mark as invalid, ip: %s", v.ip.String()))
			v.SetInvalid()
		}
	}
}
