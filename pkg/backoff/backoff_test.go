package backoff

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/wait"
)

// TestBackoff tests if Backoff returns the correct backoff strategy for given keys.
func TestBackoff(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		expected ExtendedBackoff
	}{
		{
			name: "Existing Key",
			key:  ENICreate,
			expected: ExtendedBackoff{
				InitialDelay: 0,
				Backoff: wait.Backoff{
					Duration: time.Second * 10,
					Factor:   2,
					Jitter:   0.3,
					Steps:    2,
				},
			},
		},
		{
			name: "Non-existing Key",
			key:  "non_existing_key",
			expected: ExtendedBackoff{
				InitialDelay: 0,
				Backoff: wait.Backoff{
					Duration: time.Second * 2,
					Factor:   1.5,
					Jitter:   0.3,
					Steps:    6,
				},
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

// TestExponentialBackoffWithInitialDelay tests the ExponentialBackoffWithInitialDelay function
func TestExponentialBackoffWithInitialDelay(t *testing.T) {
	ctx := context.Background()

	// Test with initial delay
	initialDelay := time.Millisecond * 100
	extendedBackoff := NewExtendedBackoff(initialDelay, wait.Backoff{
		Duration: time.Millisecond * 50,
		Factor:   1.0,
		Jitter:   0.0,
		Steps:    2,
	})

	startTime := time.Now()
	called := false

	err := ExponentialBackoffWithInitialDelay(ctx, extendedBackoff, func(ctx context.Context) (bool, error) {
		called = true
		return true, nil
	})

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if !called {
		t.Error("Expected condition function to be called")
	}

	// Check that initial delay was respected
	elapsed := time.Since(startTime)
	if elapsed < initialDelay {
		t.Errorf("Expected at least %v elapsed time, got %v", initialDelay, elapsed)
	}
}

// TestExponentialBackoffWithInitialDelayFromKey tests the ExponentialBackoffWithInitialDelayFromKey function

// TestExponentialBackoffWithInitialDelayContextCancellation tests context cancellation
func TestExponentialBackoffWithInitialDelayContextCancellation(t *testing.T) {
	initialDelay := time.Second * 10
	extendedBackoff := NewExtendedBackoff(initialDelay, wait.Backoff{
		Duration: time.Millisecond * 50,
		Factor:   1.0,
		Jitter:   0.0,
		Steps:    2,
	})

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel context immediately
	cancel()

	startTime := time.Now()
	err := ExponentialBackoffWithInitialDelay(ctx, extendedBackoff, func(ctx context.Context) (bool, error) {
		return true, nil
	})

	if err != context.Canceled {
		t.Errorf("Expected context.Canceled error, got %v", err)
	}

	// Check that function returned quickly due to context cancellation
	elapsed := time.Since(startTime)
	if elapsed > time.Millisecond*100 {
		t.Errorf("Expected quick return due to context cancellation, but took %v", elapsed)
	}
}

// TestExponentialBackoffWithInitialDelayZeroDelay tests with zero initial delay
func TestExponentialBackoffWithInitialDelayZeroDelay(t *testing.T) {
	ctx := context.Background()

	// Test with zero initial delay
	extendedBackoff := NewExtendedBackoff(0, wait.Backoff{
		Duration: time.Millisecond * 50,
		Factor:   1.0,
		Jitter:   0.0,
		Steps:    2,
	})

	startTime := time.Now()
	called := false

	err := ExponentialBackoffWithInitialDelay(ctx, extendedBackoff, func(ctx context.Context) (bool, error) {
		called = true
		return true, nil
	})

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if !called {
		t.Error("Expected condition function to be called")
	}

	// Check that no initial delay was applied
	elapsed := time.Since(startTime)
	if elapsed > time.Millisecond*100 {
		t.Errorf("Expected no initial delay, but took %v", elapsed)
	}
}

func TestConfig(t *testing.T) {
	type Config struct {
		BackoffOverride map[string]ExtendedBackoff `json:"backoffOverride,omitempty"`
	}

	data := `
{
	"backoffOverride": {
		"wait_node_status": {
			"Duration": 1000000000,
			"Factor": 1.1,
			"Jitter": 1.2,
			"Steps": 10
		}
	}
}
`
	cfg := &Config{}
	err := json.Unmarshal([]byte(data), cfg)
	assert.NoError(t, err)

	assert.Equal(t, time.Duration(1000000000), cfg.BackoffOverride["wait_node_status"].Backoff.Duration)
	assert.Equal(t, 1.1, cfg.BackoffOverride["wait_node_status"].Backoff.Factor)
}

func TestOverrideBackoff(t *testing.T) {
	backoffMap = make(map[string]ExtendedBackoff)

	tests := []struct {
		name     string
		input    map[string]ExtendedBackoff
		expected map[string]ExtendedBackoff
	}{
		{
			name: "Normal input",
			input: map[string]ExtendedBackoff{
				"key1": {InitialDelay: 10},
				"key2": {InitialDelay: 0},
			},
			expected: map[string]ExtendedBackoff{
				"key1": {InitialDelay: 10},
				"key2": {InitialDelay: 0},
			},
		},
		{
			name:     "Empty input",
			input:    map[string]ExtendedBackoff{},
			expected: map[string]ExtendedBackoff{},
		},
		{
			name: "Uninitialized backoffMap",
			input: map[string]ExtendedBackoff{
				"key1": {InitialDelay: 5},
			},
			expected: map[string]ExtendedBackoff{
				"key1": {InitialDelay: 5},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			OverrideBackoff(tt.input)
			for k, v := range tt.expected {
				if backoffMap[k].InitialDelay != v.InitialDelay {
					t.Errorf("Test %s failed: expected %v, got %v", tt.name, v, backoffMap[k])
				}
			}
		})
	}
}
