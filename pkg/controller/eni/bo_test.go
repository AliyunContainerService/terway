package eni

import (
	"errors"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

func TestBackoffManager(t *testing.T) {
	manager := &BackoffManager{}
	key := "test-key"

	// Create a simple backoff strategy
	bo := wait.Backoff{
		Steps:    3,
		Duration: 10 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.1,
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
	boExhausted := wait.Backoff{
		Steps:    1,
		Duration: 10 * time.Millisecond,
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
	bo := wait.Backoff{
		Steps:    100, // Sufficiently many steps
		Duration: 10 * time.Millisecond,
		Factor:   1.1,
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
