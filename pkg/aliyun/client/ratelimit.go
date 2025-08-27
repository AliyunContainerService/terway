package client

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/AliyunContainerService/terway/pkg/metric"
)

// LimitConfig Configuration structure, supporting both QPS and QPM modes
type LimitConfig map[string]Limit

// Limit Rate limiting configuration
// If QPM is set, QPM mode is used; otherwise QPS mode is used
type Limit struct {
	QPS   *float64 // Requests per second (pointer, nil means not set)
	QPM   *int     // Requests per minute (pointer, nil means not set)
	Burst int      // Burst requests
}

// IsQPM Check if it is QPM mode
func (l *Limit) IsQPM() bool {
	return l.QPM != nil
}

// GetQPS Get QPS value
func (l *Limit) GetQPS() float64 {
	if l.QPM != nil {
		return float64(*l.QPM) / 60
	}
	if l.QPS != nil {
		return *l.QPS
	}
	return 0
}

// ThrottleInfo Rate limiting suppression information
type ThrottleInfo struct {
	Enabled      bool          // Whether rate limit suppression is enabled
	Factor       float64       // Rate limit factor (0.1 means reduced to 10% of original)
	StartTime    time.Time     // Start time
	Duration     time.Duration // Duration
	RecoveryRate float64       // Recovery rate (percentage recovered per second)
}

// RateLimiter Enhanced rate limiter
type RateLimiter struct {
	mu           sync.RWMutex
	store        map[string]*rate.Limiter
	throttleInfo map[string]*ThrottleInfo
	originalQPS  map[string]float64 // Save original QPS values for recovery
}

// NewRateLimiter Create a new rate limiter
func NewRateLimiter(cfg LimitConfig) *RateLimiter {
	r := &RateLimiter{
		store:        make(map[string]*rate.Limiter),
		throttleInfo: make(map[string]*ThrottleInfo),
		originalQPS:  make(map[string]float64),
	}

	// Initialize default rate limiters
	for k, v := range defaultLimit {
		qps := v.GetQPS()
		r.store[k] = rate.NewLimiter(rate.Limit(qps), v.Burst)
		r.originalQPS[k] = qps
		r.throttleInfo[k] = &ThrottleInfo{
			Enabled:      false,
			Factor:       1.0,
			RecoveryRate: 0.1, // Recover 10% per second
		}
	}

	// Apply custom configuration
	for k, v := range cfg {
		qps := v.GetQPS()
		r.store[k] = rate.NewLimiter(rate.Limit(qps), v.Burst)
		r.originalQPS[k] = qps
		r.throttleInfo[k] = &ThrottleInfo{
			Enabled:      false,
			Factor:       1.0,
			RecoveryRate: 0.1,
		}
	}

	return r
}

// checkAndRecoverThrottle Check and recover throttle status lazily
func (r *RateLimiter) checkAndRecoverThrottle(api string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	throttle, exists := r.throttleInfo[api]
	if !exists || !throttle.Enabled {
		return
	}

	now := time.Now()

	// Check if duration has exceeded
	if now.Sub(throttle.StartTime) >= throttle.Duration {
		// Full recovery
		r.restoreOriginalRate(api)
		return
	}

	// Gradual recovery
	elapsed := now.Sub(throttle.StartTime).Seconds()
	recoveryAmount := elapsed * throttle.RecoveryRate
	newFactor := throttle.Factor + recoveryAmount

	if newFactor >= 1.0 {
		// Full recovery
		r.restoreOriginalRate(api)
	} else {
		// Partial recovery
		throttle.Factor = newFactor
		r.updateLimiterRate(api, throttle.Factor)
	}
}

// restoreOriginalRate Restore original rate
func (r *RateLimiter) restoreOriginalRate(api string) {
	if limiter, exists := r.store[api]; exists {
		originalQPS := r.originalQPS[api]
		limiter.SetLimit(rate.Limit(originalQPS))
		r.throttleInfo[api].Enabled = false
		r.throttleInfo[api].Factor = 1.0

		logf.Log.Info("Rate limit restored", "api", api, "qps", originalQPS)
	}
}

// updateLimiterRate Update rate limiter rate
func (r *RateLimiter) updateLimiterRate(api string, factor float64) {
	if limiter, exists := r.store[api]; exists {
		originalQPS := r.originalQPS[api]
		newQPS := originalQPS * factor
		limiter.SetLimit(rate.Limit(newQPS))

		logf.Log.Info("Rate limit updated", "api", api, "factor", factor, "qps", newQPS)
	}
}

