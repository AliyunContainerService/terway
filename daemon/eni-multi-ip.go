package daemon

import (
	"fmt"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/prometheus/client_golang/prometheus"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/AliyunContainerService/terway/pkg/pool"
	"github.com/AliyunContainerService/terway/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	maxEniOperating = 3
	maxIPBacklog    = 10
)

const (
	eniIPAllocInhibitTimeout = 10 * time.Minute

	poolNameENIIP    = "eniip-%s"
	factoryNameENIIP = "eniip-%s"
)

const timeFormat = "2006-01-02 15:04:05"

type eniIPFactory struct {
	name         string
	eniFactory   *eniFactory
	enis         []*ENI
	maxENI       chan struct{}
	eniMaxIP     int
	primaryIP    net.IP
	eniOperChan  chan struct{}
	ipResultChan chan *ENIIP
	sync.RWMutex
	// metrics
	metricENICount prometheus.Gauge
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
	primaryIP net.IP
	pending   int
	ipBacklog chan struct{}
	ecs       aliyun.ECS
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

	popAll:
		for {
			select {
			case <-e.ipBacklog:
				toAllocate++
			default:
				break popAll
			}
		}
		logrus.Debugf("allocate %v ips for eni", toAllocate)
		ips, err := e.ecs.AssignNIPsForENI(e.ENI.ID, toAllocate)
		logrus.Debugf("allocated ips for eni: eni = %+v, ips = %+v, err = %v", e.ENI, ips, err)
		if err != nil {
			logrus.Errorf("error allocate ips for eni: %v", err)
			metric.ENIIPFactoryIPAllocCount.WithLabelValues(e.MAC, metric.ENIIPAllocActionFail).Add(float64(toAllocate))
			for i := 0; i < toAllocate; i++ {
				resultChan <- &ENIIP{
					ENIIP: &types.ENIIP{
						Eni: e.ENI,
					},
					err: errors.Errorf("error assign ip for ENI: %v", err),
				}
			}
		} else {
			metric.ENIIPFactoryIPAllocCount.WithLabelValues(e.MAC, metric.ENIIPAllocActionSucceed).Add(float64(toAllocate))
			for _, ip := range ips {
				resultChan <- &ENIIP{
					ENIIP: &types.ENIIP{
						Eni:        e.ENI,
						SecAddress: ip,
						PrimaryIP:  e.primaryIP,
					},
					err: nil,
				}
			}
		}
	}
}

func (f *eniIPFactory) getEnis() ([]*ENI, error) {
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
	// in descending order per each switch's availabel IP count.
	vSwitches, err := f.eniFactory.GetVSwitches()
	logrus.Infof("adjusted vswitch slice: %+v, original eni slice: %+v", vSwitches, f.enis)
	if err != nil {
		logrus.Errorf("error to get vswitch slice: %v, instead use original eni slice in eniIPFactory: %v", err, f.enis)
		return f.enis, err
	}
	for _, vswitch := range vSwitches {
		for _, eni := range f.enis {
			if eni.ENI == nil {
				pendingEnis = append(pendingEnis, eni)
				continue
			}
			if vswitch == eni.VSwitch {
				enis = append(enis, eni)
			}
		}
	}
	enis = append(enis, pendingEnis...)
	return enis, nil

}

func (f *eniIPFactory) submit() error {
	f.Lock()
	defer f.Unlock()
	var enis []*ENI
	enis, _ = f.getEnis()
	for _, eni := range enis {
		logrus.Infof("check existing eni: %+v", eni)
		eni.lock.Lock()
		now := time.Now()
		if eni.ENI != nil {
			logrus.Infof("check if the current eni is in the time window for IP allocation inhibition: "+
				"eni = %+v, vsw= %s, now = %s, expireAt = %s", eni, eni.VSwitch, now.Format(timeFormat), eni.ipAllocInhibitExpireAt.Format(timeFormat))
		}
		// if the current eni has been inhibited for Pod IP allocation, then skip current eni.
		if now.Before(eni.ipAllocInhibitExpireAt) && eni.ENI != nil {
			eni.lock.Unlock()
			logrus.Debugf("skip IP allocation: eni = %+v, vsw = %s", eni, eni.VSwitch)
			continue
		}

		logrus.Debugf("check if the current eni will reach eni IP quota with new pending IP added: "+
			"eni = %+v, eni.pending = %d, len(eni.ips) = %d, eni.MaxIPs = %d", eni, eni.pending, len(eni.ips), f.eniMaxIP)
		if eni.getIPCountLocked() < f.eniMaxIP {
			select {
			case eni.ipBacklog <- struct{}{}:
			default:
				eni.lock.Unlock()
				continue
			}
			eni.pending++
			eni.lock.Unlock()
			return nil
		}
		eni.lock.Unlock()
	}
	return errors.Errorf("trigger ENIIP throttle, max operating concurrent: %v", maxIPBacklog)
}

