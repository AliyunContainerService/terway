package pool

type priorityQeueu struct {
	slots    []*poolItem
	size     int
	capacity int
}

func newPriorityQueue() *priorityQeueu {
	return &priorityQeueu{
		capacity: 10,
		size:     0,
		slots:    make([]*poolItem, 10),
	}
}

func (q *priorityQeueu) Pop() *poolItem {
	if q.size == 0 {
		return nil
	}
	ret := q.slots[0]
	q.slots[0] = q.slots[q.size-1]
	q.size--
	q.bubbleDown(0)
	return ret
}

func (q *priorityQeueu) bubbleUp(index int) {
	for index > 0 {
		parent := (index - 1) / 2
		if !q.slots[index].lessThan(q.slots[parent]) {
			break
		}
		q.swap(index, parent)
		index = parent
	}
}

func (q *priorityQeueu) swap(x, y int) {
	tmp := q.slots[x]
	q.slots[x] = q.slots[y]
	q.slots[y] = tmp
}

func (q *priorityQeueu) bubbleDown(index int) {
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

func (q *priorityQeueu) Peek() *poolItem {
	if q.size == 0 {
		return nil
	}
	return q.slots[0]
}

func (q *priorityQeueu) Rob(id string) *poolItem {
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

func (q *priorityQeueu) Find(id string) *poolItem {
	for i := 0; i < q.size; i++ {
		if q.slots[i].res.GetResourceID() == id {
			return q.slots[i]
		}
	}
	return nil
}

func (q *priorityQeueu) Push(item *poolItem) {
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

func (q *priorityQeueu) Size() int {
	return q.size
}
