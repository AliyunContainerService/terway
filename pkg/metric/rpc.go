package metric

import "github.com/prometheus/client_golang/prometheus"

var (
	// RPCLatency terway grpc latency for grpc by cni binary
	RPCLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "terway_rpc_lantency_ms",
			Help: "terway rpc latency in ms",
		},
		[]string{"rpc_api", "error"},
	)
)
