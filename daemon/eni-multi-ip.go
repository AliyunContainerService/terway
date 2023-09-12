package daemon

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	terwayIP "github.com/AliyunContainerService/terway/pkg/ip"
	"github.com/AliyunContainerService/terway/pkg/ipam"
	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/pkg/pool"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
)

type AllocCtx struct {
	Trace []Trace
}

func (a *AllocCtx) String() string {
	var s []string
	for _, t := range a.Trace {
		s = append(s, t.Msg)
	}
	return strings.Join(s, "\n")
}

type Trace struct {
	Msg string
}

var eniIPLog = logger.DefaultLogger

const (
	maxEniOperating = 3
	maxIPBacklog    = 10
)

const (
	eniIPAllocInhibitTimeout = 10 * time.Minute

	typeNameENIIP    = "eniip"
	poolNameENIIP    = "eniip"
	factoryNameENIIP = "eniip"

	tracingKeyENIMaxIP         = "eni_max_ip"
	tracingKeyENICount         = "eni_count"
	tracingKeySecondaryIPCount = "secondary_ip_count"

	commandAudit = "audit"
)

const timeFormat = "2006-01-02 15:04:05"

type eniIPFactory struct {
	name         string
	enableTrunk  bool
	trunkOnEni   string
	eniFactory   *eniFactory
	enis         []*ENI
	maxENI       chan struct{}
	eniMaxIP     int
	nodeMaxENI   int
	eniOperChan  chan struct{}
	ipResultChan chan *ENIIP
	sync.RWMutex
	// metrics
	metricENICount prometheus.Gauge

	ipFamily *types.IPFamily
}

// ENIIP the secondary ip of eni
type ENIIP struct {
	*types.ENIIP
	err error
}

// ENI to hold ENI's secondary config
type ENI struct {
	lock sync.Mutex
	*types.ENI
	ips       []*ENIIP
	pending   int
	ipBacklog chan struct{}
	ecs       ipam.API
	done      chan struct{}
	// Unix timestamp to mark when this ENI can allocate Pod IP.
	ipAllocInhibitExpireAt time.Time
}

func (e *ENI) getIPCountLocked() int {
	return e.pending + len(e.ips)
}

// eni ip allocator
func (e *ENI) allocateWorker(resultChan chan<- *ENIIP) {
	for {
		toAllocate := 0
		select {
		case <-e.done:
			return
		case <-e.ipBacklog:
			toAllocate = 1
		}
		// wait 300ms for aggregation the cni request
		time.Sleep(300 * time.Millisecond)
	popAll:
		for {
			select {
			case <-e.ipBacklog:
				toAllocate++
			default:
				break popAll
			}
			if toAllocate >= maxIPBacklog {
				break
			}
		}
		eniIPLog.Debugf("allocate %v ips for eni", toAllocate)
		v4, v6, err := e.ecs.AssignNIPsForENI(context.Background(), e.ENI.ID, e.ENI.MAC, toAllocate)
		eniIPLog.Debugf("allocated ips for eni: eni = %+v, v4 = %+v,v6 = %+v, err = %v", e.ENI, v4, v6, err)
		if err != nil {
			eniIPLog.Errorf("error allocate ips for eni: %v", err)
			metric.ENIIPFactoryIPAllocCount.WithLabelValues(e.MAC, metric.ENIIPAllocActionFail).Add(float64(toAllocate))
			for i := 0; i < toAllocate; i++ {
				resultChan <- &ENIIP{
					ENIIP: &types.ENIIP{
						ENI: e.ENI,
					},
					err: errors.Errorf("error assign ip for ENI: %v", err),
				}
			}
		} else {
			metric.ENIIPFactoryIPAllocCount.WithLabelValues(e.MAC, metric.ENIIPAllocActionSucceed).Add(float64(toAllocate))
			for _, ip := range types.MergeIPs(v4, v6) {
				resultChan <- &ENIIP{
					ENIIP: &types.ENIIP{
						ENI:   e.ENI,
						IPSet: ip,
					},
					err: nil,
				}
			}
		}
	}
}

func (f *eniIPFactory) getEnis(ctx *AllocCtx) ([]*ENI, error) {
	var (
		enis        []*ENI
		pendingEnis []*ENI
	)
	enisLen := len(f.enis)
	// If there is only one switch, then no need for ordering.
	if enisLen <= 1 {
		return f.enis, nil
	}
	// pre sort eni by ip count to balance ip allocation on enis
	sort.Slice(f.enis, func(i, j int) bool {
		return f.enis[i].getIPCountLocked() < f.enis[j].getIPCountLocked()
	})

	// If VSwitchSelectionPolicy is ordered, then call f.eniFactory.GetVSwitches() API to get a switch slice
	// in descending order per each switch's available IP count.
	vSwitches, err := f.eniFactory.GetVSwitches()
	eniIPLog.Infof("adjusted vswitch slice: %+v, original eni slice: %s", vSwitches, func(enis []*ENI) string {
		var vsw []string
		for i := 0; i < len(enis); i++ {
			if enis[i] == nil || enis[i].ENI == nil {
				continue
			}
			vsw = append(vsw, enis[i].VSwitchID)
		}
		return strings.Join(vsw, ",")
	}(f.enis))
	if err != nil {
		eniIPLog.Errorf("error to get vswitch slice: %v, instead use original eni slice in eniIPFactory: %v", err, f.enis)
		return f.enis, err
	}
	for _, vsw := range vSwitches {
		for _, eni := range f.enis {
			if eni.ENI == nil {
				pendingEnis = append(pendingEnis, eni)
				continue
			}
			if vsw.id == eni.VSwitchID {
				enis = append(enis, eni)
			}
		}
	}
	enis = append(enis, pendingEnis...)
	if len(enis) == 0 && len(f.enis) != 0 {
		ctx.Trace = append(ctx.Trace, Trace{
			Msg: "current eni have vSwitch not as expected, please check eni-config",
		})
	}

	return enis, nil
}

