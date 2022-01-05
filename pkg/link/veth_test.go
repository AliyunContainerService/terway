package link

import "testing"

func TestVethNameForPod(t *testing.T) {
	if veth, _ := VethNameForPod("client-b6989bf87-2bgtc", "default", "", "cali"); veth != "calic95a4947e07" {
		t.Fatalf("veth name failed: expect: %s, actual: %s", "calic95a4947e07", veth)
	}
}
