package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/Jeffail/gabs/v2"
	"github.com/samber/lo"
	"github.com/spf13/cobra"

	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	"github.com/AliyunContainerService/terway/types"
)

var readFunc func(name string) ([]byte, error)

type PolicyConfig struct {
	Datapath             string
	EnableNetworkPolicy  bool
	PolicyProvider       string
	ExclusiveENI         bool
	HealthCheckPort      string
	IPv6                 bool
	InClusterLoadBalance bool
	HasCiliumChainer     bool
	EnableKPR            bool
}

type CNIConfig struct {
	HubbleEnabled       string `json:"cilium_enable_hubble,omitempty"`
	HubbleMetrics       string `json:"cilium_hubble_metrics,omitempty"`
	HubbleListenAddress string `json:"cilium_hubble_listen_address,omitempty"`
	HubbleMetricServer  string `json:"cilium_hubble_metrics_server,omitempty"`
	CiliumExtraArgs     string `json:"cilium_args,omitempty"` // legacy way. should move to config map
}

var policyCmd = &cobra.Command{
	Use:          "policy",
	SilenceUsage: true,
	Run: func(cmd *cobra.Command, args []string) {
		readFunc = os.ReadFile

		err := initPolicy(cmd, args)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "failed to init policy: %v\n", err)
			os.Exit(1)
		}
	},
}

func getPolicyConfig(capFilePath string) (*PolicyConfig, error) {
	cfg := &PolicyConfig{}

	_, err := os.Stat(capFilePath)
	if err != nil {
		// file must exist
		return nil, err
	}

	store := nodecap.NewFileNodeCapabilities(capFilePath)
	err = store.Load()
	if err != nil {
		return nil, err
	}

	if store.Get(nodecap.NodeCapabilityIPv6) == True {
		cfg.IPv6 = true
	}

	if store.Get(nodecap.NodeCapabilityExclusiveENI) == string(types.ExclusiveENIOnly) {
		cfg.ExclusiveENI = true
	}
	cfg.Datapath = store.Get(nodecap.NodeCapabilityDataPath)
	cfg.PolicyProvider = store.Get(nodecap.NodeCapabilityNetworkPolicyProvider)
	cfg.HasCiliumChainer = store.Get(nodecap.NodeCapabilityHasCiliumChainer) == True
	cfg.EnableKPR = store.Get(nodecap.NodeCapabilityKubeProxyReplacement) == True

	cfg.HealthCheckPort = os.Getenv("FELIX_HEALTHPORT")
	if cfg.HealthCheckPort == "" {
		cfg.HealthCheckPort = "9099"
	}

	cm, err := getAllConfig(eniConfBasePath)
	if err != nil {
		return nil, err
	}
	cfg.EnableNetworkPolicy = cm.enableNetworkPolicy
	cfg.InClusterLoadBalance = cm.enableInClusterLB

	return cfg, nil
}

func initPolicy(cmd *cobra.Command, args []string) error {
	cfg, err := getPolicyConfig("/var-run-eni/node_capabilities")
	if err != nil {
		return err
	}
	if cfg.ExclusiveENI {
		return runExclusiveENI(cfg)
	}

	switch cfg.Datapath {
	case dataPathDefault, dataPathVeth:
		if cfg.PolicyProvider != NetworkPolicyProviderEBPF {
			if cfg.EnableNetworkPolicy {
				return runCalico(cfg)
			}
			err = cleanUPFelix()
			if err != nil {
				return err
			}
			return runSocat(cfg)
		}
		if !cfg.HasCiliumChainer {
			return runSocat(cfg)
		}
		fmt.Printf("enable ebpf provider, run cilium")
		fallthrough
	case dataPathIPvlan, dataPathV2:
		return runCilium(cfg)
	}

	return nil
}

func runExclusiveENI(cfg *PolicyConfig) error {
	err := configENIOnlyMasq("iptables")
	if err != nil {
		return err
	}
	if cfg.IPv6 {
		err = configENIOnlyMasq("ip6tables")
		if err != nil {
			return err
		}
	}

	return runSocat(cfg)
}

