package instance

import (
	"encoding/json"
	"os"
)

func EfloPopulate() *Instance {
	cfg := loadEfloConfig()

	return &Instance{
		RegionID:     cfg.RegionID,
		InstanceType: cfg.InstanceType,
		InstanceID:   cfg.NodeID,
		ZoneID:       cfg.ZoneID,
	}
}

func loadEfloConfig() *efloConfig {
	file, err := os.ReadFile("/etc/eflo_config/lingjun_config")
	if err != nil {
		panic(err)
	}

	cfg := &efloConfig{}
	err = json.Unmarshal(file, cfg)
	if err != nil {
		panic(err)
	}

	return cfg
}

type efloConfig struct {
	RegionID     string `json:"RegionId"`
	ZoneID       string `json:"ZoneId"`
	NodeID       string `json:"NodeId"`
	InstanceType string `json:"InstanceType"`
	AckNicName   string `json:"AckNicName"`
}
