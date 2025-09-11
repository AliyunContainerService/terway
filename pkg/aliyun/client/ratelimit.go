package client

import (
	"context"
	"strings"
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
	// eflo
	"AssignLeniPrivateIpAddress":               300,
	"AttachElasticNetworkInterface":            300,
	"DetachElasticNetworkInterface":            300,
	"ListElasticNetworkInterfaces":             100 * 60,
	"CreateElasticNetworkInterface":            20 * 60,
	"DeleteElasticNetworkInterface":            20 * 60,
	"CreateHighDensityElasticNetworkInterface": 15 * 60,
	"DeleteHighDensityElasticNetworkInterface": 300,
	"AttachHighDensityElasticNetworkInterface": 300,
	"DetachHighDensityElasticNetworkInterface": 300,
	"ListHighDensityElasticNetworkInterfaces":  100 * 60,
	"GetNodeInfoForPod":                        100 * 60,
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

// AdaptiveRateLimiter provides adaptive rate limiting based on AWS's implementation
type AdaptiveRateLimiter struct {
	store map[string]*adaptiveRateLimit
}

// RateLimiterInterface defines the common interface for both rate limiters
type RateLimiterInterface interface {
	Wait(ctx context.Context, name string) error
}

// ResponseHandler defines how to handle API responses for adaptive rate limiting
type ResponseHandler interface {
	HandleResponse(apiName string, err error)
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

// NewAdaptiveRateLimiter creates a new adaptive rate limiter
func NewAdaptiveRateLimiter(cfg LimitConfig) *AdaptiveRateLimiter {
	r := &AdaptiveRateLimiter{
		store: make(map[string]*adaptiveRateLimit),
	}
	
	// Initialize with default limits converted to initial rates
	for k, v := range defaultLimit {
		limiter := newAdaptiveRateLimit()
		// Set initial rate based on default limit
		initialRate := float64(v) / 60.0 // Convert per-minute to per-second
		limiter.fillRate = initialRate
		limiter.tokenBucket = newAdaptiveTokenBucket(initialRate * 2) // 2 second burst capacity
		r.store[k] = limiter
	}
	
	// Override with custom config
	for k, v := range cfg {
		limiter := newAdaptiveRateLimit()
		limiter.fillRate = v.QPS
		limiter.tokenBucket = newAdaptiveTokenBucket(float64(v.Burst))
		r.store[k] = limiter
	}
	
	return r
}

func (r *AdaptiveRateLimiter) Wait(ctx context.Context, name string) error {
	start := time.Now()
	defer func() {
		took := time.Since(start)
		metric.RateLimiterLatency.WithLabelValues(name).Observe(float64(took.Milliseconds()))

		if took >= longThrottleLatency {
			l := logf.FromContext(ctx)
			l.Info("adaptive rate limit", "api", name, "took", took.Seconds())
		}
	}()

	limiter, ok := r.store[name]
	if !ok {
		limiter = r.store[""]
	}
	
	// If still no limiter found, return error
	if limiter == nil {
		return nil // No rate limiting applied
	}

	// Try to acquire token with adaptive rate limiting
	for {
		acquired, waitDuration := limiter.AcquireToken(1)
		if acquired {
			break
		}

		if waitDuration > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(waitDuration):
				continue
			}
		}
	}

	return nil
}

// HandleResponse updates the adaptive rate limiter based on the API response
func (r *AdaptiveRateLimiter) HandleResponse(apiName string, err error) {
	limiter, ok := r.store[apiName]
	if !ok {
		limiter = r.store[""]
	}
	
	// If no limiter found, skip update
	if limiter == nil {
		return
	}

	throttled := isThrottleError(err)
	limiter.Update(throttled)
}

// isThrottleError determines if an error indicates throttling from Aliyun API
func isThrottleError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	// Common Aliyun throttling error patterns
	throttlePatterns := []string{
		"Throttling",
		"RequestThrottled",
		"Throttled",
		"TooManyRequests",
		"Rate exceeded",
		"QPS Limit Exceeded",
		"Bandwidth.Out.Limit.Exceeded",
		"Request was denied due to request throttling",
	}

	for _, pattern := range throttlePatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}
