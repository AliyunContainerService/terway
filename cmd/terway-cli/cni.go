package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Jeffail/gabs/v2"
	"github.com/docker/docker/pkg/parsers/kernel"
	"github.com/spf13/cobra"
)

type feature struct {
	EBPF bool
	EDT  bool
}

var outPutPath string

func init() {
	fs := cniCmd.Flags()
	fs.StringVar(&outPutPath, "output", "", "output path")
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

	err := processInput(args)
	if err != nil {
		return fmt.Errorf("failed process input: %v", err)
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
	f.EBPF = kernel.CheckKernelVersion(4, 19, 0)

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

	hasIPvlan := false
	requireIPvlan := false

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
			if !ebpfSupport || !requireIPvlan {
				continue
			}
			hasIPvlan = true
		case "terway":
			if plugin.Exists("eniip_virtual_type") {
				virtualType, ok := plugin.Path("eniip_virtual_type").Data().(string)
				if !ok {
					return "", fmt.Errorf("eniip_virtual_type not found")
				}
				if strings.ToLower(virtualType) == "ipvlan" {
					requireIPvlan = true

					if !ebpfSupport {
						err = plugin.Delete("eniip_virtual_type")
						if err != nil {
							return "", err
						}
					} else {
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
			}
		}

		err = g.ArrayConcat(plugin.Data(), "plugins")
		if err != nil {
			return "", err
		}
	}

	if ebpfSupport && requireIPvlan && !hasIPvlan {
		err = g.ArrayAppend(map[string]string{"type": "cilium-cni"}, "plugins")
		if err != nil {
			return "", err
		}
	}

	return g.StringIndent("", "  "), nil
}
