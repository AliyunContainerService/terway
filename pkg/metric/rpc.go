package metric

import "github.com/prometheus/client_golang/prometheus"

var (
	// RPCLatency terway grpc latency for grpc by cni binary
	RPCLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "terway_rpc_latency",
			Help:    "terway rpc latency in ms",
			Buckets: []float64{50, 100, 200, 400, 800, 1600, 3200, 6400, 12800, 25600, 26600, 27600, 29600, 33600, 41600, 57600, 89600, 110000, 120000},
		},
		[]string{"rpc_api", "error"},
	)
)
