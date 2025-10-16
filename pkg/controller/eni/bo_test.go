package eni

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/pkg/backoff"
	"k8s.io/apimachinery/pkg/util/wait"
)

func TestBackoffManager(t *testing.T) {
	manager := &BackoffManager{}
	key := "test-key"

	// Create a simple backoff strategy without initial delay
	bo := backoff.ExtendedBackoff{
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Steps:    3,
			Duration: 10 * time.Millisecond,
			Factor:   2.0,
			Jitter:   0.1,
		},
	}

	// The first call should create a new ResourceBackoff and return a duration
	d1, err := manager.Get(key, bo)
	if err != nil {
		t.Fatalf("First Get failed: %v", err)
	}
	if d1 <= 0 {
		t.Errorf("Expected positive duration, got %v", d1)
	}

	// Verify that NextTS has been set
	nextTS, ok := manager.GetNextTS(key)
	if !ok {
		t.Fatalf("Failed to get NextTS")
	}
	expectedTS := time.Now().Add(d1)
	if nextTS.Sub(expectedTS).Abs() > time.Second {
		t.Errorf("NextTS not set correctly. Got %v, expected around %v", nextTS, expectedTS)
	}

	// Wait for a while to ensure the next call does not skip the backoff due to being too fast
	time.Sleep(d1 + 5*time.Millisecond)

	// The second call should use the same ResourceBackoff and perform the next step of backoff
	d2, err := manager.Get(key, bo)
	if err != nil {
		t.Fatalf("Second Get failed: %v", err)
	}
	if d2 <= 0 {
		t.Errorf("Expected positive duration, got %v", d2)
	}

	// d2 should be approximately twice d1 (considering jitter)
	if float64(d2) < float64(d1)*1.5 || float64(d2) > float64(d1)*2.5 {
		t.Errorf("Expected d2 to be about twice d1. Got d1=%v, d2=%v", d1, d2)
	}

	// Test deletion functionality
	manager.Del(key)
	_, ok = manager.GetNextTS(key)
	if ok {
		t.Error("Key should have been deleted")
	}

	// Test the case where steps are exhausted
	boExhausted := backoff.ExtendedBackoff{
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Steps:    1,
			Duration: 10 * time.Millisecond,
		},
	}

	// The first call should succeed
	_, err = manager.Get("exhausted", boExhausted)
	if err != nil {
		t.Fatalf("Get with exhaustible backoff failed: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	// The second call should return a timeout error
	_, err = manager.Get("exhausted", boExhausted)
	if !errors.Is(err, errTimeOut) {
		t.Errorf("Expected timeout error, got: %v", err)
	}
}

func TestResourceBackoffConcurrency(t *testing.T) {
	manager := &BackoffManager{}
	key := "concurrent-key"

	// Create a simple backoff strategy
	bo := backoff.ExtendedBackoff{
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Steps:    100, // Sufficiently many steps
			Duration: 10 * time.Millisecond,
			Factor:   1.1,
		},
	}

	// Concurrently call the Get method
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, err := manager.Get(key, bo)
				if err != nil && !errors.Is(err, errTimeOut) {
					t.Errorf("Unexpected error: %v", err)
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()

	// Verify that the final Steps value has decreased
	v, ok := manager.store.Load(key)
	if !ok {
		t.Fatal("Key not found after concurrent operations")
	}
	actual := v.(*ResourceBackoff)

	// Because of multiple calls, Steps should decrease
	if actual.Bo.Steps >= 100 {
		t.Errorf("Steps not decremented properly: %d", actual.Bo.Steps)
	}
}

func TestBackoffManagerWithInitialDelay(t *testing.T) {
	manager := &BackoffManager{}
	key := "test-initial-delay"

	// Create a backoff strategy with initial delay
	initialDelay := 50 * time.Millisecond
	bo := backoff.ExtendedBackoff{
		InitialDelay: initialDelay,
		Backoff: wait.Backoff{
			Steps:    3,
			Duration: 20 * time.Millisecond,
			Factor:   2.0,
			Jitter:   0.1,
		},
	}

	// The first call should return the initial delay
	d1, err := manager.Get(key, bo)
	if err != nil {
		t.Fatalf("First Get failed: %v", err)
	}

	// Verify that the first call returns the initial delay
	if d1 < initialDelay*9/10 || d1 > initialDelay*11/10 {
		t.Errorf("Expected initial delay around %v, got %v", initialDelay, d1)
	}

	// Wait for the delay to pass
	time.Sleep(d1 + 5*time.Millisecond)

	// The second call should use the backoff duration, not the initial delay
	d2, err := manager.Get(key, bo)
	if err != nil {
		t.Fatalf("Second Get failed: %v", err)
	}

	// d2 should be around the backoff duration (20ms), not the initial delay (50ms)
	if d2 > initialDelay/2 {
		t.Errorf("Second call should use backoff duration, not initial delay. Got %v", d2)
	}

	// Wait for the delay to pass
	time.Sleep(d2 + 5*time.Millisecond)

	// The third call should use the next step of backoff (approximately 40ms)
	d3, err := manager.Get(key, bo)
	if err != nil {
		t.Fatalf("Third Get failed: %v", err)
	}

	// d3 should be approximately twice d2 (considering jitter)
	if float64(d3) < float64(d2)*1.5 || float64(d3) > float64(d2)*2.5 {
		t.Errorf("Expected d3 to be about twice d2. Got d2=%v, d3=%v", d2, d3)
	}
}

func TestBackoffManagerZeroInitialDelay(t *testing.T) {
	manager := &BackoffManager{}
	key := "test-zero-initial-delay"

	// Create a backoff strategy with zero initial delay
	bo := backoff.ExtendedBackoff{
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Steps:    3,
			Duration: 10 * time.Millisecond,
			Factor:   2.0,
			Jitter:   0.1,
		},
	}

	// The first call should use the backoff duration directly, not wait for initial delay
	d1, err := manager.Get(key, bo)
	if err != nil {
		t.Fatalf("First Get failed: %v", err)
	}

	// With zero initial delay, the first call should use backoff duration (around 10ms)
	if d1 < 5*time.Millisecond || d1 > 15*time.Millisecond {
		t.Errorf("Expected duration around 10ms for zero initial delay, got %v", d1)
	}
}