func (f *eniIPFactory) submit(ctx *AllocCtx) error {
	f.Lock()
	defer f.Unlock()
	var enis []*ENI
	enis, _ = f.getEnis(ctx)
	for _, eni := range enis {
		eniIPLog.Infof("check existing eni: %+v", eni)
		eni.lock.Lock()
		now := time.Now()
		if eni.ENI != nil {
			eniIPLog.Infof("check if the current eni is in the time window for IP allocation inhibition: "+
				"eni = %+v, vsw= %s, now = %s, expireAt = %s", eni, eni.VSwitchID, now.Format(timeFormat), eni.ipAllocInhibitExpireAt.Format(timeFormat))
		}
		// if the current eni has been inhibited for Pod IP allocation, then skip current eni.
		if now.Before(eni.ipAllocInhibitExpireAt) && eni.ENI != nil {
			eni.lock.Unlock()

			// vsw have insufficient ip
			ctx.Trace = append(ctx.Trace, Trace{Msg: fmt.Sprintf("eni %s vSwitch %s have insufficient IP, next check %s", eni.ID, eni.VSwitchID, eni.ipAllocInhibitExpireAt)})

			eniIPLog.Debugf("skip IP allocation: eni = %+v, vsw = %s", eni, eni.VSwitchID)
			continue
		}

		eniIPLog.Debugf("check if the current eni will reach eni IP quota with new pending IP added: "+
			"eni = %+v, eni.pending = %d, len(eni.ips) = %d, eni.MaxIPs = %d", eni, eni.pending, len(eni.ips), f.eniMaxIP)
		if eni.getIPCountLocked() < f.eniMaxIP {
			select {
			case eni.ipBacklog <- struct{}{}:
			default:
				eni.lock.Unlock()
				continue
			}
			eniIPLog.Debugf("submit eniip to the eni backlog: %+v", eni)
			eni.pending++
			eni.lock.Unlock()
			return nil
		}
		eni.lock.Unlock()
	}
	return errors.Errorf("trigger ENIIP throttle, max operating concurrent: %v", maxIPBacklog)
}

func (f *eniIPFactory) ipExhaustErrorLocked(apiErr error) *types.IPInsufficientError {
	// apiErr already be Insufficient error, should be return by eniFactory
	if err, ok := apiErr.(*types.IPInsufficientError); ok {
		return err
	}
	// ordered policy has vswitches ip count
	if f.eniFactory.vswitchSelectionPolicy == types.VSwitchSelectionPolicyOrdered {
		vswitches, err := f.eniFactory.GetVSwitches()
		if err != nil {
			eniIPLog.Warnf("cannot get vswitches %v on judging error type", err)
			return nil
		}
		// node already has free eni slot, should retry in eniFactory
		if len(f.enis) < f.nodeMaxENI {
			return nil
		}
		var allEniIDs []string
		var allEniVswitches = map[string]int{}
		for _, eni := range f.enis {
			if eni.ENI == nil {
				// node still has pending eni maybe can allocate eniip
				return nil
			}
			for _, vsw := range vswitches {
				if eni.VSwitchID == vsw.id {
					// the node still has some eni can allocate eniip
					if eni.getIPCountLocked() < f.eniMaxIP && vsw.ipCount >= 0 && !strings.Contains(apiErr.Error(), vsw.id) {
						return nil
					}
					allEniIDs = append(allEniIDs, eni.ID)
					allEniVswitches[vsw.id] = 0
				}
			}
		}
		return &types.IPInsufficientError{
			Err:    apiErr,
			Reason: fmt.Sprintf("ENI: %v on vswitches: %v \n all has no available ips to allocate", strings.Join(allEniIDs, ","), allEniVswitches),
		}
	}
	return nil
}

