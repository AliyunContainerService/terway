package daemon

import "testing"

func TestGenerateEniName(t *testing.T) {
	t.Logf("eni name: %s", generateEniName())
}
