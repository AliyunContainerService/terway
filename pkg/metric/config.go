package metric

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	ConfigInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "terway_controlplane_config_info",
			Help: "Configured max concurrent reconciles for terway controlplane controllers",
		},
		[]string{"controller", "max_concurrent"},
	)
)

type ControllerConcurrentConfig struct {
	Name          string
	MaxConcurrent int
}

func SetConfigMetrics(controllers []ControllerConcurrentConfig) {
	ConfigInfo.Reset()
	for _, c := range controllers {
		ConfigInfo.WithLabelValues(c.Name, fmt.Sprintf("%d", c.MaxConcurrent)).Set(1)
	}
}
