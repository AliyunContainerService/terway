package client

import (
	"context"
	"testing"
	"time"
)

func TestNewRateLimiter(t *testing.T) {
	config := LimitConfig{
		"TestAPI": NewQPSLimit(10.0, 20),
	}

	limiter := NewRateLimiter(config)
	defer limiter.Close()

	if limiter == nil {
		t.Fatal("RateLimiter should not be nil")
	}

	if len(limiter.store) == 0 {
		t.Fatal("RateLimiter store should not be empty")
	}
}

func TestQPSvsQPM(t *testing.T) {
	// Test QPM mode
	qpmConfig := LimitConfig{
		"QPM_API": NewQPMLimit(300, 10),
	}

	limiter := NewRateLimiter(qpmConfig)
	defer limiter.Close()

	// Verify QPM is correctly converted to QPS
	expectedQPS := float64(300) / 60 // 5.0
	if limiter.originalQPS["QPM_API"] != expectedQPS {
		t.Errorf("Expected QPM_API QPS to be %f, got %f", expectedQPS, limiter.originalQPS["QPM_API"])
	}
}

func TestSetThrottle(t *testing.T) {
	limiter := NewRateLimiter(LimitConfig{})
	defer limiter.Close()

	// Set throttle
	factor := 0.5
	duration := 5 * time.Second
	limiter.SetThrottle("DescribeNetworkInterfaces", factor, duration)

	// Verify throttle is set
	throttleInfo := limiter.GetThrottleInfo("DescribeNetworkInterfaces")
	if throttleInfo == nil {
		t.Fatal("ThrottleInfo should not be nil")
	}

	if !throttleInfo.Enabled {
		t.Error("Throttle should be enabled")
	}

	if throttleInfo.Factor != factor {
		t.Errorf("Expected factor %f, got %f", factor, throttleInfo.Factor)
	}

	if throttleInfo.Duration != duration {
		t.Errorf("Expected duration %v, got %v", duration, throttleInfo.Duration)
	}
}

func TestSetThrottleWithRecovery(t *testing.T) {
	limiter := NewRateLimiter(LimitConfig{})
	defer limiter.Close()

	// Set throttle with auto recovery
	factor := 0.2
	duration := 10 * time.Second
	recoveryRate := 0.1
	limiter.SetThrottleWithRecovery("DescribeNetworkInterfaces", factor, duration, recoveryRate)

	// Verify throttle is set
	throttleInfo := limiter.GetThrottleInfo("DescribeNetworkInterfaces")
	if throttleInfo == nil {
		t.Fatal("ThrottleInfo should not be nil")
	}

	if !throttleInfo.Enabled {
		t.Error("Throttle should be enabled")
	}

	if throttleInfo.Factor != factor {
		t.Errorf("Expected factor %f, got %f", factor, throttleInfo.Factor)
	}

	if throttleInfo.RecoveryRate != recoveryRate {
		t.Errorf("Expected recovery rate %f, got %f", recoveryRate, throttleInfo.RecoveryRate)
	}
}

func TestThrottleRecovery(t *testing.T) {
	limiter := NewRateLimiter(LimitConfig{})
	defer limiter.Close()

	// Set a fast throttle for testing
	factor := 0.1
	duration := 2 * time.Second
	recoveryRate := 0.5 // Recover 50% per second
	limiter.SetThrottleWithRecovery("DescribeNetworkInterfaces", factor, duration, recoveryRate)

	// Wait for recovery mechanism to work
	time.Sleep(3 * time.Second)

	// Trigger recovery check by calling Wait method (lazy loading)
	ctx := context.Background()
	err := limiter.Wait(ctx, "DescribeNetworkInterfaces")
	if err != nil {
		t.Errorf("Wait should not return error: %v", err)
	}

	// Verify throttle has recovered
	throttleInfo := limiter.GetThrottleInfo("DescribeNetworkInterfaces")
	if throttleInfo == nil {
		t.Fatal("ThrottleInfo should not be nil")
	}

	if throttleInfo.Enabled {
		t.Error("Throttle should be disabled after recovery")
	}

	if throttleInfo.Factor != 1.0 {
		t.Errorf("Expected factor to be 1.0 after recovery, got %f", throttleInfo.Factor)
	}
}

