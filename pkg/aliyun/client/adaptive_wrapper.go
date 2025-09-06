package client

import (
	"context"
)

// AdaptiveRateLimiterWrapper wraps the adaptive rate limiter with response handling
type AdaptiveRateLimiterWrapper struct {
	limiter *AdaptiveRateLimiter
}

// NewAdaptiveRateLimiterWrapper creates a new adaptive rate limiter wrapper
func NewAdaptiveRateLimiterWrapper(cfg LimitConfig) *AdaptiveRateLimiterWrapper {
	return &AdaptiveRateLimiterWrapper{
		limiter: NewAdaptiveRateLimiter(cfg),
	}
}

// Wait implements the same interface as the original RateLimiter
func (w *AdaptiveRateLimiterWrapper) Wait(ctx context.Context, name string) error {
	return w.limiter.Wait(ctx, name)
}

// HandleResponse updates the rate limiter based on API response
func (w *AdaptiveRateLimiterWrapper) HandleResponse(apiName string, err error) {
	w.limiter.HandleResponse(apiName, err)
}

// WaitAndHandle combines waiting and response handling for convenience
func (w *AdaptiveRateLimiterWrapper) WaitAndHandle(ctx context.Context, apiName string, apiCall func() error) error {
	// Wait for rate limit
	if err := w.Wait(ctx, apiName); err != nil {
		return err
	}

	// Execute the API call
	err := apiCall()

	// Handle the response to update adaptive rate limiting
	w.HandleResponse(apiName, err)

	return err
}