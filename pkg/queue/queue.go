package queue

import (
	"time"
)

// Interface the interface to operate queue
type Interface interface {
	Push(x interface{}) error
	PushWithTimeStamp(obj interface{}, timeStamp time.Time) error
	Pop(fcs ...FilterOutFunc) (interface{}, bool)
	PopN(num int, numToKeep int, fcs ...FilterOutFunc) ([]interface{}, bool)
	PopByID(id string) (interface{}, bool)
	Len() int
	List() []interface{}
	ListKeys() []string
	StatByID(id string) bool
}

// KeyFunc get UID from obj
type KeyFunc func(obj interface{}) (string, error)

// FilterOutFunc returns true if should pop out
type FilterOutFunc func(item Item) bool

// Item hold obj in queue
type Item struct {
	Val       interface{}
	TimeStamp time.Time
}

var _ Interface = (*Queue)(nil)

// Queue a thread NOT safe queue
type Queue struct {
	items map[string]Item
	queue []string

	keyFunc KeyFunc
}

func (q *Queue) Push(obj interface{}) error {
	return q.PushWithTimeStamp(obj, time.Time{})
}

func (q *Queue) PushWithTimeStamp(obj interface{}, timeStamp time.Time) error {
	id, err := q.keyFunc(obj)
	if err != nil {
		return err
	}
	_, ok := q.items[id]
	if !ok {
		q.queue = append(q.queue, id)
	}
	if timeStamp.After(time.Time{}) {
		timeStamp = time.Now()
	}
	q.items[id] = Item{
		Val:       obj,
		TimeStamp: timeStamp,
	}
	return nil
}

// Pop return the oldest
func (q *Queue) Pop(fcs ...FilterOutFunc) (interface{}, bool) {
	return q.pop(fcs...)
}

func (q *Queue) pop(fcs ...FilterOutFunc) (interface{}, bool) {
	length := len(q.queue)
	if length == 0 {
		return nil, false
	}

OuterLoop:
	for i := 0; i < len(q.queue); {
		id := q.queue[i]
		v, ok := q.items[id]
		if !ok {
			q.queue = append(q.queue[:i], q.queue[i+1:]...)
			continue
		}

		for _, filter := range fcs {
			if !filter(v) {
				i++
				continue OuterLoop
			}
		}
		q.queue = append(q.queue[:i], q.queue[i+1:]...)
		delete(q.items, id)
		return v.Val, true
	}
	return nil, false
}

// PopN like Pop and NOT guaranteed
func (q *Queue) PopN(num int, numToKeep int, fcs ...FilterOutFunc) ([]interface{}, bool) {
	if len(q.queue) == 0 {
		return nil, false
	}

	want := min(max(len(q.queue)-numToKeep, 0), num)

	var result []interface{}
	for i := 0; i < want; i++ {
		v, ok := q.pop(fcs...)
		if !ok {
			continue
		}
		result = append(result, v)
	}
	return result, true
}

// PopByID PopByID
func (q *Queue) PopByID(id string) (interface{}, bool) {
	if len(q.queue) == 0 {
		return nil, false
	}
	v, ok := q.items[id]
	if !ok {
		return nil, false
	}
	delete(q.items, id)
	for i := 0; i < len(q.queue); i++ {
		if id == q.queue[i] {
			q.queue = append(q.queue[:i], q.queue[i+1:]...)
			return v.Val, true
		}
	}
	return nil, false
}

// Len len
func (q *Queue) Len() int {
	return len(q.queue)
}

// ListKeys
func (q *Queue) List() []interface{} {
	list := make([]interface{}, 0, len(q.queue))
	for _, item := range q.queue {
		list = append(list, q.items[item].Val)
	}
	return list
}

// ListKeys
func (q *Queue) ListKeys() []string {
	keys := make([]string, len(q.queue))
	copy(keys, q.queue)
	return keys
}

// StatByID
func (q *Queue) StatByID(id string) bool {
	_, ok := q.items[id]
	return ok
}

// NewQueue get a queue
func NewQueue(keyFunc KeyFunc) *Queue {
	return &Queue{
		items:   make(map[string]Item),
		keyFunc: keyFunc,
	}
}

func max(a, b int) int {
	if a < b {
		return b
	}
	return a
}

func min(a, b int) int {
	if a > b {
		return b
	}
	return a
}
