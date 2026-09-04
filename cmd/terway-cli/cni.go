package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	"github.com/Jeffail/gabs/v2"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	k8sClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/AliyunContainerService/terway/pkg/version"
	"github.com/AliyunContainerService/terway/types"
)

type checkKernelVersionFunc func(int, int, int) bool

var _checkKernelVersion checkKernelVersionFunc

type switchDataPathV2Func func() bool

var _switchDataPathV2 switchDataPathV2Func

type detectMTUFunc func() int

var _detectMTU detectMTUFunc

const defaultMTU = 1500

const (
	dataPathDefault = ""
	dataPathVeth    = "veth"
	dataPathIPvlan  = "ipvlan"
	dataPathV2      = "datapathv2"
)

const (
	NetworkPolicyProviderIpt  = "iptables"
	NetworkPolicyProviderEBPF = "ebpf"
)

type feature struct {
	EBPF bool
	EDT  bool

	EnableNetworkPolicy bool
}

var (
	outPutPath string

	featureGates map[string]bool
)

func init() {
	fs := cniCmd.Flags()
	fs.StringVar(&outPutPath, "output", "", "output path")
}

var cniCmd = &cobra.Command{
	Use:          "cni",
	SilenceUsage: true,
	Run: func(cmd *cobra.Command, args []string) {
		err := processCNIConfig(cmd, args)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	},
}

func processCNIConfig(cmd *cobra.Command, args []string) error {
	flag.Parse()

	_checkKernelVersion = checkKernelVersion

	_switchDataPathV2 = switchDataPathV2

	_detectMTU = detectMTU

	err := processInput()
	if err != nil {
		return fmt.Errorf("failed process input: %v", err)
	}

	cni, err := os.ReadFile(outPutPath)
	if err != nil {
		return err
	}
	cniJSON, err := gabs.ParseJSON(cni)
	if err != nil {
		return err
	}

	return storeRuntimeConfig(nodeCapabilitiesFile, cniJSON)
}

func processInput() error {
	cm, err := getAllConfig(eniConfBasePath)
	if err != nil {
		return err
	}

	chain, chainSet, err := resolveCNIChain(cm)
	if err != nil {
		return err
	}
	configs, err := buildInputConfigs(cm, chain, chainSet)
	if err != nil {
		return err
	}

	if !_checkKernelVersion(5, 10, 0) {
		return fmt.Errorf("unsupport kernel version, require >=5.10")
	}

	f := feature{}
	f.EBPF = _checkKernelVersion(4, 19, 0)

	if f.EBPF {
		f.EDT, err = checkBpfFeature("bpf_skb_ecn_set_ce")
		if err != nil {
			return err
		}
	}

	f.EnableNetworkPolicy = cm.enableNetworkPolicy

	out, err := mergeConfigList(configs, &f)
	if err != nil {
		return err
	}

	return os.WriteFile(outPutPath, []byte(out), 0644)
}

func resolveCNIChain(cm *TerwayConfig) ([]byte, bool, error) {
	chain, set := cm.cniChain, cm.cniChainSet
	nodeName := os.Getenv("K8S_NODE_NAME")
	if nodeName == "" {
		return chain, set, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dynamicChain, dynamicSet, err := getNodeCNIChain(ctx, nodeName)
	if err != nil {
		return nil, false, err
	}
	if dynamicSet {
		return dynamicChain, true, nil
	}
	return chain, set, nil
}

func getNodeCNIChain(ctx context.Context, nodeName string) ([]byte, bool, error) {
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return nil, false, fmt.Errorf("get kubernetes config: %w", err)
	}
	restConfig.UserAgent = version.UA
	c, err := k8sClient.New(restConfig, k8sClient.Options{Scheme: types.Scheme, Mapper: types.NewRESTMapper()})
	if err != nil {
		return nil, false, fmt.Errorf("create kubernetes client: %w", err)
	}
	return getNodeCNIChainWithClient(ctx, c, nodeName)
}

func getNodeCNIChainWithClient(ctx context.Context, c k8sClient.Client, nodeName string) ([]byte, bool, error) {
	node := &corev1.Node{}
	if err := c.Get(ctx, k8sClient.ObjectKey{Name: nodeName}, node, &k8sClient.GetOptions{Raw: &metav1.GetOptions{ResourceVersion: "0"}}); err != nil {
		return nil, false, fmt.Errorf("get node %s: %w", nodeName, err)
	}
	configName := node.Labels["terway-config"]
	if configName == "" {
		return nil, false, nil
	}

	configMap := &corev1.ConfigMap{}
	if err := c.Get(ctx, k8sClient.ObjectKey{Namespace: "kube-system", Name: configName}, configMap); err != nil {
		return nil, false, fmt.Errorf("get node cni config kube-system/%s: %w", configName, err)
	}
	value, ok := configMap.Data["cni_chain"]
	return []byte(value), ok, nil
}

