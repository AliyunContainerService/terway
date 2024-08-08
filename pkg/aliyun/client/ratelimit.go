package client

import (
	"context"
	"time"

	"golang.org/x/time/rate"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/AliyunContainerService/terway/pkg/metric"
)

type LimitConfig map[string]Limit

type Limit struct {
	QPS   float64
	Burst int
}

var defaultLimit = map[string]int{
	"":                           500,
	"AttachNetworkInterface":     500,
	"CreateNetworkInterface":     500,
	"DeleteNetworkInterface":     500,
	"DescribeNetworkInterfaces":  800,
	"DetachNetworkInterface":     400,
	"AssignPrivateIpAddresses":   400,
	"UnassignPrivateIpAddresses": 400,
	"AssignIpv6Addresses":        400,
	"UnassignIpv6Addresses":      400,
	"DescribeInstanceTypes":      400,
	"DescribeVSwitches":          300,
}

const (
	longThrottleLatency = 5 * time.Second
)

func FromMap(in map[string]int) LimitConfig {
	l := make(LimitConfig)
	for k, v := range in {
		l[k] = Limit{
			QPS:   float64(v) / 60,
			Burst: v,
		}
	}
	return l
}

type RateLimiter struct {
	store map[string]*rate.Limiter
}

func NewRateLimiter(cfg LimitConfig) *RateLimiter {
	r := &RateLimiter{
		store: make(map[string]*rate.Limiter),
	}
	for k, v := range defaultLimit {
		r.store[k] = rate.NewLimiter(rate.Limit(float64(v)/60), v)
	}
	for k, v := range cfg {
		r.store[k] = rate.NewLimiter(rate.Limit(v.QPS), v.Burst)
	}

	return r
}

func (r *RateLimiter) Wait(ctx context.Context, name string) error {
	start := time.Now()
	defer func() {
		took := time.Since(start)
		metric.RateLimiterLatency.WithLabelValues(name).Observe(float64(took.Milliseconds()))

		if took >= longThrottleLatency {
			l := logf.FromContext(ctx)
			l.Info("client rate limit", "api", name, "took", took.Seconds())
		}
	}()
	v, ok := r.store[name]
	if ok {
		return v.Wait(ctx)
	}
	return r.store[""].Wait(ctx)
}
