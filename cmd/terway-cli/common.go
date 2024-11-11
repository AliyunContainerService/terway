package main

import (
	"os"
	"path/filepath"
)

const (
	pluginTypeTerway = "terway"
	pluginTypeCilium = "cilium-cni"
)
const eniConfBasePath = "/etc/eni"

const (
	True  = "true"
	False = "false"
)

type TerwayConfig struct {
	enableNetworkPolicy bool
	enableInClusterLB   bool

	eniConfig     []byte
	cniConfig     []byte
	cniConfigList []byte
}

// getAllConfig ready terway configmap mounted on path
func getAllConfig(base string) (*TerwayConfig, error) {
	cfg := &TerwayConfig{
		enableNetworkPolicy: true,
	}

	r, err := os.ReadFile(filepath.Join(base, "10-terway.conf"))
	if err != nil {
		// this file must exist
		return nil, err
	}

	cfg.cniConfig = r

	r, err = os.ReadFile(filepath.Join(base, "10-terway.conflist"))
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		cfg.cniConfigList = r
	}

	r, err = os.ReadFile(filepath.Join(base, "disable_network_policy"))
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		// default enable policy
	} else {
		switch string(r) {
		case "false", "0", "":
			cfg.enableNetworkPolicy = true
		default:
			cfg.enableNetworkPolicy = false
		}
	}

	r, err = os.ReadFile(filepath.Join(base, "eni_conf"))
	if err != nil {
		// this file must exist
		return nil, err
	}
	cfg.eniConfig = r

	r, err = os.ReadFile(filepath.Join(base, "in_cluster_loadbalance"))
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if string(r) == True {
		cfg.enableInClusterLB = true
	}

	return cfg, nil
}
