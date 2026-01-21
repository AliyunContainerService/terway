package metric

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestOpenAPILatency(t *testing.T) {
	// Test that OpenAPILatency is initialized
	assert.NotNil(t, OpenAPILatency)

	// Test that we can observe values
	OpenAPILatency.WithLabelValues("CreateNetworkInterface", "").Observe(100)
	OpenAPILatency.WithLabelValues("CreateNetworkInterface", "error").Observe(200)
}

func TestRateLimiterLatency(t *testing.T) {
	// Test that RateLimiterLatency is initialized
	assert.NotNil(t, RateLimiterLatency)

	// Test that we can observe values
	RateLimiterLatency.WithLabelValues("ECS").Observe(50)
	RateLimiterLatency.WithLabelValues("VPC").Observe(100)
}

func TestAliyunMetricRegistration(t *testing.T) {
	// Test that metrics can be registered with prometheus
	registry := prometheus.NewRegistry()

	err := registry.Register(OpenAPILatency)
	assert.NoError(t, err)

	err = registry.Register(RateLimiterLatency)
	assert.NoError(t, err)
}
