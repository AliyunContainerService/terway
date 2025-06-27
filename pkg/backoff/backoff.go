package backoff

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

// backoff keys
const (
	DefaultKey                 = ""
	ENICreate                  = "eni_create"
	ENIOps                     = "eni_ops"
	ENIRelease                 = "eni_release"
	ENIIPOps                   = "eni_ip_ops"
	WaitENIStatus              = "wait_eni_status"
	WaitMemberENIStatus        = "wait_member_eni_status"
	WaitLENIStatus             = "wait_leni_status"
	WaitHDENIStatus            = "wait_hdeni_status"
	WaitPodENIStatus           = "wait_podeni_status"
	WaitNetworkInterfaceStatus = "wait_networkinterface_status"
	MetaAssignPrivateIP        = "meta_assign_private_ip"
	MetaUnAssignPrivateIP      = "meta_unassign_private_ip"
	WaitStsTokenReady          = "wait_sts_token_ready"
	WaitNodeStatus             = "wait_node_status"
)

// ExtendedBackoff extends the backoff configuration with initial delay support
type ExtendedBackoff struct {
	// InitialDelay is the initial wait time before starting retries
	InitialDelay time.Duration
	// Backoff is the original k8s backoff configuration
	wait.Backoff
}

// NewExtendedBackoff creates a new extended backoff configuration
func NewExtendedBackoff(initialDelay time.Duration, backoff wait.Backoff) ExtendedBackoff {
	return ExtendedBackoff{
		InitialDelay: initialDelay,
		Backoff:      backoff,
	}
}

var backoffMap = map[string]ExtendedBackoff{
	DefaultKey: {
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Duration: time.Second * 2,
			Factor:   1.5,
			Jitter:   0.3,
			Steps:    6,
		},
	},
	ENICreate: {
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Duration: time.Second * 10,
			Factor:   2,
			Jitter:   0.3,
			Steps:    2,
		},
	},
	ENIOps: {
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Duration: time.Second * 5,
			Factor:   2,
			Jitter:   0.3,
			Steps:    6,
		},
	},
	ENIRelease: {
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Duration: time.Second * 4,
			Factor:   2,
			Jitter:   0.5,
			Steps:    8},
	},
	ENIIPOps: {
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Duration: time.Second * 5,
			Factor:   1,
			Jitter:   1.3,
			Steps:    6,
		},
	},
	WaitENIStatus: {
		InitialDelay: 3 * time.Second,
		Backoff: wait.Backoff{
			Duration: time.Second * 3,
			Factor:   1.5,
			Jitter:   0.5,
			Steps:    8,
		},
	},
	WaitMemberENIStatus: {
		InitialDelay: 2 * time.Second,
		Backoff: wait.Backoff{
			Duration: time.Second * 1,
			Factor:   1.5,
			Jitter:   0.5,
			Steps:    8,
		},
	},
	WaitLENIStatus: {
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Duration: time.Second * 5,
			Factor:   1.5,
			Jitter:   0.5,
			Steps:    8,
		},
	},
	WaitHDENIStatus: {
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Duration: time.Second * 5,
			Factor:   1.5,
			Jitter:   0.5,
			Steps:    8,
		},
	},
	WaitPodENIStatus: {
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Duration: time.Second * 5,
			Factor:   1,
			Jitter:   0.3,
			Steps:    20,
		},
	},
	WaitNetworkInterfaceStatus: {
		InitialDelay: 2,
		Backoff: wait.Backoff{
			Duration: time.Second * 1,
			Factor:   1,
			Jitter:   0.3,
			Steps:    120,
		},
	},
	MetaAssignPrivateIP: {
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Duration: time.Millisecond * 1100,
			Factor:   1,
			Jitter:   0.2,
			Steps:    10,
		},
	},
	MetaUnAssignPrivateIP: {
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Duration: time.Millisecond * 1100,
			Factor:   1,
			Jitter:   0.2,
			Steps:    10,
		},
	},
	WaitStsTokenReady: {
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Duration: time.Second,
			Factor:   1,
			Jitter:   0.2,
			Steps:    60,
		},
	},
	WaitNodeStatus: {
		InitialDelay: 0,
		Backoff: wait.Backoff{
			Duration: time.Second * 1,
			Factor:   1,
			Jitter:   0.3,
			Steps:    90,
		},
	},
}

func OverrideBackoff(in map[string]ExtendedBackoff) {
	for k, v := range in {
		// set default for InitialDelay
		if v.InitialDelay != 0 {
			backoffMap[k] = v
		} else {
			if prev, ok := backoffMap[k]; ok {
				v.InitialDelay = prev.InitialDelay
			}
			backoffMap[k] = v
		}
	}
}

func Backoff(key string) ExtendedBackoff {
	b, ok := backoffMap[key]
	if !ok {
		return backoffMap[DefaultKey]
	}
	return b
}

// ExponentialBackoffWithInitialDelay extends ExponentialBackoffWithContext with initial delay support
func ExponentialBackoffWithInitialDelay(ctx context.Context, extendedBackoff ExtendedBackoff, condition func(context.Context) (bool, error)) error {
	// If there's an initial delay, wait first
	if extendedBackoff.InitialDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(extendedBackoff.InitialDelay):
			// Initial delay completed, continue execution
		}
	}

	// Use the original ExponentialBackoffWithContext
	return wait.ExponentialBackoffWithContext(ctx, extendedBackoff.Backoff, condition)
}
