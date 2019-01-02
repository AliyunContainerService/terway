package pool

import (
	"fmt"
	"github.com/AliyunContainerService/terway/types"
	"sync"
	"context"
	"errors"
	log "github.com/sirupsen/logrus"
	"strings"
)

var (
	ErrNoAvailableResource = errors.New("no available resource")
	ErrInvalidState        = errors.New("invalid state")
	ErrNotFound            = errors.New("not found")
	ErrContextDone         = errors.New("context done")
	ErrInvalidArguments    = errors.New("invalid arguments")
)

type ObjectPool interface {
	Acquire(ctx context.Context, resid string) (types.NetworkResource, error)
	Release(resId string) error
	AcquireAny(ctx context.Context) (types.NetworkResource, error)
	Stat(resId string) error
}

type ResourceHolder interface {
	AddIdle(resource types.NetworkResource)
	AddInuse(resource types.NetworkResource)
}

//type Selector func([]types.NetworkResource) types.NetworkResource

type ObjectFactory interface {
	Create() (types.NetworkResource, error)
	Dispose(types.NetworkResource) error
}

type SimpleObjectPool struct {
	inuse    map[string]types.NetworkResource
	idle     map[string]types.NetworkResource
	lock     sync.Mutex
	factory  ObjectFactory
	maxIdle  int
	minIdle  int
	capacity int
	// concurrency to create resource. tokenCh = capacity - (idle + inuse + dispose)
	tokenCh   chan struct{}
	disposeCh chan types.NetworkResource
}

type PoolConfig struct {
	Factory     ObjectFactory
	Initializer Initializer
	MinIdle     int
	MaxIdle     int
	Capacity    int
}

const DefaultMaxIdle = 20
const DefaultCapacity = 50

type Initializer func(holder ResourceHolder) (error)

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
		factory:   cfg.Factory,
		inuse:     make(map[string]types.NetworkResource),
		idle:      make(map[string]types.NetworkResource),
		maxIdle:   cfg.MaxIdle,
		minIdle:   cfg.MinIdle,
		capacity:  cfg.Capacity,
		tokenCh:   make(chan struct{}, cfg.Capacity),
		disposeCh: make(chan types.NetworkResource, cfg.Capacity),
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
		keys(pool.idle),
		keys(pool.inuse))

	go pool.dispose()
	pool.tryDispose()

	return pool, nil
}

func keys(m map[string]types.NetworkResource) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	return strings.Join(keys, ", ")
}

func (p *SimpleObjectPool) dispose() {
	log.Infof("begin dispose routine")
	for res := range p.disposeCh {
		log.Infof("try dispose res %+v", res)
		if err := p.factory.Dispose(res); err != nil {
			//put it back on dispose fail
			log.Warnf("failed dispose %s: %v, put it back to idle", res.GetResourceId(), err)
			p.AddIdle(res)
		} else {
			p.tokenCh <- struct{}{}
		}
	}
}

func (p *SimpleObjectPool) tooManyIdle() bool {
	return len(p.idle) > p.maxIdle || len(p.idle) > 0 && p.size() > p.capacity
}

//found resources that can be disposed, put them into dispose channel
//must in lock
func (p *SimpleObjectPool) tryDispose() {
	for ; p.tooManyIdle(); {
		res := p.getOneLocked("")
		delete(p.idle, res.GetResourceId())
		p.disposeCh <- res
	}
}

func (p *SimpleObjectPool) preload() error {
	for {
		// init resource sequential to avoid huge creating request on startup
		if len(p.idle) >= p.minIdle {
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
	return len(p.idle) + len(p.inuse) + len(p.disposeCh)
}

func (p *SimpleObjectPool) getOneLocked(resId string) types.NetworkResource {
	if len(resId) > 0 {
		if v, ok := p.idle[resId]; ok {
			return v
		}
	}
	for _, v := range p.idle {
		return v
	}
	return nil
}

func (p *SimpleObjectPool) Acquire(ctx context.Context, resId string) (types.NetworkResource, error) {
	p.lock.Lock()
	//defer p.lock.Unlock()
	idleCount := len(p.idle)
	if idleCount > 0 {
		res := p.getOneLocked(resId)
		delete(p.idle, res.GetResourceId())
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
	_, ok = p.idle[resId]
	if ok {
		return nil
	}

	return ErrNotFound
}

func (p *SimpleObjectPool) Release(resId string) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	res, ok := p.inuse[resId]
	if !ok {
		log.Infof("release %s: return err %v", resId, ErrInvalidState)
		return ErrInvalidState
	}

	log.Infof("release %s: return success", resId)
	delete(p.inuse, resId)
	p.idle[resId] = res

	p.tryDispose()
	return nil
}

func (p *SimpleObjectPool) AddIdle(resource types.NetworkResource) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.idle[resource.GetResourceId()] = resource

}

func (p *SimpleObjectPool) AddInuse(resource types.NetworkResource) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.inuse[resource.GetResourceId()] = resource
}
