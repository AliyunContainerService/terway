package pool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/types"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/util/wait"
)

var log = logger.DefaultLogger.WithField("subSys", "pool")

// Errors of pool
var (
	ErrNoAvailableResource = errors.New("no available resource")
	ErrInvalidState        = errors.New("invalid state")
	ErrNotFound            = errors.New("not found")
	ErrContextDone         = errors.New("context done")
	ErrInvalidArguments    = errors.New("invalid arguments")
)

const (
	// CheckIdleInterval the interval of check and process idle eni
	CheckIdleInterval  = 2 * time.Minute
	defaultPoolBackoff = 1 * time.Minute

	tracingKeyName     = "name"
	tracingKeyMaxIdle  = "max_idle"
	tracingKeyMinIdle  = "min_idle"
	tracingKeyCapacity = "capacity"
	tracingKeyIdle     = "idle"
	tracingKeyInuse    = "inuse"

	commandMapping = "mapping"

	ipExhaustiveConditionPeriod = 10 * time.Minute
)

// ResUsage ResUsage
type ResUsage struct {
	ID     string
	Type   string
	Status types.ResStatus
}

func (r *ResUsage) GetID() string {
	return r.ID
}

func (r *ResUsage) GetType() string {
	return r.Type
}

func (r *ResUsage) GetStatus() types.ResStatus {
	return r.Status
}

// Usage store res booth local and remote
type Usage struct {
	Local  map[string]types.Res
	Remote map[string]types.Res
}

func (u *Usage) GetLocal() map[string]types.Res {
	return u.Local
}

func (u *Usage) GetRemote() map[string]types.Res {
	return u.Remote
}

// ObjectPool object pool interface
type ObjectPool interface {
	Acquire(ctx context.Context, resID, idempotentKey string) (types.NetworkResource, error)
	ReleaseWithReservation(resID string, reservation time.Duration) error
	Release(resID string) error
	AcquireAny(ctx context.Context, idempotentKey string) (types.NetworkResource, error)
	Stat(resID string) (types.NetworkResource, error)
	GetName() string
	tracing.ResourceMappingHandler
}

// ResourceHolder interface to initialize pool
type ResourceHolder interface {
	AddIdle(resource types.NetworkResource)
	AddInvalid(resource types.NetworkResource)
	AddInuse(resource types.NetworkResource, idempotentKey string)
}

// ObjectFactory interface of network resource object factory
type ObjectFactory interface {
	// Create res with count
	Create(count int) ([]types.NetworkResource, error)
	Dispose(types.NetworkResource) error
	ListResource() (map[string]types.NetworkResource, error)
	Check(types.NetworkResource) error
	// Reconcile run periodicity
	Reconcile()
}

type simpleObjectPool struct {
	name     string
	inuse    map[string]poolItem
	idle     *priorityQueue
	invalid  map[string]poolItem // hole invalid also idle resource
	lock     sync.Mutex
	factory  ObjectFactory
	maxIdle  int
	minIdle  int
	capacity int
	notifyCh chan interface{}
	// concurrency to create resource. tokenCh = capacity - (idle + inuse + dispose)
	tokenCh     chan struct{}
	backoffTime time.Duration
	// metrics
	metricIdle     prometheus.Gauge
	metricTotal    prometheus.Gauge
	metricDisposed prometheus.Counter
	// node conditions
	factoryIPExhaustive      *atomic.Bool
	factoryIPExhaustiveTimer *time.Timer
	IPConditionHandler       NodeConditionHandler
}

// Config configuration of pool
type Config struct {
	Name               string
	Type               string
	Factory            ObjectFactory
	Initializer        Initializer
	MinIdle            int
	MaxIdle            int
	Capacity           int
	IPConditionHandler NodeConditionHandler
}

type poolItem struct {
	res           types.NetworkResource
	reservation   time.Time
	idempotentKey string
}

func (i *poolItem) lessThan(other *poolItem) bool {
	return i.reservation.Before(other.reservation)
}

// Initializer of pool
type Initializer func(holder ResourceHolder) error

type NodeConditionHandler func(status corev1.ConditionStatus, reason, message string) error

