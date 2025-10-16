package node

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// ResourcePoolTotal terway total source amount in the pool
	ResourcePoolTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gc_count_total",
			Help: "controlplane gc total",
		},
		[]string{"node"},
	)

	SyncOpenAPITotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "sync_openapi_count_total",
			Help: "controlplane sync data with openapi total",
		},
		[]string{"node"},
	)

	// ReconcileLatency records the latency of different reconcile methods
	ReconcileLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "controlplane_node_reconcile_latency_ms",
			Help:    "Latency of node controller reconcile methods in milliseconds",
			Buckets: []float64{10, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 30000, 60000},
		},
		[]string{"method", "node"},
	)
)
