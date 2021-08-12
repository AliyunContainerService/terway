package tests

import (
	"flag"
)

var enableTrunk bool
var enablePolicy bool

func init() {
	flag.BoolVar(&enableTrunk, "trunk", false, "install trunk policy")
	flag.BoolVar(&enablePolicy, "policy", false, "install network policy")
}

type TestResConfig struct {
	Description string

	Name        string
	Namespace   string
	Labels      map[string]string
	HostNetwork bool
}

type Resource struct {
	Label map[string]string
}

type TestCase struct {
	Type TestType

	Src Resource
	Dst Resource

	Status bool // status true or fail
}

type TestType string

const (
	TestTypePodToPod     TestType = "pod2pod"
	TestTypePodToService TestType = "pod2service"
)

var podConnA = TestResConfig{
	Description: "",
	Name:        "container-network-pod-src",
	Namespace:   "network-test",
	Labels: map[string]string{
		"app": "container-network-pod-src",
		"e2e": "true",
	},
	HostNetwork: false,
}

var podConnB = TestResConfig{
	Description: "",
	Name:        "container-network-pod-dst",
	Namespace:   "network-test",
	Labels: map[string]string{
		"app": "container-network-pod-dst",
		"e2e": "true",
	},
	HostNetwork: false,
}

var hostNetworkPod = TestResConfig{
	Description: "",
	Name:        "host-network-pod",
	Namespace:   "network-test",
	Labels: map[string]string{
		"app": "host-network-pod",
		"e2e": "true",
	},
	HostNetwork: true,
}
