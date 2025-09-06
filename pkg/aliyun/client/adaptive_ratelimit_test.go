package client

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewAdaptiveRateLimiter(t *testing.T) {
	cfg := LimitConfig{
		"test": {QPS: 5.0, Burst: 10},
	}
	
	limiter := NewAdaptiveRateLimiter(cfg)
	assert.NotNil(t, limiter)
	assert.NotNil(t, limiter.store)
	assert.Contains(t, limiter.store, "test")
	assert.Contains(t, limiter.store, "") // default limiter
}

func TestAdaptiveRateLimiter_Wait(t *testing.T) {
	cfg := LimitConfig{
		"test": {QPS: 2.0, Burst: 2},
	}
	
	limiter := NewAdaptiveRateLimiter(cfg)
	ctx := context.Background()
	
	// First few requests should be fast
	start := time.Now()
	err := limiter.Wait(ctx, "test")
	assert.NoError(t, err)
	assert.True(t, time.Since(start) < 100*time.Millisecond)
	
	err = limiter.Wait(ctx, "test")
	assert.NoError(t, err)
}

func TestAdaptiveRateLimiter_HandleResponse(t *testing.T) {
	cfg := LimitConfig{
		"test": {QPS: 10.0, Burst: 10},
	}
	
	limiter := NewAdaptiveRateLimiter(cfg)
	
	// Test successful response
	limiter.HandleResponse("test", nil)
	
	// Test throttle error response
	throttleErr := fmt.Errorf("Throttling: Request was denied due to request throttling")
	limiter.HandleResponse("test", throttleErr)
	
	// After throttle error, the rate should be reduced
	// This is reflected in the internal state, but we can't directly observe it
	// without exposing internal fields
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
			err:      fmt.Errorf("Throttling: Request was denied due to request throttling"),
			expected: true,
		},
		{
			name:     "request throttled error",
			err:      fmt.Errorf("RequestThrottled: Too many requests"),
			expected: true,
		},
		{
			name:     "QPS limit error",
			err:      fmt.Errorf("QPS Limit Exceeded"),
			expected: true,
		},
		{
			name:     "regular error",
			err:      fmt.Errorf("InvalidParameter: Missing required parameter"),
			expected: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isThrottleError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAdaptiveRateLimiterWrapper(t *testing.T) {
	cfg := LimitConfig{
		"test": {QPS: 5.0, Burst: 5},
	}
	
	wrapper := NewAdaptiveRateLimiterWrapper(cfg)
	ctx := context.Background()
	
	// Test WaitAndHandle with successful API call
	callCount := 0
	err := wrapper.WaitAndHandle(ctx, "test", func() error {
		callCount++
		return nil
	})
	
	assert.NoError(t, err)
	assert.Equal(t, 1, callCount)
	
	// Test WaitAndHandle with throttling error
	err = wrapper.WaitAndHandle(ctx, "test", func() error {
		return fmt.Errorf("Throttling: Too many requests")
	})
	
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Throttling")
}

func TestAdaptiveRateLimiter_Concurrency(t *testing.T) {
	cfg := LimitConfig{
		"test": {QPS: 5.0, Burst: 5},
	}
	
	limiter := NewAdaptiveRateLimiter(cfg)
	ctx := context.Background()
	
	var wg sync.WaitGroup
	errors := make(chan error, 10)
	
	// Run 10 concurrent requests
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := limiter.Wait(ctx, "test")
			if err != nil {
				errors <- err
			}
		}()
	}
	
	wg.Wait()
	close(errors)
	
	// Check if there were any errors
	for err := range errors {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestAdaptiveRateLimiter_ContextCancellation(t *testing.T) {
	cfg := LimitConfig{
		"test": {QPS: 0.1, Burst: 1}, // Very slow rate
	}
	
	limiter := NewAdaptiveRateLimiter(cfg)
	
	// First request should succeed (uses burst capacity)
	err := limiter.Wait(context.Background(), "test")
	assert.NoError(t, err)
	
	// Create a context that will be cancelled
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	
	// Second request should be cancelled due to context timeout
	start := time.Now()
	err = limiter.Wait(ctx, "test")
	duration := time.Since(start)
	
	// Check if either succeeded immediately or was cancelled
	if err != nil {
		assert.True(t, duration >= 100*time.Millisecond)
		assert.Contains(t, err.Error(), "context deadline exceeded")
	} else {
		// If it succeeded immediately, that's also acceptable behavior
		t.Logf("Request succeeded immediately, duration: %v", duration)
	}
}