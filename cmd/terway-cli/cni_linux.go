package main

import (
	"errors"
	"fmt"

	"github.com/docker/docker/pkg/parsers/kernel"
	"github.com/vishvananda/netlink"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	terwayfeature "github.com/AliyunContainerService/terway/pkg/feature"
	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
)

func switchDataPathV2() bool {
	if !utilfeature.DefaultFeatureGate.Enabled(terwayfeature.AutoDataPathV2) {
		return false
	}

	prevDatapath := nodecap.GetNodeCapabilities(nodeCapabilityDatapath)
	if prevDatapath == dataPathV2 {
		fmt.Println("datapath is already v2")
		return true
	}

	_, err := netlink.LinkByName("cilium_net")
	return errors.As(err, &netlink.LinkNotFoundError{})
}

func checkKernelVersion(k, major, minor int) bool {
	return kernel.CheckKernelVersion(k, major, minor)
}