// NewSimpleObjectPool return an object pool implement
func NewSimpleObjectPool(cfg Config) (ObjectPool, error) {
	if cfg.MinIdle > cfg.MaxIdle {
		return nil, ErrInvalidArguments
	}

	if cfg.MaxIdle > cfg.Capacity {
		return nil, ErrInvalidArguments
	}

	pool := &simpleObjectPool{
		name:                     cfg.Name,
		factory:                  cfg.Factory,
		inuse:                    make(map[string]poolItem),
		idle:                     newPriorityQueue(),
		invalid:                  make(map[string]poolItem),
		maxIdle:                  cfg.MaxIdle,
		minIdle:                  cfg.MinIdle,
		capacity:                 cfg.Capacity,
		notifyCh:                 make(chan interface{}, 1),
		tokenCh:                  make(chan struct{}, cfg.Capacity),
		backoffTime:              defaultPoolBackoff,
		factoryIPExhaustiveTimer: time.NewTimer(0),
		factoryIPExhaustive:      atomic.NewBool(true),
		IPConditionHandler:       cfg.IPConditionHandler,
		// create metrics with labels in the pool struct
		// and it will show in metrics even if it has not been triggered yet
		metricIdle: metric.ResourcePoolIdle.WithLabelValues(cfg.Name, cfg.Type, fmt.Sprint(cfg.Capacity),
			fmt.Sprint(cfg.MaxIdle), fmt.Sprint(cfg.MinIdle)),
		metricTotal: metric.ResourcePoolTotal.WithLabelValues(cfg.Name, cfg.Type, fmt.Sprint(cfg.Capacity),
			fmt.Sprint(cfg.MaxIdle), fmt.Sprint(cfg.MinIdle)),
		metricDisposed: metric.ResourcePoolDisposed.WithLabelValues(cfg.Name, cfg.Type, fmt.Sprint(cfg.Capacity),
			fmt.Sprint(cfg.MaxIdle), fmt.Sprint(cfg.MinIdle)),
	}

	if cfg.Initializer != nil {
		if err := cfg.Initializer(pool); err != nil {
			return nil, err
		}
	}

	if err := pool.preload(); err != nil {
		return nil, err
	}

	log.WithFields(map[string]interface{}{
		"capacity": pool.capacity,
		"maxIdle":  pool.maxIdle,
		"minIdle":  pool.minIdle,
		"idle":     pool.idle.Size(),
		"inUse":    len(pool.inuse),
		"invalid":  len(pool.invalid),
	}).Infof("pool initial state ,idle: %s, inuse: %s, invalid: %s", queueKeys(pool.idle), mapKeys(pool.inuse), mapKeys(pool.invalid))

	go pool.startCheckIdleTicker()
	go pool.checkIPExhaustive()

	_ = tracing.Register(tracing.ResourceTypeResourcePool, pool.name, pool)
	return pool, nil
}

func (p *simpleObjectPool) startCheckIdleTicker() {
	tick := make(chan struct{})
	go wait.JitterUntil(func() {
		tick <- struct{}{}
	}, CheckIdleInterval, 0.2, true, wait.NeverStop)
	reconcileTick := make(chan struct{})
	go wait.JitterUntil(func() {
		reconcileTick <- struct{}{}
	}, time.Hour, 0.2, true, wait.NeverStop)
	for {
		select {
		case <-tick:
			p.checkResSync() // make sure pool is synced
			p.checkIdle()
			p.checkInsufficient()
		case <-p.notifyCh:
			p.checkIdle()
			p.checkInsufficient()
		case <-reconcileTick:
			p.factory.Reconcile()
		}
	}
}

func (p *simpleObjectPool) setIPExhaustive(err *types.IPInsufficientError) {
	if p.IPConditionHandler == nil {
		return
	}
	if err := p.IPConditionHandler(corev1.ConditionFalse, types.IPResInsufficientReason,
		fmt.Sprintf("node has insufficient IP, error: %v", err.Reason)); err != nil {
		log.Errorf("set IPExhaustive condition failed: %v", err)
	}
	if !p.factoryIPExhaustive.Load() {
		p.factoryIPExhaustive.Store(true)
	}
	p.factoryIPExhaustiveTimer.Reset(ipExhaustiveConditionPeriod)
}

func (p *simpleObjectPool) unsetIPExhaustive() {
	if p.factoryIPExhaustive.Load() {
		p.factoryIPExhaustiveTimer.Reset(0)
	}
}

func (p *simpleObjectPool) checkIPExhaustive() {
	for range p.factoryIPExhaustiveTimer.C {
		if p.factoryIPExhaustive.Load() {
			if p.IPConditionHandler != nil {
				if err := p.IPConditionHandler(corev1.ConditionTrue, types.IPResSufficientReason,
					fmt.Sprintf("node has sufficient IP or pass the exhaustive period: %v", ipExhaustiveConditionPeriod)); err != nil {
					log.Errorf("set IPExhaustive condition failed: %v", err)
				}
			}
			p.factoryIPExhaustive.Store(false)
		}
	}
}

