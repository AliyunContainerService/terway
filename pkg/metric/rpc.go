package metric

import "github.com/prometheus/client_golang/prometheus"

var (
	// RPCLatency terway grpc latency for grpc by cni binary
	RPCLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "terway_rpc_latency",
			Help:    "terway rpc latency in ms",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10),
		},
		[]string{"rpc_api", "error"},
	)
)
