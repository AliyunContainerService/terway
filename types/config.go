package types

type Configure struct {
	Version       string `yaml:"version" json:"version"`
	AccessId      string `yaml:"access_key" json:"access_key"` //TODO 使用instance profile
	AccessSecret  string `yaml:"access_secret" json:"access_secret"`
	ServiceCIDR   string `yaml:"service_cidr" json:"service_cidr"` //TODO from cluster-info?
	VSwitches     map[string]string
	MaxPoolSize   int    `yaml:"max_pool_size" json:"max_pool_size"`
	MinPoolSize   int    `yaml:"min_pool_size" json:"min_pool_size"`
	Prefix        string `yaml:"prefix" json:"prefix"`
	SecurityGroup string `yaml:"security_group" json:"security_group"`
	HotPlug       string `yaml:"hot_plug" json:"hot_plug"`
}

type PoolConfig struct {
	MaxPoolSize   int
	MinPoolSize   int
	VPC           string
	Zone          string
	VSwitch       string
	Region        string
	SecurityGroup string
	InstanceID    string
	AccessId      string
	AccessSecret  string
	HotPlug       bool
}
