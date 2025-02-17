package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"

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

func checkKernelVersion(iMajor, iMinor, iPatch int) bool {
	var un syscall.Utsname
	_ = syscall.Uname(&un)
	var sb strings.Builder
	for _, b := range un.Release[:] {
		if b == 0 {
			break
		}
		sb.WriteByte(byte(b))
	}
	major, minor, patch, ok := parseRelease(sb.String())
	return ok && (major > iMajor ||
		major == iMajor && minor > iMinor ||
		major == iMajor && minor == iMinor && iPatch >= patch)
}

// parseRelease parses a dot-separated version number. It follows the semver
// syntax, but allows the minor and patch versions to be elided.
//
// This is a copy of the Go runtime's parseRelease from
// https://golang.org/cl/209597.
func parseRelease(rel string) (major, minor, patch int, ok bool) {
	// Strip anything after a dash or plus.
	for i := 0; i < len(rel); i++ {
		if rel[i] == '-' || rel[i] == '+' {
			rel = rel[:i]
			break
		}
	}

	next := func() (int, bool) {
		for i := 0; i < len(rel); i++ {
			if rel[i] == '.' {
				ver, err := strconv.Atoi(rel[:i])
				rel = rel[i+1:]
				return ver, err == nil
			}
		}
		ver, err := strconv.Atoi(rel)
		rel = ""
		return ver, err == nil
	}
	if major, ok = next(); !ok || rel == "" {
		return
	}
	if minor, ok = next(); !ok || rel == "" {
		return
	}
	patch, ok = next()
	return
}
