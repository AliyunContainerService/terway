package backoff

import (
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

// TestBackoff tests if Backoff returns the correct backoff strategy for given keys.
func TestBackoff(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		expected wait.Backoff
	}{
		{
			name: "Existing Key",
			key:  ENICreate,
			expected: wait.Backoff{
				Duration: time.Second * 10,
				Factor:   2,
				Jitter:   0.3,
				Steps:    2,
			},
		},
		{
			name: "Non-existing Key",
			key:  "non_existing_key",
			expected: wait.Backoff{
				Duration: time.Second * 2,
				Factor:   1.5,
				Jitter:   0.3,
				Steps:    6,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Backoff(tt.key)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Test %s failed, expected %v, got %v", tt.name, tt.expected, result)
			}
		})
	}
}
