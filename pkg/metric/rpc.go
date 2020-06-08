package metric

import "github.com/prometheus/client_golang/prometheus"

var (
	// RPCLatency terway grpc latency for grpc by cni binary
	RPCLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "terway_rpc_latency_ms",
			Help:       "terway rpc latency in ms",
			Objectives: map[float64]float64{0.5: 0.05, 0.8: 0.01, 0.95: 0.001},
		},
		[]string{"rpc_api", "error"},
	)
)
