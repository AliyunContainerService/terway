package metric

import "github.com/prometheus/client_golang/prometheus"

const (
	ResourcePoolTypeLocal  string = "LocalIP"
	ResourcePoolTypeRemote string = "RemoteIP"
)

var (
	// ResourcePoolTotal terway total source amount in the pool
	ResourcePoolTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "terway_resource_pool_total_count",
			Help: "terway total resources amount in the pool",
		},
		// not accessory to put capacity, max_idle or min_idle into labels ?
		[]string{"type", "ipStack"},
	)

	// ResourcePoolIdle terway amount of idle resource in the pool
	ResourcePoolIdle = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "terway_resource_pool_idle_count",
			Help: "terway amount of idle resources in the pool",
		},
		[]string{"type", "ipStack"},
	)

	// ResourcePoolDisposed terway resource count of begin disposed
	ResourcePoolDisposed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "terway_resource_pool_disposed_count",
			Help: "terway resource count of being disposed",
		},
		[]string{"type", "ipStack"},
	)

	// ResourcePoolAllocatedCount terway resource allocation count
	ResourcePoolAllocated = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "terway_resource_pool_allocated_count",
			Help: "terway resource allocation count",
		},
		[]string{"type"},
	)
)