func runCalico(cfg *PolicyConfig) error {
	args := []string{
		"calico-felix",
	}
	env := os.Environ()
	env = append(env,
		"FELIX_IPTABLESBACKEND=NFT",
		"FELIX_LOGSEVERITYSYS=none",
		"FELIX_LOGSEVERITYSCREEN=info",
		"CALICO_NETWORKING_BACKEND=none",
		"CLUSTER_TYPE=k8s,aliyun",
		"CALICO_DISABLE_FILE_LOGGING=true",
		"FELIX_DATASTORETYPE=kubernetes",
		"FELIX_FELIXHOSTNAME="+os.Getenv("NODENAME"),
		"FELIX_IPTABLESREFRESHINTERVAL=60",
		"FELIX_IPV6SUPPORT=true",
		"WAIT_FOR_DATASTORE=true",
		"NO_DEFAULT_POOLS=true",
		"FELIX_DEFAULTENDPOINTTOHOSTACTION=ACCEPT",
		"FELIX_HEALTHENABLED=true",
		"FELIX_LOGFILEPATH=/dev/null",
		"FELIX_BPFENABLED=false",
		"FELIX_XDPENABLED=false",
		"FELIX_BPFCONNECTTIMELOADBALANCINGENABLED=false",
		"FELIX_BPFKUBEPROXYIPTABLESCLEANUPENABLED=false",
		"FELIX_USAGEREPORTINGENABLED=false",
	)

	binary, err := exec.LookPath("calico-felix")
	if err != nil {
		return fmt.Errorf("calico-felix is not installed %w", err)
	}
	err = syscall.Exec(binary, args, env)
	return err
}

func runCilium(cfg *PolicyConfig) error {
	if !cfg.HasCiliumChainer {
		return fmt.Errorf("no cilium chainer is installed")
	}

	extraArgs, err := parsePolicyConfig()
	if err != nil {
		return err
	}

	args := []string{
		"cilium-agent",
		"--routing-mode=native",
		"--cni-chaining-mode=terway-chainer",
		"--enable-ipv4-masquerade=false",
		"--enable-ipv6-masquerade=false",
		"--disable-envoy-version-check=true",
		"--local-router-ipv4=169.254.10.1",
		"--local-router-ipv6=fe80:2400:3200:baba::1",
		"--enable-local-node-route=false",
		"--enable-endpoint-health-checking=false",
		"--enable-health-checking=false",
		"--enable-service-topology=true",
		"--k8s-heartbeat-timeout=0",
		"--enable-session-affinity=true",
		"--install-iptables-rules=false",
		"--enable-l7-proxy=false",
		"--ipam=delegated-plugin",
		"--enable-bandwidth-manager=true",
		"--agent-health-port=" + cfg.HealthCheckPort,
	}

	if cfg.EnableNetworkPolicy {
		args = append(args, "--enable-policy=default")
	} else {
		args = append(args, "--enable-policy=never")
		args = append(args, "--labels=k8s:io\\.kubernetes\\.pod\\.namespace")
	}

	switch cfg.Datapath {
	case dataPathIPvlan:
		args = append(args, "--datapath-mode=ipvlan")
	case dataPathV2:
		args = append(args, "--datapath-mode=veth")

		if cfg.EnableKPR {
			args = append(args, "--kube-proxy-replacement=true")
			args = append(args, "--enable-node-port=true")
			args = append(args, "--enable-host-port=true")
			args = append(args, "--enable-external-ips=true")
		}
	}

	args = append(args, "--enable-endpoint-routes=true")
	args = append(args, "--enable-l2-neigh-discovery=false")

	if cfg.InClusterLoadBalance {
		args = append(args, "--enable-in-cluster-loadbalance=true")
	}

	args = append(args, extraArgs...)

	args, err = mutateCiliumArgs(args)
	if err != nil {
		return err
	}

	env := os.Environ()
	binary, err := exec.LookPath("cilium-agent")
	if err != nil {
		return fmt.Errorf("cilium-agent is not installed %w", err)
	}
	err = syscall.Exec(binary, args, env)
	return err
}

