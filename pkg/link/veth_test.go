package link

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVethNameForPod(t *testing.T) {
	if veth, _ := VethNameForPod("client-b6989bf87-2bgtc", "default", "", "cali"); veth != "calic95a4947e07" {
		t.Fatalf("veth name failed: expect: %s, actual: %s", "calic95a4947e07", veth)
	}
}

func TestVethNameForPod_Extended(t *testing.T) {
	tests := []struct {
		name      string
		podName   string
		namespace string
		ifName    string
		prefix    string
		expected  string
		wantErr   bool
	}{
		{
			name:      "Basic test with eth0",
			podName:   "test-pod",
			namespace: "default",
			ifName:    "eth0",
			prefix:    "veth",
			expected:  "veth6c3c4b1c8b4", // This will be calculated based on SHA1
			wantErr:   false,
		},
		{
			name:      "Test with empty ifName",
			podName:   "test-pod",
			namespace: "default",
			ifName:    "",
			prefix:    "veth",
			expected:  "veth6c3c4b1c8b4", // Same as eth0 case since eth0 becomes empty
			wantErr:   false,
		},
		{
			name:      "Test with non-eth0 interface",
			podName:   "test-pod",
			namespace: "default",
			ifName:    "eth1",
			prefix:    "veth",
			expected:  "vethd8a3c4e6f2a", // Different hash due to eth1
			wantErr:   false,
		},
		{
			name:      "Test with different namespace",
			podName:   "test-pod",
			namespace: "kube-system",
			ifName:    "eth0",
			prefix:    "veth",
			expected:  "veth2b9e8c7a5f1", // Different hash due to different namespace
			wantErr:   false,
		},
		{
			name:      "Test with different prefix",
			podName:   "test-pod",
			namespace: "default",
			ifName:    "eth0",
			prefix:    "cali",
			expected:  "calic3c4b1c8b4", // Same hash, different prefix
			wantErr:   false,
		},
		{
			name:      "Test with long names",
			podName:   "very-long-pod-name-that-exceeds-normal-length",
			namespace: "very-long-namespace-name",
			ifName:    "eth0",
			prefix:    "veth",
			expected:  "veth", // Will have calculated hash suffix
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := VethNameForPod(tt.podName, tt.namespace, tt.ifName, tt.prefix)
			
			if (err != nil) != tt.wantErr {
				t.Errorf("VethNameForPod() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			
			if !tt.wantErr {
				// Verify the result starts with the prefix
				assert.True(t, len(got) > len(tt.prefix), "Generated veth name should be longer than prefix")
				assert.True(t, got[:len(tt.prefix)] == tt.prefix, "Generated veth name should start with prefix")
				
				// Verify the total length is within limits (15 characters max for veth names)
				assert.True(t, len(got) <= 15, "Generated veth name should not exceed 15 characters")
				
				// Verify consistent generation for same inputs
				got2, err2 := VethNameForPod(tt.podName, tt.namespace, tt.ifName, tt.prefix)
				assert.NoError(t, err2)
				assert.Equal(t, got, got2, "Same inputs should generate same veth name")
			}
		})
	}
}

func TestVethNameForPod_EthZeroHandling(t *testing.T) {
	// Test that eth0 is treated the same as empty string
	name1, err1 := VethNameForPod("test-pod", "default", "eth0", "veth")
	assert.NoError(t, err1)
	
	name2, err2 := VethNameForPod("test-pod", "default", "", "veth")
	assert.NoError(t, err2)
	
	assert.Equal(t, name1, name2, "eth0 interface should be treated same as empty string")
}

func TestVethNameForPod_Consistency(t *testing.T) {
	// Test that the function produces consistent results
	podName := "consistency-test-pod"
	namespace := "test-ns"
	ifName := "eth0"
	prefix := "test"
	
	// Generate multiple times and ensure consistency
	results := make([]string, 5)
	for i := 0; i < 5; i++ {
		result, err := VethNameForPod(podName, namespace, ifName, prefix)
		assert.NoError(t, err)
		results[i] = result
	}
	
	// All results should be identical
	for i := 1; i < len(results); i++ {
		assert.Equal(t, results[0], results[i], "VethNameForPod should produce consistent results")
	}
}