func (f *eniIPFactory) popResult() (ip *types.ENIIP, err error) {
	result := <-f.ipResultChan
	if result.ENIIP == nil || result.err != nil {
		// There are two error cases:
		// Error Case 1. The ENI-associated VSwitch has no available IP for Pod IP allocation.
		// Error Case 2. The IP number allocated has reached ENI quota.
		f.Lock()
		defer f.Unlock()
		if result.ENIIP != nil && result.Eni != nil {
			for _, eni := range f.enis {
				if eni.MAC == result.Eni.MAC {
					eni.pending--
					// if an error message with InvalidVSwitchIDIPNotEnough returned, then mark the ENI as IP allocation inhibited.
					if strings.Contains(result.err.Error(), aliyun.InvalidVSwitchIDIPNotEnough) {
						eni.ipAllocInhibitExpireAt = time.Now().Add(eniIPAllocInhibitTimeout)
						logrus.Infof("eni's associated vswitch %s has no available IP, set eni ipAllocInhibitExpireAt = %s",
							eni.VSwitch, eni.ipAllocInhibitExpireAt.Format(timeFormat))
					}
				}
			}
		}
		return nil, errors.Errorf("error allocate ip from eni: %v", result.err)
	}
	f.Lock()
	defer f.Unlock()
	for _, eni := range f.enis {
		if eni.ENI != nil && eni.MAC == result.Eni.MAC {
			eni.pending--
			eni.lock.Lock()
			eni.ips = append(eni.ips, result)
			eni.lock.Unlock()
			metric.ENIIPFactoryIPCount.WithLabelValues(f.name, eni.MAC, fmt.Sprint(eni.MaxIPs)).Inc()
			return result.ENIIP, nil
		}
	}
	return nil, errors.Errorf("unexpected eni ip allocated: %v", result)
}

func (f *eniIPFactory) Create(count int) ([]types.NetworkResource, error) {
	var (
		ipResult []types.NetworkResource
		err      error
		waiting  int
	)
	defer func() {
		if len(ipResult) == 0 {
			logrus.Debugf("create result: %v, error: %v", ipResult, err)
		} else {
			for _, ip := range ipResult {
				logrus.Debugf("create result nil: %+v, error: %v", ip, err)
			}
		}
	}()

	// find for available ENIs and submit for ip allocation
	for ; waiting < count; waiting++ {
		err = f.submit()
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
		logrus.Debugf("create eni async, ip count: %+v", initENIIPCount)
		_, err = f.createENIAsync(initENIIPCount)
		if err == nil {
			waiting += initENIIPCount
		} else {
			logrus.Errorf("error create eni async: %+v", err)
		}
	}

	// no ip has been created
	if waiting == 0 {
		return ipResult, errors.Errorf("error submit ip create request: %+v", err)
	}

	var ip *types.ENIIP
	for ; waiting > 0; waiting-- { // receive allocate result
		ip, err = f.popResult()
		if err != nil {
			logrus.Errorf("error allocate ip address: %+v", err)
		} else {
			ipResult = append(ipResult, ip)
		}
	}
	if len(ipResult) == 0 {
		return ipResult, errors.Errorf("error allocate ip address: %+v", err)
	}

	return ipResult, nil
}

