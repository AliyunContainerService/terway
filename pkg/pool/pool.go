package pool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/AliyunContainerService/terway/types"
	log "github.com/sirupsen/logrus"
)

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
	CheckIdleInterval = 2 * time.Minute
)

// ObjectPool object pool interface
type ObjectPool interface {
	Acquire(ctx context.Context, resID string) (types.NetworkResource, error)
	ReleaseWithReverse(resID string, reverse time.Duration) error
	Release(resID string) error
	AcquireAny(ctx context.Context) (types.NetworkResource, error)
	Stat(resID string) error
}

// ResourceHolder interface to initialize pool
type ResourceHolder interface {
	AddIdle(resource types.NetworkResource)
	AddInuse(resource types.NetworkResource)
}

// ObjectFactory interface of network resource object factory
type ObjectFactory interface {
	Create() (types.NetworkResource, error)
	Dispose(types.NetworkResource) error
}

type simpleObjectPool struct {
	inuse      map[string]types.NetworkResource
	idle       *priorityQeueu
	lock       sync.Mutex
	factory    ObjectFactory
	maxIdle    int
	minIdle    int
	capacity   int
	maxBackoff time.Duration
	notifyCh   chan interface{}
	// concurrency to create resource. tokenCh = capacity - (idle + inuse + dispose)
	tokenCh chan struct{}
}

// Config configuration of pool
type Config struct {
	Factory     ObjectFactory
	Initializer Initializer
	MinIdle     int
	MaxIdle     int
	Capacity    int
}

type poolItem struct {
	res     types.NetworkResource
	reverse time.Time
}

func (i *poolItem) lessThan(other *poolItem) bool {
	return i.reverse.Before(other.reverse)
}

// Initializer of pool
type Initializer func(holder ResourceHolder) error

// NewSimpleObjectPool return an object pool implement
func NewSimpleObjectPool(cfg Config) (ObjectPool, error) {
	if cfg.MinIdle > cfg.MaxIdle {
		return nil, ErrInvalidArguments
	}

	if cfg.MaxIdle > cfg.Capacity {
		return nil, ErrInvalidArguments
	}

	pool := &simpleObjectPool{
		factory:  cfg.Factory,
		inuse:    make(map[string]types.NetworkResource),
		idle:     newPriorityQueue(),
		maxIdle:  cfg.MaxIdle,
		minIdle:  cfg.MinIdle,
		capacity: cfg.Capacity,
		notifyCh: make(chan interface{}),
		tokenCh:  make(chan struct{}, cfg.Capacity),
	}

	if cfg.Initializer != nil {
		if err := cfg.Initializer(pool); err != nil {
			return nil, err
		}
	}

	if err := pool.preload(); err != nil {
		return nil, err
	}

	log.Infof("pool initial state, capacity %d, maxIdle: %d, minIdle %d, idle: %s, inuse: %s",
		pool.capacity,
		pool.maxIdle,
		pool.minIdle,
		queueKeys(pool.idle),
		mapKeys(pool.inuse))

	go pool.startCheckIdleTicker()

	return pool, nil
}

func (p *simpleObjectPool) startCheckIdleTicker() {
	p.checkIdle()
	ticker := time.NewTicker(CheckIdleInterval)
	for {
		select {
		case <-ticker.C:
			p.checkIdle()
		case <-p.notifyCh:
			p.checkIdle()
		}
	}
}

func mapKeys(m map[string]types.NetworkResource) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	return strings.Join(keys, ", ")
}

func queueKeys(q *priorityQeueu) string {
	var keys []string
	for i := 0; i < q.size; i++ {
		keys = append(keys, q.slots[i].res.GetResourceID())
	}
	return strings.Join(keys, ", ")
}

func (p *simpleObjectPool) dispose(res types.NetworkResource) {
	log.Infof("try dispose res %+v", res)
	if err := p.factory.Dispose(res); err != nil {
		//put it back on dispose fail
		log.Warnf("failed dispose %s: %v, put it back to idle", res.GetResourceID(), err)
	} else {
		p.tokenCh <- struct{}{}
	}
}

