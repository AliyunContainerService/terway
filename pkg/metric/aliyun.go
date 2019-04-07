package metric

import "github.com/prometheus/client_golang/prometheus"

var (
	// OpenAPILatency aliyun open api latency
	OpenAPILatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "aliyun_openapi_latency",
			Help: "aliyun openapi latency in ms",
		},
		[]string{"api", "error"},
	)
	// MetadataLatency aliyun metadata latency
	MetadataLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "aliyun_metadata_latency",
			Help: "aliyun metadata latency in ms",
		},
		[]string{"url", "error"},
	)
)