// SetThrottle Set active rate limit suppression
func (r *RateLimiter) SetThrottle(api string, factor float64, duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if throttle, exists := r.throttleInfo[api]; exists {
		throttle.Enabled = true
		throttle.Factor = factor
		throttle.StartTime = time.Now()
		throttle.Duration = duration

		// Apply rate limit immediately
		r.updateLimiterRate(api, factor)

		logf.Log.Info("Throttle applied", "api", api, "factor", factor, "duration", duration)
	}
}

// SetThrottleWithRecovery Set rate limit suppression with automatic recovery
func (r *RateLimiter) SetThrottleWithRecovery(api string, factor float64, duration time.Duration, recoveryRate float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if throttle, exists := r.throttleInfo[api]; exists {
		throttle.Enabled = true
		throttle.Factor = factor
		throttle.StartTime = time.Now()
		throttle.Duration = duration
		throttle.RecoveryRate = recoveryRate

		// Apply rate limit immediately
		r.updateLimiterRate(api, factor)

		logf.Log.Info("Throttle with recovery applied", "api", api, "factor", factor, "duration", duration, "recoveryRate", recoveryRate)
	}
}

// GetThrottleInfo Get rate limit suppression information
func (r *RateLimiter) GetThrottleInfo(api string) *ThrottleInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if throttle, exists := r.throttleInfo[api]; exists {
		return &ThrottleInfo{
			Enabled:      throttle.Enabled,
			Factor:       throttle.Factor,
			StartTime:    throttle.StartTime,
			Duration:     throttle.Duration,
			RecoveryRate: throttle.RecoveryRate,
		}
	}
	return nil
}

// Wait Wait for the rate limiter to allow the request
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

	r.mu.RLock()
	v, ok := r.store[name]
	r.mu.RUnlock()

	if ok {
		// Check and recover throttle status before waiting
		r.checkAndRecoverThrottle(name)
		return v.Wait(ctx)
	}

	// Use default rate limiter
	r.mu.RLock()
	defaultLimiter := r.store[""]
	r.mu.RUnlock()

	// Check and recover throttle status for default limiter
	r.checkAndRecoverThrottle("")
	return defaultLimiter.Wait(ctx)
}

// Close Close the rate limiter
func (r *RateLimiter) Close() {
	// No need to stop ticker or close channel anymore
}

// UpdateConfig Update rate limiter configuration with hot reload support
// This method will reapply default configuration and overlay with new custom configuration
func (r *RateLimiter) UpdateConfig(cfg LimitConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Store current throttle states to preserve them during config update
	currentThrottleStates := make(map[string]*ThrottleInfo)
	for api, throttle := range r.throttleInfo {
		if throttle.Enabled {
			currentThrottleStates[api] = &ThrottleInfo{
				Enabled:      throttle.Enabled,
				Factor:       throttle.Factor,
				StartTime:    throttle.StartTime,
				Duration:     throttle.Duration,
				RecoveryRate: throttle.RecoveryRate,
			}
		}
	}

	// Clear existing configuration
	r.store = make(map[string]*rate.Limiter)
	r.throttleInfo = make(map[string]*ThrottleInfo)
	r.originalQPS = make(map[string]float64)

	// Reapply default configuration first
	for k, v := range defaultLimit {
		qps := v.GetQPS()
		r.store[k] = rate.NewLimiter(rate.Limit(qps), v.Burst)
		r.originalQPS[k] = qps
		r.throttleInfo[k] = &ThrottleInfo{
			Enabled:      false,
			Factor:       1.0,
			RecoveryRate: 0.1, // Recover 10% per second
		}
	}

	// Apply new custom configuration (overriding defaults)
	for k, v := range cfg {
		qps := v.GetQPS()
		r.store[k] = rate.NewLimiter(rate.Limit(qps), v.Burst)
		r.originalQPS[k] = qps
		r.throttleInfo[k] = &ThrottleInfo{
			Enabled:      false,
			Factor:       1.0,
			RecoveryRate: 0.1,
		}
	}

	// Restore throttle states for APIs that still exist
	for api, throttleState := range currentThrottleStates {
		if newThrottle, exists := r.throttleInfo[api]; exists {
			// Restore throttle state
			newThrottle.Enabled = throttleState.Enabled
			newThrottle.Factor = throttleState.Factor
			newThrottle.StartTime = throttleState.StartTime
			newThrottle.Duration = throttleState.Duration
			newThrottle.RecoveryRate = throttleState.RecoveryRate

			// Apply current throttle factor to the new limiter
			if throttleState.Enabled {
				originalQPS := r.originalQPS[api]
				newQPS := originalQPS * throttleState.Factor
				if limiter, exists := r.store[api]; exists {
					limiter.SetLimit(rate.Limit(newQPS))
				}
			}
		}
	}

	logf.Log.Info("Rate limiter configuration updated", "config", cfg)
}

