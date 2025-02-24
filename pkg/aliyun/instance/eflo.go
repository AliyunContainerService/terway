package instance

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
)

var ErrUnSupport = errors.New("unsupported")

type EFLO struct {
	f     *efloConfig
	mutex sync.Mutex
}

func (e *EFLO) GetRegionID() (string, error) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	if e.f == nil {
		cfg, err := loadEfloConfig()
		if err != nil {
			return "", err
		}
		e.f = cfg
	}
	return e.f.RegionID, nil
}

func (e *EFLO) GetZoneID() (string, error) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	if e.f == nil {
		cfg, err := loadEfloConfig()
		if err != nil {
			return "", err
		}
		e.f = cfg
	}
	return e.f.ZoneID, nil
}

func (e *EFLO) GetVSwitchID() (string, error) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	if e.f == nil {
		cfg, err := loadEfloConfig()
		if err != nil {
			return "", err
		}
		e.f = cfg
	}
	return e.f.AckNicName, nil
}

func (e *EFLO) GetPrimaryMAC() (string, error) {
	return "", ErrUnSupport
}

func (e *EFLO) GetInstanceID() (string, error) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	if e.f == nil {
		cfg, err := loadEfloConfig()
		if err != nil {
			return "", err
		}
		e.f = cfg
	}
	return e.f.NodeID, nil
}

func (e *EFLO) GetInstanceType() (string, error) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	if e.f == nil {
		cfg, err := loadEfloConfig()
		if err != nil {
			return "", err
		}
		e.f = cfg
	}
	return e.f.InstanceType, nil
}

func loadEfloConfig() (*efloConfig, error) {
	file, err := os.ReadFile("/etc/eflo_config/lingjun_config")
	if err != nil {
		return nil, err
	}

	cfg := &efloConfig{}
	err = json.Unmarshal(file, cfg)

	return cfg, err
}

type efloConfig struct {
	RegionID     string `json:"RegionId"`
	ZoneID       string `json:"ZoneId"`
	NodeID       string `json:"NodeId"`
	InstanceType string `json:"InstanceType"`
	AckNicName   string `json:"AckNicName"`
}
