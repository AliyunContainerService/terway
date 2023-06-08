package pool

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/AliyunContainerService/terway/types"
	"github.com/stretchr/testify/assert"
)

type mockNetworkResource struct {
	ID string
}

func (n *mockNetworkResource) ToResItems() []types.ResourceItem {
	return []types.ResourceItem{
		{
			Type: n.GetType(),
			ID:   n.GetResourceID(),
		},
	}
}

func (n mockNetworkResource) GetResourceID() string {
	return n.ID
}

func (n mockNetworkResource) GetType() string {
	return "mock"
}

type mockObjectFactory struct {
	createDelay   time.Duration
	disposeDeplay time.Duration
	err           error
	totalCreated  int
	totalDisposed int
	idBegin       int

	Res map[string]string

	lock sync.Mutex
}

func newMockObjectFactory(id int) *mockObjectFactory {
	return &mockObjectFactory{
		idBegin: id,
		Res:     map[string]string{},
	}
}

func (f *mockObjectFactory) ListResource() (map[string]types.NetworkResource, error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	mapping := make(map[string]types.NetworkResource)
	for _, id := range f.Res {
		mapping[id] = &mockNetworkResource{
			ID: id,
		}
	}
	return mapping, nil
}

func (f *mockObjectFactory) Create(count int) ([]types.NetworkResource, error) {
	time.Sleep(f.createDelay)
	if f.err != nil {
		return nil, f.err
	}
	f.lock.Lock()
	defer f.lock.Unlock()

	var result []types.NetworkResource
	for i := 0; i < count; i++ {
		f.totalCreated++
		f.idBegin++
		res := &mockNetworkResource{ID: fmt.Sprintf("%d", f.idBegin)}
		f.Res[res.GetResourceID()] = res.GetResourceID()
		result = append(result, res)
	}

	return result, nil
}

func (f *mockObjectFactory) Put(count int) ([]types.NetworkResource, error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	var result []types.NetworkResource
	for i := 0; i < count; i++ {
		f.idBegin++
		res := &mockNetworkResource{ID: fmt.Sprintf("%d", f.idBegin)}
		f.Res[res.GetResourceID()] = res.GetResourceID()
		result = append(result, res)
	}

	return result, nil
}

func (f *mockObjectFactory) Dispose(in types.NetworkResource) error {
	time.Sleep(f.disposeDeplay)
	f.lock.Lock()
	defer f.lock.Unlock()
	f.totalDisposed++

	res, ok := in.(*mockNetworkResource)
	if !ok {
		return fmt.Errorf("err type")
	}
	delete(f.Res, res.ID)
	return f.err
}

func (f *mockObjectFactory) Check(in types.NetworkResource) error {
	return nil
}

func (f *mockObjectFactory) Reconcile() {}

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
	factory := newMockObjectFactory(1000)
	createPool(factory, 3, 5, 3, 0)
	time.Sleep(time.Second)
	assert.Equal(t, 0, factory.getTotalCreated())
	assert.Equal(t, 0, factory.getTotalDisposed())
}

func TestInitializerWithAutoCreate(t *testing.T) {
	factory := newMockObjectFactory(1000)
	createPool(factory, 3, 5, 0, 0)
	time.Sleep(time.Second)
	assert.Equal(t, 3, factory.getTotalCreated())
	assert.Equal(t, 0, factory.getTotalDisposed())
}

func createPool(factory *mockObjectFactory, minIdle, maxIdle, initIdle, initInuse int) ObjectPool {
	cfg := Config{
		Factory: factory,
		Initializer: func(holder ResourceHolder) error {
			idleRes, err := factory.Put(initIdle)
			if err != nil {
				panic(err)
			}
			for _, res := range idleRes {
				holder.AddIdle(res)
			}

			inuseRes, err := factory.Put(initInuse)
			if err != nil {
				panic(err)
			}
			for _, res := range inuseRes {
				holder.AddInuse(res, "")
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
	factory := newMockObjectFactory(1000)
	createPool(factory, 3, 5, 6, 0)
	time.Sleep(1 * time.Second)
	assert.Equal(t, 0, factory.getTotalCreated())
	assert.Equal(t, 1, factory.getTotalDisposed())
}

func TestInitializerExceedCapacity(t *testing.T) {
	factory := newMockObjectFactory(1000)
	createPool(factory, 3, 5, 1, 10)
	time.Sleep(time.Second)
	assert.Equal(t, 0, factory.getTotalCreated())
	assert.Equal(t, 1, factory.getTotalDisposed())
}

func TestAcquireIdle(t *testing.T) {
	factory := newMockObjectFactory(1000)
	pool := createPool(factory, 0, 5, 3, 0)
	_, err := pool.Acquire(context.Background(), "", "")
	assert.Nil(t, err)
	assert.Equal(t, 0, factory.getTotalCreated())
}

func TestAutoAddition(t *testing.T) {
	factory := newMockObjectFactory(1000)
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
	factory := newMockObjectFactory(1000)
	pool := createPool(factory, 0, 5, 3, 0)
	_, err := pool.Acquire(context.Background(), "1000", "")
	assert.Nil(t, err)
	assert.Equal(t, 0, factory.getTotalCreated())
}

func TestAcquireExists(t *testing.T) {
	factory := newMockObjectFactory(0)
	pool := createPool(factory, 0, 5, 3, 0)
	res, err := pool.Acquire(context.Background(), "2", "")
	assert.Nil(t, err)
	assert.Equal(t, 0, factory.getTotalCreated())
	assert.Equal(t, "2", res.GetResourceID())
}

func TestConcurrencyAcquireNoMoreThanCapacity(t *testing.T) {
	factory := newMockObjectFactory(0)

	pool := createPool(factory, 0, 5, 1, 0)
	wg := sync.WaitGroup{}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		ctx, cancel := context.WithTimeout(context.Background(), 11*time.Second)
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
	factory := newMockObjectFactory(1000)

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
	factory := newMockObjectFactory(1000)

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
	factory := newMockObjectFactory(1000)
	pool := createPool(factory, 3, 5, 3, 0)
	err := pool.Release("not-exists")
	assert.Equal(t, err, ErrInvalidState)
}

func TestGetResourceMapping(t *testing.T) {
	factory := newMockObjectFactory(1000)
	pool := createPool(factory, 3, 5, 3, 2)

	for i := 0; i < 5; i++ {
		_, err := pool.Acquire(context.Background(), "", "")
		assert.Equal(t, nil, err)
	}

	mapping, err := pool.GetResourceMapping()
	assert.Equal(t, nil, err)
	assert.NotNil(t, mapping.GetLocal())
	assert.NotNil(t, mapping.GetRemote())
}