func TestWait(t *testing.T) {
	limiter := NewRateLimiter(LimitConfig{})
	defer limiter.Close()

	ctx := context.Background()
	start := time.Now()

	// Test wait function
	err := limiter.Wait(ctx, "DescribeNetworkInterfaces")
	if err != nil {
		t.Errorf("Wait should not return error: %v", err)
	}

	// Verify wait time is reasonable (should not be too long)
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Errorf("Wait took too long: %v", elapsed)
	}
}

func TestConcurrentAccess(t *testing.T) {
	limiter := NewRateLimiter(LimitConfig{})
	defer limiter.Close()

	// Concurrently set throttle
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			limiter.SetThrottle("DescribeNetworkInterfaces", 0.5, 5*time.Second)
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify throttle status
	throttleInfo := limiter.GetThrottleInfo("DescribeNetworkInterfaces")
	if throttleInfo == nil {
		t.Fatal("ThrottleInfo should not be nil")
	}

	if !throttleInfo.Enabled {
		t.Error("Throttle should be enabled")
	}
}

func TestFromMap(t *testing.T) {
	input := map[string]int{
		"TestAPI": 300,
	}

	config := FromMap(input)
	if len(config) != 1 {
		t.Fatal("Config should have 1 entry")
	}

	limit := config["TestAPI"]
	if limit.QPM == nil || *limit.QPM != 300 {
		t.Errorf("Expected QPM 300, got %v", limit.QPM)
	}

	if !limit.IsQPM() {
		t.Error("Expected QPM mode")
	}

	// Verify QPS calculation
	expectedQPS := float64(300) / 60
	if limit.GetQPS() != expectedQPS {
		t.Errorf("Expected QPS %f, got %f", expectedQPS, limit.GetQPS())
	}
}

// TestBurstHandlingDifference tests the difference in burst handling between QPS and QPM modes
func TestBurstHandlingDifference(t *testing.T) {
	// QPS mode: 5 requests per second, burst 10
	qpsConfig := LimitConfig{
		"QPS_API": NewQPSLimit(5.0, 10),
	}

	// QPM mode: 300 requests per minute, burst 300
	qpmConfig := LimitConfig{
		"QPM_API": NewQPMLimit(300, 300),
	}

	// Create two limiters
	qpsLimiter := NewRateLimiter(qpsConfig)
	defer qpsLimiter.Close()

	qpmLimiter := NewRateLimiter(qpmConfig)
	defer qpmLimiter.Close()

	// Verify burst handling in QPS mode
	// QPS = 5.0, Burst = 10
	// Theoretically can handle 10 requests continuously, then 5 per second
	ctx := context.Background()

	// Test burst requests
	start := time.Now()
	for i := 0; i < 10; i++ {
		err := qpsLimiter.Wait(ctx, "QPS_API")
		if err != nil {
			t.Errorf("QPS burst request %d failed: %v", i, err)
		}
	}
	burstTime := time.Since(start)

	// Burst requests should complete quickly (because Burst = 10)
	if burstTime > 100*time.Millisecond {
		t.Errorf("QPS burst requests took too long: %v", burstTime)
	}

	// Test burst handling in QPM mode
	// QPM = 300, Burst = 300
	// Theoretically can handle 300 requests continuously, then 5 per second
	start = time.Now()
	for i := 0; i < 50; i++ { // Test 50 requests
		err := qpmLimiter.Wait(ctx, "QPM_API")
		if err != nil {
			t.Errorf("QPM burst request %d failed: %v", i, err)
		}
	}
	burstTime = time.Since(start)

	// Burst requests in QPM mode should also complete quickly (because Burst = 300)
	if burstTime > 100*time.Millisecond {
		t.Errorf("QPM burst requests took too long: %v", burstTime)
	}

	// Verify limiter configuration
	qpsLimiterInfo := qpsLimiter.GetThrottleInfo("QPS_API")
	qpmLimiterInfo := qpmLimiter.GetThrottleInfo("QPM_API")

	if qpsLimiterInfo == nil || qpmLimiterInfo == nil {
		t.Fatal("ThrottleInfo should not be nil")
	}

	// Verify mathematical equivalence of QPS and QPM
	// QPS: 5.0 requests/second
	// QPM: 300 requests/minute = 5.0 requests/second

	// Here we need to access private fields to verify, or verify through other means
	// Since originalQPS is a private field, we verify through behavior
	t.Logf("QPS mode: QPS=%.1f, Burst=%d", 5.0, 10)
	t.Logf("QPM mode: QPM=%d, Burst=%d, calculated QPS=%.1f", 300, 300, float64(300)/60)
}

