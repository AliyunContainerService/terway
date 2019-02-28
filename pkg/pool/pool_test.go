package pool

import (
	"context"
	"fmt"
	"github.com/AliyunContainerService/terway/types"
	"github.com/stretchr/testify/assert"
	"sync"
	"testing"
	"time"
)

type mockObjectFactory struct {
	createDelay    time.Duration
	disposeDeplay  time.Duration
	err            error
	totalCreated   int
	totalDisplosed int
	idGenerator    int
	lock           sync.Mutex
}

type mockNetworkResource struct {
	id string
}

func (n mockNetworkResource) GetResourceId() string {
	return n.id
}

func (n mockNetworkResource) GetType() string {
	return "mock"
}

func (f *mockObjectFactory) Create() (types.NetworkResource, error) {
	time.Sleep(f.createDelay)
	if f.err != nil {
		return nil, f.err
	}
	f.lock.Lock()
	defer f.lock.Unlock()
	if f.idGenerator == 0 {
		//start from 1000
		f.idGenerator = 1000
	}

	f.idGenerator++
	f.totalCreated++
	return &mockNetworkResource{
		id: fmt.Sprintf("%d", f.idGenerator),
	}, nil
}

func (f *mockObjectFactory) Dispose(types.NetworkResource) error {
	time.Sleep(f.disposeDeplay)
	f.lock.Lock()
	defer f.lock.Unlock()
	f.totalDisplosed++
	return f.err
}

func TestInitializerWithoutAutoCreate(t *testing.T) {
	factory := &mockObjectFactory{}
	createPool(factory, 3, 0)
	time.Sleep(time.Second)
	assert.Equal(t, 0, factory.totalCreated)
	assert.Equal(t, 0, factory.totalDisplosed)
}

func TestInitializerWithAutoCreate(t *testing.T) {
	factory := &mockObjectFactory{}
	createPool(factory, 0, 0)
	time.Sleep(time.Second)
	assert.Equal(t, 3, factory.totalCreated)
	assert.Equal(t, 0, factory.totalDisplosed)
}

func createPool(factory ObjectFactory, initIdle, initInuse int) ObjectPool {
	id := 0
	cfg := PoolConfig{
		Factory: factory,
		Initializer: func(holder ResourceHolder) error {
			for i := 0; i < initIdle; i++ {
				id++
				holder.AddIdle(mockNetworkResource{fmt.Sprintf("%d", id)})
			}
			for i := 0; i < initInuse; i++ {
				id++
				holder.AddInuse(mockNetworkResource{fmt.Sprintf("%d", id)})
			}
			return nil
		},
		MinIdle:  3,
		MaxIdle:  5,
		Capacity: 10,
	}
	pool, err := NewSimpleObjectPool(cfg)
	if err != nil {
		panic(err)
	}
	return pool
}

func TestInitializerExceedMaxIdle(t *testing.T) {
	factory := &mockObjectFactory{}
	createPool(factory, 6, 0)
	time.Sleep(1 * time.Second)
	assert.Equal(t, 0, factory.totalCreated)
	assert.Equal(t, 1, factory.totalDisplosed)
}

func TestInitializerExceedCapacity(t *testing.T) {
	factory := &mockObjectFactory{}
	createPool(factory, 1, 10)
	time.Sleep(time.Second)
	assert.Equal(t, 0, factory.totalCreated)
	assert.Equal(t, 1, factory.totalDisplosed)
}

func TestAcquireIdle(t *testing.T) {
	factory := &mockObjectFactory{}
	pool := createPool(factory, 3, 0)
	_, err := pool.Acquire(context.Background(), "")
	assert.Nil(t, err)
	assert.Equal(t, 0, factory.totalCreated)
}
func TestAcquireNonExists(t *testing.T) {
	factory := &mockObjectFactory{}
	pool := createPool(factory, 3, 0)
	_, err := pool.Acquire(context.Background(), "1000")
	assert.Nil(t, err)
	assert.Equal(t, 0, factory.totalCreated)
}

func TestAcquireExists(t *testing.T) {
	factory := &mockObjectFactory{}
	pool := createPool(factory, 3, 0)
	res, err := pool.Acquire(context.Background(), "2")
	assert.Nil(t, err)
	assert.Equal(t, 0, factory.totalCreated)
	assert.Equal(t, "2", res.GetResourceId())
}

func TestConcurrencyAcquireNoMoreThanCapacity(t *testing.T) {
	factory := &mockObjectFactory{
		createDelay: 2 * time.Millisecond,
	}
	pool := createPool(factory, 1, 0)
	wg := sync.WaitGroup{}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		ctx, _ := context.WithTimeout(context.Background(), 1*time.Second)
		go func() {
			_, err := pool.Acquire(ctx, "")
			assert.Nil(t, err)
			wg.Done()
		}()
	}
	wg.Wait()
}

func TestConcurrencyAcquireMoreThanCapacity(t *testing.T) {
	factory := &mockObjectFactory{
		createDelay: 2 * time.Millisecond,
	}
	pool := createPool(factory, 3, 0)
	wg := sync.WaitGroup{}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		ctx, _ := context.WithTimeout(context.Background(), 1*time.Second)
		go func() {
			pool.Acquire(ctx, "")
			wg.Done()
		}()
	}
	wg.Wait()
	assert.Equal(t, 7, factory.totalCreated)
}

func TestRelease(t *testing.T) {
	factory := &mockObjectFactory{
		createDelay: 1 * time.Millisecond,
	}
	pool := createPool(factory, 3, 0)
	n1, _ := pool.Acquire(context.Background(), "")
	n2, _ := pool.Acquire(context.Background(), "")
	n3, _ := pool.Acquire(context.Background(), "")
	n4, _ := pool.Acquire(context.Background(), "")
	n5, _ := pool.Acquire(context.Background(), "")
	n6, _ := pool.Acquire(context.Background(), "")
	assert.Equal(t, 3, factory.totalCreated)
	pool.Release(n1.GetResourceId())
	pool.Release(n2.GetResourceId())
	pool.Release(n3.GetResourceId())
	time.Sleep(1 * time.Second)
	assert.Equal(t, 0, factory.totalDisplosed)
	pool.Release(n4.GetResourceId())
	pool.Release(n5.GetResourceId())
	time.Sleep(1 * time.Second)
	assert.Equal(t, 0, factory.totalDisplosed)
	pool.Release(n6.GetResourceId())
	time.Sleep(1 * time.Second)
	assert.Equal(t, 1, factory.totalDisplosed)
}

func TestReleaseInvalid(t *testing.T) {
	factory := &mockObjectFactory{}
	pool := createPool(factory, 3, 0)
	err := pool.Release("not-exists")
	assert.Equal(t, err, ErrInvalidState)
}
