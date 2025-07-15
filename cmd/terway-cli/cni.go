package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Jeffail/gabs/v2"
	"github.com/docker/docker/pkg/parsers/kernel"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	cliflag "k8s.io/component-base/cli/flag"

	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
)

type checkKernelVersionFunc func(int, int, int) bool

var _checkKernelVersion checkKernelVersionFunc

type switchDataPathV2Func func() bool

var _switchDataPathV2 switchDataPathV2Func

const (
	dataPathDefault        = ""
	dataPathVeth           = "veth"
	dataPathIPvlan         = "ipvlan"
	dataPathV2             = "datapathv2"
	nodeCapabilityDatapath = "datapath"
)

const (
	NetworkPolicyProviderIpt  = "iptables"
	NetworkPolicyProviderEBPF = "ebpf"
)

type feature struct {
	EBPF bool
	EDT  bool
}

var (
	outPutPath string

	featureGates map[string]bool
)

func init() {
	fs := cniCmd.Flags()
	fs.StringVar(&outPutPath, "output", "", "output path")
	fs.Var(cliflag.NewMapStringBool(&featureGates), "feature-gates", "A set of key=value pairs that describe feature gates for alpha/experimental features. "+
		"Options are:\n"+strings.Join(utilfeature.DefaultFeatureGate.KnownFeatures(), "\n"))
}

var cniCmd = &cobra.Command{
	Use:          "cni",
	SilenceUsage: true,
	Args:         cobra.MinimumNArgs(1),
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

	_checkKernelVersion = kernel.CheckKernelVersion

	_switchDataPathV2 = switchDataPathV2

	err := utilfeature.DefaultMutableFeatureGate.SetFromMap(featureGates)
	if err != nil {
		return fmt.Errorf("failed to set feature gates: %v", err)
	}

	err = processInput(args)
	if err != nil {
		return fmt.Errorf("failed process input: %v", err)
	}

	// mount bpf fs if needed
	cni, err := os.ReadFile(outPutPath)
	if err != nil {
		return err
	}
	cniJSON, err := gabs.ParseJSON(cni)
	if err != nil {
		return err
	}
	for _, plugin := range cniJSON.Path("plugins").Children() {
		if plugin.Path("type").Data().(string) == "cilium-cni" {
			err = mountHostBpf()
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func processInput(files []string) error {
	var configs [][]byte
	for _, file := range files {
		out, err := os.ReadFile(file)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		c, err := gabs.ParseJSON(out)
		if err != nil {
			return err
		}
		if c.Exists("plugins") {
			for _, cc := range c.Path("plugins").Children() {
				configs = append(configs, cc.Bytes())
			}
		} else {
			configs = append(configs, out)
		}
		break
	}

	var err error
	f := feature{}
	f.EBPF = _checkKernelVersion(4, 19, 0)

	if f.EBPF {
		f.EDT, err = checkBpfFeature("bpf_skb_ecn_set_ce")
		if err != nil {
			return err
		}
	}
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
		case "cilium-cni":
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

		case "terway":
			if plugin.Exists("network_policy_provider") {
				networkPolicyProvider, ok = plugin.Path("network_policy_provider").Data().(string)
				if !ok {
					return "", fmt.Errorf("network_policy_provider type error")
				}
			}
			if plugin.Exists("eniip_virtual_type") {
				virtualType, ok := plugin.Path("eniip_virtual_type").Data().(string)
				if !ok {
					return "", fmt.Errorf("eniip_virtual_type not found")
				}
				if !ebpfSupport {
					err = plugin.Delete("eniip_virtual_type")
					if err != nil {
						return "", err
					}
				} else {
					requireIPvlan := false

					switch strings.ToLower(virtualType) {
					case dataPathVeth, dataPathDefault:
						// only for terway-eniip
						if ebpfSupport && networkPolicyProvider == NetworkPolicyProviderEBPF {
							requireEBPFChainer = true
							datapath = dataPathVeth
						}
					case dataPathIPvlan:
						requireIPvlan = true
						datapath = dataPathIPvlan

						fallthrough
					case dataPathV2:
						requireEBPFChainer = true

						if requireIPvlan && !_switchDataPathV2() {
							fmt.Printf("keep ipvlan mode %v %v\n", requireIPvlan, !_switchDataPathV2())
							_, err = plugin.Set("IPVlan", "eniip_virtual_type")
							if err != nil {
								return "", err
							}
						} else {
							fmt.Printf("datapathv2 enabled\n")
							_, err = plugin.Set(dataPathV2, "eniip_virtual_type")
							if err != nil {
								return "", err
							}

							datapath = dataPathV2

							err = nodecap.WriteNodeCapabilities(nodeCapabilityDatapath, dataPathV2)
							if err != nil {
								return "", err
							}
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
			} else {
				if ebpfSupport && networkPolicyProvider == NetworkPolicyProviderEBPF {
					requireEBPFChainer = true
					datapath = dataPathVeth
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
