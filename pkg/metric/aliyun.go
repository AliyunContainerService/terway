package metric

import "github.com/prometheus/client_golang/prometheus"

var (
	// OpenAPILatency aliyun open api latency
	OpenAPILatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "aliyun_openapi_latency",
			Help: "aliyun openapi latency in ms",
			// not sure what appropriate objectives(q & e) should be choose; need more validation
			Objectives: map[float64]float64{0.5: 0.05, 0.8: 0.01, 0.95: 0.001},
		},
		[]string{"api", "error"},
	)
	// MetadataLatency aliyun metadata latency
	MetadataLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "aliyun_metadata_latency",
			Help:       "aliyun metadata latency in ms",
			Objectives: map[float64]float64{0.5: 0.05, 0.8: 0.01, 0.95: 0.001},
		},
		[]string{"url", "error"},
	)
)
