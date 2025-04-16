package metric

import (
	"github.com/prometheus/client_golang/prometheus"
)

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

	RateLimiterLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "rate_limiter_latency",
			Help:    "rate_limiter_latency in ms",
			Buckets: []float64{200, 400, 800, 1600, 3200, 6400, 12800, 13800, 14800, 16800, 20800, 28800, 44800},
		},
		[]string{"api"},
	)
)
