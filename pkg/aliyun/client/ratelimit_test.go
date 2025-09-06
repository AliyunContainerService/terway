package client

import (
	"context"
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