func mapKeys(m map[string]poolItem) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	return strings.Join(keys, ", ")
}

func queueKeys(q *priorityQueue) string {
	var keys []string
	for i := 0; i < q.size; i++ {
		keys = append(keys, q.slots[i].res.GetResourceID())
	}
	return strings.Join(keys, ", ")
}

func (p *simpleObjectPool) tooManyIdleLocked() bool {
	return p.idle.Size() > p.maxIdle || (p.idle.Size() > 0 && p.sizeLocked() > p.capacity)
}

func (p *simpleObjectPool) needAddition() int {
	p.lock.Lock()
	defer p.lock.Unlock()
	addition := p.minIdle - p.idle.Size()
	if addition > (p.capacity - p.sizeLocked()) {
		return p.capacity - p.sizeLocked()
	}
	return addition
}

func (p *simpleObjectPool) peekOverfullIdle() *poolItem {
	p.lock.Lock()
	defer p.lock.Unlock()

	if !p.tooManyIdleLocked() {
		return nil
	}

	item := p.idle.Peek()
	if item == nil {
		return nil
	}

	if item.reservation.After(time.Now()) {
		return nil
	}
	return p.idle.Pop()
}

// found resources that can be disposed, put them into dispose channel
func (p *simpleObjectPool) checkIdle() {
	for {
		item := p.peekOverfullIdle()
		if item == nil {
			break
		}

		p.metricIdle.Dec()
		p.metricTotal.Dec()

		res := item.res
		log.Infof("try dispose res %+v", res)
		err := p.factory.Dispose(res)
		if err == nil {
			p.tokenCh <- struct{}{}
			p.backoffTime = defaultPoolBackoff
			// one item popped from idle and total
			p.metricDisposed.Inc()
		} else {
			log.Warnf("error dispose res: %+v", err)
			p.backoffTime = p.backoffTime * 2
			p.AddIdle(res)
			time.Sleep(p.backoffTime)
		}
	}
}

func (p *simpleObjectPool) checkInsufficient() {
	addition := p.needAddition()
	if addition <= 0 {
		return
	}
	var tokenAcquired int
	for i := 0; i < addition; i++ {
		// pending resources
		select {
		case <-p.tokenCh:
			tokenAcquired++
		default:
			continue
		}
	}
	log.Debugf("token acquired count: %v", tokenAcquired)
	if tokenAcquired <= 0 {
		return
	}
	resList, err := p.factory.Create(tokenAcquired)
	if err != nil {
		inSufErr, ok := err.(*types.IPInsufficientError)
		if ok {
			p.setIPExhaustive(inSufErr)
		}
		log.Errorf("error add idle network resources: %v", err)
	}
	if tokenAcquired == len(resList) {
		p.backoffTime = defaultPoolBackoff
	}
	for _, res := range resList {
		log.Infof("add resource %s to pool idle", res.GetResourceID())
		p.AddIdle(res)
		tokenAcquired--
	}
	for i := 0; i < tokenAcquired; i++ {
		// release token
		p.tokenCh <- struct{}{}
	}
	if tokenAcquired != 0 {
		log.Debugf("token acquired left: %d, err: %v", tokenAcquired, err)
		p.notify()
	}

	if err != nil {
		p.backoffTime = p.backoffTime * 2
		time.Sleep(p.backoffTime)
	}
}

func (p *simpleObjectPool) preload() error {
	p.lock.Lock()
	defer p.lock.Unlock()

	tokenCount := p.capacity - p.sizeLocked()
	for i := 0; i < tokenCount; i++ {
		p.tokenCh <- struct{}{}
	}

	return nil
}

func (p *simpleObjectPool) sizeLocked() int {
	return p.idle.Size() + len(p.inuse) + len(p.invalid)
}

func (p *simpleObjectPool) getOneLocked(resID string) *poolItem {
	if len(resID) > 0 {
		item := p.idle.Rob(resID)
		if item != nil {
			return item
		}
	}
	return p.idle.Pop()
}

