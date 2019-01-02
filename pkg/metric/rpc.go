package metric

import "github.com/prometheus/client_golang/prometheus"

var (
	RPCLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "terway_rpc_lantency_ms",
			Help: "terway rpc latency in ms",
		},
		[]string{"rpc_api", "error"},
	)
)
