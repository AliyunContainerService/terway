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

func TestFromMap(t *testing.T) {
	in := map[string]int{
		"API1": 60,
		"API2": 120,
	}
	cfg := FromMap(in)
	assert.Len(t, cfg, 2)
	assert.Equal(t, 1.0, cfg["API1"].QPS)
	assert.Equal(t, 60, cfg["API1"].Burst)
	assert.Equal(t, 2.0, cfg["API2"].QPS)
	assert.Equal(t, 120, cfg["API2"].Burst)
}

func TestRateLimiter_Wait_unknownName(t *testing.T) {
	r := NewRateLimiter(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := r.Wait(ctx, "NonExistentAPI")
	assert.NoError(t, err)
}
