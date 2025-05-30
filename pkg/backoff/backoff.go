package backoff

import (
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

// backoff keys
const (
	DefaultKey            = ""
	ENICreate             = "eni_create"
	ENIOps                = "eni_ops"
	ENIRelease            = "eni_release"
	ENIIPOps              = "eni_ip_ops"
	WaitENIStatus         = "wait_eni_status"
	WaitLENIStatus        = "wait_leni_status"
	WaitHDENIStatus       = "wait_hdeni_status"
	WaitPodENIStatus      = "wait_podeni_status"
	MetaAssignPrivateIP   = "meta_assign_private_ip"
	MetaUnAssignPrivateIP = "meta_unassign_private_ip"
	WaitStsTokenReady     = "wait_sts_token_ready"
	WaitNodeStatus        = "wait_node_status"
)

var backoffMap = map[string]wait.Backoff{
	DefaultKey: {
		Duration: time.Second * 2,
		Factor:   1.5,
		Jitter:   0.3,
		Steps:    6,
	},
	ENICreate: {
		Duration: time.Second * 10,
		Factor:   2,
		Jitter:   0.3,
		Steps:    2,
	},
	ENIOps: {
		Duration: time.Second * 5,
		Factor:   2,
		Jitter:   0.3,
		Steps:    6,
	},
	ENIRelease: {
		Duration: time.Second * 4,
		Factor:   2,
		Jitter:   0.5,
		Steps:    8,
	},
	ENIIPOps: {
		Duration: time.Second * 5,
		Factor:   1,
		Jitter:   1.3,
		Steps:    6,
	},
	WaitENIStatus: {
		Duration: time.Second * 3,
		Factor:   1.5,
		Jitter:   0.5,
		Steps:    8,
	},
	WaitLENIStatus: {
		Duration: time.Second * 5,
		Factor:   1.5,
		Jitter:   0.5,
		Steps:    8,
	},
	WaitHDENIStatus: {
		Duration: time.Second * 5,
		Factor:   1.5,
		Jitter:   0.5,
		Steps:    8,
	},
	WaitPodENIStatus: {
		Duration: time.Second * 5,
		Factor:   1,
		Jitter:   0.3,
		Steps:    20,
	},
	MetaAssignPrivateIP: {
		Duration: time.Millisecond * 1100,
		Factor:   1,
		Jitter:   0.2,
		Steps:    10,
	},
	MetaUnAssignPrivateIP: {
		Duration: time.Millisecond * 1100,
		Factor:   1,
		Jitter:   0.2,
		Steps:    10,
	},
	WaitStsTokenReady: {
		Duration: time.Second,
		Factor:   1,
		Jitter:   0.2,
		Steps:    60,
	},
	WaitNodeStatus: {
		Duration: time.Second * 1,
		Factor:   1,
		Jitter:   0.3,
		Steps:    90,
	},
}

func OverrideBackoff(in map[string]wait.Backoff) {
	for k, v := range in {
		backoffMap[k] = v
	}
}

func Backoff(key string) wait.Backoff {
	b, ok := backoffMap[key]
	if !ok {
		return backoffMap[DefaultKey]
	}
	return b
}
