package metric

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestRPCLatency(t *testing.T) {
	// Test that RPCLatency is initialized
	assert.NotNil(t, RPCLatency)

	// Test that we can observe values
	RPCLatency.WithLabelValues("AllocIP", "").Observe(50)
	RPCLatency.WithLabelValues("AllocIP", "error").Observe(100)
	RPCLatency.WithLabelValues("ReleaseIP", "").Observe(30)
}

func TestRPCMetricRegistration(t *testing.T) {
	// Test that metrics can be registered with prometheus
	registry := prometheus.NewRegistry()

	err := registry.Register(RPCLatency)
	assert.NoError(t, err)
}

func TestRPCLatencyBuckets(t *testing.T) {
	// Test that we can observe values in different buckets
	// This helps verify the bucket configuration
	RPCLatency.WithLabelValues("test", "").Observe(25)     // Should be in first bucket
	RPCLatency.WithLabelValues("test", "").Observe(100)    // Should be in middle bucket
	RPCLatency.WithLabelValues("test", "").Observe(10000)  // Should be in higher bucket
	RPCLatency.WithLabelValues("test", "").Observe(100000) // Should be in highest bucket
}
