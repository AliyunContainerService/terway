package daemon

import (
	"net"
	"testing"

	"github.com/AliyunContainerService/terway/types"
)

func TestENI_GetResourceID(t *testing.T) {
	eni := &ENI{
		MAC: "00:00:00:00:00:01",
	}

	expected := "00:00:00:00:00:01"
	actual := eni.GetResourceID()

	if actual != expected {
		t.Errorf("Expected GetResourceID to return %s, but got %s", expected, actual)
	}
}

func TestENI_GetType(t *testing.T) {
	eni := &ENI{}

	expected := ResourceTypeENI
	actual := eni.GetType()

	if actual != expected {
		t.Errorf("Expected GetType to return %s, but got %s", expected, actual)
	}
}

func TestENI_ToResItems(t *testing.T) {
	ipv4, ipv6 := net.ParseIP("192.168.1.1"), net.ParseIP("2001:db8::1")
	eni := &ENI{
		ID:  "eni-12345",
		MAC: "00:00:00:00:00:01",
		PrimaryIP: types.IPSet{
			IPv4: ipv4,
			IPv6: ipv6,
		},
	}

	items := eni.ToResItems()

	if len(items) != 1 {
		t.Errorf("Expected ToResItems to return 1 item, but got %d", len(items))
	}

	item := items[0]
	if item.Type != ResourceTypeENI {
		t.Errorf("Expected item type to be %s, but got %s", ResourceTypeENI, item.Type)
	}
	if item.ID != eni.MAC {
		t.Errorf("Expected item ID to be %s, but got %s", eni.MAC, item.ID)
	}
	if item.ENIID != "eni-12345" {
		t.Errorf("Expected item ENIID to be eni-12345, but got %s", item.ENIID)
	}
	if item.ENIMAC != "00:00:00:00:00:01" {
		t.Errorf("Expected item ENIMAC to be 00:00:00:00:00:01, but got %s", item.ENIMAC)
	}
	if item.IPv4 != ipv4.String() {
		t.Errorf("Expected item IPv4 to be %v, but got %v", ipv4, item.IPv4)
	}
	if item.IPv6 != ipv6.String() {
		t.Errorf("Expected item IPv6 to be %v, but got %v", ipv6, item.IPv6)
	}
}

func TestENIIP_GetResourceID(t *testing.T) {
	eni := &ENI{
		MAC: "00:00:00:00:00:01",
	}
	eniip := &ENIIP{
		ENI: eni,
		IPSet: types.IPSet{
			IPv4: net.ParseIP("192.168.1.1"),
		},
	}

	expected := "00:00:00:00:00:01.192.168.1.1"
	actual := eniip.GetResourceID()

	if actual != expected {
		t.Errorf("Expected GetResourceID to return %s, but got %s", expected, actual)
	}
}

func TestENIIP_GetType(t *testing.T) {
	eniip := &ENIIP{}

	expected := ResourceTypeENIIP
	actual := eniip.GetType()

	if actual != expected {
		t.Errorf("Expected GetType to return %s, but got %s", expected, actual)
	}
}

func TestENIIP_ToResItems(t *testing.T) {
	ipv4, ipv6 := net.ParseIP("192.168.1.2"), net.ParseIP("2001:db8::2")
	eni := &ENI{
		ID:  "eni-67890",
		MAC: "00:00:00:00:00:02",
	}
	eniip := &ENIIP{
		ENI: eni,
		IPSet: types.IPSet{
			IPv4: ipv4,
			IPv6: ipv6,
		},
	}

	items := eniip.ToResItems()

	if len(items) != 1 {
		t.Errorf("Expected ToResItems to return 1 item, but got %d", len(items))
	}

	item := items[0]
	if item.Type != ResourceTypeENIIP {
		t.Errorf("Expected item type to be %s, but got %s", ResourceTypeENIIP, item.Type)
	}
	if item.ID != eniip.GetResourceID() {
		t.Errorf("Expected item ID to be %s, but got %s", eniip.GetResourceID(), item.ID)
	}
	if item.ENIID != "eni-67890" {
		t.Errorf("Expected item ENIID to be eni-67890, but got %s", item.ENIID)
	}
	if item.ENIMAC != "00:00:00:00:00:02" {
		t.Errorf("Expected item ENIMAC to be 00:00:00:00:00:02, but got %s", item.ENIMAC)
	}
	if item.IPv4 != ipv4.String() {
		t.Errorf("Expected item IPv4 to be %v, but got %v", ipv4, item.IPv4)
	}
	if item.IPv6 != ipv6.String() {
		t.Errorf("Expected item IPv6 to be %v, but got %v", ipv6, item.IPv6)
	}
}