func parsePolicyConfig() ([]string, error) {
	cni, err := os.ReadFile(cniFilePath)
	if err != nil {
		return nil, err
	}

	cniJSON, err := gabs.ParseJSON(cni)
	if err != nil {
		return nil, err
	}

	return policyConfig(cniJSON)
}

func policyConfig(container *gabs.Container) ([]string, error) {
	var ciliumArgs []string
	for _, plugin := range container.Path("plugins").Children() {
		if plugin.Path("type").Data().(string) != pluginTypeTerway {
			continue
		}
		h := &CNIConfig{}

		err := json.Unmarshal(plugin.Bytes(), h)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal args: %w", err)
		}

		if h.HubbleEnabled == "true" {
			if h.HubbleMetrics == "" {
				h.HubbleMetrics = "drop"
			}
			if h.HubbleListenAddress == "" {
				h.HubbleListenAddress = ":4244"
			}
			if h.HubbleMetricServer == "" {
				h.HubbleMetricServer = ":9091"
			}
			ciliumArgs = append(ciliumArgs, []string{
				"--enable-hubble=true",
				"--hubble-disable-tls=true",
				"--hubble-metrics=" + h.HubbleMetrics,
				"--hubble-listen-address=" + h.HubbleListenAddress,
				"--hubble-metrics-server=" + h.HubbleMetricServer,
			}...)
		}

		// parse extra args
		ciliumArgs = append(ciliumArgs, extractArgs(h.CiliumExtraArgs)...)
	}

	var err error
	ciliumArgs = lo.Filter(ciliumArgs, func(item string, index int) bool {
		if strings.Contains(item, "disable-per-package-lb") {
			should, innerErr := shouldAppend()
			if innerErr != nil {
				err = innerErr
			}
			return should
		}
		return true
	})

	return ciliumArgs, err
}

func extractArgs(in string) []string {
	return lo.FilterMap(strings.Split(in, "--"), func(item string, index int) (string, bool) {
		if strings.TrimSpace(item) == "" {
			return "", false
		}
		return "--" + strings.TrimSpace(item), true
	})
}

func configENIOnlyMasq(ipt string) error {
	binary, err := exec.LookPath("bash")
	if err != nil {
		return fmt.Errorf("bash is not installed %w", err)
	}
	cmd := exec.Command(binary, "-cx", "source uninstall_policy.sh;masq_eni_only "+ipt)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("eni only masq failed: %w", err)
	}
	return nil
}

func cleanUPFelix() error {
	binary, err := exec.LookPath("bash")
	if err != nil {
		return fmt.Errorf("bash is not installed %w", err)
	}
	cmd := exec.Command(binary, "-c", "source uninstall_policy.sh;cleanup_felix")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
	return nil
}

func runSocat(cfg *PolicyConfig) error {
	port := cfg.HealthCheckPort
	if port == "" {
		port = "9099"
	}
	args := []string{
		"socat",
		fmt.Sprintf("TCP-LISTEN:%s,bind=127.0.0.1,fork,reuseaddr", port),
		"system:'sleep 2;kill -9 $SOCAT_PID 2>/dev/null'",
	}
	env := os.Environ()
	binary, err := exec.LookPath("socat")
	if err != nil {
		return fmt.Errorf("socat is not installed %w", err)
	}
	return syscall.Exec(binary, args, env)
}

func mutateCiliumArgs(in []string) ([]string, error) {
	var err error
	hasLegacy := false

	args := lo.Filter(in, func(item string, index int) bool {
		if strings.Contains(item, "disable-per-package-lb") {
			var innerErr error
			hasLegacy, innerErr = shouldAppend()
			if innerErr != nil {
				err = innerErr
			}

			return hasLegacy
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	if hasLegacy {
		args = lo.Filter(args, func(item string, index int) bool {
			return !strings.Contains(item, "datapath-mode=veth")
		})
	}

	return args, nil
}

// shouldAppend check whether disable-per-package-lb should be appended
func shouldAppend() (bool, error) {
	out, err := readFunc("/var/run/cilium/state/globals/node_config.h")
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return strings.Contains(string(out), "DISABLE_PER_PACKET_LB"), nil
}
