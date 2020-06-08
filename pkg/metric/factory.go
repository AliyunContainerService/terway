package metric

import "github.com/prometheus/client_golang/prometheus"

var (
	ENIIPFactoryENICount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "terway_eniip_factory_eni_count",
			Help: "amount of allocated terway multi-ip factory eni",
		},
		[]string{"name", "max_eni"},
	)

	ENIIPFactoryIPCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "terway_eniip_factory_ip_count",
			Help: "amount of allocated terway multi-ip secondary ip",
		},
		// eni is represented by its mac address
		[]string{"name", "eni", "max_ip"},
	)

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
	ENIIPAllocActionSucceed = "succeed"
	ENIIPAllocActionFail    = "fail"
)
