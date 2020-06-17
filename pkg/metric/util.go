package metric

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// MsSince returns milliseconds since start.
func MsSince(start time.Time) float64 {
	return float64(time.Since(start) / time.Millisecond)
}

// RegisterPrometheus register metrics to prometheus server
func RegisterPrometheus() {
	prometheus.MustRegister(RPCLatency)
	prometheus.MustRegister(OpenAPILatency)
	prometheus.MustRegister(MetadataLatency)
	// ResourcePool
	prometheus.MustRegister(ResourcePoolTotal)
	prometheus.MustRegister(ResourcePoolIdle)
	prometheus.MustRegister(ResourcePoolDisposed)
	// ENIIP
	prometheus.MustRegister(ENIIPFactoryIPCount)
	prometheus.MustRegister(ENIIPFactoryENICount)
	prometheus.MustRegister(ENIIPFactoryIPAllocCount)
}