// Default rate limiting configuration
// Most APIs use QPM mode, some high-frequency APIs use QPS mode
var defaultLimit = map[string]Limit{
	"": {
		QPM:   &[]int{500}[0],
		Burst: 500,
	},
	// Basic network interface operations - using QPM mode
	"AttachNetworkInterface": {
		QPM:   &[]int{500}[0],
		Burst: 500,
	},
	"CreateNetworkInterface": {
		QPM:   &[]int{500}[0],
		Burst: 500,
	},
	"DeleteNetworkInterface": {
		QPM:   &[]int{500}[0],
		Burst: 500,
	},
	"DescribeNetworkInterfaces": {
		QPM:   &[]int{800}[0],
		Burst: 800,
	},
	"DetachNetworkInterface": {
		QPM:   &[]int{400}[0],
		Burst: 400,
	},
	"AssignPrivateIpAddresses": {
		QPM:   &[]int{400}[0],
		Burst: 400,
	},
	"UnassignPrivateIpAddresses": {
		QPM:   &[]int{400}[0],
		Burst: 400,
	},
	"AssignIpv6Addresses": {
		QPM:   &[]int{400}[0],
		Burst: 400,
	},
	"UnassignIpv6Addresses": {
		QPM:   &[]int{400}[0],
		Burst: 400,
	},
	"DescribeInstanceTypes": {
		QPM:   &[]int{400}[0],
		Burst: 400,
	},
	"DescribeVSwitches": {
		QPM:   &[]int{300}[0],
		Burst: 300,
	},

	// EFLO related APIs - using QPM mode
	"AssignLeniPrivateIpAddress": {
		QPM:   &[]int{300}[0],
		Burst: 300,
	},
	"AttachElasticNetworkInterface": {
		QPM:   &[]int{300}[0],
		Burst: 300,
	},
	"DetachElasticNetworkInterface": {
		QPM:   &[]int{300}[0],
		Burst: 300,
	},
	"DeleteHighDensityElasticNetworkInterface": {
		QPM:   &[]int{300}[0],
		Burst: 300,
	},
	"AttachHighDensityElasticNetworkInterface": {
		QPM:   &[]int{300}[0],
		Burst: 300,
	},
	"DetachHighDensityElasticNetworkInterface": {
		QPM:   &[]int{300}[0],
		Burst: 300,
	},

	// High-frequency APIs - using QPS mode for more fine-grained control
	"ListElasticNetworkInterfaces": {
		QPS:   &[]float64{100.0}[0],
		Burst: 100,
	},
	"CreateElasticNetworkInterface": {
		QPS:   &[]float64{20.0}[0],
		Burst: 20,
	},
	"DeleteElasticNetworkInterface": {
		QPS:   &[]float64{20.0}[0],
		Burst: 20,
	},
	"CreateHighDensityElasticNetworkInterface": {
		QPS:   &[]float64{15.0}[0],
		Burst: 15,
	},
	"ListHighDensityElasticNetworkInterfaces": {
		QPS:   &[]float64{100.0}[0],
		Burst: 100,
	},
	"GetNodeInfoForPod": {
		QPS:   &[]float64{100.0}[0],
		Burst: 100,
	},
}

const (
	longThrottleLatency = 5 * time.Second
)

// FromMap Create rate limit configuration from map (maintain backward compatibility)
func FromMap(in map[string]int) LimitConfig {
	l := make(LimitConfig)
	for k, v := range in {
		l[k] = Limit{
			QPS:   nil,
			QPM:   &v,
			Burst: v,
		}
	}
	return l
}

// NewQPSLimit Create QPS mode rate limit configuration
func NewQPSLimit(qps float64, burst int) Limit {
	return Limit{
		QPS:   &qps,
		QPM:   nil,
		Burst: burst,
	}
}

// NewQPMLimit Create QPM mode rate limit configuration
func NewQPMLimit(qpm int, burst int) Limit {
	return Limit{
		QPS:   nil,
		QPM:   &qpm,
		Burst: burst,
	}
}