func (f *eniIPFactory) popResult() (ip *types.ENIIP, err error) {
	result := <-f.ipResultChan
	eniIPLog.Debugf("pop result from resultChan: %+v", result)
	if result.ENIIP == nil || result.err != nil {
		// There are two error cases:
		// Error Case 1. The ENI-associated VSwitch has no available IP for Pod IP allocation.
		// Error Case 2. The IP number allocated has reached ENI quota.
		f.Lock()
		defer f.Unlock()
		if result.ENIIP != nil && result.ENI != nil {
			for _, eni := range f.enis {
				// eni is not ready
				if eni == nil {
					continue
				}
				if eni.MAC == result.ENI.MAC {
					eni.pending--
					// if an error message with InvalidVSwitchIDIPNotEnough returned, then mark the ENI as IP allocation inhibited.
					if strings.Contains(result.err.Error(), apiErr.InvalidVSwitchIDIPNotEnough) {
						eni.ipAllocInhibitExpireAt = time.Now().Add(eniIPAllocInhibitTimeout)
						eniIPLog.Infof("eni's associated vswitch %s has no available IP, set eni ipAllocInhibitExpireAt = %s",
							eni.VSwitchID, eni.ipAllocInhibitExpireAt.Format(timeFormat))
						ipExhaustErr := f.ipExhaustErrorLocked(result.err)
						if ipExhaustErr != nil {
							result.err = ipExhaustErr
						}
					}
				}
			}
		}
		if strings.Contains(result.err.Error(), apiErr.ErrSecurityGroupInstanceLimitExceed) {
			return nil, &types.IPInsufficientError{
				Err:    err,
				Reason: "security group of eni exceeded max ip limit",
			}
		}
		if _, ok := result.err.(*types.IPInsufficientError); ok {
			return nil, result.err
		}
		return nil, errors.Errorf("error allocate ip from eni: %v", result.err)
	}
	f.Lock()
	defer f.Unlock()
	for _, eni := range f.enis {
		if eni.ENI != nil && eni.MAC == result.ENI.MAC {
			eni.pending--
			eni.lock.Lock()
			eni.ips = append(eni.ips, result)
			eni.lock.Unlock()
			metric.ENIIPFactoryIPCount.WithLabelValues(f.name, eni.MAC, fmt.Sprint(f.eniMaxIP)).Inc()
			return result.ENIIP, nil
		}
	}
	return nil, errors.Errorf("unexpected eni ip allocated: %v", result)
}

func (f *eniIPFactory) Create(count int) ([]types.NetworkResource, error) {
	ctx := &AllocCtx{}
	var (
		ipResult []types.NetworkResource
		err      error
		waiting  int
	)
	defer func() {
		if len(ipResult) == 0 {
			eniIPLog.Debugf("create result: %v, error: %v", ipResult, err)
		} else {
			for _, ip := range ipResult {
				eniIPLog.Debugf("create result nil: %+v, error: %v", ip, err)
			}
		}
	}()

	// find for available ENIs and submit for ip allocation
	for ; waiting < count; waiting++ {
		err = f.submit(ctx)
		if err != nil {
			break
		}
	}
	// there are (count - waiting) ips still wait for allocation
	// create a new ENI with initial ip count (count - waiting) as initENIIPCount
	// initENIIPCount can't be greater than eniMaxIP and maxIPBacklog
	initENIIPCount := count - waiting
	if initENIIPCount > f.eniMaxIP {
		initENIIPCount = f.eniMaxIP
	}
	if initENIIPCount > maxIPBacklog {
		initENIIPCount = maxIPBacklog
	}
	if initENIIPCount > 0 {
		eniIPLog.Debugf("create eni async, ip count: %+v", initENIIPCount)
		_, err = f.createENIAsync(initENIIPCount)
		if err == nil {
			waiting += initENIIPCount
		} else {
			eniIPLog.Errorf("error create eni async: %+v", err)
		}
	}

	// no ip has been created
	if waiting == 0 {
		return ipResult, errors.Errorf("error submit ip create request: %v,%s", err, ctx.String())
	}

	var ip *types.ENIIP
	for ; waiting > 0; waiting-- { // receive allocate result
		ip, err = f.popResult()
		if err != nil {
			eniIPLog.Errorf("error allocate ip address: %+v", err)
		} else {
			ipResult = append(ipResult, ip)
		}
	}
	if len(ipResult) == 0 {
		if _, ok := err.(*types.IPInsufficientError); ok {
			return nil, err
		}
		return ipResult, errors.Errorf("error allocate ip address: %v", err)
	}

	return ipResult, nil
}

