//go:build !linux

package main

import (
	"github.com/AliyunContainerService/terway/plugin/driver/types"
)

func switchDataPathV2() bool {
	return true
}

func checkKernelVersion(k, major, minor int) bool {
	return false
}

func allowEBPFNetworkPolicy(enable bool) (bool, error) {
	return enable, nil
}

func isOldNode() (bool, error) {
	return false, nil
}

func canUseHostRouting() (bool, error) { return false, nil }

func hasCilium() (bool, error) { return false, nil }

func configureNetworkRulesWithConfig(ipv4, ipv6 bool, config *types.SymmetricRoutingConfig) error {
	return nil
}

func mountHostBpf() error { return nil }
