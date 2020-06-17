package pool

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/pkg/tracing"

	"github.com/sirupsen/logrus"

	"github.com/AliyunContainerService/terway/types"
	"github.com/stretchr/testify/assert"
)

type mockObjectFactory struct {
	createDelay   time.Duration
	disposeDeplay time.Duration
	err           error
	totalCreated  int
	totalDisposed int
	idGenerator   int
	lock          sync.Mutex
}

func (f *mockObjectFactory) GetResourceMapping() ([]tracing.FactoryResourceMapping, error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	var mapping []tracing.FactoryResourceMapping

	for i := 1001; i <= f.idGenerator; i++ {
		mapping = append(mapping, tracing.FactoryResourceMapping{
			ResID: fmt.Sprint(i),
			ENI:   nil,
			ENIIP: nil,
		})
	}

	return mapping, nil
}

type mockNetworkResource struct {
	id string
}

func (n mockNetworkResource) GetResourceID() string {
	return n.id
}

func (n mockNetworkResource) GetType() string {
	return "mock"
}

func (f *mockObjectFactory) Create(int) ([]types.NetworkResource, error) {
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
	return []types.NetworkResource{&mockNetworkResource{
		id: fmt.Sprintf("%d", f.idGenerator),
	}}, nil
}

func (f *mockObjectFactory) Dispose(types.NetworkResource) error {
	time.Sleep(f.disposeDeplay)
	f.lock.Lock()
	defer f.lock.Unlock()
	f.totalDisposed++
	return f.err
}

func (f *mockObjectFactory) getTotalDisposed() int {
	f.lock.Lock()
	defer f.lock.Unlock()
	return f.totalDisposed
}

func (f *mockObjectFactory) getTotalCreated() int {
	f.lock.Lock()
	defer f.lock.Unlock()
	return f.totalCreated
}

func TestInitializerWithoutAutoCreate(t *testing.T) {
	factory := &mockObjectFactory{}
	createPool(factory, 3, 5, 3, 0)
	time.Sleep(time.Second)
	assert.Equal(t, 0, factory.getTotalCreated())
	assert.Equal(t, 0, factory.getTotalDisposed())
}

func TestInitializerWithAutoCreate(t *testing.T) {
	factory := &mockObjectFactory{}
	createPool(factory, 3, 5, 0, 0)
	time.Sleep(time.Second)
	assert.Equal(t, 3, factory.getTotalCreated())
	assert.Equal(t, 0, factory.getTotalDisposed())
}

func createPool(factory ObjectFactory, minIdle, maxIdle, initIdle, initInuse int) ObjectPool {
	id := 0
	cfg := Config{
		Factory: factory,
		Initializer: func(holder ResourceHolder) error {
			for i := 0; i < initIdle; i++ {
				id++
				holder.AddIdle(mockNetworkResource{fmt.Sprintf("%d", id)})
			}
			for i := 0; i < initInuse; i++ {
				id++
				holder.AddInuse(mockNetworkResource{fmt.Sprintf("%d", id)}, "")
			}
			return nil
		},
		MinIdle:  minIdle,
		MaxIdle:  maxIdle,
		Capacity: 10,
	}
	pool, err := NewSimpleObjectPool(cfg)
	if err != nil {
		panic(err)
	}
	return pool
}

func TestMain(m *testing.M) {
	logrus.SetLevel(logrus.DebugLevel)
	os.Exit(m.Run())
}

func TestInitializerExceedMaxIdle(t *testing.T) {
	factory := &mockObjectFactory{}
	createPool(factory, 3, 5, 6, 0)
	time.Sleep(1 * time.Second)
	assert.Equal(t, 0, factory.getTotalCreated())
	assert.Equal(t, 1, factory.getTotalDisposed())
}

func TestInitializerExceedCapacity(t *testing.T) {
	factory := &mockObjectFactory{}
	createPool(factory, 3, 5, 1, 10)
	time.Sleep(time.Second)
	assert.Equal(t, 0, factory.getTotalCreated())
	assert.Equal(t, 1, factory.getTotalDisposed())
}

func TestAcquireIdle(t *testing.T) {
	factory := &mockObjectFactory{}
	pool := createPool(factory, 0, 5, 3, 0)
	_, err := pool.Acquire(context.Background(), "", "")
	assert.Nil(t, err)
	assert.Equal(t, 0, factory.getTotalCreated())
}

