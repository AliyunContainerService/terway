package client

import (
	"context"
	"sync"
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
				assert.Equal(t, 400, r.store["DescribeInstanceTypes"].Burst())
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
				assert.Equal(t, 600, r.store["DescribeInstanceTypes"].Burst())
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

	start := time.Now()
	wg := sync.WaitGroup{}

	for i := 0; i < 2; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()
			err := r.Wait(context.Background(), "foo")
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
	assert.True(t, 1*time.Second < time.Since(start))
}
