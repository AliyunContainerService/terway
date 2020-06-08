package daemon

import "testing"

func TestRandomString(t *testing.T) {
	for i := 0; i < 10; i++ {
		t.Log(randomString())
	}
}
