package metric

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestResourcePoolConstants(t *testing.T) {
	// Test constants
	assert.Equal(t, "LocalIP", ResourcePoolTypeLocal)
	assert.Equal(t, "RemoteIP", ResourcePoolTypeRemote)
}

func TestResourcePoolTotal(t *testing.T) {
	// Test that ResourcePoolTotal is initialized
	assert.NotNil(t, ResourcePoolTotal)

	// Test that we can set values
	ResourcePoolTotal.WithLabelValues(ResourcePoolTypeLocal, "ipv4").Set(10)
	ResourcePoolTotal.WithLabelValues(ResourcePoolTypeRemote, "ipv4").Set(20)
	ResourcePoolTotal.WithLabelValues(ResourcePoolTypeLocal, "ipv6").Set(5)
}

func TestResourcePoolIdle(t *testing.T) {
	// Test that ResourcePoolIdle is initialized
	assert.NotNil(t, ResourcePoolIdle)

	// Test that we can set values
	ResourcePoolIdle.WithLabelValues(ResourcePoolTypeLocal, "ipv4").Set(5)
	ResourcePoolIdle.WithLabelValues(ResourcePoolTypeRemote, "ipv4").Set(10)
}

func TestResourcePoolDisposed(t *testing.T) {
	// Test that ResourcePoolDisposed is initialized
	assert.NotNil(t, ResourcePoolDisposed)

	// Test that we can increment counters
	ResourcePoolDisposed.WithLabelValues(ResourcePoolTypeLocal, "ipv4").Inc()
	ResourcePoolDisposed.WithLabelValues(ResourcePoolTypeRemote, "ipv4").Inc()
}

func TestResourcePoolAllocated(t *testing.T) {
	// Test that ResourcePoolAllocated is initialized
	assert.NotNil(t, ResourcePoolAllocated)

	// Test that we can increment counters
	ResourcePoolAllocated.WithLabelValues(ResourcePoolTypeLocal).Inc()
	ResourcePoolAllocated.WithLabelValues(ResourcePoolTypeRemote).Inc()
}

func TestResourcePoolMetricRegistration(t *testing.T) {
	// Test that metrics can be registered with prometheus
	registry := prometheus.NewRegistry()

	err := registry.Register(ResourcePoolTotal)
	assert.NoError(t, err)

	err = registry.Register(ResourcePoolIdle)
	assert.NoError(t, err)

	err = registry.Register(ResourcePoolDisposed)
	assert.NoError(t, err)

	err = registry.Register(ResourcePoolAllocated)
	assert.NoError(t, err)
}
