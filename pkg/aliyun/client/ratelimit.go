package client

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
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

// AliyunThrottleError represents a throttle error from Aliyun API
type AliyunThrottleError struct {
	Code    string
	Message string
}

func (e *AliyunThrottleError) Error() string {
	return fmt.Sprintf("throttle error: %s - %s", e.Code, e.Message)
}

// IsThrottleError checks if an error is a throttle error
func IsThrottleError(err error) bool {
	if err == nil {
		return false
	}
	
	// Check for common Aliyun throttle error codes
	throttleCodes := []string{
		"Throttling",
		"Throttling.User",
		"Throttling.Api",
		"Throttling.RateLimit",
		"Throttling.Quota",
		"ServiceUnavailable",
		"RequestLimitExceeded",
		"RequestThrottled",
		"TooManyRequests",
	}
	
	errStr := err.Error()
	for _, code := range throttleCodes {
		if contains(errStr, code) {
			return true
		}
	}
	
	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || 
		(len(s) > len(substr) && (s[:len(substr)] == substr || 
		s[len(s)-len(substr):] == substr || 
		containsSubstring(s, substr))))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
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
	store map[string]*awsAdaptiveRetryer
}

// awsAdaptiveRetryer wraps AWS AdaptiveMode for per-API rate limiting
type awsAdaptiveRetryer struct {
	retryer aws.RetryerV2
}

func NewRateLimiter(cfg LimitConfig) *RateLimiter {
	r := &RateLimiter{
		store: make(map[string]*awsAdaptiveRetryer),
	}
	
	// Initialize with default limits
	for k := range defaultLimit {
		adaptiveRetryer := &awsAdaptiveRetryer{
			retryer: retry.NewAdaptiveMode(),
		}
		r.store[k] = adaptiveRetryer
	}
	
	// Override with custom config
	for k := range cfg {
		adaptiveRetryer := &awsAdaptiveRetryer{
			retryer: retry.NewAdaptiveMode(),
		}
		r.store[k] = adaptiveRetryer
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
	
	// Use AWS AdaptiveMode to get attempt token
	releaseToken, err := v.retryer.GetAttemptToken(ctx)
	if err != nil {
		return fmt.Errorf("failed to get attempt token: %w", err)
	}
	
	// Release the token immediately since we're just using it for rate limiting
	// The actual API call will be made by the caller
	releaseToken(nil)
	
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
//   wasThrottled := IsThrottleError(apiResponse)
//   r.UpdateThrottleStatus("DescribeInstances", wasThrottled)
func (r *RateLimiter) UpdateThrottleStatus(name string, wasThrottled bool) {
	v, ok := r.store[name]
	if !ok {
		v = r.store[""]
	}
	
	// Create a throttle error if needed for AWS retryer
	var err error
	if wasThrottled {
		err = &AliyunThrottleError{
			Code:    "Throttling",
			Message: "Rate limit exceeded",
		}
	}
	
	// Use AWS retryer's GetRetryToken to update the adaptive rate limiter
	ctx := context.Background()
	releaseToken, retryErr := v.retryer.GetRetryToken(ctx, err)
	if retryErr != nil {
		// Log error but don't fail the operation
		l := logf.FromContext(ctx)
		l.Error(retryErr, "failed to update throttle status", "api", name)
		return
	}
	
	// Release the token to complete the update
	releaseToken(err)
}