func (f *eniIPFactory) Dispose(res types.NetworkResource) (err error) {
	defer func() {
		eniIPLog.Debugf("dispose result: %v, error: %v", res.GetResourceID(), err != nil)
	}()
	ip := res.(*types.ENIIP)
	if ip.ENI == nil {
		return nil
	}
	var (
		eni   *ENI
		eniip *ENIIP
	)
	f.RLock()
	for _, e := range f.enis {
		if ip.ENI.ID == e.ID {
			eni = e
			e.lock.Lock()
			for _, eip := range e.ips {
				if eip.IPSet.String() == ip.IPSet.String() {
					eniip = eip
				}
			}
			e.lock.Unlock()
		}
	}
	f.RUnlock()
	if eni == nil || eniip == nil {
		return fmt.Errorf("invalid resource to dispose")
	}

	eni.lock.Lock()
	if len(eni.ips) == 1 {
		if f.enableTrunk && eni.Trunk {
			eni.lock.Unlock()
			return fmt.Errorf("trunk ENI %+v will not dispose", eni.ID)
		}

		if eni.pending > 0 {
			eni.lock.Unlock()
			return fmt.Errorf("ENI have pending ips to be allocate")
		}
		// block ip allocate
		eni.pending = f.eniMaxIP
		eni.lock.Unlock()

		f.Lock()
		for i, e := range f.enis {
			if ip.ENI.ID == e.ID {
				close(eni.done)
				f.enis[len(f.enis)-1], f.enis[i] = f.enis[i], f.enis[len(f.enis)-1]
				f.enis = f.enis[:len(f.enis)-1]
				break
			}
		}
		f.metricENICount.Dec()
		f.Unlock()

		err = f.destroyENICompartment(ip.ENI)
		if err != nil {
			return fmt.Errorf("error destroy eni compartment before disposing: %v", err)
		}

		f.eniOperChan <- struct{}{}
		// only remain ENI main ip address, release the ENI interface
		err = f.eniFactory.Dispose(ip.ENI)
		<-f.eniOperChan
		if err != nil {
			return fmt.Errorf("error dispose ENI for eniip, %v", err)
		}
		<-f.maxENI
		return nil
	}
	eni.lock.Unlock()

	// main ip of ENI, raise put_it_back error
	if ip.ENI.PrimaryIP.IPv4.Equal(ip.IPSet.IPv4) {
		// if in dual-stack and have no ipv6 address may need add one ip for it
		return fmt.Errorf("ip to be release is primary ip of ENI")
	}

	var v4, v6 []net.IP
	if ip.IPSet.IPv4 != nil {
		v4 = append(v4, ip.IPSet.IPv4)
	}
	if ip.IPSet.IPv6 != nil {
		v6 = append(v6, ip.IPSet.IPv6)
	}
	err = f.eniFactory.ecs.UnAssignIPsForENI(context.Background(), ip.ENI.ID, ip.ENI.MAC, v4, v6)
	if err != nil {
		return fmt.Errorf("error unassign eniip, %v", err)
	}
	eni.lock.Lock()
	for i, e := range eni.ips {
		if e.IPSet.IPv4.Equal(eniip.IPSet.IPv4) {
			eni.ips[len(eni.ips)-1], eni.ips[i] = eni.ips[i], eni.ips[len(eni.ips)-1]
			eni.ips = eni.ips[:len(eni.ips)-1]
			break
		}
	}
	eni.lock.Unlock()
	metric.ENIIPFactoryIPCount.WithLabelValues(f.name, eni.MAC, fmt.Sprint(f.eniMaxIP)).Dec()
	return nil
}

// Check resource in remote
func (f *eniIPFactory) Check(res types.NetworkResource) error {
	eniIP, ok := res.(*types.ENIIP)
	if !ok {
		return fmt.Errorf("unsupported type %T", res)
	}

	ipv4, ipv6, err := f.eniFactory.ecs.GetENIIPs(context.Background(), eniIP.ENI.MAC)
	if err != nil {
		return err
	}
	if utils.IsWindowsOS() {
		// NB(thxCode): don't assign the primary IP of the assistant eni.
		ipv4, ipv6 = dropPrimaryIP(eniIP.ENI, ipv4, ipv6)
	}

	if eniIP.IPSet.IPv4 != nil {
		if !terwayIP.IPsIntersect([]net.IP{eniIP.IPSet.IPv4}, ipv4) {
			return apiErr.ErrNotFound
		}
	}

	if eniIP.IPSet.IPv6 != nil {
		if !terwayIP.IPsIntersect([]net.IP{eniIP.IPSet.IPv6}, ipv6) {
			return apiErr.ErrNotFound
		}
	}

	return nil
}

// ListResource load all eni info from metadata
func (f *eniIPFactory) ListResource() (map[string]types.NetworkResource, error) {
	f.RLock()
	defer f.RUnlock()

	// list all resource status in our pool
	mapping := make(map[string]types.NetworkResource)
	var inUseENIIPs []*types.ENIIP
	for _, e := range f.enis {
		e.lock.Lock()
		for _, eniIP := range e.ips {
			inUseENIIPs = append(inUseENIIPs, eniIP.ENIIP)
		}

		e.lock.Unlock()
	}
	ctx := context.Background()
	macs, err := f.eniFactory.ecs.GetSecondaryENIMACs(ctx)
	if err != nil {
		return nil, err
	}

	for _, mac := range macs {
		// get secondary ips from one mac
		ipv4s, ipv6s, err := f.eniFactory.ecs.GetENIIPs(ctx, mac)
		if err != nil {
			if errors.Is(err, apiErr.ErrNotFound) {
				continue
			}
			return nil, err
		}
		ipv4Set := terwayIP.ToIPMap(ipv4s)
		ipv6Set := terwayIP.ToIPMap(ipv6s)

		for _, eniIP := range inUseENIIPs {
			if eniIP.ENI.MAC != mac {
				continue
			}

			var v4, v6 net.IP
			if eniIP.IPSet.IPv4 != nil {
				_, ok := ipv4Set[eniIP.IPSet.IPv4.String()]
				if ok {
					v4 = eniIP.IPSet.IPv4
				}
			}
			if eniIP.IPSet.IPv6 != nil {
				_, ok := ipv6Set[eniIP.IPSet.IPv6.String()]
				if ok {
					v6 = eniIP.IPSet.IPv6
				}
			}

			tmp := types.ENIIP{
				ENI: &types.ENI{
					MAC: mac,
				},
				IPSet: types.IPSet{
					IPv4: v4,
					IPv6: v6,
				},
			}

			mapping[tmp.GetResourceID()] = &tmp
		}
	}

	return mapping, nil
}

