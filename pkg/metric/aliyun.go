package metric

import "github.com/prometheus/client_golang/prometheus"

var (
	// OpenAPILatency aliyun open api latency
	OpenAPILatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "aliyun_openapi_latency",
			Help:    "aliyun openapi latency in ms",
			Buckets: []float64{50, 100, 200, 400, 800, 1600, 3200, 6400, 12800, 13800, 14800, 16800, 20800, 28800, 44800},
		},
		[]string{"api", "error"},
	)
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
