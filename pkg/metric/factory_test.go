package metric

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestENIIPFactoryENICount(t *testing.T) {
	// Test that ENIIPFactoryENICount is initialized
	assert.NotNil(t, ENIIPFactoryENICount)

	// Test that we can set values
	ENIIPFactoryENICount.WithLabelValues("test-factory", "10").Set(5)
	ENIIPFactoryENICount.WithLabelValues("test-factory", "20").Set(10)
}

func TestENIIPFactoryIPCount(t *testing.T) {
	// Test that ENIIPFactoryIPCount is initialized
	assert.NotNil(t, ENIIPFactoryIPCount)

	// Test that we can set values
	ENIIPFactoryIPCount.WithLabelValues("test-factory", "00:11:22:33:44:55", "10").Set(3)
	ENIIPFactoryIPCount.WithLabelValues("test-factory", "00:11:22:33:44:66", "20").Set(5)
}

func TestENIIPFactoryIPAllocCount(t *testing.T) {
	// Test that ENIIPFactoryIPAllocCount is initialized
	assert.NotNil(t, ENIIPFactoryIPAllocCount)

	// Test that we can increment counters
	ENIIPFactoryIPAllocCount.WithLabelValues("00:11:22:33:44:55", ENIIPAllocActionSucceed).Inc()
	ENIIPFactoryIPAllocCount.WithLabelValues("00:11:22:33:44:55", ENIIPAllocActionFail).Inc()
}

func TestENIIPAllocActionConstants(t *testing.T) {
	// Test constants
	assert.Equal(t, "succeed", ENIIPAllocActionSucceed)
	assert.Equal(t, "fail", ENIIPAllocActionFail)
}

func TestFactoryMetricRegistration(t *testing.T) {
	// Test that metrics can be registered with prometheus
	registry := prometheus.NewRegistry()

	err := registry.Register(ENIIPFactoryENICount)
	assert.NoError(t, err)

	err = registry.Register(ENIIPFactoryIPCount)
	assert.NoError(t, err)

	err = registry.Register(ENIIPFactoryIPAllocCount)
	assert.NoError(t, err)
}
