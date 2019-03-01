package pool

import (
	"fmt"
	"github.com/AliyunContainerService/terway/types"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestInit(t *testing.T) {
	queue := NewPriorityQueue()
	assert.Zero(t, queue.Size())
}

func createNetworkResource(id string) types.NetworkResource {
	return mockNetworkResource{id: id}
}

func createPoolItem(id int) *poolItem {
	return &poolItem{res: createNetworkResource(fmt.Sprintf("%d", id)), reverse: time.Now().Add(time.Hour * time.Duration(id))}
}

func TestPop(t *testing.T) {
	queue := NewPriorityQueue()
	for i := 0; i < 100; i++ {
		item := createPoolItem(i)
		queue.Push(item)
	}
	i := 0
	for {
		item := queue.Pop()
		if item == nil {
			break
		}
		assert.Equal(t, fmt.Sprintf("%d", i), item.res.GetResourceId())
		i++
	}
	assert.Equal(t, 100, i)
}

func TestPush(t *testing.T) {
	queue := NewPriorityQueue()
	for i := 0; i < 10; i += 2 {
		item := createPoolItem(i)
		queue.Push(item)
	}
	for i := 1; i < 10; i += 2 {
		item := createPoolItem(i)
		queue.Push(item)
	}

	i := 0
	for {
		item := queue.Pop()
		if item == nil {
			break
		}
		assert.Equal(t, fmt.Sprintf("%d", i), item.res.GetResourceId())
		i++
	}
	assert.Equal(t, 10, i)
}

func TestRob(t *testing.T) {
	queue := NewPriorityQueue()
	for i := 0; i < 100; i += 2 {
		item := createPoolItem(i)
		queue.Push(item)
	}
	assert.Nil(t, queue.Rob("5"))
	item := queue.Rob("6")

	assert.Equal(t, "6", item.res.GetResourceId())
	assert.Equal(t, 49, queue.Size())
}

func TestFind(t *testing.T) {
	queue := NewPriorityQueue()
	for i := 0; i < 100; i += 2 {
		item := createPoolItem(i)
		queue.Push(item)
	}
	assert.Nil(t, queue.Find("5"))
	item := queue.Find("6")

	assert.Equal(t, "6", item.res.GetResourceId())
	assert.Equal(t, 50, queue.Size())
}
