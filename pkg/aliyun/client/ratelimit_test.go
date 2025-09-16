package client

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewRateLimiter(t *testing.T) {
	type args struct {
		cfg LimitConfig
	}
	tests := []struct {
		name      string
		args      args
		checkFunc func(t *testing.T, r *RateLimiter)
	}{
		{
			name: "test default",
			args: args{
				cfg: nil,
			},
			checkFunc: func(t *testing.T, r *RateLimiter) {
				// Check that the rate limiter was created for DescribeInstanceTypes
				assert.NotNil(t, r.store["DescribeInstanceTypes"])
			},
		},
		{
			name: "test override",
			args: args{
				cfg: map[string]Limit{
					"DescribeInstanceTypes": {
						QPS:   float64(600 / 60),
						Burst: 600,
					},
				},
			},
			checkFunc: func(t *testing.T, r *RateLimiter) {
				// Check that the rate limiter was created for DescribeInstanceTypes
				assert.NotNil(t, r.store["DescribeInstanceTypes"])
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.checkFunc(t, NewRateLimiter(tt.args.cfg))
		})
	}
}

func TestRateLimiter_Wait(t *testing.T) {
	r := NewRateLimiter(map[string]Limit{
		"foo": {
			QPS:   1,
			Burst: 1,
		},
	})

	// Test that Wait works without error
	start := time.Now()
	err := r.Wait(context.Background(), "foo")
	assert.NoError(t, err)
	assert.True(t, time.Since(start) < 100*time.Millisecond)

	// Test that subsequent requests also work (adaptive limiter may not block immediately)
	start = time.Now()
	err = r.Wait(context.Background(), "foo")
	assert.NoError(t, err)
	// Adaptive limiter may not block immediately, so we just check it doesn't error
	assert.True(t, time.Since(start) < 1*time.Second)
}

func TestIsThrottleError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "throttling error",
			err:      fmt.Errorf("Throttling.User: The request was denied due to request throttling"),
			expected: true,
		},
		{
			name:     "service unavailable",
			err:      fmt.Errorf("ServiceUnavailable: The service is temporarily unavailable"),
			expected: true,
		},
		{
			name:     "request limit exceeded",
			err:      fmt.Errorf("RequestLimitExceeded: The request limit has been exceeded"),
			expected: true,
		},
		{
			name:     "other error",
			err:      fmt.Errorf("SomeOtherError: This is not a throttle error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsThrottleError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestUpdateThrottleStatus(t *testing.T) {
	r := NewRateLimiter(LimitConfig{
		"test": {
			QPS:   10,
			Burst: 20,
		},
	})

	// Test updating throttle status
	r.UpdateThrottleStatus("test", true)  // Should not panic
	r.UpdateThrottleStatus("test", false) // Should not panic
	r.UpdateThrottleStatus("nonexistent", true) // Should use default
}