func buildInputConfigs(cm *TerwayConfig, chain []byte, chainSet bool) ([][]byte, error) {
	if chainSet {
		plugins, err := parseCNIChain(chain)
		if err != nil {
			return nil, fmt.Errorf("parse cni_chain: %w", err)
		}
		return append([][]byte{cm.cniConfig}, plugins...), nil
	}

	input := cm.cniConfig
	if cm.cniConfigList != nil {
		input = cm.cniConfigList
	}
	c, err := gabs.ParseJSON(input)
	if err != nil {
		return nil, err
	}
	if !c.Exists("plugins") {
		return [][]byte{input}, nil
	}
	var configs [][]byte
	for _, plugin := range c.Path("plugins").Children() {
		configs = append(configs, plugin.Bytes())
	}
	return configs, nil
}

func parseCNIChain(value []byte) ([][]byte, error) {
	if len(value) > 64*1024 {
		return nil, fmt.Errorf("is larger than 64 KiB")
	}
	jsonValue, err := yaml.YAMLToJSON(value)
	if err != nil {
		return nil, err
	}
	if len(jsonValue) == 0 || jsonValue[0] != '[' {
		return nil, fmt.Errorf("must be a list of CNI plugin objects")
	}
	var plugins []json.RawMessage
	if err = json.Unmarshal(jsonValue, &plugins); err != nil {
		return nil, fmt.Errorf("must be a list of CNI plugin objects: %w", err)
	}
	if len(plugins) > 16 {
		return nil, fmt.Errorf("contains %d plugins, maximum is 16", len(plugins))
	}
	result := make([][]byte, 0, len(plugins))
	for i, raw := range plugins {
		var plugin map[string]any
		if err = json.Unmarshal(raw, &plugin); err != nil {
			return nil, fmt.Errorf("plugin %d must be an object: %w", i, err)
		}
		pluginType, ok := plugin["type"].(string)
		if !ok || pluginType == "" {
			return nil, fmt.Errorf("plugin %d has no non-empty type", i)
		}
		if pluginType == pluginTypeTerway {
			return nil, fmt.Errorf("plugin %d must not use type %q", i, pluginTypeTerway)
		}
		for _, field := range []string{"name", "cniVersion", "plugins"} {
			if _, exists := plugin[field]; exists {
				return nil, fmt.Errorf("plugin %d must not set %q", i, field)
			}
		}
		result = append(result, raw)
	}
	return result, nil
}

func checkBpfFeature(key string) (bool, error) {
	out, err := exec.Command("bpftool", "-j", "feature", "probe").CombinedOutput()
	if err != nil {
		return false, err
	}

	return strings.Contains(string(out), key), nil
}

func mergeConfigList(configs [][]byte, f *feature) (string, error) {
	ebpfSupport := f.EBPF
	edtSupport := f.EDT

	var err error

	g := gabs.New()
	_, err = g.Set("0.4.0", "cniVersion")
	if err != nil {
		return "", err
	}
	_, err = g.Set("terway-chainer", "name")
	if err != nil {
		return "", err
	}

	requireEBPFChainer := false
	ebpfChainerExist := false
	datapath := ""

	networkPolicyProvider := NetworkPolicyProviderIpt

	for _, config := range configs {
		plugin, err := gabs.ParseJSON(config)
		if err != nil {
			return "", err
		}
		_ = plugin.Delete("cniVersion")
		_ = plugin.Delete("name")

		pluginType, ok := plugin.Path("type").Data().(string)
		if !ok {
			return "", fmt.Errorf("type not found")
		}

		switch pluginType {
		case pluginTypeCilium:
			// make sure cilium-cni is behind terway
			if !ebpfSupport {
				continue
			}
			requireEBPFChainer = true
			ebpfChainerExist = true

			_, err = plugin.Set(datapath, "datapath")
			if err != nil {
				return "", err
			}

		case pluginTypeTerway:
			if plugin.Exists("network_policy_provider") {
				networkPolicyProvider, ok = plugin.Path("network_policy_provider").Data().(string)
				if !ok {
					return "", fmt.Errorf("network_policy_provider type error")
				}
			}

			virtualType, ok := plugin.Path("eniip_virtual_type").Data().(string)
			if !ok {
				virtualType = dataPathVeth
			}
			if !ebpfSupport {
				_ = plugin.Delete("eniip_virtual_type")
			} else {
				switch strings.ToLower(virtualType) {
				case dataPathVeth, dataPathDefault:
					datapath = dataPathVeth

					if ebpfSupport && networkPolicyProvider == NetworkPolicyProviderEBPF {
						allow, err := allowEBPFNetworkPolicy(f.EnableNetworkPolicy)
						if err != nil {
							return "", err
						}
						can, err := canUseHostRouting()
						if err != nil {
							return "", err
						}
						if allow && can {
							datapath = dataPathV2
						}
					}
				case dataPathIPvlan:
					if _switchDataPathV2() {
						datapath = dataPathV2
					} else {
						return "", fmt.Errorf("ipvlan is unsupported")
					}
				case dataPathV2:
					datapath = dataPathV2
				}

				switch datapath {
				case dataPathVeth:
					requireEBPFChainer = false
					edtSupport = false

					// special case
					ok, err := hasCilium()
					if err != nil {
						return "", err
					}
					if ok {
						requireEBPFChainer = true
					}

					_, err = plugin.Set(dataPathVeth, "eniip_virtual_type")
					if err != nil {
						return "", err
					}
				case dataPathIPvlan:
					requireEBPFChainer = true
					_, err = plugin.Set(dataPathIPvlan, "eniip_virtual_type")
					if err != nil {
						return "", err
					}
				case dataPathV2:
					requireEBPFChainer = true
					_, err = plugin.Set(dataPathV2, "eniip_virtual_type")
					if err != nil {
						return "", err
					}
				default:
					return "", fmt.Errorf("invalid datapath %s", datapath)
				}

				if edtSupport {
					_, err = plugin.Set("edt", "bandwidth_mode")
				} else {
					_, err = plugin.Set("tc", "bandwidth_mode")
				}
				if err != nil {
					return "", err
				}
			}

			if autoMTU, ok := plugin.Path("auto_mtu").Data().(bool); ok {
				_ = plugin.Delete("auto_mtu")
				if autoMTU && _detectMTU != nil {
					if err = applyMTU(plugin, _detectMTU()); err != nil {
						return "", err
					}
				}
			}
		}

		err = g.ArrayConcat(plugin.Data(), "plugins")
		if err != nil {
			return "", err
		}
	}

	if ebpfSupport && requireEBPFChainer && !ebpfChainerExist {
		err = g.ArrayAppend(map[string]any{"type": "cilium-cni", "enable-debug": false, "log-file": "/var/run/cilium/cilium-cni.log", "data-path": datapath}, "plugins")
		if err != nil {
			return "", err
		}
	}

	return g.StringIndent("", "  "), nil
}

