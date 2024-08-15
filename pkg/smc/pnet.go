package smc

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

const (
	smcPnet = "smc_pnet"
)

func ensureSMCR() bool {
	_, err := exec.Command("modprobe", "smc").CombinedOutput()
	if err != nil {
		return false
	}
	// smc module is load
	_, err = os.Stat("/proc/sys/net/smc/tcp2smc")
	return err == nil
}

func supportSMCR() bool {
	// rdma device attached
	rdmaLink, err := netlink.RdmaLinkList()
	if err != nil || len(rdmaLink) == 0 {
		return false
	}
	// smc-tools installed
	_, err = exec.LookPath(smcPnet)
	if err != nil {
		return false
	}
	return ensureSMCR()
}

func pnetID(name string) string {
	nameSlice := strings.Split(name, "_")
	return nameSlice[len(nameSlice)-1]
}

// fixme: reflect to netlink
func ensureForERDMADev(name string) error {
	output, err := exec.Command(smcPnet, "-s").CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get smc-pnet stat: %v, output: %v", err, string(output))
	}
	if bytes.Contains(output, []byte(name)) {
		return nil
	}
	output, err = exec.Command(smcPnet, "-a", pnetID(name), "-D", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to config smc-pnet rdma device: %v, output: %v", err, string(output))
	}
	return nil
}

func ensureForNetDevice(erdmaDev string, netDevice string) error {
	output, err := exec.Command(smcPnet, "-s").CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get smc-pnet stat for net device: %v, output: %v", err, string(output))
	}
	if bytes.Contains(output, []byte(netDevice)) {
		return nil
	}
	output, err = exec.Command(smcPnet, "-a", pnetID(erdmaDev), "-I", netDevice).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to config smc-pnet net device: %v, output: %v", err, string(output))
	}
	return nil
}

func ConfigSMCForDevice(erdmaDev string, netDevice string, netns ns.NetNS) error {
	if !supportSMCR() {
		return nil
	}
	if err := ensureForERDMADev(erdmaDev); err != nil {
		return err
	}
	if netns != nil {
		return netns.Do(func(ns ns.NetNS) error {
			return ensureForNetDevice(erdmaDev, netDevice)
		})
	}
	return ensureForNetDevice(erdmaDev, netDevice)
}