func (f *eniIPFactory) Reconcile() {
	// check security group
	err := f.eniFactory.ecs.CheckEniSecurityGroup(context.Background(), f.eniFactory.securityGroupIDs)
	if err != nil {
		_ = tracing.RecordNodeEvent(corev1.EventTypeWarning, "ResourceInvalid", fmt.Sprintf("eni has misconfiged security group. %s", err.Error()))
	}
}

func (f *eniIPFactory) initialENI(eni *ENI, ipCount int) {
	if utils.IsWindowsOS() {
		// NB(thxCode): create eni with one more IP in windows at initialization.
		ipCount++
	}
	rawEni, err := f.eniFactory.CreateWithIPCount(ipCount, false)
	var ipv4s []net.IP
	var ipv6s []net.IP
	// eni operate finished
	<-f.eniOperChan
	if err != nil || len(rawEni) != 1 {
		// create eni failed, put quota back
		<-f.maxENI
	} else {
		var ok bool
		eni.ENI, ok = rawEni[0].(*types.ENI)
		if !ok {
			err = fmt.Errorf("error get type ENI from factory, got: %+v, rollback it", rawEni)
			eniIPLog.Error(err)
			errDispose := f.eniFactory.Dispose(rawEni[0])
			if errDispose != nil {
				eniIPLog.Errorf("rollback %+v failed", rawEni)
			}
			<-f.maxENI
		} else {
			ipv4s, ipv6s, err = f.eniFactory.ecs.GetENIIPs(context.Background(), eni.MAC)
			if err != nil {
				eniIPLog.Errorf("error get eni secondary address: %+v, rollback it", err)
				errDispose := f.eniFactory.Dispose(rawEni[0])
				if errDispose != nil {
					eniIPLog.Errorf("rollback %+v failed", rawEni)
				}
				<-f.maxENI
			}
			if f.ipFamily.IPv4 && f.ipFamily.IPv6 {
				if len(ipv4s) != len(ipv6s) {
					eniIPLog.Errorf("error get eni secondary address: ipv4 ipv6 length is not equal, rollback it")
					errDispose := f.eniFactory.Dispose(rawEni[0])
					if errDispose != nil {
						eniIPLog.Errorf("rollback %+v failed", rawEni)
					}
					<-f.maxENI
				}
			}
			err = f.setupENICompartment(eni.ENI)
			if err != nil {
				eniIPLog.Errorf("error setup eni compartment after initialization: %v", err)
				errDispose := f.eniFactory.Dispose(rawEni[0])
				if errDispose != nil {
					eniIPLog.Errorf("rollback %+v failed", rawEni)
				}
				<-f.maxENI
			}
		}
	}

	eniIPLog.Debugf("eni initial finished: %+v, err: %+v", eni, err)

	if err != nil {
		eni.lock.Lock()
		//failed all pending on this initial eni
		for i := 0; i < eni.pending; i++ {
			eniip := &ENIIP{
				ENIIP: &types.ENIIP{
					ENI: nil,
				},
				err: fmt.Errorf("error initial ENI: %w", err),
			}
			if _, ok := err.(*types.IPInsufficientError); ok {
				eniip.err = err
			}
			f.ipResultChan <- eniip
		}
		// disable eni for submit
		eni.pending = f.eniMaxIP
		eni.lock.Unlock()

		// remove from eni list
		f.Lock()
		for i, e := range f.enis {
			if e == eni {
				f.enis[len(f.enis)-1], f.enis[i] = f.enis[i], f.enis[len(f.enis)-1]
				f.enis = f.enis[:len(f.enis)-1]
				break
			}
		}
		f.metricENICount.Dec()
		f.Unlock()

		return
	}

	eni.lock.Lock()
	if utils.IsWindowsOS() {
		// NB(thxCode): don't assign the primary IP of the assistant eni.
		ipv4s, ipv6s = dropPrimaryIP(eni.ENI, ipv4s, ipv6s)
	}
	eniIPLog.Infof("allocate status on async eni: %+v, pending: %v, ips: %v, backlog: %v",
		eni, eni.pending, ipv4s, len(eni.ipBacklog))

	for _, ipSet := range types.MergeIPs(ipv4s, ipv6s) {
		eniIP := &types.ENIIP{
			ENI:   eni.ENI,
			IPSet: ipSet,
		}

		f.ipResultChan <- &ENIIP{
			ENIIP: eniIP,
			err:   nil,
		}
	}

	eni.lock.Unlock()
	go eni.allocateWorker(f.ipResultChan)
}