func TestAutoAddition(t *testing.T) {
	factory := &mockObjectFactory{}
	pool := createPool(factory, 3, 5, 0, 0)
	time.Sleep(1 * time.Second)
	_, err := pool.Acquire(context.Background(), "", "")
	assert.Nil(t, err)
	_, err = pool.Acquire(context.Background(), "", "")
	assert.Nil(t, err)
	_, err = pool.Acquire(context.Background(), "", "")
	assert.Nil(t, err)
	time.Sleep(1 * time.Second)
	assert.Equal(t, 6, factory.getTotalCreated())
}
func TestAcquireNonExists(t *testing.T) {
	factory := &mockObjectFactory{}
	pool := createPool(factory, 0, 5, 3, 0)
	_, err := pool.Acquire(context.Background(), "1000", "")
	assert.Nil(t, err)
	assert.Equal(t, 0, factory.getTotalCreated())
}

func TestAcquireExists(t *testing.T) {
	factory := &mockObjectFactory{}
	pool := createPool(factory, 0, 5, 3, 0)
	res, err := pool.Acquire(context.Background(), "2", "")
	assert.Nil(t, err)
	assert.Equal(t, 0, factory.getTotalCreated())
	assert.Equal(t, "2", res.GetResourceID())
}

func TestConcurrencyAcquireNoMoreThanCapacity(t *testing.T) {
	factory := &mockObjectFactory{
		createDelay: 2 * time.Millisecond,
	}
	pool := createPool(factory, 0, 5, 1, 0)
	wg := sync.WaitGroup{}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		go func() {
			_, err := pool.Acquire(ctx, "", "")
			cancel()
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
	pool := createPool(factory, 3, 5, 3, 0)
	wg := sync.WaitGroup{}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		go func() {
			res, _ := pool.Acquire(ctx, "", "")
			t.Logf("concurrency acquire resource: %+v", res)
			cancel()
			wg.Done()
		}()
	}
	wg.Wait()
	assert.Equal(t, 7, factory.getTotalCreated())
}

func TestRelease(t *testing.T) {
	factory := &mockObjectFactory{
		createDelay: 1 * time.Millisecond,
	}
	pool := createPool(factory, 0, 5, 3, 0)
	n1, _ := pool.Acquire(context.Background(), "", "")
	n2, _ := pool.Acquire(context.Background(), "", "")
	n3, _ := pool.Acquire(context.Background(), "", "")
	n4, _ := pool.Acquire(context.Background(), "", "")
	n5, _ := pool.Acquire(context.Background(), "", "")
	n6, _ := pool.Acquire(context.Background(), "", "")
	assert.Equal(t, 3, factory.getTotalCreated())
	err := pool.Release(n1.GetResourceID())
	assert.Equal(t, err, nil)
	err = pool.Release(n2.GetResourceID())
	assert.Equal(t, err, nil)
	err = pool.Release(n3.GetResourceID())
	assert.Equal(t, err, nil)
	time.Sleep(1 * time.Second)
	assert.Equal(t, 0, factory.getTotalDisposed())
	err = pool.Release(n4.GetResourceID())
	assert.Equal(t, err, nil)
	err = pool.Release(n5.GetResourceID())
	assert.Equal(t, err, nil)
	time.Sleep(1 * time.Second)
	assert.Equal(t, 0, factory.getTotalDisposed())
	err = pool.Release(n6.GetResourceID())
	assert.Equal(t, err, nil)
	time.Sleep(1 * time.Second)
	assert.Equal(t, 1, factory.getTotalDisposed())
}

func TestReleaseInvalid(t *testing.T) {
	factory := &mockObjectFactory{}
	pool := createPool(factory, 3, 5, 3, 0)
	err := pool.Release("not-exists")
	assert.Equal(t, err, ErrInvalidState)
}

func TestGetResourceMapping(t *testing.T) {
	factory := &mockObjectFactory{
		createDelay: 1 * time.Millisecond,
	}
	pool := createPool(factory, 3, 5, 3, 2)

	for i := 0; i < 5; i++ {
		_, err := pool.Acquire(context.Background(), "", "")
		assert.Equal(t, nil, err)
	}

	mapping, err := pool.GetResourceMapping()
	assert.Equal(t, nil, err)

	for _, v := range mapping {
		//t.Log(v.ResID)
		resID, err := strconv.Atoi(v.ResID)
		assert.Equal(t, nil, err)

		if resID >= 1000 { // generated from factory
			assert.Equal(t, v.Valid, true)
		} else {
			assert.Equal(t, v.Valid, false)
		}
	}

}
