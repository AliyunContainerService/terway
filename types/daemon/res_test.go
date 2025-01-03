package daemon

import (
	"reflect"
	"testing"
)

// TestGetResourceItemByType tests the GetResourceItemByType method of PodResources.
func TestGetResourceItemByType(t *testing.T) {
	tests := []struct {
		name     string
		resType  string
		res      []ResourceItem
		expected []ResourceItem
	}{
		{
			name:    "MatchingType",
			resType: "network",
			res: []ResourceItem{
				{Type: "network", ID: "1", ENIID: "eni-1", ENIMAC: "02:12:34:56:78:90", IPv4: "10.0.0.1", IPv6: "2001:0db8:85a3:0000:0000:8a2e:0700:7344"},
				{Type: "storage", ID: "2", ENIID: "eni-2", ENIMAC: "02:12:34:56:78:91", IPv4: "10.0.0.2", IPv6: "2001:0db8:85a3:0000:0000:8a2e:0700:7345"},
			},
			expected: []ResourceItem{
				{Type: "network", ID: "1", ENIID: "eni-1", ENIMAC: "02:12:34:56:78:90", IPv4: "10.0.0.1", IPv6: "2001:0db8:85a3:0000:0000:8a2e:0700:7344"},
			},
		},
		{
			name:    "MultipleMatchingTypes",
			resType: "network",
			res: []ResourceItem{
				{Type: "network", ID: "1", ENIID: "eni-1", ENIMAC: "02:12:34:56:78:90", IPv4: "10.0.0.1", IPv6: "2001:0db8:85a3:0000:0000:8a2e:0700:7344"},
				{Type: "network", ID: "2", ENIID: "eni-2", ENIMAC: "02:12:34:56:78:91", IPv4: "10.0.0.2", IPv6: "2001:0db8:85a3:0000:0000:8a2e:0700:7345"},
			},
			expected: []ResourceItem{
				{Type: "network", ID: "1", ENIID: "eni-1", ENIMAC: "02:12:34:56:78:90", IPv4: "10.0.0.1", IPv6: "2001:0db8:85a3:0000:0000:8a2e:0700:7344"},
				{Type: "network", ID: "2", ENIID: "eni-2", ENIMAC: "02:12:34:56:78:91", IPv4: "10.0.0.2", IPv6: "2001:0db8:85a3:0000:0000:8a2e:0700:7345"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			podResources := PodResources{Resources: test.res}
			result := podResources.GetResourceItemByType(test.resType)
			if !reflect.DeepEqual(result, test.expected) {
				t.Errorf("GetResourceItemByType(%s) = %v, want %v", test.resType, result, test.expected)
			}
		})
	}
}
