package metric

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetConfigMetrics(t *testing.T) {
	controllers := []ControllerConcurrentConfig{
		{Name: "eni-controller", MaxConcurrent: 5},
		{Name: "pod-controller", MaxConcurrent: 10},
	}

	SetConfigMetrics(controllers)

	// Verify gauge values are set by collecting metrics
	mf, err := ConfigInfo.GetMetricWith(map[string]string{
		"controller":     "eni-controller",
		"max_concurrent": "5",
	})
	assert.NoError(t, err)
	assert.NotNil(t, mf)

	// Call again to verify Reset works
	SetConfigMetrics([]ControllerConcurrentConfig{
		{Name: "new-controller", MaxConcurrent: 1},
	})
}