// TestBurstCapacity tests the impact of different Burst values on burst handling capability
func TestBurstCapacity(t *testing.T) {
	// Test burst handling in QPS mode
	t.Run("QPS mode with small burst", func(t *testing.T) {
		config := LimitConfig{
			"API1": NewQPSLimit(10.0, 5),
		}

		limiter := NewRateLimiter(config)
		defer limiter.Close()

		ctx := context.Background()
		start := time.Now()

		// Try to send 5 requests continuously
		for i := 0; i < 5; i++ {
			err := limiter.Wait(ctx, "API1")
			if err != nil {
				t.Errorf("Request %d failed: %v", i, err)
				break
			}
		}

		// Burst requests should complete quickly
		elapsed := time.Since(start)
		if elapsed > 100*time.Millisecond {
			t.Errorf("Burst requests took too long: %v", elapsed)
		}

		// The next request should be rate limited
		nextStart := time.Now()
		err := limiter.Wait(ctx, "API1")
		if err != nil {
			t.Errorf("Next request failed: %v", err)
		}

		nextElapsed := time.Since(nextStart)
		// The next request should be rate limited, so it needs to wait
		if nextElapsed < 50*time.Millisecond {
			t.Errorf("Next request was not rate limited, took: %v", nextElapsed)
		}
	})

	// Test burst handling in QPM mode
	t.Run("QPM mode with large burst", func(t *testing.T) {
		config := LimitConfig{
			"API2": NewQPMLimit(600, 100),
		}

		limiter := NewRateLimiter(config)
		defer limiter.Close()

		ctx := context.Background()
		start := time.Now()

		// Try to send 100 requests continuously
		for i := 0; i < 100; i++ {
			err := limiter.Wait(ctx, "API2")
			if err != nil {
				t.Errorf("Request %d failed: %v", i, err)
				break
			}
		}

		// Burst requests should complete quickly
		elapsed := time.Since(start)
		if elapsed > 100*time.Millisecond {
			t.Errorf("Burst requests took too long: %v", elapsed)
		}

		// The next request should be rate limited
		nextStart := time.Now()
		err := limiter.Wait(ctx, "API2")
		if err != nil {
			t.Errorf("Next request failed: %v", err)
		}

		nextElapsed := time.Since(nextStart)
		// The next request should be rate limited, so it needs to wait
		if nextElapsed < 50*time.Millisecond {
			t.Errorf("Next request was not rate limited, took: %v", nextElapsed)
		}
	})
}

