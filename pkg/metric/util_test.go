package metric

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMsSince(t *testing.T) {
	// Test with zero time
	start := time.Time{}
	result := MsSince(start)
	// Should be a large positive number (time since epoch)
	assert.Greater(t, result, float64(0))

	// Test with current time
	start = time.Now()
	time.Sleep(10 * time.Millisecond)
	result = MsSince(start)
	// Should be approximately 10ms, allow some tolerance
	assert.GreaterOrEqual(t, result, float64(5))
	assert.LessOrEqual(t, result, float64(50))

	// Test with past time
	start = time.Now().Add(-100 * time.Millisecond)
	result = MsSince(start)
	// Should be approximately 100ms
	assert.GreaterOrEqual(t, result, float64(90))
	assert.LessOrEqual(t, result, float64(150))

	// Test with future time (should be negative or very small)
	start = time.Now().Add(100 * time.Millisecond)
	result = MsSince(start)
	// Should be negative or very small
	assert.LessOrEqual(t, result, float64(10))
}

func TestMsSincePrecision(t *testing.T) {
	// Test precision with different durations
	start := time.Now()
	time.Sleep(1 * time.Millisecond)
	result := MsSince(start)
	// Should be at least 1ms
	assert.GreaterOrEqual(t, result, float64(0.5))

	start = time.Now()
	time.Sleep(50 * time.Millisecond)
	result = MsSince(start)
	// Should be approximately 50ms
	assert.GreaterOrEqual(t, result, float64(40))
	assert.LessOrEqual(t, result, float64(100))
}
