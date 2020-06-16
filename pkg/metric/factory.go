package metric

import "github.com/prometheus/client_golang/prometheus"

var (
	// ENIIPFactoryENICount amount of allocated terway multi-ip factory eni
	ENIIPFactoryENICount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "terway_eniip_factory_eni_count",
			Help: "amount of allocated terway multi-ip factory eni",
		},
		[]string{"name", "max_eni"},
	)

	// ENIIPFactoryIPCount amount of allocated terway multi-ip secondary ip
	ENIIPFactoryIPCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "terway_eniip_factory_ip_count",
			Help: "amount of allocated terway multi-ip secondary ip",
		},
		// eni is represented by its mac address
		[]string{"name", "eni", "max_ip"},
	)

	// ENIIPFactoryIPAllocCount counter of eniip factory ip allocation
	ENIIPFactoryIPAllocCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "terway_eniip_factory_ip_alloc_count",
			Help: "counter of eniip factory ip allocation",
		},
		// status in "succeed" or "fail"
		[]string{"eni", "status"},
	)
)

const (
	// ENIIPAllocActionSucceed represents a succeeded ip alloc request
	ENIIPAllocActionSucceed = "succeed"
	// ENIIPAllocActionFail represents a failed ip alloc request
	ENIIPAllocActionFail = "fail"
)
