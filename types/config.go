package types

import (
	"github.com/AliyunContainerService/terway/pkg/vswitch"
)

type ENIConfig struct {
	ZoneID           string
	VSwitchOptions   []string
	ENITags          map[string]string
	SecurityGroupIDs []string
	InstanceID       string

	VSwitchSelectionPolicy vswitch.SelectionPolicy

	ResourceGroupID string

	EniTypeAttr Feat

	EnableIPv4 bool
	EnableIPv6 bool
}

// PoolConfig configuration of pool and resource factory
type PoolConfig struct {
	EnableIPv4 bool
	EnableIPv6 bool

	Capacity      int // the max res can hold in the pool
	MaxENI        int // the max eni terway can be created (already exclude main eni)
	MaxMemberENI  int // the max member eni can be created
	ERdmaCapacity int // the max erdma res can be created
	MaxIPPerENI   int
	BatchSize     int

	MaxPoolSize int
	MinPoolSize int
}

type Feat uint8

const (
	FeatTrunk Feat = 1 << iota
	FeatERDMA
)

func EnableFeature(features *Feat, feature Feat) {
	*features |= feature
}

func DisableFeature(features *Feat, feature Feat) {
	*features &= ^feature
}

func IsFeatureEnabled(features Feat, feature Feat) bool {
	return features&feature != 0
}
