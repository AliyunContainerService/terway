package client

import (
	"context"
	"math"
	"sync"
	"time"

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

// adaptiveTokenBucket implements a token bucket for adaptive rate limiting
type adaptiveTokenBucket struct {
	capacity float64
	tokens   float64
	mu       sync.Mutex
}

func newAdaptiveTokenBucket(capacity float64) *adaptiveTokenBucket {
	return &adaptiveTokenBucket{
		capacity: capacity,
		tokens:   capacity,
	}
}

func (tb *adaptiveTokenBucket) Retrieve(amount float64) (float64, bool) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	if tb.tokens >= amount {
		tb.tokens -= amount
		return amount, true
	}

	available := tb.tokens
	tb.tokens = 0
	return available, false
}

func (tb *adaptiveTokenBucket) Refund(amount float64) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.tokens = math.Min(tb.capacity, tb.tokens+amount)
}

func (tb *adaptiveTokenBucket) Resize(newCapacity float64) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.capacity = newCapacity
	if tb.tokens > newCapacity {
		tb.tokens = newCapacity
	}
}

// adaptiveRateLimit implements AWS-style adaptive rate limiting
type adaptiveRateLimit struct {
	tokenBucketEnabled bool

	smooth        float64
	beta          float64
	scaleConstant float64
	minFillRate   float64

	fillRate         float64
	calculatedRate   float64
	lastRefilled     time.Time
	measuredTxRate   float64
	lastTxRateBucket float64
	requestCount     int64
	lastMaxRate      float64
	lastThrottleTime time.Time
	timeWindow       float64

	tokenBucket *adaptiveTokenBucket

	mu sync.Mutex
}

func newAdaptiveRateLimit() *adaptiveRateLimit {
	now := time.Now()
	return &adaptiveRateLimit{
		smooth:        0.8,
		beta:          0.7,
		scaleConstant: 0.4,

		minFillRate: 0.5,

		lastTxRateBucket: math.Floor(timeFloat64Seconds(now)),
		lastThrottleTime: now,

		tokenBucket: newAdaptiveTokenBucket(0),
	}
}

func (a *adaptiveRateLimit) Enable(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.tokenBucketEnabled = v
}

func (a *adaptiveRateLimit) AcquireToken(amount uint) (tokenAcquired bool, waitTryAgain time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.tokenBucketEnabled {
		return true, 0
	}

	a.tokenBucketRefill()

	available, ok := a.tokenBucket.Retrieve(float64(amount))
	if !ok {
		waitDur := float64Seconds((float64(amount) - available) / a.fillRate)
		return false, waitDur
	}

	return true, 0
}

func (a *adaptiveRateLimit) Update(throttled bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.updateMeasuredRate()

	if throttled {
		rateToUse := a.measuredTxRate
		if a.tokenBucketEnabled {
			rateToUse = math.Min(a.measuredTxRate, a.fillRate)
		}

		a.lastMaxRate = rateToUse
		a.calculateTimeWindow()
		a.lastThrottleTime = time.Now()
		a.calculatedRate = a.cubicThrottle(rateToUse)
		a.tokenBucketEnabled = true
	} else {
		a.calculateTimeWindow()
		a.calculatedRate = a.cubicSuccess(time.Now())
	}

	newRate := math.Min(a.calculatedRate, 2*a.measuredTxRate)
	a.tokenBucketUpdateRate(newRate)
}

func (a *adaptiveRateLimit) cubicSuccess(t time.Time) float64 {
	dt := secondsFloat64(t.Sub(a.lastThrottleTime))
	return (a.scaleConstant * math.Pow(dt-a.timeWindow, 3)) + a.lastMaxRate
}

func (a *adaptiveRateLimit) cubicThrottle(rateToUse float64) float64 {
	return rateToUse * a.beta
}

func (a *adaptiveRateLimit) calculateTimeWindow() {
	a.timeWindow = math.Pow((a.lastMaxRate*(1.-a.beta))/a.scaleConstant, 1./3.)
}

func (a *adaptiveRateLimit) tokenBucketUpdateRate(newRPS float64) {
	a.tokenBucketRefill()
	a.fillRate = math.Max(newRPS, a.minFillRate)
	a.tokenBucket.Resize(newRPS)
}

func (a *adaptiveRateLimit) updateMeasuredRate() {
	now := time.Now()
	timeBucket := math.Floor(timeFloat64Seconds(now)*2.) / 2.
	a.requestCount++

	if timeBucket > a.lastTxRateBucket {
		currentRate := float64(a.requestCount) / (timeBucket - a.lastTxRateBucket)

		a.measuredTxRate = (currentRate * a.smooth) + (a.measuredTxRate * (1. - a.smooth))

		a.requestCount = 0
		a.lastTxRateBucket = timeBucket
	}
}

func (a *adaptiveRateLimit) tokenBucketRefill() {
	now := time.Now()
	if a.lastRefilled.IsZero() {
		a.lastRefilled = now
		return
	}

	fillAmount := secondsFloat64(now.Sub(a.lastRefilled)) * a.fillRate
	a.tokenBucket.Refund(fillAmount)
	a.lastRefilled = now
}

func float64Seconds(v float64) time.Duration {
	return time.Duration(v * float64(time.Second))
}

func secondsFloat64(v time.Duration) float64 {
	return float64(v) / float64(time.Second)
}

func timeFloat64Seconds(v time.Time) float64 {
	return float64(v.UnixNano()) / float64(time.Second)
}

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
	store map[string]*adaptiveRateLimit
}

func NewRateLimiter(cfg LimitConfig) *RateLimiter {
	r := &RateLimiter{
		store: make(map[string]*adaptiveRateLimit),
	}
	
	// Initialize with default limits
	for k, v := range defaultLimit {
		adaptiveLimit := newAdaptiveRateLimit()
		// Set initial rate based on default limit
		adaptiveLimit.tokenBucketUpdateRate(float64(v) / 60)
		r.store[k] = adaptiveLimit
	}
	
	// Override with custom config
	for k, v := range cfg {
		adaptiveLimit := newAdaptiveRateLimit()
		adaptiveLimit.tokenBucketUpdateRate(v.QPS)
		r.store[k] = adaptiveLimit
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
	if !ok {
		v = r.store[""]
	}
	
	// Try to acquire token
	for {
		acquired, waitTime := v.AcquireToken(1)
		if acquired {
			break
		}
		
		// Wait for token to be available
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitTime):
			// Continue trying
		}
	}
	
	return nil
}

// UpdateThrottleStatus should be called after each API call to update the adaptive rate limiter
// based on whether the call was throttled or not. This is crucial for the adaptive behavior to work.
// 
// Example usage:
//   err := r.Wait(ctx, "DescribeInstances")
//   if err != nil {
//       // handle error
//   }
//   // After making the API call, update the status based on the response
//   wasThrottled := isThrottleError(apiResponse)
//   r.UpdateThrottleStatus("DescribeInstances", wasThrottled)
func (r *RateLimiter) UpdateThrottleStatus(name string, wasThrottled bool) {
	v, ok := r.store[name]
	if !ok {
		v = r.store[""]
	}
	
	v.Update(wasThrottled)
}
