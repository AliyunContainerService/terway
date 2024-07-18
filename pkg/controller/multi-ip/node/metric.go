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
)