func (f *eniIPFactory) createENIAsync(initIPs int) (*ENI, error) {
	eni := &ENI{
		ENI:       nil,
		ips:       make([]*ENIIP, 0),
		pending:   initIPs,
		ipBacklog: make(chan struct{}, maxIPBacklog),
		ecs:       f.eniFactory.ecs,
		done:      make(chan struct{}, 1),
	}
	select {
	case f.maxENI <- struct{}{}:
		select {
		case f.eniOperChan <- struct{}{}:
		default:
			<-f.maxENI
			return nil, fmt.Errorf("trigger ENI throttle, max operating concurrent: %v", maxEniOperating)
		}
		go f.initialENI(eni, eni.pending)
	default:
		return nil, &types.IPInsufficientError{
			Err:    fmt.Errorf("max ENI exceeded"),
			Reason: "all ENIs bind on host can not allocate ip address, and node has no slot to allocate ENI",
		}
	}
	f.Lock()
	f.enis = append(f.enis, eni)
	f.Unlock()
	// metric
	f.metricENICount.Inc()
	return eni, nil
}

func (f *eniIPFactory) Config() []tracing.MapKeyValueEntry {
	config := []tracing.MapKeyValueEntry{
		{Key: tracingKeyName, Value: f.name},
		{Key: tracingKeyENIMaxIP, Value: fmt.Sprint(f.eniMaxIP)},
	}

	return config
}

func (f *eniIPFactory) Trace() []tracing.MapKeyValueEntry {
	var trace []tracing.MapKeyValueEntry

	secIPCount := 0

	trace = append(trace, tracing.MapKeyValueEntry{Key: tracingKeyENICount, Value: fmt.Sprint(len(f.enis))})
	trace = append(trace, tracing.MapKeyValueEntry{Key: tracingKeySecondaryIPCount, Value: ""}) // placeholder

	for _, v := range f.enis {
		secIPCount += len(v.ips)
		trace = append(trace, tracing.MapKeyValueEntry{
			Key:   fmt.Sprintf("eni/%s/pending", v.MAC),
			Value: fmt.Sprint(v.pending),
		})

		var secIPs []string
		for _, v := range v.ips {
			secIPs = append(secIPs, v.IPSet.String())
		}

		trace = append(trace, tracing.MapKeyValueEntry{
			Key:   fmt.Sprintf("eni/%s/secondary_ips", v.MAC),
			Value: strings.Join(secIPs, " "),
		})

		trace = append(trace, tracing.MapKeyValueEntry{
			Key:   fmt.Sprintf("eni/%s/ip_alloc_inhibit_expire_at", v.MAC),
			Value: v.ipAllocInhibitExpireAt.Format(timeFormat),
		})
	}

	trace[1].Value = fmt.Sprint(secIPCount)

	return trace
}

func (f *eniIPFactory) Execute(cmd string, _ []string, message chan<- string) {
	switch cmd {
	case commandAudit: // check account
		f.checkAccount(message)
	case commandMapping:
		mapping, err := f.ListResource()
		message <- fmt.Sprintf("mapping: %v, err: %s\n", mapping, err)
	default:
		message <- "can't recognize command\n"
	}

	close(message)
}

func (f *eniIPFactory) checkAccount(message chan<- string) {
	// get ENIs via Aliyun API
	message <- "fetching attached ENIs from aliyun\n"
	ctx := context.Background()
	enis, err := f.eniFactory.ecs.GetAttachedENIs(ctx, false, f.trunkOnEni)
	if err != nil {
		message <- fmt.Sprintf("error while fetching from remote: %s\n", err.Error())
		return
	}

	message <- fmt.Sprintf("%d enis fetched\n", len(enis))

	f.Lock()
	// check local enis count
	if len(f.enis) != len(enis) {
		message <- fmt.Sprintf("remote eni count not equal to local: remote %d, local: %d\n", len(enis), len(f.enis))
	}

	// diff
	diffMap := make(map[string]*ENI)

	for _, v := range f.enis {
		diffMap[v.ID] = v
	}
	f.Unlock()

	// range for remote enis
	for _, v := range enis {
		message <- fmt.Sprintf("checking eni %s(%s)\n", v.ID, v.MAC)

		eni, ok := diffMap[v.ID]
		if !ok {
			message <- fmt.Sprintf("remote eni %s(%s) not found on local machine\n", v.ID, v.MAC)
			continue
		}

		// diff secondary ips
		remoteIPs, _, err := f.eniFactory.ecs.GetENIIPs(ctx, v.MAC)
		if err != nil {
			message <- fmt.Sprintf("error while fetching ips: %s", err.Error())
			return
		}
		if utils.IsWindowsOS() {
			// NB(thxCode): don't assign the primary IP of the assistant eni.
			remoteIPs, _ = dropPrimaryIP(v, remoteIPs, nil)
		}

		diffIPMap := make(map[string]struct{})

		for _, ip := range eni.ips {
			diffIPMap[ip.IPSet.IPv4.String()] = struct{}{}
		}

		// range for remote ips
		for _, ip := range remoteIPs {
			_, ok := diffIPMap[ip.String()]
			if !ok {
				message <- fmt.Sprintf("ip %s attached to eni %s(%s) not found on local machine\n", ip.String(), v.ID, v.MAC)
			}
		}
	}

	message <- "done.\n"
}