func (p *simpleObjectPool) tooManyIdleLocked() bool {
	return p.idle.Size() > p.maxIdle || (p.idle.Size() > 0 && p.sizeLocked() > p.capacity)
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

	if item.reverse.After(time.Now()) {
		return nil
	}
	return p.idle.Pop()
}

//found resources that can be disposed, put them into dispose channel
func (p *simpleObjectPool) checkIdle() {
	for {
		item := p.peekOverfullIdle()
		if item == nil {
			break
		}

		res := item.res
		log.Infof("try dispose res %+v", res)
		err := p.factory.Dispose(res)
		if err == nil {
			p.tokenCh <- struct{}{}
		} else {
			log.Warnf("error dispose res: %+v", err)
			p.AddIdle(res)
		}

	}
}

func (p *simpleObjectPool) preload() error {
	p.lock.Lock()
	defer p.lock.Unlock()
	for {
		// init resource sequential to avoid huge creating request on startup
		if p.idle.Size() >= p.minIdle {
			break
		}

		if p.sizeLocked() >= p.capacity {
			break
		}

		res, err := p.factory.Create()
		if err != nil {
			return err
		}
		p.idle.Push(&poolItem{res: res, reverse: time.Now()})
	}

	tokenCount := p.capacity - p.sizeLocked()
	for i := 0; i < tokenCount; i++ {
		p.tokenCh <- struct{}{}
	}

	return nil
}

func (p *simpleObjectPool) sizeLocked() int {
	return p.idle.Size() + len(p.inuse)
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

func (p *simpleObjectPool) Acquire(ctx context.Context, resID string) (types.NetworkResource, error) {
	p.lock.Lock()
	//defer p.lock.Unlock()
	if p.idle.Size() > 0 {
		res := p.getOneLocked(resID).res
		p.inuse[res.GetResourceID()] = res
		p.lock.Unlock()
		log.Infof("acquire (expect %s): return idle %s", resID, res.GetResourceID())
		return res, nil
	}
	size := p.sizeLocked()
	if size >= p.capacity {
		p.lock.Unlock()
		log.Infof("acquire (expect %s), size %d, capacity %d: return err %v", resID, size, p.capacity, ErrNoAvailableResource)
		return nil, ErrNoAvailableResource
	}

	p.lock.Unlock()

	select {
	case <-p.tokenCh:
		//should we pass ctx into factory.Create?
		res, err := p.factory.Create()
		if err != nil {
			p.tokenCh <- struct{}{}
			return nil, fmt.Errorf("error create from factory: %v", err)
		}
		log.Infof("acquire (expect %s): return newly %s", resID, res.GetResourceID())
		p.AddInuse(res)
		return res, nil
	case <-ctx.Done():
		log.Infof("acquire (expect %s): return err %v", resID, ErrContextDone)
		return nil, ErrContextDone
	}
}

func (p *simpleObjectPool) AcquireAny(ctx context.Context) (types.NetworkResource, error) {
	return p.Acquire(ctx, "")
}

func (p *simpleObjectPool) Stat(resID string) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	_, ok := p.inuse[resID]
	if ok {
		return nil
	}

	if p.idle.Find(resID) != nil {
		return nil
	}

	return ErrNotFound
}

func (p *simpleObjectPool) notify() {
	select {
	case p.notifyCh <- true:
	default:
	}
}

func (p *simpleObjectPool) ReleaseWithReverse(resID string, reverse time.Duration) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	res, ok := p.inuse[resID]
	if !ok {
		log.Infof("release %s: return err %v", resID, ErrInvalidState)
		return ErrInvalidState
	}

	log.Infof("release %s, reverse %v: return success", resID, reverse)
	delete(p.inuse, resID)
	reverseTo := time.Now()
	if reverse > 0 {
		reverseTo = reverseTo.Add(reverse)
	}
	p.idle.Push(&poolItem{res: res, reverse: reverseTo})
	p.notify()
	return nil
}
func (p *simpleObjectPool) Release(resID string) error {
	return p.ReleaseWithReverse(resID, time.Duration(0))
}

func (p *simpleObjectPool) AddIdle(resource types.NetworkResource) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.idle.Push(&poolItem{res: resource, reverse: time.Now()})
}

func (p *simpleObjectPool) AddInuse(res types.NetworkResource) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.inuse[res.GetResourceID()] = res
}
