package daemon

import (
	"testing"
)

func TestMapSorter(t *testing.T) {
	ms := newMapSorter(map[string]int{
		"a": 100,
		"b": 90,
		"c": 110,
		"d": 0,
	})
	ms.SortInDescendingOrder()
	for i := 1; i < len(ms); i++ {
		if ms[i].Val > ms[i-1].Val {
			t.Fatalf("unexpect sorting result: %+v", ms)
		}
	}
}
