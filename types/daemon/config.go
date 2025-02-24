package daemon

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types/secret"

	jsonpatch "github.com/evanphx/json-patch"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/route"
)

const (
	addonSecretPath      = "/var/alibaba-addon-secret"
	addonSecretKeyID     = "access-key-id"
	addonSecretKeySecret = "access-key-secret"
)

var addonSecretRootPath = addonSecretPath

// Config configuration of terway daemon
type Config struct {
	Version                     string                  `yaml:"version" json:"version"`
	AccessID                    secret.Secret           `yaml:"access_key" json:"access_key"`
	AccessSecret                secret.Secret           `yaml:"access_secret" json:"access_secret"`
	RegionID                    string                  `yaml:"region_id" json:"region_id"`
	CredentialPath              string                  `yaml:"credential_path" json:"credential_path"`
	ServiceCIDR                 string                  `yaml:"service_cidr" json:"service_cidr"`
	VSwitches                   map[string][]string     `yaml:"vswitches" json:"vswitches"`
	ENITags                     map[string]string       `yaml:"eni_tags" json:"eni_tags"`
	MaxPoolSize                 int                     `yaml:"max_pool_size" json:"max_pool_size"`
	MinPoolSize                 int                     `yaml:"min_pool_size" json:"min_pool_size"`
	MinENI                      int                     `yaml:"min_eni" json:"min_eni"`
	MaxENI                      int                     `yaml:"max_eni" json:"max_eni"`
	Prefix                      string                  `yaml:"prefix" json:"prefix"`
	SecurityGroup               string                  `yaml:"security_group" json:"security_group"`
	SecurityGroups              []string                `yaml:"security_groups" json:"security_groups"`
	EniCapRatio                 float64                 `yaml:"eni_cap_ratio" json:"eni_cap_ratio" mod:"default=1"`
	EniCapShift                 int                     `yaml:"eni_cap_shift" json:"eni_cap_shift"`
	VSwitchSelectionPolicy      string                  `yaml:"vswitch_selection_policy" json:"vswitch_selection_policy" mod:"default=random"`
	EniSelectionPolicy          string                  `yaml:"eni_selection_policy" json:"eni_selection_policy" mod:"default=most_ips"`
	IPStack                     string                  `yaml:"ip_stack" json:"ip_stack" validate:"oneof=ipv4 ipv6 dual" mod:"default=ipv4"` // default ipv4 , support ipv4 dual
	EnableENITrunking           bool                    `yaml:"enable_eni_trunking" json:"enable_eni_trunking"`
	EnableERDMA                 bool                    `yaml:"enable_erdma" json:"enable_erdma"`
	CustomStatefulWorkloadKinds []string                `yaml:"custom_stateful_workload_kinds" json:"custom_stateful_workload_kinds"`
	IPAMType                    types.IPAMType          `yaml:"ipam_type" json:"ipam_type"` // crd or default
	BackoffOverride             map[string]wait.Backoff `json:"backoff_override,omitempty"`
	ExtraRoutes                 []route.Route           `json:"extra_routes,omitempty"`
	DisableDevicePlugin         bool                    `json:"disable_device_plugin"`
	ENITagFilter                map[string]string       `json:"eni_tag_filter"` // if set , only enis match filter, will be managed
	KubeClientQPS               float32                 `json:"kube_client_qps"`
	KubeClientBurst             int                     `json:"kube_client_burst"`
	ResourceGroupID             string                  `json:"resource_group_id"`
	RateLimit                   map[string]int          `json:"rate_limit"`
	EnablePatchPodIPs           *bool                   `json:"enable_patch_pod_ips,omitempty"  mod:"default=true"`
}

func (c *Config) GetSecurityGroups() []string {
	sgIDs := sets.NewString()
	if c.SecurityGroup != "" {
		sgIDs.Insert(c.SecurityGroup)
	}
	sgIDs.Insert(c.SecurityGroups...)
	return sgIDs.List()
}

func (c *Config) GetVSwitchIDs() []string {
	var vsws []string
	for _, ids := range c.VSwitches {
		vsws = append(vsws, ids...)
	}
	return vsws
}

func (c *Config) GetExtraRoutes() []string {
	var vsws []string
	for _, ids := range c.VSwitches {
		vsws = append(vsws, ids...)
	}
	return vsws
}

func (c *Config) Populate() {
	if c.EniCapRatio == 0 {
		c.EniCapRatio = 1
	}

	if c.VSwitchSelectionPolicy == "" {
		c.VSwitchSelectionPolicy = VSwitchSelectionPolicyRandom
	}

	if c.IPStack == "" {
		c.IPStack = string(types.IPStackIPv4)
	}

	if c.EnablePatchPodIPs == nil {
		enable := true
		c.EnablePatchPodIPs = &enable
	}
}

func (c *Config) Validate() error {
	switch c.IPStack {
	case "", string(types.IPStackIPv4), string(types.IPStackDual):
	default:
		return fmt.Errorf("unsupported ipStack %s in configMap", c.IPStack)
	}

	if len(c.SecurityGroups) > 5 {
		return fmt.Errorf("security groups should not be more than 5, current %d", len(c.SecurityGroups))
	}

	return nil
}

// GetConfigFromFileWithMerge parse Config from file
func GetConfigFromFileWithMerge(filePath string, cfg []byte) (*Config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	config, err := MergeConfigAndUnmarshal(cfg, data)
	if err != nil {
		return nil, err
	}

	ak, sk, err := GetAddonSecret()
	if err != nil {
		return nil, err
	}
	if ak != "" && sk != "" {
		config.AccessID = secret.Secret(ak)
		config.AccessSecret = secret.Secret(sk)
	}

	return config, nil
}

func MergeConfigAndUnmarshal(topCfg, baseCfg []byte) (*Config, error) {
	if len(topCfg) == 0 { // no topCfg, unmarshal baseCfg and return
		config := &Config{}
		err := json.Unmarshal(baseCfg, config)
		return config, err
	}

	// MergePatch in RFC7396
	jsonBytes, err := jsonpatch.MergePatch(baseCfg, topCfg)
	if err != nil {
		return nil, err
	}

	config := &Config{}
	err = json.Unmarshal(jsonBytes, config)

	return config, err
}

// GetAddonSecret return ak/sk from file, return nil if not present.
func GetAddonSecret() (string, string, error) {
	keyID, err := os.ReadFile(filepath.Join(addonSecretRootPath, addonSecretKeyID))
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil
		}
		return "", "", err
	}
	keySecret, err := os.ReadFile(filepath.Join(addonSecretRootPath, addonSecretKeySecret))
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil
		}
		return "", "", err
	}
	return string(keyID), string(keySecret), nil
}

type EniSelectionPolicy string

// Network interface Selection Policy
const (
	EniSelectionPolicyLeastIPs EniSelectionPolicy = "least_ips"
	EniSelectionPolicyMostIPs  EniSelectionPolicy = "most_ips"
)

type ENIConfig struct {
	ZoneID           string
	VSwitchOptions   []string
	ENITags          map[string]string
	SecurityGroupIDs []string
	InstanceID       string

	VSwitchSelectionPolicy vswitch.SelectionPolicy
	EniSelectionPolicy     EniSelectionPolicy

	ResourceGroupID string

	EniTypeAttr Feat

	EnableIPv4 bool
	EnableIPv6 bool

	TagFilter map[string]string
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