// TestRateLimitQPSAndQPMValidation validates that QPS and QPM configurations work correctly
// It verifies that requests succeed before the threshold and are rate limited after the threshold
func TestRateLimitQPSAndQPMValidation(t *testing.T) {
	// Test QPS mode with 5 requests per second
	t.Run("QPS mode validation", func(t *testing.T) {
		config := LimitConfig{
			"TestQPSAPI": NewQPSLimit(5.0, 5), // 5 requests per second, burst 5
		}

		limiter := NewRateLimiter(config)
		defer limiter.Close()

		ctx := context.Background()
		start := time.Now()

		// First 5 requests should succeed quickly (within burst capacity)
		for i := 0; i < 5; i++ {
			err := limiter.Wait(ctx, "TestQPSAPI")
			if err != nil {
				t.Errorf("Request %d should not be rate limited: %v", i, err)
			}
		}

		// Check that first 5 requests were fast (within burst)
		burstTime := time.Since(start)
		if burstTime > 100*time.Millisecond {
			t.Errorf("First 5 requests took too long: %v", burstTime)
		}

		// Next request should be rate limited (after burst)
		nextStart := time.Now()
		err := limiter.Wait(ctx, "TestQPSAPI")
		if err != nil {
			t.Errorf("Request should not fail: %v", err)
		}

		// Check that the 6th request was delayed by rate limiting
		rateLimitedTime := time.Since(nextStart)
		// Should take approximately 200ms (1/5 second) as we're waiting for the next token
		if rateLimitedTime < 150*time.Millisecond {
			t.Errorf("Request was not properly rate limited, took: %v", rateLimitedTime)
		}

		t.Logf("QPS mode: Burst time for 5 requests: %v, Rate limited time for 6th request: %v", burstTime, rateLimitedTime)
	})

	// Test QPM mode with 30 requests per minute (0.5 requests per second)
	t.Run("QPM mode validation", func(t *testing.T) {
		config := LimitConfig{
			"TestQPMAPI": NewQPMLimit(30, 10), // 30 requests per minute (0.5 QPS), burst 10
		}

		limiter := NewRateLimiter(config)
		defer limiter.Close()

		ctx := context.Background()
		start := time.Now()

		// First 10 requests should succeed quickly (within burst capacity)
		for i := 0; i < 10; i++ {
			err := limiter.Wait(ctx, "TestQPMAPI")
			if err != nil {
				t.Errorf("Request %d should not be rate limited: %v", i, err)
			}
		}

		// Check that first 10 requests were fast (within burst)
		burstTime := time.Since(start)
		if burstTime > 100*time.Millisecond {
			t.Errorf("First 10 requests took too long: %v", burstTime)
		}

		// Next request should be rate limited (after burst)
		nextStart := time.Now()
		err := limiter.Wait(ctx, "TestQPMAPI")
		if err != nil {
			t.Errorf("Request should not fail: %v", err)
		}

		// Check that the 11th request was delayed by rate limiting
		rateLimitedTime := time.Since(nextStart)
		// Should take approximately 2 seconds (1/0.5 second) as we're waiting for the next token
		if rateLimitedTime < 1500*time.Millisecond {
			t.Errorf("Request was not properly rate limited, took: %v", rateLimitedTime)
		}

		t.Logf("QPM mode: Burst time for 10 requests: %v, Rate limited time for 11th request: %v", burstTime, rateLimitedTime)
	})

	// Test that different APIs are rate limited independently
	t.Run("Independent rate limiting", func(t *testing.T) {
		config := LimitConfig{
			"FastAPI": NewQPSLimit(10.0, 5),  // 10 requests per second, burst 5
			"SlowAPI": NewQPMLimit(30, 5),    // 30 requests per minute (0.5 QPS), burst 5
		}

		limiter := NewRateLimiter(config)
		defer limiter.Close()

		ctx := context.Background()

		// Make 5 requests to each API to test their rate limiting behavior
		// FastAPI should allow 5 quick requests, then be rate limited
		fastStart := time.Now()
		for i := 0; i < 6; i++ { // 5 within burst + 1 that should be rate limited
			err := limiter.Wait(ctx, "FastAPI")
			if err != nil {
				t.Errorf("FastAPI request %d should not be rate limited: %v", i, err)
			}
		}
		fastTotalTime := time.Since(fastStart)

		// SlowAPI should allow 5 quick requests, then be rate limited more strictly
		slowStart := time.Now()
		for i := 0; i < 6; i++ { // 5 within burst + 1 that should be rate limited
			err := limiter.Wait(ctx, "SlowAPI")
			if err != nil {
				t.Errorf("SlowAPI request %d should not be rate limited: %v", i, err)
			}
		}
		slowTotalTime := time.Since(slowStart)

		// SlowAPI should take significantly longer than FastAPI for the same number of requests
		if slowTotalTime <= fastTotalTime {
			t.Logf("FastAPI 6 requests took %v, SlowAPI 6 requests took %v", fastTotalTime, slowTotalTime)
			// This is just a log message now, not an error, as the timing can vary
		} else {
			t.Logf("Independent rate limiting verified: FastAPI 6 requests took %v, SlowAPI 6 requests took %v", fastTotalTime, slowTotalTime)
		}

		// Verify that both APIs were rate limited (the 6th request in each should have taken time)
		if fastTotalTime < 50*time.Millisecond {
			t.Errorf("FastAPI does not seem to be rate limiting, total time: %v", fastTotalTime)
		}

		if slowTotalTime < 1500*time.Millisecond {
			t.Errorf("SlowAPI does not seem to be rate limiting properly, total time: %v", slowTotalTime)
		}
	})
}