func TestBackoffManagerMultipleKeys(t *testing.T) {
	manager := &BackoffManager{}

	key1 := "key1"
	key2 := "key2"

	bo1 := backoff.ExtendedBackoff{
		InitialDelay: 30 * time.Millisecond,
		Backoff: wait.Backoff{
			Steps:    2,
			Duration: 10 * time.Millisecond,
			Factor:   2.0,
		},
	}

	bo2 := backoff.ExtendedBackoff{
		InitialDelay: 60 * time.Millisecond,
		Backoff: wait.Backoff{
			Steps:    2,
			Duration: 15 * time.Millisecond,
			Factor:   2.0,
		},
	}

	// First call for key1 should return its initial delay
	d1, err := manager.Get(key1, bo1)
	if err != nil {
		t.Fatalf("Get for key1 failed: %v", err)
	}
	if d1 < 25*time.Millisecond || d1 > 35*time.Millisecond {
		t.Errorf("Expected initial delay around 30ms for key1, got %v", d1)
	}

	// First call for key2 should return its initial delay
	d2, err := manager.Get(key2, bo2)
	if err != nil {
		t.Fatalf("Get for key2 failed: %v", err)
	}
	if d2 < 55*time.Millisecond || d2 > 65*time.Millisecond {
		t.Errorf("Expected initial delay around 60ms for key2, got %v", d2)
	}

	// Verify both keys exist independently
	_, ok1 := manager.GetNextTS(key1)
	_, ok2 := manager.GetNextTS(key2)
	if !ok1 || !ok2 {
		t.Error("Both keys should exist in the manager")
	}

	// Delete key1 and verify key2 still exists
	manager.Del(key1)
	_, ok1 = manager.GetNextTS(key1)
	_, ok2 = manager.GetNextTS(key2)
	if ok1 {
		t.Error("Key1 should have been deleted")
	}
	if !ok2 {
		t.Error("Key2 should still exist")
	}
}

func TestBackoffManagerConcurrentFirstCall(t *testing.T) {
	manager := &BackoffManager{}
	key := "concurrent-first-call"

	initialDelay := 50 * time.Millisecond
	bo := backoff.ExtendedBackoff{
		InitialDelay: initialDelay,
		Backoff: wait.Backoff{
			Steps:    10,
			Duration: 20 * time.Millisecond,
			Factor:   2.0,
			Jitter:   0.1,
		},
	}

	// Simulate multiple goroutines calling Get simultaneously for the first time
	var wg sync.WaitGroup
	results := make([]time.Duration, 10)
	errors := make([]error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			d, err := manager.Get(key, bo)
			results[idx] = d
			errors[idx] = err
		}(i)
	}

	wg.Wait()

	// All goroutines should get the same result (initial delay) without error
	for i, err := range errors {
		if err != nil {
			t.Errorf("Goroutine %d got error: %v", i, err)
		}
	}

	// All results should be around the initial delay
	for i, d := range results {
		if d < initialDelay*9/10 || d > initialDelay*11/10 {
			t.Errorf("Goroutine %d: expected initial delay around %v, got %v", i, initialDelay, d)
		}
	}

	// Verify that the ResourceBackoff was created only once
	v, ok := manager.store.Load(key)
	if !ok {
		t.Fatal("Key should exist in the manager")
	}
	actual := v.(*ResourceBackoff)

	// Steps should not have been consumed on the first call (only InitialDelay was returned)
	if actual.Bo.Steps != 10 {
		t.Errorf("Expected Steps to be 10, got %d", actual.Bo.Steps)
	}

	// NextTS should be set to now + initialDelay
	expectedNextTS := time.Now().Add(initialDelay)
	if actual.NextTS.Sub(expectedNextTS).Abs() > 100*time.Millisecond {
		t.Errorf("NextTS not set correctly after initial delay")
	}
}
