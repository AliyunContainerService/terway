package metric

import "github.com/prometheus/client_golang/prometheus"

var (
	// assume that the initial value of gauge has been set to 0
	ResourcePoolTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "terway_resource_pool_total_count",
			Help: "terway total resources amount in the pool",
		},
		// not accessory to put capacity, max_idle or min_idle into labels ?
		[]string{"name", "capacity", "max_idle", "min_idle"},
	)

	ResourcePoolIdle = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "terway_resource_pool_idle_count",
			Help: "terway idle resources amount in the pool",
		},
		[]string{"name", "capacity", "max_idle", "min_idle"},
	)

	ResourcePoolDisposed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "terway_resource_pool_disposed_count",
			Help: "terway resource count of being disposed",
		},
		[]string{"name", "capacity", "max_idle", "min_idle"},
	)
)
