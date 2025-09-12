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
