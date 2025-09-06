package client

import (
	"context"
	"fmt"
)

// Example showing how to migrate from the old RateLimiter to AdaptiveRateLimiter

// Old usage pattern:
func exampleOldUsage() {
	cfg := LimitConfig{
		"DescribeNetworkInterfaces": {QPS: 10.0, Burst: 20},
		"CreateNetworkInterface":    {QPS: 5.0, Burst: 10},
	}
	
	rateLimiter := NewRateLimiter(cfg)
	ctx := context.Background()
	
	// Wait for rate limit before API call
	err := rateLimiter.Wait(ctx, "DescribeNetworkInterfaces")
	if err != nil {
		fmt.Printf("Rate limit wait failed: %v\n", err)
		return
	}
	
	// Make API call (example)
	// result, err := apiClient.DescribeNetworkInterfaces(...)
}

// New adaptive usage pattern - Option 1: Direct replacement
func exampleNewUsageDirectReplacement() {
	cfg := LimitConfig{
		"DescribeNetworkInterfaces": {QPS: 10.0, Burst: 20},
		"CreateNetworkInterface":    {QPS: 5.0, Burst: 10},
	}
	
	// Can directly replace NewRateLimiter with NewAdaptiveRateLimiter
	rateLimiter := NewAdaptiveRateLimiter(cfg)
	ctx := context.Background()
	
	// Same interface as before
	err := rateLimiter.Wait(ctx, "DescribeNetworkInterfaces")
	if err != nil {
		fmt.Printf("Adaptive rate limit wait failed: %v\n", err)
		return
	}
	
	// Make API call (example)
	// result, err := apiClient.DescribeNetworkInterfaces(...)
	
	// NEW: Handle the response to enable adaptive behavior
	// rateLimiter.HandleResponse("DescribeNetworkInterfaces", err)
}

// New adaptive usage pattern - Option 2: Using the wrapper for automatic handling
func exampleNewUsageWithWrapper() {
	cfg := LimitConfig{
		"DescribeNetworkInterfaces": {QPS: 10.0, Burst: 20},
		"CreateNetworkInterface":    {QPS: 5.0, Burst: 10},
	}
	
	wrapper := NewAdaptiveRateLimiterWrapper(cfg)
	ctx := context.Background()
	
	// Use WaitAndHandle for automatic response handling
	err := wrapper.WaitAndHandle(ctx, "DescribeNetworkInterfaces", func() error {
		// Make your API call here
		// return apiClient.DescribeNetworkInterfaces(...)
		return nil // example
	})
	
	if err != nil {
		fmt.Printf("API call failed: %v\n", err)
		return
	}
}

// Migration strategy for existing services:
// 
// 1. For immediate compatibility: Replace NewRateLimiter with NewAdaptiveRateLimiter
//    - No code changes needed in API call sites
//    - Adaptive behavior won't be enabled until HandleResponse is called
//
// 2. For full adaptive behavior: Add HandleResponse calls after API calls
//    - Or use the WaitAndHandle wrapper method
//
// 3. For gradual migration: Use feature flags to switch between implementations
func exampleGradualMigration(useAdaptive bool) RateLimiterInterface {
	cfg := LimitConfig{
		"DescribeNetworkInterfaces": {QPS: 10.0, Burst: 20},
	}
	
	if useAdaptive {
		return NewAdaptiveRateLimiter(cfg)
	}
	return NewRateLimiter(cfg)
}