// applyMTU sets the mtu field on the plugin when it is not already set and
// the detected mtu is positive.
func applyMTU(plugin *gabs.Container, mtu int) error {
	if plugin.Exists("mtu") || mtu <= 0 {
		return nil
	}
	_, err := plugin.Set(mtu, "mtu")
	return err
}

// readAutoMTUFromConfig reads the auto_mtu setting from the terway plugin
// in the CNI config file at the given base path.
func readAutoMTUFromConfig(basePath string) bool {
	cfg, err := getAllConfig(basePath)
	if err != nil {
		return false
	}
	input := cfg.cniConfig
	if cfg.cniConfigList != nil {
		input = cfg.cniConfigList
	}
	c, err := gabs.ParseJSON(input)
	if err != nil {
		return false
	}

	var plugins []*gabs.Container
	if c.Exists("plugins") {
		plugins = c.Path("plugins").Children()
	} else {
		plugins = []*gabs.Container{c}
	}
	for _, plugin := range plugins {
		if pluginType, ok := plugin.Path("type").Data().(string); ok && pluginType == pluginTypeTerway {
			if autoMTU, ok := plugin.Path("auto_mtu").Data().(bool); ok {
				return autoMTU
			}
		}
	}
	return false
}

func isMounted(path string) (bool, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), " ")
		if len(fields) >= 2 && fields[1] == path {
			return true, nil
		}
	}
	return false, nil
}

func storeRuntimeConfig(filePath string, container *gabs.Container) error {
	store := nodecap.NewFileNodeCapabilities(filePath)
	err := store.Load()
	if err != nil {
		return err
	}

	hasCilium := false
	// write back current runtime config
	for _, plugin := range container.Path("plugins").Children() {
		pluginType, ok := plugin.Path("type").Data().(string)
		if !ok {
			return fmt.Errorf("type must be string")
		}
		switch pluginType {
		case pluginTypeCilium:
			// mount bpf fs if needed

			err = mountHostBpf()
			if err != nil {
				return err
			}
			hasCilium = true
		case pluginTypeTerway:
			if plugin.Exists("network_policy_provider") {
				networkPolicyProvider := plugin.Path("network_policy_provider").Data().(string)
				store.Set(nodecap.NodeCapabilityNetworkPolicyProvider, networkPolicyProvider)
			}
			if plugin.Exists("eniip_virtual_type") {
				datapath := plugin.Path("eniip_virtual_type").Data().(string)
				store.Set(nodecap.NodeCapabilityDataPath, datapath)
			}
		}
	}
	if hasCilium {
		store.Set(nodecap.NodeCapabilityHasCiliumChainer, True)
	} else {
		store.Set(nodecap.NodeCapabilityHasCiliumChainer, False)
	}

	return store.Save()
}