type eniIPResourceManager struct {
	trunkENI *types.ENI
	pool     pool.ObjectPool
}

func newENIIPResourceManager(poolConfig *types.PoolConfig, ecs ipam.API, k8s Kubernetes, allocatedResources map[string]resourceManagerInitItem, ipFamily *types.IPFamily) (ResourceManager, error) {
	eniFactory, err := newENIFactory(poolConfig, ecs)
	if err != nil {
		return nil, fmt.Errorf("error get ENI factory for eniip factory, %w", err)
	}

	factory := &eniIPFactory{
		name:         factoryNameENIIP,
		eniFactory:   eniFactory,
		trunkOnEni:   poolConfig.TrunkENIID,
		enableTrunk:  poolConfig.TrunkENIID != "",
		enis:         []*ENI{},
		eniOperChan:  make(chan struct{}, maxEniOperating),
		ipResultChan: make(chan *ENIIP, maxIPBacklog),
		ipFamily:     ipFamily,
		maxENI:       make(chan struct{}, poolConfig.MaxENI),
		eniMaxIP:     poolConfig.MaxIPPerENI,
		nodeMaxENI:   poolConfig.MaxENI,
	}

	// eniip factory metrics
	factory.metricENICount = metric.ENIIPFactoryENICount.WithLabelValues(factory.name, fmt.Sprint(poolConfig.MaxENI))
	var trunkENI *types.ENI
	poolCfg := pool.Config{
		Name:               poolNameENIIP,
		Type:               typeNameENIIP,
		MaxIdle:            poolConfig.MaxPoolSize,
		MinIdle:            poolConfig.MinPoolSize,
		Factory:            factory,
		Capacity:           poolConfig.Capacity,
		IPConditionHandler: k8s.PatchNodeIPResCondition,
		Initializer: func(holder pool.ResourceHolder) error {
			ctx := context.Background()
			// not use main ENI for ENI multiple ip allocate
			enis, err := ecs.GetAttachedENIs(ctx, false, factory.trunkOnEni)
			if err != nil {
				return fmt.Errorf("error get attach ENI on pool init, %w", err)
			}

			if factory.trunkOnEni != "" {
				found := false
				for _, eni := range enis {
					if eni.Trunk && factory.trunkOnEni == eni.ID {
						found = true
						trunkENI = eni
						break
					}
				}
				if !found {
					return fmt.Errorf("trunk eni %s not found", factory.trunkOnEni)
				}
			}

			for _, eni := range enis {
				ipv4s, ipv6s, err := ecs.GetENIIPs(ctx, eni.MAC)
				if err != nil {
					return fmt.Errorf("error get ENI's ip on pool init, %w", err)
				}
				err = factory.setupENICompartment(eni)
				if err != nil {
					// NB(thxCode): an unbinding eni stuck and then block starting,
					// we just ignore this kind of error.
					if strings.Contains(err.Error(), "no interface with given MAC") {
						continue
					}
					return errors.Wrap(err, "error setup eni compartment")
				}
				if utils.IsWindowsOS() {
					// NB(thxCode): don't assign the primary IP of one assistant eni.
					ipv4s, ipv6s = dropPrimaryIP(eni, ipv4s, ipv6s)
				}
				poolENI := &ENI{
					ENI:       eni,
					ips:       []*ENIIP{},
					ecs:       ecs,
					ipBacklog: make(chan struct{}, maxIPBacklog),
					done:      make(chan struct{}, 1),
				}
				factory.enis = append(factory.enis, poolENI)
				factory.metricENICount.Inc()
				if ipFamily.IPv4 && !ipFamily.IPv6 {
					for _, ip := range ipv4s {
						eniIP := &types.ENIIP{
							ENI:   eni,
							IPSet: types.IPSet{IPv4: ip},
						}
						res, ok := allocatedResources[eniIP.GetResourceID()]

						poolENI.ips = append(poolENI.ips, &ENIIP{
							ENIIP: eniIP,
						})
						metric.ENIIPFactoryIPCount.WithLabelValues(factory.name, poolENI.MAC, fmt.Sprint(poolConfig.MaxENI)).Inc()

						if !ok {
							holder.AddIdle(eniIP)
						} else {
							holder.AddInuse(eniIP, podInfoKey(res.podInfo.Namespace, res.podInfo.Name))
						}
					}
				} else {
					v4Map := terwayIP.ToIPMap(ipv4s)
					v6Map := terwayIP.ToIPMap(ipv6s)

					// put all local res in
					for id, res := range allocatedResources {
						if res.item.ENIMAC != eni.MAC {
							continue
						}
						ipSet := types.IPSet{}
						eniIP := &types.ENIIP{
							ENI:   eni,
							IPSet: *ipSet.SetIP(res.item.IPv4).SetIP(res.item.IPv6),
						}

						poolENI.ips = append(poolENI.ips, &ENIIP{
							ENIIP: eniIP,
						})
						metric.ENIIPFactoryIPCount.WithLabelValues(factory.name, poolENI.MAC, fmt.Sprint(poolConfig.MaxENI)).Inc()

						holder.AddInuse(eniIP, podInfoKey(res.podInfo.Namespace, res.podInfo.Name))

						if ipSet.IPv4 != nil {
							delete(v4Map, ipSet.IPv4.String())
						}
						if ipSet.IPv6 != nil {
							delete(v6Map, ipSet.IPv6.String())
						}
						delete(allocatedResources, id)
					}
					var v4List, v6List []net.IP
					for _, v4 := range v4Map {
						v4List = append(v4List, v4)
					}
					for _, v6 := range v6Map {
						v6List = append(v6List, v6)
					}
					for _, unUsed := range types.MergeIPs(v4List, v6List) {
						eniIP := &types.ENIIP{
							ENI:   eni,
							IPSet: unUsed,
						}
						poolENI.ips = append(poolENI.ips, &ENIIP{
							ENIIP: eniIP,
						})
						metric.ENIIPFactoryIPCount.WithLabelValues(factory.name, poolENI.MAC, fmt.Sprint(poolConfig.MaxENI)).Inc()

						if ipFamily.IPv4 && ipFamily.IPv6 && (unUsed.IPv6 == nil || unUsed.IPv4 == nil) {
							holder.AddInvalid(eniIP)
						} else {
							holder.AddIdle(eniIP)
						}
					}
				}

				eniIPLog.Debugf("init factory's exist ENI: %+v", poolENI)
				select {
				case factory.maxENI <- struct{}{}:
				default:
					eniIPLog.Warnf("exist enis already over eni limits, maxENI config will not be available")
				}
				go poolENI.allocateWorker(factory.ipResultChan)
			}
			return nil
		},
	}
	p, err := pool.NewSimpleObjectPool(poolCfg)
	if err != nil {
		return nil, err
	}
	mgr := &eniIPResourceManager{
		trunkENI: trunkENI,
		pool:     p,
	}

	_ = tracing.Register(tracing.ResourceTypeFactory, factory.name, factory)
	return mgr, nil
}

