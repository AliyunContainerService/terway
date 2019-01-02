package metric

import (
	"github.com/prometheus/client_golang/prometheus"
	"time"
)

// MsSince returns milliseconds since start.
func MsSince(start time.Time) float64 {
	return float64(time.Since(start) / time.Millisecond)
}

func RegisterPrometheus() {
	prometheus.MustRegister(RPCLatency)
	prometheus.MustRegister(OpenAPILatency)
	prometheus.MustRegister(MetadataLatency)
}