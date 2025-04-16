package metadata

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	// MetadataLatency aliyun metadata latency
	MetadataLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "aliyun_metadata_latency",
			Help:    "aliyun metadata latency in ms",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10),
		},
		[]string{"url", "error"},
	)
)

func MsSince(start time.Time) float64 {
	return float64(time.Since(start) / time.Millisecond)
}
