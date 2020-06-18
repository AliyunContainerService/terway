package metric

import "github.com/prometheus/client_golang/prometheus"

var (
	// OpenAPILatency aliyun open api latency
	OpenAPILatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "aliyun_openapi_latency",
			Help:    "aliyun openapi latency in ms",
			Buckets: prometheus.ExponentialBuckets(50, 2, 10),
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
