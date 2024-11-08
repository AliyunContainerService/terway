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

	prevDatapath := nodecap.GetNodeCapabilities(nodecap.NodeCapabilityDataPath)
	if prevDatapath == dataPathV2 {
		fmt.Println("datapath is already v2")
		return true
	}

	_, err := netlink.LinkByName("cilium_net")
	return errors.As(err, &netlink.LinkNotFoundError{})
}

// allowEBPFNetworkPolicy check in veth datapath
// policy
// false -> true:
// old node(has cilium already) keep old behave
// old node(do not has cilium)  keep old behave
// new node ( based on user require).
// true -> false: keep cilium chain, but disable policy
func allowEBPFNetworkPolicy(require bool) (bool, error) {
	store := nodecap.NewFileNodeCapabilities(nodeCapabilitiesFile)
	if err := store.Load(); err != nil {
		return false, err
	}
	switch store.Get(nodecap.NodeCapabilityHasCiliumChainer) {
	case True:
		fmt.Printf("has prev cilium chainer\n")
		return true, nil
	case False:
		fmt.Printf("no prev cilium chainer\n")
		return false, nil
	}

	_, err := netlink.LinkByName("cilium_net")
	if err == nil {
		fmt.Printf("link cilium_net exist\n")
		return true, nil
	}
	if !errors.As(err, &netlink.LinkNotFoundError{}) {
		return false, err
	}

	return require, nil
}

func checkKernelVersion(k, major, minor int) bool {
	return kernel.CheckKernelVersion(k, major, minor)
}