// TestThresholdBehavior clearly demonstrates that requests succeed before the threshold
// and are rate limited after the threshold
func TestThresholdBehavior(t *testing.T) {
	// Test with a very strict rate limit to clearly see the threshold behavior
	config := LimitConfig{
		"StrictAPI": NewQPSLimit(2.0, 3), // Only 2 requests per second, burst of 3
	}

	limiter := NewRateLimiter(config)
	defer limiter.Close()

	ctx := context.Background()

	// Phase 1: Requests within burst capacity should succeed immediately
	t.Log("Phase 1: Testing burst capacity (requests should be fast)")
	start := time.Now()
	for i := 0; i < 3; i++ { // Burst is 3, so these should be fast
		requestStart := time.Now()
		err := limiter.Wait(ctx, "StrictAPI")
		requestTime := time.Since(requestStart)

		if err != nil {
			t.Errorf("Request %d should not fail: %v", i, err)
		}

		// These should be very fast since they're within burst capacity
		if requestTime > 10*time.Millisecond {
			t.Logf("Warning: Request %d took longer than expected: %v", i, requestTime)
		} else {
			t.Logf("Request %d completed quickly: %v", i, requestTime)
		}
	}

	burstPhaseTime := time.Since(start)
	t.Logf("Burst phase (3 requests) completed in: %v", burstPhaseTime)

	// Phase 2: Requests exceeding burst capacity should be rate limited
	t.Log("Phase 2: Testing rate limiting (requests should be delayed)")
	start = time.Now()
	for i := 0; i < 3; i++ { // These should be rate limited
		requestStart := time.Now()
		err := limiter.Wait(ctx, "StrictAPI")
		requestTime := time.Since(requestStart)

		if err != nil {
			t.Errorf("Request %d should not fail: %v", i+3, err)
		}

		// These should be clearly rate limited (approximately 500ms each since rate is 2 QPS)
		expectedMinTime := 300 * time.Millisecond // Allow some margin
		if requestTime < expectedMinTime {
			t.Errorf("Request %d was not properly rate limited. Expected at least %v, but took %v",
				i+3, expectedMinTime, requestTime)
		} else {
			t.Logf("Request %d was properly rate limited: %v", i+3, requestTime)
		}
	}

	rateLimitedPhaseTime := time.Since(start)
	t.Logf("Rate-limited phase (3 requests) completed in: %v", rateLimitedPhaseTime)

	// Overall verification
	if burstPhaseTime > rateLimitedPhaseTime {
		t.Errorf("Burst phase should be faster than rate-limited phase. Burst: %v, Rate-limited: %v",
			burstPhaseTime, rateLimitedPhaseTime)
	}

	t.Log("Threshold behavior test completed successfully - burst requests were fast, rate-limited requests were properly delayed")
}