func (m *eniIPResourceManager) Allocate(ctx *networkContext, prefer string) (types.NetworkResource, error) {
	return m.pool.Acquire(ctx, prefer, podInfoKey(ctx.pod.Namespace, ctx.pod.Name))
}

func (m *eniIPResourceManager) Release(context *networkContext, resItem types.ResourceItem) error {
	if context != nil && context.pod != nil {
		return m.pool.ReleaseWithReservation(resItem.ID, context.pod.IPStickTime)
	}
	return m.pool.Release(resItem.ID)
}

func (m *eniIPResourceManager) GarbageCollection(inUseResSet map[string]types.ResourceItem, expireResSet map[string]types.ResourceItem) error {
	for expireRes, expireItem := range expireResSet {
		if _, err := m.pool.Stat(expireRes); err == nil {
			err = m.Release(nil, expireItem)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *eniIPResourceManager) Stat(context *networkContext, resID string) (types.NetworkResource, error) {
	return m.pool.Stat(resID)
}

func (m *eniIPResourceManager) GetResourceMapping() (tracing.ResourcePoolStats, error) {
	return m.pool.GetResourceMapping()
}

func dropPrimaryIP(eni *types.ENI, ipv4s, ipv6s []net.IP) ([]net.IP, []net.IP) {
	if eni == nil {
		return ipv4s, ipv6s
	}

	var ipv4sLen = len(ipv4s)
	if ipv4Primary := eni.PrimaryIP.IPv4; len(ipv4Primary) != 0 && ipv4sLen != 0 {
		var ipv4sNew []net.IP
		for idx, ipv4 := range ipv4s {
			if ipv4.Equal(ipv4Primary) {
				switch idx {
				case 0:
					ipv4sNew = ipv4s[1:]
				case ipv4sLen - 1:
					ipv4sNew = ipv4s[:ipv4sLen-1]
				default:
					ipv4sNew = append(ipv4s[0:idx], ipv4s[idx+1:]...)
				}
				break
			}
		}
		ipv4s = ipv4sNew
	}

	var ipv6sLen = len(ipv6s)
	if ipv6Primary := eni.PrimaryIP.IPv6; len(ipv6Primary) != 0 && ipv6sLen != 0 {
		var ipv6sNew []net.IP
		for idx, ipv6 := range ipv6s {
			if ipv6.Equal(ipv6Primary) {
				switch idx {
				case 0:
					ipv6sNew = ipv6s[1:]
				case ipv6sLen - 1:
					ipv6sNew = ipv6s[:ipv6sLen-1]
				default:
					ipv6sNew = append(ipv6s[0:idx], ipv6s[idx+1:]...)
				}
				break
			}
		}
		ipv6s = ipv6sNew
	}

	return ipv4s, ipv6s
}
