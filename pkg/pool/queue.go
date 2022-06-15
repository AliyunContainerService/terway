package pool

type priorityQueue struct {
	slots    []*poolItem
	size     int
	capacity int
}

func newPriorityQueue() *priorityQueue {
	return &priorityQueue{
		capacity: 10,
		size:     0,
		slots:    make([]*poolItem, 10),
	}
}

func (q *priorityQueue) Pop() *poolItem {
	if q.size == 0 {
		return nil
	}
	ret := q.slots[0]
	q.slots[0] = q.slots[q.size-1]
	q.size--
	q.bubbleDown(0)
	return ret
}

func (q *priorityQueue) bubbleUp(index int) {
	for index > 0 {
		parent := (index - 1) / 2
		if !q.slots[index].lessThan(q.slots[parent]) {
			break
		}
		q.swap(index, parent)
		index = parent
	}
}

func (q *priorityQueue) swap(x, y int) {
	q.slots[x], q.slots[y] = q.slots[y], q.slots[x]
}

func (q *priorityQueue) bubbleDown(index int) {
	for index < q.size {
		left := index*2 + 1
		right := index*2 + 2
		var minChild int
		if left < q.size && right < q.size {
			if q.slots[left].lessThan(q.slots[right]) {
				minChild = left
			} else {
				minChild = right
			}
		} else if left < q.size {
			minChild = left
		} else if right < q.size {
			minChild = right
		} else {
			break
		}
		if q.slots[minChild].lessThan(q.slots[index]) {
			//fmt.Printf("min: %d\n", min)
			q.swap(index, minChild)
			index = minChild
		} else {
			break
		}
	}
}

func (q *priorityQueue) Peek() *poolItem {
	if q.size == 0 {
		return nil
	}
	return q.slots[0]
}

func (q *priorityQueue) Rob(id string) *poolItem {
	for i := 0; i < q.size; i++ {
		item := q.slots[i]
		if item.res.GetResourceID() == id {
			q.slots[i] = q.slots[q.size-1]
			q.size--
			q.bubbleDown(i)
			return item
		}
	}

	return nil
}

func (q *priorityQueue) Find(id string) *poolItem {
	for i := 0; i < q.size; i++ {
		if q.slots[i].res.GetResourceID() == id {
			return q.slots[i]
		}
	}
	return nil
}

func (q *priorityQueue) Push(item *poolItem) {
	q.slots[q.size] = item
	q.size++
	q.bubbleUp(q.size - 1)
	if q.size >= q.capacity {
		q.capacity *= 2
		newSlots := make([]*poolItem, q.capacity)
		copy(newSlots, q.slots)
		q.slots = newSlots
	}
}

func (q *priorityQueue) Size() int {
	return q.size
}
