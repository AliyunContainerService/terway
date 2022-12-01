package types

// PoolConfig configuration of pool and resource factory
type PoolConfig struct {
	MaxPoolSize               int
	MinPoolSize               int
	MinENI                    int
	MaxENI                    int
	VPC                       string
	Zone                      string
	VSwitch                   []string
	ENITags                   map[string]string
	SecurityGroups            []string
	InstanceID                string
	AccessID                  string
	AccessSecret              string
	EniCapRatio               float64
	EniCapShift               int
	VSwitchSelectionPolicy    string
	EnableENITrunking         bool
	ENICapPolicy              ENICapPolicy
	DisableDevicePlugin       bool
	WaitTrunkENI              bool
	DisableSecurityGroupCheck bool
}