func (p *simpleObjectPool) Acquire(ctx context.Context, resID, idempotentKey string) (types.NetworkResource, error) {
	if p.factoryIPExhaustive.Load() {
		// slowdown ip allocation on ip exhaustive
		time.Sleep(10 * time.Second)
	}
	p.lock.Lock()
	if resItem, ok := p.inuse[resID]; ok && resItem.idempotentKey == idempotentKey {
		p.lock.Unlock()
		return resItem.res, nil
	}

	if p.idle.Size() > 0 {
		res := p.getOneLocked(resID).res
		p.inuse[res.GetResourceID()] = poolItem{res: res, idempotentKey: idempotentKey}
		p.lock.Unlock()
		log.Infof("acquire (expect %s): return idle %s", resID, res.GetResourceID())
		p.metricIdle.Dec()
		p.notify()
		return res, nil
	}
	size := p.sizeLocked()
	if size >= p.capacity {
		p.lock.Unlock()
		p.setIPExhaustive(&types.IPInsufficientError{
			Err:    ErrNoAvailableResource,
			Reason: fmt.Sprintf("exceed node ip resource capacity: %v", p.capacity),
		})
		log.Infof("acquire (expect %s), size %d, capacity %d: return err %v", resID, size, p.capacity, ErrNoAvailableResource)
		return nil, ErrNoAvailableResource
	}

	p.lock.Unlock()

	select {
	case <-p.tokenCh:
		// should we pass ctx into factory.Create?
		res, err := p.factory.Create(1)
		if err != nil || len(res) == 0 {
			p.tokenCh <- struct{}{}
			inSufErr, ok := err.(*types.IPInsufficientError)
			if ok {
				p.setIPExhaustive(inSufErr)
			}
			return nil, fmt.Errorf("error create from factory: %v", err)
		}
		log.Infof("acquire (expect %s): return newly %s", resID, res[0].GetResourceID())
		p.AddInuse(res[0], idempotentKey)
		return res[0], nil
	case <-ctx.Done():
		log.Infof("acquire (expect %s): return err %v", resID, ErrContextDone)
		return nil, ErrContextDone
	}
}

func (p *simpleObjectPool) AcquireAny(ctx context.Context, idempotentKey string) (types.NetworkResource, error) {
	return p.Acquire(ctx, "", idempotentKey)
}

func (p *simpleObjectPool) Stat(resID string) (types.NetworkResource, error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	v, ok := p.inuse[resID]
	if ok {
		return v.res, nil
	}
	vv := p.idle.Find(resID)
	if vv != nil {
		return vv.res, nil
	}

	return nil, ErrNotFound
}

func (p *simpleObjectPool) GetName() string {
	return p.name
}

func (p *simpleObjectPool) Config() []tracing.MapKeyValueEntry {
	config := []tracing.MapKeyValueEntry{
		{Key: tracingKeyName, Value: p.name},
		{Key: tracingKeyMaxIdle, Value: fmt.Sprint(p.maxIdle)},
		{Key: tracingKeyMinIdle, Value: fmt.Sprint(p.minIdle)},
		{Key: tracingKeyCapacity, Value: fmt.Sprint(p.capacity)},
	}

	return config
}

func (p *simpleObjectPool) Trace() []tracing.MapKeyValueEntry {
	trace := []tracing.MapKeyValueEntry{
		{Key: tracingKeyIdle, Value: queueKeys(p.idle)},
		{Key: tracingKeyInuse, Value: mapKeys(p.inuse)},
	}

	return trace
}

func (p *simpleObjectPool) Execute(cmd string, _ []string, message chan<- string) {
	switch cmd {
	case commandMapping:
		mapping, err := p.GetResourceMapping()
		message <- fmt.Sprintf("mapping: %v, err: %s\n", mapping, err)
	default:
		message <- "can't recognize command\n"
	}

	close(message)
}

func (p *simpleObjectPool) notify() {
	select {
	case p.notifyCh <- true:
	default:
	}
}

func (p *simpleObjectPool) ReleaseWithReservation(resID string, reservation time.Duration) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	res, ok := p.inuse[resID]
	if !ok {
		log.Infof("release %s: return err %v", resID, ErrInvalidState)
		return ErrInvalidState
	}
	log.Infof("release %s, reservation %v: return success", resID, reservation)
	delete(p.inuse, resID)

	// check metadata
	err := p.factory.Check(res.res)
	if errors.Is(err, apiErr.ErrNotFound) {
		log.Warnf("release %s, resource not exist in metadata, ignored", resID)
		if err = p.factory.Dispose(res.res); err == nil {
			p.tokenCh <- struct{}{}
			p.metricTotal.Dec()
			p.metricDisposed.Inc()
			return nil
		}
		log.Warnf("release %s, err %v", resID, err)

		// put resource to invalid
		p.invalid[resID] = res
		return nil
	}

	reserveTo := time.Now()
	if reservation > 0 {
		reserveTo = reserveTo.Add(reservation)
	}
	p.idle.Push(&poolItem{res: res.res, reservation: reserveTo})
	p.unsetIPExhaustive()
	p.metricIdle.Inc()
	p.notify()
	return nil
}