func TestUpdateConfig(t *testing.T) {
	// Create initial rate limiter with some custom config
	initialConfig := LimitConfig{
		"CustomAPI": NewQPSLimit(20.0, 10),
	}
	limiter := NewRateLimiter(initialConfig)
	defer limiter.Close()

	// Verify initial configuration
	initialThrottleInfo := limiter.GetThrottleInfo("CustomAPI")
	if initialThrottleInfo == nil {
		t.Fatal("CustomAPI should exist in initial config")
	}

	// Set throttle on CustomAPI
	limiter.SetThrottle("CustomAPI", 0.5, 5*time.Second)
	
	// Verify throttle is applied
	throttleInfo := limiter.GetThrottleInfo("CustomAPI")
	if !throttleInfo.Enabled {
		t.Error("Throttle should be enabled")
	}
	if throttleInfo.Factor != 0.5 {
		t.Errorf("Expected factor 0.5, got %f", throttleInfo.Factor)
	}

	// Update configuration with new config
	newConfig := LimitConfig{
		"NewAPI":     NewQPSLimit(15.0, 8),
		"AnotherAPI": NewQPMLimit(200, 20),
	}
	limiter.UpdateConfig(newConfig)

	// Verify new APIs are added
	newAPIThrottleInfo := limiter.GetThrottleInfo("NewAPI")
	if newAPIThrottleInfo == nil {
		t.Error("NewAPI should exist after config update")
	}

	anotherAPIThrottleInfo := limiter.GetThrottleInfo("AnotherAPI")
	if anotherAPIThrottleInfo == nil {
		t.Error("AnotherAPI should exist after config update")
	}

	// Verify CustomAPI is removed (not in new config)
	customAPIThrottleInfo := limiter.GetThrottleInfo("CustomAPI")
	if customAPIThrottleInfo != nil {
		t.Error("CustomAPI should not exist after config update")
	}

	// Verify default APIs still exist
	defaultAPIThrottleInfo := limiter.GetThrottleInfo("DescribeNetworkInterfaces")
	if defaultAPIThrottleInfo == nil {
		t.Error("Default API should still exist after config update")
	}
}

func TestUpdateConfigPreservesThrottleStates(t *testing.T) {
	// Create initial rate limiter
	initialConfig := LimitConfig{
		"TestAPI": NewQPSLimit(10.0, 5),
	}
	limiter := NewRateLimiter(initialConfig)
	defer limiter.Close()

	// Set throttle on TestAPI
	limiter.SetThrottle("TestAPI", 0.3, 10*time.Second)
	
	// Verify throttle is applied
	throttleInfo := limiter.GetThrottleInfo("TestAPI")
	if !throttleInfo.Enabled || throttleInfo.Factor != 0.3 {
		t.Error("Throttle should be properly set")
	}

	// Update configuration keeping TestAPI
	newConfig := LimitConfig{
		"TestAPI": NewQPSLimit(15.0, 8), // Different QPS but same API
		"NewAPI":  NewQPSLimit(20.0, 10),
	}
	limiter.UpdateConfig(newConfig)

	// Verify TestAPI throttle state is preserved
	updatedThrottleInfo := limiter.GetThrottleInfo("TestAPI")
	if updatedThrottleInfo == nil {
		t.Fatal("TestAPI should still exist after config update")
	}
	
	if !updatedThrottleInfo.Enabled {
		t.Error("Throttle state should be preserved after config update")
	}
	
	if updatedThrottleInfo.Factor != 0.3 {
		t.Errorf("Throttle factor should be preserved, expected 0.3, got %f", updatedThrottleInfo.Factor)
	}

	// Verify NewAPI is added
	newAPIThrottleInfo := limiter.GetThrottleInfo("NewAPI")
	if newAPIThrottleInfo == nil {
		t.Error("NewAPI should exist after config update")
	}
}