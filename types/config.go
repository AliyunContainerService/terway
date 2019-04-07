package types

import "github.com/denverdino/aliyungo/common"

type Configure struct {
	Version       string              `yaml:"version" json:"version"`
	AccessId      string              `yaml:"access_key" json:"access_key"`
	AccessSecret  string              `yaml:"access_secret" json:"access_secret"`
	ServiceCIDR   string              `yaml:"service_cidr" json:"service_cidr"`
	VSwitches     map[string][]string `yaml:"vswitches" json:"vswitches"`
	MaxPoolSize   int                 `yaml:"max_pool_size" json:"max_pool_size"`
	MinPoolSize   int                 `yaml:"min_pool_size" json:"min_pool_size"`
	Prefix        string              `yaml:"prefix" json:"prefix"`
	SecurityGroup string              `yaml:"security_group" json:"security_group"`
	HotPlug       string              `yaml:"hot_plug" json:"hot_plug"`
	EniCapRatio   float64             `yaml:"eni_cap_ratio" json:"eni_cap_ratio"`
	EniCapShift   int                 `yaml:"eni_cap_shift" json:"eni_cap_shift"`
}

type PoolConfig struct {
	MaxPoolSize   int
	MinPoolSize   int
	VPC           string
	Zone          string
	VSwitch       []string
	Region        common.Region
	SecurityGroup string
	InstanceID    string
	AccessId      string
	AccessSecret  string
	HotPlug       bool
	EniCapRatio   float64
	EniCapShift   int
}
