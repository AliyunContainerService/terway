package queue

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQueue(t *testing.T) {
	q := NewQueue(func(obj interface{}) (string, error) {
		return obj.(string), nil
	})

	q.Push("1")
	q.Push("2")
	q.Push("3")
	v, _ := q.Pop()
	assert.Equal(t, "1", v.(string))
	v, _ = q.Pop()
	assert.Equal(t, "2", v.(string))
	v, _ = q.Pop()
	assert.Equal(t, "3", v.(string))
	v, _ = q.Pop()
	assert.Nil(t, v)
	q.Push("1")
	q.Push("2")
	q.Push("3")
	v, _ = q.PopByID("2")
	assert.Equal(t, "2", v.(string))
	v, _ = q.Pop()
	assert.Equal(t, "1", v.(string))
	v, _ = q.Pop()
	assert.Equal(t, "3", v.(string))
}

func TestQueue_PopN(t *testing.T) {
	q := NewQueue(func(obj interface{}) (string, error) {
		return obj.(string), nil
	})
	q.Push("1")
	q.Push("2")
	q.Push("3")
	v, _ := q.PopN(2, 0)
	assert.ElementsMatch(t, []string{"1", "2"}, v)

	v, _ = q.PopN(2, 10)
	assert.Nil(t, v)
	v, _ = q.PopN(2, 1)
	assert.Nil(t, v)

	v, _ = q.PopN(2, 0)
	assert.ElementsMatch(t, []string{"3"}, v)
}

func TestQueue_PopWithFilter(t *testing.T) {
	q := NewQueue(func(obj interface{}) (string, error) {
		return obj.(string), nil
	})
	q.Push("1")
	q.Push("2")
	q.Push("3")

	v, _ := q.pop(func(item Item) bool {
		if item.Val.(string) == "1" {
			return false
		}
		return true
	})
	assert.Equal(t, "2", v)
	v, _ = q.pop(func(item Item) bool {
		if item.Val.(string) == "1" {
			return false
		}
		return true
	})
	assert.Equal(t, "3", v)
	v, _ = q.pop(func(item Item) bool {
		if item.Val.(string) == "1" {
			return false
		}
		return true
	})
	assert.Nil(t, v)
}

func TestQueue_List(t *testing.T) {
	q := NewQueue(func(obj interface{}) (string, error) {
		return obj.(string), nil
	})
	q.Push("0")
	q.Push("1")
	q.Push("2")

	for i, v := range q.List() {
		assert.Equal(t, strconv.Itoa(i), v.(string))
	}
}
