//go:build !windows

package eni

import (
	"github.com/AliyunContainerService/terway/types/daemon"
)

func setupENICompartment(eni *daemon.ENI) error {
	return nil
}

func destroyENICompartment(eni *daemon.ENI) error {
	return nil
}
