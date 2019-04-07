package metric

import "github.com/prometheus/client_golang/prometheus"

var (
	OpenAPILatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "aliyun_openapi_latency",
			Help: "aliyun openapi latency in ms",
		},
		[]string{"api", "error"},
	)
)

var (
	MetadataLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "aliyun_metadata_latency",
			Help: "aliyun metadata latency in ms",
		},
		[]string{"url", "error"},
	)
)
