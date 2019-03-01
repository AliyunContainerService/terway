package pool

import (
	"context"
	"errors"
	"fmt"
	"github.com/AliyunContainerService/terway/types"
	log "github.com/sirupsen/logrus"
	"strings"
	"sync"
	"time"
)

var (
	ErrNoAvailableResource = errors.New("no available resource")
	ErrInvalidState        = errors.New("invalid state")
	ErrNotFound            = errors.New("not found")
	ErrContextDone         = errors.New("context done")
	ErrInvalidArguments    = errors.New("invalid arguments")
)

const (
	CheckIdleInterval = 2 * time.Minute
)

type ObjectPool interface {
	Acquire(ctx context.Context, resid string) (types.NetworkResource, error)
	ReleaseWithReverse(resId string, reverse time.Duration) error
	Release(resId string) error
	AcquireAny(ctx context.Context) (types.NetworkResource, error)
	Stat(resId string) error
}

type ResourceHolder interface {
	AddIdle(resource types.NetworkResource)
	AddInuse(resource types.NetworkResource)
}

type ObjectFactory interface {
	Create() (types.NetworkResource, error)
	Dispose(types.NetworkResource) error
}

type SimpleObjectPool struct {
	inuse      map[string]types.NetworkResource
	idle       *PriorityQeueu
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

type PoolConfig struct {
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

const DefaultMaxIdle = 20
const DefaultCapacity = 50

type Initializer func(holder ResourceHolder) error

func NewSimpleObjectPool(cfg PoolConfig) (ObjectPool, error) {
	if cfg.MinIdle > cfg.MaxIdle {
		return nil, ErrInvalidArguments
	}

	if cfg.MaxIdle > cfg.Capacity {
		return nil, ErrInvalidArguments
	}

	if cfg.MaxIdle == 0 {
		cfg.MaxIdle = DefaultMaxIdle
	}
	if cfg.Capacity == 0 {
		cfg.Capacity = DefaultCapacity
	}

	pool := &SimpleObjectPool{
		factory:  cfg.Factory,
		inuse:    make(map[string]types.NetworkResource),
		idle:     NewPriorityQueue(),
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

func (p *SimpleObjectPool) startCheckIdleTicker() {
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

func queueKeys(q *PriorityQeueu) string {
	var keys []string
	for i := 0; i < q.size; i++ {
		keys = append(keys, q.slots[i].res.GetResourceId())
	}
	return strings.Join(keys, ", ")
}

func (p *SimpleObjectPool) dispose(res types.NetworkResource) {
	log.Infof("try dispose res %+v", res)
	if err := p.factory.Dispose(res); err != nil {
		//put it back on dispose fail
		log.Warnf("failed dispose %s: %v, put it back to idle", res.GetResourceId(), err)
	} else {
		p.tokenCh <- struct{}{}
	}
}

func (p *SimpleObjectPool) tooManyIdle() bool {
	return p.idle.Size() > p.maxIdle || (p.idle.Size() > 0 && p.size() > p.capacity)
}

//found resources that can be disposed, put them into dispose channel
//must in lock
func (p *SimpleObjectPool) checkIdle() {
	for p.tooManyIdle() {
		p.lock.Lock()
		item := p.idle.Peek()
		if item == nil {
			//impossible
			break
		}
		if item.reverse.After(time.Now()) {
			break
		}
		item = p.idle.Pop()
		p.lock.Unlock()
		res := item.res
		log.Infof("try dispose res %+v", res)
		err := p.factory.Dispose(res)
		if err == nil {
			p.tokenCh <- struct{}{}
		} else {
			p.AddIdle(res)
		}
	}
}

func (p *SimpleObjectPool) preload() error {
	for {
		// init resource sequential to avoid huge creating request on startup
		if p.idle.Size() >= p.minIdle {
			break
		}

		if p.size() >= p.capacity {
			break
		}

		res, err := p.factory.Create()
		if err != nil {
			return err
		}
		p.AddIdle(res)
	}

	tokenCount := p.capacity - p.size()
	for i := 0; i < tokenCount; i++ {
		p.tokenCh <- struct{}{}
	}

	return nil
}

func (p *SimpleObjectPool) size() int {
	return p.idle.Size() + len(p.inuse)
}

func (p *SimpleObjectPool) getOneLocked(resId string) *poolItem {
	if len(resId) > 0 {
		item := p.idle.Rob(resId)
		if item != nil {
			return item
		}
	}
	return p.idle.Pop()
}

func (p *SimpleObjectPool) Acquire(ctx context.Context, resId string) (types.NetworkResource, error) {
	p.lock.Lock()
	//defer p.lock.Unlock()
	if p.idle.Size() > 0 {
		res := p.getOneLocked(resId).res
		p.inuse[res.GetResourceId()] = res
		p.lock.Unlock()
		log.Infof("acquire (expect %s): return idle %s", resId, res.GetResourceId())
		return res, nil
	}
	size := p.size()
	if size >= p.capacity {
		p.lock.Unlock()
		log.Infof("acquire (expect %s), size %d, capacity %d: return err %v", resId, size, p.capacity, ErrNoAvailableResource)
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
		log.Infof("acquire (expect %s): return newly %s", resId, res.GetResourceId())
		p.AddInuse(res)
		return res, nil
	case <-ctx.Done():
		log.Infof("acquire (expect %s): return err %v", resId, ErrContextDone)
		return nil, ErrContextDone
	}
}

func (p *SimpleObjectPool) AcquireAny(ctx context.Context) (types.NetworkResource, error) {
	return p.Acquire(ctx, "")
}

func (p *SimpleObjectPool) Stat(resId string) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	_, ok := p.inuse[resId]
	if ok {
		return nil
	}

	if p.idle.Find(resId) != nil {
		return nil
	}

	return ErrNotFound
}

func (p *SimpleObjectPool) notify() {
	select {
	case p.notifyCh <- true:
	default:
	}
}

func (p *SimpleObjectPool) ReleaseWithReverse(resId string, reverse time.Duration) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	res, ok := p.inuse[resId]
	if !ok {
		log.Infof("release %s: return err %v", resId, ErrInvalidState)
		return ErrInvalidState
	}

	log.Infof("release %s: return success", resId)
	delete(p.inuse, resId)
	reverseTo := time.Now()
	if reverse > 0 {
		reverseTo = reverseTo.Add(reverse)
	}
	p.idle.Push(&poolItem{res: res, reverse: reverseTo})
	p.notify()
	return nil
}
func (p *SimpleObjectPool) Release(resId string) error {
	return p.ReleaseWithReverse(resId, time.Duration(0))
}

func (p *SimpleObjectPool) AddIdle(resource types.NetworkResource) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.idle.Push(&poolItem{res: resource, reverse: time.Now()})
}

func (p *SimpleObjectPool) AddInuse(res types.NetworkResource) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.inuse[res.GetResourceId()] = res
}
