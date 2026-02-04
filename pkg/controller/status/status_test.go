package status

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
)

func TestNewCache(t *testing.T) {
	cache := NewCache[string]()
	assert.NotNil(t, cache)

	cache.LoadOrStore("test", ptr.To("test"))
	v, ok := cache.Get("test")
	assert.True(t, ok)
	assert.Equal(t, "test", *v)
}

func TestRequestNetworkIndex(t *testing.T) {
	nodeStatus := NewNodeStatus(2)
	wg := wait.Group{}
	for i := 0; i < 100; i++ {
		wg.Start(func() {
			nodeStatus.RequestNetworkIndex(fmt.Sprintf("%d", i), nil, nil)
		})
	}
	wg.Wait()

	assert.Equal(t, 2, len(nodeStatus.NetworkCards))
	assert.Equal(t, 50, len(nodeStatus.NetworkCards[0].NetworkInterfaces))
	assert.Equal(t, 50, len(nodeStatus.NetworkCards[1].NetworkInterfaces))

	for i := 0; i < 100; i++ {
		if i%2 == 0 {
			continue
		}
		wg.Start(func() {
			nodeStatus.DetachNetworkIndex(fmt.Sprintf("%d", i))
		})
	}

	wg.Wait()
	assert.Equal(t, 50, nodeStatus.NetworkCards[1].NetworkInterfaces.Len()+nodeStatus.NetworkCards[0].NetworkInterfaces.Len())
}

func TestRequestNetworkIndex_Numa(t *testing.T) {
	nodeStatus := NewNodeStatus(2)
	numa := 1
	wg := wait.Group{}
	for i := 0; i < 100; i++ {
		wg.Start(func() {
			nodeStatus.RequestNetworkIndex(fmt.Sprintf("%d", i), nil, &numa)
		})
	}
	wg.Wait()

	assert.Equal(t, 2, len(nodeStatus.NetworkCards))
	assert.Equal(t, 0, len(nodeStatus.NetworkCards[0].NetworkInterfaces))
	assert.Equal(t, 100, len(nodeStatus.NetworkCards[1].NetworkInterfaces))

	index := 0
	for i := 0; i < 100; i++ {
		wg.Start(func() {
			nodeStatus.RequestNetworkIndex(fmt.Sprintf("%d", i), &index, &numa)
		})
	}

	wg.Wait()
	assert.Equal(t, 100, len(nodeStatus.NetworkCards[0].NetworkInterfaces))
	assert.Equal(t, 0, len(nodeStatus.NetworkCards[1].NetworkInterfaces))
}

func TestMetaCtx(t *testing.T) {
	ctx := context.Background()
	nodeStatus := NewNodeStatus(1)
	ctx = WithMeta(ctx, nodeStatus)

	v, ok := MetaCtx[NodeStatus](ctx)
	assert.True(t, ok)
	assert.Equal(t, nodeStatus, v)
}

func TestMetaCtx_NoValue(t *testing.T) {
	ctx := context.Background()
	v, ok := MetaCtx[NodeStatus](ctx)
	assert.False(t, ok)
	assert.Nil(t, v)
}

func TestMetaCtx_WrongType(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxMetaKey{}, "not NodeStatus")
	v, ok := MetaCtx[NodeStatus](ctx)
	assert.False(t, ok)
	assert.Nil(t, v)
}

func TestCache_DeleteAndGet(t *testing.T) {
	cache := NewCache[string]()
	cache.LoadOrStore("k1", ptr.To("v1"))
	v, ok := cache.Get("k1")
	assert.True(t, ok)
	assert.Equal(t, "v1", *v)

	cache.Delete("k1")
	v, ok = cache.Get("k1")
	assert.False(t, ok)
	assert.Nil(t, v)
}

func TestRequestNetworkIndex_PreferIndexOutOfRange(t *testing.T) {
	nodeStatus := NewNodeStatus(2)
	idx := 10
	result := nodeStatus.RequestNetworkIndex("eni-1", &idx, nil)
	assert.Nil(t, result)
}

func TestRequestNetworkIndex_WithPreferIndex(t *testing.T) {
	nodeStatus := NewNodeStatus(4)
	idx := 2
	result := nodeStatus.RequestNetworkIndex("eni-1", &idx, nil)
	assert.NotNil(t, result)
	assert.Equal(t, 2, *result)
	assert.True(t, nodeStatus.NetworkCards[2].NetworkInterfaces.Has(NetworkInterfaceID("eni-1")))
}

func TestRequestNetworkIndex_SelectedEmptyReturnsNil(t *testing.T) {
	nodeStatus := NewNodeStatus(2)
	numa := 2
	result := nodeStatus.RequestNetworkIndex("eni-1", nil, &numa)
	assert.Nil(t, result)
}
