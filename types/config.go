package types

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

	ZoneID           string
	VSwitchOptions   []string
	ENITags          map[string]string
	SecurityGroupIDs []string
	InstanceID       string

	VSwitchSelectionPolicy string

	DisableSecurityGroupCheck bool

	TrunkENIID string

	ResourceGroupID string
}
