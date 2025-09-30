package daemon

import (
	"context"
	"strconv"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/k8s"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

// getDynamicConfig returns (config, label, error) specified in node
// ("", "", nil) for no dynamic config for this node
func getDynamicConfig(ctx context.Context, k8s k8s.Kubernetes) (string, string, error) {
	label := k8s.GetNodeDynamicConfigLabel()
	if label == "" {
		return "", "", nil
	}

	cfg, err := k8s.GetDynamicConfigWithName(ctx, label)

	return cfg, label, err
}

func getENIConfig(cfg *daemon.Config, zoneID string) *daemon.ENIConfig {
	vswitchSelectionPolicy := vswitch.VSwitchSelectionPolicyRandom
	switch cfg.VSwitchSelectionPolicy {
	case "ordered":
		// keep the previous behave
		vswitchSelectionPolicy = vswitch.VSwitchSelectionPolicyMost
	}

	eniSelectionPolicy := daemon.EniSelectionPolicyMostIPs
	switch cfg.EniSelectionPolicy {
	case "least_ips":
		eniSelectionPolicy = daemon.EniSelectionPolicyLeastIPs
	}

	eniConfig := &daemon.ENIConfig{
		ZoneID:                 zoneID,
		VSwitchOptions:         nil,
		ENITags:                cfg.ENITags,
		SecurityGroupIDs:       cfg.GetSecurityGroups(),
		VSwitchSelectionPolicy: vswitchSelectionPolicy,
		EniSelectionPolicy:     eniSelectionPolicy,
		ResourceGroupID:        cfg.ResourceGroupID,
		EniTypeAttr:            0,
		TagFilter:              cfg.ENITagFilter,
	}

	if cfg.VSwitches != nil {
		zoneVswitchs, ok := cfg.VSwitches[eniConfig.ZoneID]
		if ok && len(zoneVswitchs) > 0 {
			eniConfig.VSwitchOptions = cfg.VSwitches[eniConfig.ZoneID]
		}
	}

	if cfg.EnableENITrunking {
		daemon.EnableFeature(&eniConfig.EniTypeAttr, daemon.FeatTrunk)
	}
	if cfg.EnableERDMA {
		daemon.EnableFeature(&eniConfig.EniTypeAttr, daemon.FeatERDMA)
	}

	return eniConfig
}

// the actual size for pool is minIdle and maxIdle
func getPoolConfig(cfg *daemon.Config, daemonMode string, limit *client.Limits) (*daemon.PoolConfig, error) {

	poolConfig := &daemon.PoolConfig{
		BatchSize: 10,
	}

	if cfg.ENITags == nil {
		cfg.ENITags = make(map[string]string)
	}
	cfg.ENITags[types.NetworkInterfaceTagCreatorKey] = types.NetworkInterfaceTagCreatorValue

	capacity := 0
	maxENI := 0
	maxMemberENI := 0

	switch daemonMode {
	case daemon.ModeENIOnly:
		maxENI = limit.Adapters
		maxENI = int(float64(maxENI)*cfg.EniCapRatio) + cfg.EniCapShift - 1

		// set max eni node can use
		if cfg.MaxENI > 0 && cfg.MaxENI < maxENI {
			maxENI = cfg.MaxENI
		}

		capacity = maxENI

		if cfg.MaxPoolSize > maxENI {
			poolConfig.MaxPoolSize = maxENI
		} else {
			poolConfig.MaxPoolSize = cfg.MaxPoolSize
		}

		poolConfig.MinPoolSize = cfg.MinPoolSize

		if cfg.MinENI > 0 {
			poolConfig.MinPoolSize = cfg.MinENI
		}

		if poolConfig.MinPoolSize > poolConfig.MaxPoolSize {
			poolConfig.MinPoolSize = poolConfig.MaxPoolSize
		}

		poolConfig.MaxIPPerENI = 1
		if cfg.EnableERDMA {
			poolConfig.ERdmaCapacity = limit.ERdmaAdapters
		}
		poolConfig.MaxPoolSize = 0
		poolConfig.MinPoolSize = 0
	case daemon.ModeENIMultiIP:
		maxENI = limit.Adapters
		maxENI = int(float64(maxENI)*cfg.EniCapRatio) + cfg.EniCapShift - 1

		// set max eni node can use
		if cfg.MaxENI > 0 && cfg.MaxENI < maxENI {
			maxENI = cfg.MaxENI
		}

		ipPerENI := limit.IPv4PerAdapter
		if utils.IsWindowsOS() {
			// NB(thxCode): don't assign the primary IP of one assistant eni.
			ipPerENI--
		}

		capacity = maxENI * ipPerENI
		if cfg.MaxPoolSize > capacity {
			poolConfig.MaxPoolSize = capacity
		} else {
			poolConfig.MaxPoolSize = cfg.MaxPoolSize
		}

		poolConfig.MinPoolSize = cfg.MinPoolSize

		if cfg.MinENI > 0 {
			poolConfig.MinPoolSize = cfg.MinENI * ipPerENI
		}
		if poolConfig.MinPoolSize > poolConfig.MaxPoolSize {
			poolConfig.MinPoolSize = poolConfig.MaxPoolSize
		}

		maxMemberENI = limit.MemberAdapterLimit

		poolConfig.MaxIPPerENI = ipPerENI

		if cfg.EnableERDMA {
			poolConfig.ERdmaCapacity = limit.ERDMARes() * limit.IPv4PerAdapter
		}
	}

	if cfg.IPAMType == types.IPAMTypeCRD {
		poolConfig.MaxPoolSize = 0
		poolConfig.MinPoolSize = 0
	}

	poolConfig.Capacity = capacity
	poolConfig.MaxENI = maxENI
	poolConfig.MaxMemberENI = maxMemberENI

	poolConfig.ReclaimBatchSize = cfg.IdleIPReclaimBatchSize
	if cfg.IdleIPReclaimInterval != nil {
		interval, err := time.ParseDuration(*cfg.IdleIPReclaimInterval)
		if err != nil {
			return nil, err
		}
		poolConfig.ReclaimInterval = interval
	}
	if cfg.IdleIPReclaimAfter != nil {
		after, err := time.ParseDuration(*cfg.IdleIPReclaimAfter)
		if err != nil {
			return nil, err
		}
		poolConfig.ReclaimAfter = after
	}
	if cfg.IdleIPReclaimJitterFactor != nil {
		factor, err := strconv.ParseFloat(*cfg.IdleIPReclaimJitterFactor, 64)
		if err != nil {
			return nil, err
		}
		poolConfig.ReclaimFactor = factor
	}

	return poolConfig, nil
}
