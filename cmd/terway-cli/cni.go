package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Jeffail/gabs/v2"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
)

type checkKernelVersionFunc func(int, int, int) bool

var _checkKernelVersion checkKernelVersionFunc

type switchDataPathV2Func func() bool

var _switchDataPathV2 switchDataPathV2Func

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

	input := cm.cniConfig
	if cm.cniConfigList != nil {
		input = cm.cniConfigList
	}

	var configs [][]byte
	c, err := gabs.ParseJSON(input)
	if err != nil {
		return err
	}
	if c.Exists("plugins") {
		for _, cc := range c.Path("plugins").Children() {
			configs = append(configs, cc.Bytes())
		}
	} else {
		configs = append(configs, input)
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
					datapath = dataPathIPvlan

					if _switchDataPathV2() {
						datapath = dataPathV2
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

func mountHostBpf() error {
	target := "/sys/fs/bpf"

	// 确保目标目录存在
	err := os.MkdirAll(target, 0755)
	if err != nil {
		return fmt.Errorf("failed to create mount point: %v", err)
	}

	// 检查是否已挂载
	mounted, err := isMounted(target)
	if err != nil {
		return fmt.Errorf("failed to check mount status: %v", err)
	}
	if mounted {
		return nil
	}

	// 执行 mount bpffs /sys/fs/bpf -t bpf
	err = unix.Mount("bpffs", target, "bpf", 0, "")
	if err != nil {
		return fmt.Errorf("mount failed: %v", err)
	}

	return nil
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
