//go:build e2e

package tests

import (
	"flag"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

var enableTrunk bool
var enablePolicy bool
var image string
var testNamespace = "network-test-" + strconv.FormatInt(time.Now().Unix(), 10)
var httpTestPort = 81
var httpsTestPort = 444

func init() {
	flag.BoolVar(&enableTrunk, "trunk", false, "install trunk policy")
	flag.BoolVar(&enablePolicy, "policy", false, "install network policy")
	flag.StringVar(&image, "image", "l1b0k/echo", "custom test image")
}

type ResConfig struct {
	Description string

	Name      string
	Namespace string
	Labels    map[string]string
}

type PodResConfig struct {
	*ResConfig
	HostNetwork bool
	Replicas    int32
}

type ServiceResConfig struct {
	*ResConfig
	PodSelectLabels map[string]string
	Type            corev1.ServiceType
	Headless        bool
}

type NetworkPolicyConfig struct {
	*ResConfig
	PodSelectLabels        map[string]string
	IngressPodLabels       map[string]string
	IngressNamespaceLabels map[string]string
}

type PodNetworkingConfig struct {
	*ResConfig
	PodSelectLabels map[string]string
	NamespaceLabels map[string]string
	IPType          v1beta1.AllocationType
}

type Resource struct {
	Label map[string]string
}

type TestCase struct {
	Type TestType
	Skip bool // skip this case or not

	Src Resource
	Dst Resource

	Status bool // status true or false
}

type TestType string

const (
	TestTypePodToPod         TestType = "pod2pod"
	TestTypePodToServiceIP   TestType = "pod2serviceIP"
	TestTypePodToServiceName TestType = "pod2serviceName"
)

var podConnA = PodResConfig{
	ResConfig: &ResConfig{
		Description: "",
		Name:        "container-network-deploy-src",
		Namespace:   testNamespace,
		Labels: map[string]string{
			"app":    "container-network-pod-src",
			"e2e":    "true",
			"ref":    "deployment",
			"access": "true", // when enable policy, src pod can access dst pod
		},
	},
	HostNetwork: false,
	Replicas:    3,
}

var podConnB = PodResConfig{
	ResConfig: &ResConfig{
		Description: "",
		Name:        "host-network-deploy-src",
		Namespace:   testNamespace,
		Labels: map[string]string{
			"app":    "host-network-pod-src",
			"e2e":    "true",
			"ref":    "deployment",
			"access": "false", // when enable policy, src pod can't access dst pod
		},
	},
	HostNetwork: true,
	Replicas:    3,
}

var podConnC = PodResConfig{
	ResConfig: &ResConfig{
		Description: "",
		Name:        "container-network-deploy-dst",
		Namespace:   testNamespace,
		Labels: map[string]string{
			"app": "container-network-pod-dst",
			"e2e": "true",
			"ref": "deployment",
		},
	},
	HostNetwork: false,
	Replicas:    2,
}

var podConnD = PodResConfig{
	ResConfig: &ResConfig{
		Description: "",
		Name:        "container-network-sts-dst",
		Namespace:   testNamespace,
		Labels: map[string]string{
			"app": "container-network-pod-dst",
			"e2e": "true",
			"ref": "stateful-set",
		},
	},
	HostNetwork: false,
	Replicas:    2,
}

var podConnPolicy = PodResConfig{
	ResConfig: &ResConfig{
		Description: "",
		Name:        "container-network-policy-deploy-src",
		Namespace:   testNamespace,
		Labels: map[string]string{
			"app":    "container-network-policy-pod-src",
			"e2e":    "true",
			"ref":    "deployment",
			"access": "false", // when enable policy, src pod can't access dst pod
		},
	},
	HostNetwork: false,
	Replicas:    3,
}

var clusterIPService = ServiceResConfig{
	ResConfig: &ResConfig{
		Description: "",
		Name:        "cluster-ip-service",
		Namespace:   testNamespace,
		Labels: map[string]string{
			"svc": "container-network-svc-dst",
			"e2e": "true",
		},
	},
	PodSelectLabels: map[string]string{
		"app": "container-network-pod-dst",
		"e2e": "true",
		"ref": "deployment",
	},
	Type:     corev1.ServiceTypeClusterIP,
	Headless: false,
}

var nodePortService = ServiceResConfig{
	ResConfig: &ResConfig{
		Description: "",
		Name:        "node-port-service",
		Namespace:   testNamespace,
		Labels: map[string]string{
			"svc": "container-network-svc-dst",
			"e2e": "true",
		},
	},
	PodSelectLabels: map[string]string{
		"app": "container-network-pod-dst",
		"e2e": "true",
		"ref": "deployment",
	},
	Type:     corev1.ServiceTypeNodePort,
	Headless: false,
}

var loadBalancerService = ServiceResConfig{
	ResConfig: &ResConfig{
		Description: "",
		Name:        "load-balancer-service",
		Namespace:   testNamespace,
		Labels: map[string]string{
			"svc": "container-network-svc-dst",
			"e2e": "true",
		},
	},
	PodSelectLabels: map[string]string{
		"app": "container-network-pod-dst",
		"e2e": "true",
		"ref": "deployment",
	},
	Type:     corev1.ServiceTypeLoadBalancer,
	Headless: false,
}

var headlessService = ServiceResConfig{
	ResConfig: &ResConfig{
		Description: "",
		Name:        "headless-service",
		Namespace:   testNamespace,
		Labels: map[string]string{
			"svc": "container-network-svc-dst",
			"e2e": "true",
		},
	},
	PodSelectLabels: map[string]string{
		"app": "container-network-pod-dst",
		"e2e": "true",
		"ref": "deployment",
	},
	Type:     corev1.ServiceTypeClusterIP,
	Headless: true,
}

var networkPolicy = NetworkPolicyConfig{
	ResConfig: &ResConfig{
		Description: "",
		Name:        "access-policy",
		Namespace:   testNamespace,
	},
	PodSelectLabels: map[string]string{
		"app": "container-network-pod-dst",
		"e2e": "true",
	},
	IngressPodLabels: map[string]string{
		"access": "true",
	},
	IngressNamespaceLabels: map[string]string{
		"project": "network-test",
	},
}

var elasticPodNetWorking = PodNetworkingConfig{
	ResConfig: &ResConfig{
		Description: "",
		Name:        "stateless",
	},
	PodSelectLabels: map[string]string{
		"app": "container-network-pod-dst",
		"e2e": "true",
		"ref": "deployment",
	},
	NamespaceLabels: map[string]string{
		"project": "network-test",
	},
	IPType: v1beta1.AllocationType{
		Type: v1beta1.IPAllocTypeElastic,
	},
}

var fixedPodNetWorking = PodNetworkingConfig{
	ResConfig: &ResConfig{
		Description: "",
		Name:        "fixed-ip",
	},
	PodSelectLabels: map[string]string{
		"app": "container-network-pod-dst",
		"e2e": "true",
		"ref": "stateful-set",
	},
	NamespaceLabels: map[string]string{
		"project": "network-test",
	},
	IPType: v1beta1.AllocationType{
		Type:            v1beta1.IPAllocTypeFixed,
		ReleaseStrategy: v1beta1.ReleaseStrategyTTL,
		ReleaseAfter:    "5m0s",
	},
}
