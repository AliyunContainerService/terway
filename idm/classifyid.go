package idm

var pod2Id map[string]int

//TODO 使用bitmap实现 & 持久化
var pool []int

const POOL_SIZE = 65535
const START = 11

func init() {
	pod2Id = make(map[string]int)

	pool = make([]int, POOL_SIZE)
	for i := 0; i < POOL_SIZE; i++ {
		pool[i] = START + i
	}
}

func GetId(pod string) int {
	if val, ok := pod2Id[pod]; ok {
		return val
	}

	return -1
}

func AcquireID(pod string) int {
	if val, ok := pod2Id[pod]; ok {
		return val
	}
	id := pool[0]
	pod2Id[pod] = id
	pool = pool[1:]
	return id
}

func ReleaseID(pod string) {
	val, ok := pod2Id[pod]
	if !ok {
		return
	}
	delete(pod2Id, pod)
	pool = append(pool, val)
}