func (p *simpleObjectPool) Release(resID string) error {
	return p.ReleaseWithReservation(resID, time.Duration(0))
}

func (p *simpleObjectPool) AddIdle(resource types.NetworkResource) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.idle.Push(&poolItem{res: resource, reservation: time.Now()})
	// assume AddIdle() adds a resource that not exists in the pool before
	// both add total and idle gauge
	p.metricTotal.Inc()
	p.metricIdle.Inc()
}

func (p *simpleObjectPool) AddInvalid(resource types.NetworkResource) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.invalid[resource.GetResourceID()] = poolItem{
		res: resource,
	}
	p.metricTotal.Inc()
	p.metricIdle.Inc() // use idle metric here
}

func (p *simpleObjectPool) AddInuse(res types.NetworkResource, idempotentKey string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.inuse[res.GetResourceID()] = poolItem{
		res:           res,
		idempotentKey: idempotentKey,
	}
	// assume AddInuse() adds a resource that not exists in the pool before
	p.metricTotal.Inc()
}

func (p *simpleObjectPool) GetResourceMapping() (tracing.ResourcePoolStats, error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	usage, err := p.getResUsage()
	if err != nil {
		return nil, err
	}

	return usage, nil
}

// checkResSync will check pool res witch metadata. make sure pool is synced
func (p *simpleObjectPool) checkResSync() {
	p.lock.Lock()
	defer p.lock.Unlock()

	for key, invalid := range p.invalid {
		l := log.WithFields(map[string]interface{}{
			"id":     invalid.res.GetResourceID(),
			"reason": "invalid",
		})
		err := p.factory.Dispose(invalid.res)
		if err != nil {
			l.Warnf("dispose failed %s", err.Error())
			continue
		}
		l.Infof("dispose succeed")
		delete(p.invalid, key)

		p.tokenCh <- struct{}{}
		p.metricTotal.Dec()
		p.metricDisposed.Inc()
	}

	usage, err := p.getResUsage()
	if err != nil {
		log.Error(err)
		return
	}
	for _, r := range usage.Local {
		_, ok := usage.Remote[r.GetID()]
		if ok {
			continue
		}
		// res store in pool but can not find in remote(metadata)
		if r.GetStatus() == types.ResStatusIdle {
			log.Warnf("res %s, type %s is removed from remote,mark as invalid", r.GetID(), r.GetType())
			invalid := p.idle.Rob(r.GetID())
			p.invalid[r.GetID()] = *invalid
		}
		log.Errorf("res %s, type %s is removed from remote,but is in use", r.GetID(), r.GetType())
	}
}

// getResUsage get current usage
func (p *simpleObjectPool) getResUsage() (*Usage, error) {
	localRes := make(map[string]types.Res)
	// idle
	for i := 0; i < p.idle.size; i++ {
		item := p.idle.slots[i]
		localRes[item.res.GetResourceID()] = &ResUsage{
			ID:     item.res.GetResourceID(),
			Type:   item.res.GetType(),
			Status: types.ResStatusIdle,
		}
	}
	// inuse
	for _, v := range p.inuse {
		localRes[v.res.GetResourceID()] = &ResUsage{
			ID:     v.res.GetResourceID(),
			Type:   v.res.GetType(),
			Status: types.ResStatusInUse,
		}
	}
	// invalid
	for _, v := range p.invalid {
		localRes[v.res.GetResourceID()] = &ResUsage{
			ID:     v.res.GetResourceID(),
			Type:   v.res.GetType(),
			Status: types.ResStatusInvalid,
		}
	}

	factoryRes, err := p.factory.ListResource()
	if err != nil {
		return nil, err
	}
	remoteRes := make(map[string]types.Res)

	// map to factory
	for _, v := range factoryRes {
		status := types.ResStatusInvalid
		lo, ok := localRes[v.GetResourceID()]
		if ok {
			status = lo.GetStatus()
		}

		remoteRes[v.GetResourceID()] = &ResUsage{
			ID:     v.GetResourceID(),
			Status: status,
		}
	}

	return &Usage{
		Local:  localRes,
		Remote: remoteRes,
	}, nil
}
