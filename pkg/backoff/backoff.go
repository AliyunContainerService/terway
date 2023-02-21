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
	WaitENIStatus         = "wait_eni_status"
	WaitPodENIStatus      = "wait_podeni_status"
	MetaAssignPrivateIP   = "meta_assign_private_ip"
	MetaUnAssignPrivateIP = "meta_unassign_private_ip"
	WaitStsTokenReady     = "wait_sts_token_ready"
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
	WaitENIStatus: {
		Duration: time.Second * 5,
		Factor:   1.5,
		Jitter:   0.5,
		Steps:    8,
	},
	WaitPodENIStatus: {
		Duration: time.Second * 5,
		Factor:   2,
		Jitter:   0.3,
		Steps:    3,
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