func (f *eniIPFactory) Dispose(res types.NetworkResource) (err error) {
	defer func() {
		logrus.Debugf("dispose result: %v, error: %v", res.GetResourceID(), err != nil)
	}()
	ip := res.(*types.ENIIP)
	var (
		eni   *ENI
		eniip *ENIIP
	)
	f.RLock()
	for _, e := range f.enis {
		if ip.Eni.ID == e.ID {
			eni = e
			e.lock.Lock()
			for _, eip := range e.ips {
				if eip.SecAddress.String() == ip.SecAddress.String() {
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
		if eni.pending > 0 {
			eni.lock.Unlock()
			return fmt.Errorf("ENI have pending ips to be allocate")
		}
		// block ip allocate
		eni.pending = eni.MaxIPs
		eni.lock.Unlock()

		f.Lock()
		for i, e := range f.enis {
			if ip.Eni.ID == e.ID {
				close(eni.done)
				f.enis[len(f.enis)-1], f.enis[i] = f.enis[i], f.enis[len(f.enis)-1]
				f.enis = f.enis[:len(f.enis)-1]
				break
			}
		}
		f.metricENICount.Dec()
		f.Unlock()

		f.eniOperChan <- struct{}{}
		// only remain ENI main ip address, release the ENI interface
		err = f.eniFactory.Dispose(ip.Eni)
		<-f.eniOperChan
		if err != nil {
			return fmt.Errorf("error dispose ENI for eniip, %v", err)
		}
		<-f.maxENI
		return nil
	}
	eni.lock.Unlock()

	// main ip of ENI, raise put_it_back error
	if ip.Eni.Address.IP.Equal(ip.SecAddress) {
		return fmt.Errorf("ip to be release is primary ip of ENI")
	}

	err = f.eniFactory.ecs.UnAssignIPForENI(ip.Eni.ID, ip.SecAddress)
	if err != nil {
		return fmt.Errorf("error unassign eniip, %v", err)
	}
	eni.lock.Lock()
	for i, e := range eni.ips {
		if e.SecAddress.Equal(eniip.SecAddress) {
			eni.ips[len(eni.ips)-1], eni.ips[i] = eni.ips[i], eni.ips[len(eni.ips)-1]
			eni.ips = eni.ips[:len(eni.ips)-1]
			break
		}
	}
	eni.lock.Unlock()
	return nil
}

func (f *eniIPFactory) initialENI(eni *ENI) {
	rawEni, err := f.eniFactory.CreateWithIPCount(eni.pending)
	var ips []net.IP
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
			logrus.Errorf("error get type ENI from factory, got: %+v, rollback it", rawEni)
			errDispose := f.eniFactory.Dispose(rawEni[0])
			if errDispose != nil {
				logrus.Errorf("rollback %+v failed", rawEni)
			}
		} else {
			ips, err = f.eniFactory.ecs.GetENIIPs(eni.ID)
			if err != nil {
				logrus.Errorf("error get eni secondary address: %+v, rollback it", err)
				errDispose := f.eniFactory.Dispose(rawEni[0])
				if errDispose != nil {
					logrus.Errorf("rollback %+v failed", rawEni)
				}
			}
		}
	}

	logrus.Debugf("eni initial finished: %+v, err: %+v", eni, err)

	if err != nil {
		eni.lock.Lock()
		//failed all pending on this initial eni
		for i := 0; i < eni.pending; i++ {
			f.ipResultChan <- &ENIIP{
				ENIIP: &types.ENIIP{
					Eni: nil,
				},
				err: errors.Errorf("error initial ENI: %v", err),
			}
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
	extraAlloc := eni.pending - len(ips)
	for _, ip := range ips {
		eniip := &types.ENIIP{
			Eni:        eni.ENI,
			SecAddress: ip,
			PrimaryIP:  eni.primaryIP,
		}
		f.ipResultChan <- &ENIIP{
			ENIIP: eniip,
			err:   nil,
		}
	}
	for i := 0; i < extraAlloc; i++ {
		select {
		case eni.ipBacklog <- struct{}{}:
		default:
			f.ipResultChan <- &ENIIP{
				ENIIP: &types.ENIIP{
					Eni: nil,
				},
				err: errors.Errorf("need retry to allocate eni ip: %v", err),
			}
		}

	}

	eni.lock.Unlock()
	go eni.allocateWorker(f.ipResultChan)
}

func (f *eniIPFactory) createENIAsync(initIPs int) (*ENI, error) {
	eni := &ENI{
		lock:      sync.Mutex{},
		ENI:       nil,
		ips:       make([]*ENIIP, 0),
		primaryIP: f.primaryIP,
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
		go f.initialENI(eni)
	default:
		return nil, fmt.Errorf("max ENI exceeded")
	}
	f.Lock()
	f.enis = append(f.enis, eni)
	f.Unlock()
	// metric
	f.metricENICount.Inc()
	return eni, nil
}

type eniIPResourceManager struct {
	pool pool.ObjectPool
}

func newENIIPResourceManager(poolConfig *types.PoolConfig, ecs aliyun.ECS, allocatedResources []resourceManagerInitItem) (ResourceManager, error) {
	primaryIP, err := aliyun.GetPrivateIPV4()
	if err != nil {
		return nil, errors.Wrapf(err, "get primary ip error")
	}
	logrus.Infof("node's primary ip is %v", primaryIP)

	eniFactory, err := newENIFactory(poolConfig, ecs)
	if err != nil {
		return nil, errors.Wrapf(err, "error get ENI factory for eniip factory")
	}

	factory := &eniIPFactory{
		name:         fmt.Sprintf(factoryNameENIIP, randomString()),
		eniFactory:   eniFactory,
		enis:         []*ENI{},
		primaryIP:    primaryIP,
		eniOperChan:  make(chan struct{}, maxEniOperating),
		ipResultChan: make(chan *ENIIP, maxIPBacklog),
	}

	maxEni, err := ecs.GetInstanceMaxENI(poolConfig.InstanceID)
	if err != nil {
		return nil, errors.Wrapf(err, "error get max eni for eniip factory")
	}
	maxEni = maxEni - 1

	ipPerENI, err := ecs.GetENIMaxIP(poolConfig.InstanceID, "")
	if err != nil {
		return nil, errors.Wrapf(err, "error get max ip per eni for eniip factory")
	}

	factory.eniMaxIP = ipPerENI

	if poolConfig.MaxENI != 0 && poolConfig.MaxENI < maxEni {
		maxEni = poolConfig.MaxENI
	}
	capacity := maxEni * ipPerENI
	if capacity < 0 {
		capacity = 0
	}
	factory.maxENI = make(chan struct{}, maxEni)

	if poolConfig.MinENI != 0 {
		poolConfig.MinPoolSize = poolConfig.MinENI * ipPerENI
	}

	if poolConfig.MinPoolSize > capacity {
		logrus.Infof("min pool size bigger than node capacity, set min pool size to capacity")
		poolConfig.MinPoolSize = capacity
	}

	if poolConfig.MaxPoolSize > capacity {
		logrus.Infof("max pool size bigger than node capacity, set max pool size to capacity")
		poolConfig.MaxPoolSize = capacity
	}

	if poolConfig.MinPoolSize > poolConfig.MaxPoolSize {
		logrus.Warnf("min_pool_size bigger: %v than max_pool_size: %v, set max_pool_size to the min_pool_size",
			poolConfig.MinPoolSize, poolConfig.MaxPoolSize)
		poolConfig.MaxPoolSize = poolConfig.MinPoolSize
	}

	// eniip factory metrics
	factory.metricENICount = metric.ENIIPFactoryENICount.WithLabelValues(factory.name, fmt.Sprint(maxEni))

	poolCfg := pool.Config{
		Name:     fmt.Sprintf(poolNameENIIP, randomString()),
		MaxIdle:  poolConfig.MaxPoolSize,
		MinIdle:  poolConfig.MinPoolSize,
		Factory:  factory,
		Capacity: capacity,
		Initializer: func(holder pool.ResourceHolder) error {
			// not use main ENI for ENI multiple ip allocate
			enis, err := ecs.GetAttachedENIs(poolConfig.InstanceID, false)
			if err != nil {
				return errors.Wrapf(err, "error get attach ENI on pool init")
			}
			stubMap := make(map[string]*podInfo)
			for _, allocated := range allocatedResources {
				stubMap[allocated.resourceID] = allocated.podInfo
			}

			for _, eni := range enis {
				ips, err := ecs.GetENIIPs(eni.ID)
				if err != nil {
					return errors.Wrapf(err, "error get ENI's ip on pool init")
				}
				poolENI := &ENI{
					lock:      sync.Mutex{},
					ENI:       eni,
					ips:       []*ENIIP{},
					ecs:       ecs,
					primaryIP: primaryIP,
					ipBacklog: make(chan struct{}, maxIPBacklog),
					done:      make(chan struct{}, 1),
				}
				factory.enis = append(factory.enis, poolENI)
				factory.metricENICount.Inc()
				for _, ip := range ips {
					eniIP := &types.ENIIP{
						Eni:        eni,
						SecAddress: ip,
						PrimaryIP:  primaryIP,
					}
					podInfo, ok := stubMap[eniIP.GetResourceID()]

					poolENI.ips = append(poolENI.ips, &ENIIP{
						ENIIP: eniIP,
					})
					metric.ENIIPFactoryIPCount.WithLabelValues(factory.name, poolENI.MAC, fmt.Sprint(poolENI.MaxIPs)).Inc()

					if !ok {
						holder.AddIdle(eniIP)
					} else {
						holder.AddInuse(eniIP, podInfoKey(podInfo.Namespace, podInfo.Name))
					}
				}
				logrus.Debugf("init factory's exist ENI: %+v", poolENI)
				select {
				case factory.maxENI <- struct{}{}:
				default:
					logrus.Warnf("exist enis already over eni limits, maxENI config will not be available")
				}
				go poolENI.allocateWorker(factory.ipResultChan)
			}
			return nil
		},
	}
	pool, err := pool.NewSimpleObjectPool(poolCfg)
	if err != nil {
		return nil, err
	}
	return &eniIPResourceManager{
		pool: pool,
	}, nil
}

func (m *eniIPResourceManager) Allocate(ctx *networkContext, prefer string) (types.NetworkResource, error) {
	return m.pool.Acquire(ctx, prefer, podInfoKey(ctx.pod.Namespace, ctx.pod.Name))
}

func (m *eniIPResourceManager) Release(context *networkContext, resID string) error {
	if context != nil && context.pod != nil {
		return m.pool.ReleaseWithReservation(resID, context.pod.IPStickTime)
	}
	return m.pool.Release(resID)
}

func (m *eniIPResourceManager) GarbageCollection(inUseSet map[string]interface{}, expireResSet map[string]interface{}) error {
	for expireRes := range expireResSet {
		if err := m.pool.Stat(expireRes); err == nil {
			err = m.Release(nil, expireRes)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
