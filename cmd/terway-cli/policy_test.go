package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Jeffail/gabs/v2"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	ctrl "sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

func Test_extractArgs(t *testing.T) {
	type args struct {
		in string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "test1",
			args: args{
				in: "--foo=bar --baz=\"aa bb\"",
			},
			want: []string{"--foo=bar", "--baz=\"aa bb\""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, extractArgs(tt.args.in), "extractArgs(%v)", tt.args.in)
		})
	}
}

func Test_shouldAppend(t *testing.T) {
	tests := []struct {
		name     string
		want     bool
		readFunc func(name string) ([]byte, error)
		wantErr  assert.ErrorAssertionFunc
	}{
		{
			name: "not found",
			want: false,
			readFunc: func(name string) ([]byte, error) {
				return nil, os.ErrNotExist
			},
			wantErr: assert.NoError,
		},
		{
			name: "exists",
			want: true,
			readFunc: func(name string) ([]byte, error) {
				return []byte("#define DIRECT_ROUTING_DEV_IFINDEX 0\n#define DISABLE_PER_PACKET_LB 1\n#define EGRESS_POLICY_MAP cilium_egress_gw_policy_v4\n#define EGRESS_POLICY_MAP_SIZE 16384\n#define ENABLE_BANDWIDTH_MANAGER 1"), nil
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readFunc = tt.readFunc
			got, err := shouldAppend()
			if !tt.wantErr(t, err, fmt.Sprintf("shouldAppend()")) {
				return
			}
			assert.Equalf(t, tt.want, got, "shouldAppend()")
		})
	}
}

func Test_policyConfig(t *testing.T) {
	type args struct {
		container *gabs.Container
	}
	tests := []struct {
		name      string
		args      args
		readFunc  func(name string) ([]byte, error)
		checkFunc func(*testing.T, []string, error)
	}{
		{
			name: "per-package-lb should exist",
			args: args{container: func() *gabs.Container {
				cniJSON, _ := gabs.ParseJSON([]byte(`{
  "cniVersion": "0.4.0",
  "name": "terway-chainer",
  "plugins": [
    {
      "bandwidth_mode": "edt",
      "capabilities": {
        "bandwidth": true
      },
      "cilium_args": "disable-per-package-lb=true",
      "eniip_virtual_type": "datapathv2",
      "network_policy_provider": "ebpf",
      "type": "terway"
    },
    {
      "data-path": "datapathv2",
      "enable-debug": false,
      "log-file": "/var/run/cilium/cilium-cni.log",
      "type": "cilium-cni"
    }
  ]
}`))
				return cniJSON
			}()},
			readFunc: func(name string) ([]byte, error) {
				return []byte("#define DIRECT_ROUTING_DEV_IFINDEX 0\n#define DISABLE_PER_PACKET_LB 1\n"), nil
			},
			checkFunc: func(t *testing.T, strings []string, err error) {
				assert.NoError(t, err)
				assert.Contains(t, strings, "--disable-per-package-lb=true")
			},
		},
		{
			name: "per-package-lb should exist",
			args: args{container: func() *gabs.Container {
				cniJSON, _ := gabs.ParseJSON([]byte(`{
  "cniVersion": "0.4.0",
  "name": "terway-chainer",
  "plugins": [
    {
      "bandwidth_mode": "edt",
      "capabilities": {
        "bandwidth": true
      },
      "cilium_args": "disable-per-package-lb=true --other=false",
      "eniip_virtual_type": "datapathv2",
      "network_policy_provider": "ebpf",
      "type": "terway"
    },
    {
      "data-path": "datapathv2",
      "enable-debug": false,
      "log-file": "/var/run/cilium/cilium-cni.log",
      "type": "cilium-cni"
    }
  ]
}`))
				return cniJSON
			}()},
			readFunc: func(name string) ([]byte, error) {
				return nil, nil
			},
			checkFunc: func(t *testing.T, strings []string, err error) {
				assert.NoError(t, err)
				assert.NotContains(t, strings, "--disable-per-package-lb=true")
				assert.Contains(t, strings, "--other=false")
			},
		},
		{
			name: "test hubble",
			args: args{container: func() *gabs.Container {
				cniJSON, _ := gabs.ParseJSON([]byte(`{
  "cniVersion": "0.4.0",
  "name": "terway-chainer",
  "plugins": [
    {
      "bandwidth_mode": "edt",
      "capabilities": {
        "bandwidth": true
      },
      "cilium_args": "disable-per-package-lb=true",
      "eniip_virtual_type": "datapathv2",
      "network_policy_provider": "ebpf",
      "cilium_enable_hubble": "true",
      "cilium_hubble_listen_address": ":4244",
      "cilium_hubble_metrics_server": ":9091",
      "cilium_hubble_metrics": "drop,tcp,flow,port-distribution,icmp",
      "type": "terway"
    },
    {
      "data-path": "datapathv2",
      "enable-debug": false,
      "log-file": "/var/run/cilium/cilium-cni.log",
      "type": "cilium-cni"
    }
  ]
}`))
				return cniJSON
			}()},
			readFunc: func(name string) ([]byte, error) {
				return []byte("#define DIRECT_ROUTING_DEV_IFINDEX 0\n#define DISABLE_PER_PACKET_LB 1\n"), nil
			},
			checkFunc: func(t *testing.T, strings []string, err error) {
				assert.NoError(t, err)
				assert.Contains(t, strings, "--disable-per-package-lb=true")
				assert.Contains(t, strings, "--enable-hubble=true")
			},
		},
		{
			name: "host stack cidr not set",
			args: args{container: func() *gabs.Container {
				cniJSON, _ := gabs.ParseJSON([]byte(`{
  "cniVersion": "0.4.0",
  "name": "terway-chainer",
  "plugins": [
    {
      "bandwidth_mode": "edt",
      "capabilities": {
        "bandwidth": true
      },
      "cilium_args": "disable-per-package-lb=true",
      "eniip_virtual_type": "datapathv2",
      "network_policy_provider": "ebpf",
      "type": "terway"
    },
    {
      "data-path": "datapathv2",
      "enable-debug": false,
      "log-file": "/var/run/cilium/cilium-cni.log",
      "type": "cilium-cni"
    }
  ]
}`))
				return cniJSON
			}()},
			readFunc: func(name string) ([]byte, error) {
				return []byte("#define DIRECT_ROUTING_DEV_IFINDEX 0\n#define DISABLE_PER_PACKET_LB 1\n"), nil
			},
			checkFunc: func(t *testing.T, strings []string, err error) {
				assert.NoError(t, err)
				assert.Contains(t, strings, "--disable-per-package-lb=true")
				assert.Contains(t, strings, "--terway-host-stack-cidr=169.254.20.10/32")
			},
		},
		{
			name: "multi host stack cidr",
			args: args{container: func() *gabs.Container {
				cniJSON, _ := gabs.ParseJSON([]byte(`{
  "cniVersion": "0.4.0",
  "name": "terway-chainer",
  "plugins": [
    {
      "bandwidth_mode": "edt",
      "capabilities": {
        "bandwidth": true
      },
      "cilium_args": "disable-per-package-lb=true",
      "eniip_virtual_type": "datapathv2",
      "network_policy_provider": "ebpf",
      "host_stack_cidrs": ["169.254.20.10/32", "169.254.20.11/32"],
      "type": "terway"
    },
    {
      "data-path": "datapathv2",
      "enable-debug": false,
      "log-file": "/var/run/cilium/cilium-cni.log",
      "type": "cilium-cni"
    }
  ]
}`))
				return cniJSON
			}()},
			readFunc: func(name string) ([]byte, error) {
				return []byte("#define DIRECT_ROUTING_DEV_IFINDEX 0\n#define DISABLE_PER_PACKET_LB 1\n"), nil
			},
			checkFunc: func(t *testing.T, strings []string, err error) {
				assert.NoError(t, err)
				assert.Contains(t, strings, "--disable-per-package-lb=true")
				assert.Contains(t, strings, "--terway-host-stack-cidr=169.254.20.10/32,169.254.20.11/32")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readFunc = tt.readFunc
			got, err := policyConfig(tt.args.container)
			tt.checkFunc(t, got, err)
		})
	}
}

func Test_mutateCiliumArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		want     []string
		readFunc func(name string) ([]byte, error)
		wantErr  assert.ErrorAssertionFunc
	}{
		{
			name: "not found",
			args: []string{
				"cilium-agent",
				"--cni-chaining-mode=terway-chainer",
				"--datapath-mode=veth",
			},
			want: []string{
				"cilium-agent",
				"--cni-chaining-mode=terway-chainer",
				"--datapath-mode=veth",
			},
			readFunc: func(name string) ([]byte, error) {
				return nil, os.ErrNotExist
			},
			wantErr: assert.NoError,
		},
		{
			name: "exists should not enable veth datapath",
			args: []string{
				"cilium-agent",
				"--cni-chaining-mode=terway-chainer",
				"--datapath-mode=veth",
				"--disable-per-package-lb",
			},
			want: []string{
				"cilium-agent",
				"--cni-chaining-mode=terway-chainer",
				"--disable-per-package-lb",
			},
			readFunc: func(name string) ([]byte, error) {
				return []byte("#define DIRECT_ROUTING_DEV_IFINDEX 0\n#define DISABLE_PER_PACKET_LB 1\n#define EGRESS_POLICY_MAP cilium_egress_gw_policy_v4\n#define EGRESS_POLICY_MAP_SIZE 16384\n#define ENABLE_BANDWIDTH_MANAGER 1"), nil
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readFunc = tt.readFunc
			got, err := mutateCiliumArgs(tt.args)
			if !tt.wantErr(t, err, fmt.Sprintf("shouldAppend()")) {
				return
			}
			assert.Equalf(t, tt.want, got, "shouldAppend()")
		})
	}
}

func Test_runHealthCheckServer(t *testing.T) {
	cfg := &PolicyConfig{
		HealthCheckPort: "18080", // Avoid conflicts by choosing a test port
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the server
	go func() {
		err := runHealthCheckServer(ctx, cfg)
		if err != nil {
			t.Errorf("runHealthCheckServer error: %v", err)
		}
	}()

	// Wait for the server to start
	time.Sleep(200 * time.Millisecond)

	// Connect to the server
	conn, err := net.Dial("tcp", "127.0.0.1:"+cfg.HealthCheckPort)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Read the response content
	resp, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if resp != "OK\n" {
		t.Errorf("unexpected response: %q", resp)
	}

	// Stop the server
	cancel()
	time.Sleep(100 * time.Millisecond)
}

func Test_runCalico(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *PolicyConfig
		setupMocks  func() *gomonkey.Patches
		expectError bool
		errorMsg    string
	}{
		{
			name: "successful execution",
			cfg: &PolicyConfig{
				HealthCheckPort: "9099",
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				// Mock exec.LookPath to return a valid path
				patches.ApplyFunc(exec.LookPath, func(file string) (string, error) {
					if file == "calico-felix" {
						return "/usr/bin/calico-felix", nil
					}
					return "", fmt.Errorf("command not found")
				})
				// Mock syscall.Exec to simulate successful execution
				patches.ApplyFunc(syscall.Exec, func(argv0 string, argv []string, envv []string) error {
					// In a real test, this would replace the current process
					// For testing purposes, we just return nil to indicate success
					return nil
				})
				return patches
			},
			expectError: false,
		},
		{
			name: "calico-felix not found",
			cfg: &PolicyConfig{
				HealthCheckPort: "9099",
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				// Mock exec.LookPath to return an error
				patches.ApplyFunc(exec.LookPath, func(file string) (string, error) {
					return "", fmt.Errorf("executable file not found in $PATH")
				})
				return patches
			},
			expectError: true,
			errorMsg:    "calico-felix is not installed",
		},
		{
			name: "syscall.Exec failure",
			cfg: &PolicyConfig{
				HealthCheckPort: "9099",
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				// Mock exec.LookPath to return a valid path
				patches.ApplyFunc(exec.LookPath, func(file string) (string, error) {
					if file == "calico-felix" {
						return "/usr/bin/calico-felix", nil
					}
					return "", fmt.Errorf("command not found")
				})
				// Mock syscall.Exec to return an error
				patches.ApplyFunc(syscall.Exec, func(argv0 string, argv []string, envv []string) error {
					return fmt.Errorf("exec failed")
				})
				return patches
			},
			expectError: true,
			errorMsg:    "exec failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mocks
			patches := tt.setupMocks()
			defer patches.Reset()

			// Set NODENAME environment variable for testing
			os.Setenv("NODENAME", "test-node")
			defer os.Unsetenv("NODENAME")

			// Execute the function
			err := runCalico(tt.cfg)

			// Verify results
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func Test_runCalico_EnvironmentVariables(t *testing.T) {
	// Test that the correct environment variables are set
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	var capturedArgs []string
	var capturedEnv []string

	// Mock exec.LookPath
	patches.ApplyFunc(exec.LookPath, func(file string) (string, error) {
		if file == "calico-felix" {
			return "/usr/bin/calico-felix", nil
		}
		return "", fmt.Errorf("command not found")
	})

	// Mock syscall.Exec to capture arguments and environment
	patches.ApplyFunc(syscall.Exec, func(argv0 string, argv []string, envv []string) error {
		// Make a copy of the arguments to avoid memory issues
		capturedArgs = make([]string, len(argv))
		copy(capturedArgs, argv)

		// Make a copy of the environment variables
		capturedEnv = make([]string, len(envv))
		copy(capturedEnv, envv)

		return nil
	})

	// Set NODENAME environment variable
	os.Setenv("NODENAME", "test-node")
	defer os.Unsetenv("NODENAME")

	cfg := &PolicyConfig{
		HealthCheckPort: "9099",
	}

	err := runCalico(cfg)
	assert.NoError(t, err)

	// Verify arguments
	expectedArgs := []string{"calico-felix"}
	assert.Equal(t, expectedArgs, capturedArgs)

	// Verify environment variables contain expected values
	envMap := make(map[string]string)
	for _, env := range capturedEnv {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	// Check key environment variables
	assert.Equal(t, "NFT", envMap["FELIX_IPTABLESBACKEND"])
	assert.Equal(t, "none", envMap["FELIX_LOGSEVERITYSYS"])
	assert.Equal(t, "info", envMap["FELIX_LOGSEVERITYSCREEN"])
	assert.Equal(t, "none", envMap["CALICO_NETWORKING_BACKEND"])
	assert.Equal(t, "k8s,aliyun", envMap["CLUSTER_TYPE"])
	assert.Equal(t, "true", envMap["CALICO_DISABLE_FILE_LOGGING"])
	assert.Equal(t, "kubernetes", envMap["FELIX_DATASTORETYPE"])
	assert.Equal(t, "test-node", envMap["FELIX_FELIXHOSTNAME"])
	assert.Equal(t, "60", envMap["FELIX_IPTABLESREFRESHINTERVAL"])
	assert.Equal(t, "true", envMap["FELIX_IPV6SUPPORT"])
	assert.Equal(t, "true", envMap["WAIT_FOR_DATASTORE"])
	assert.Equal(t, "true", envMap["NO_DEFAULT_POOLS"])
	assert.Equal(t, "ACCEPT", envMap["FELIX_DEFAULTENDPOINTTOHOSTACTION"])
	assert.Equal(t, "true", envMap["FELIX_HEALTHENABLED"])
	assert.Equal(t, "/dev/null", envMap["FELIX_LOGFILEPATH"])
	assert.Equal(t, "false", envMap["FELIX_BPFENABLED"])
	assert.Equal(t, "false", envMap["FELIX_XDPENABLED"])
	assert.Equal(t, "false", envMap["FELIX_BPFCONNECTTIMELOADBALANCINGENABLED"])
	assert.Equal(t, "false", envMap["FELIX_BPFKUBEPROXYIPTABLESCLEANUPENABLED"])
	assert.Equal(t, "false", envMap["FELIX_USAGEREPORTINGENABLED"])
}

func TestMasqENIOnlyRules(t *testing.T) {
	script := "../../policy/uninstall_policy.sh"

	retLine := `-A terway-masq -o cali+ -m comment --comment "terway:masq-pod-bypass" -j RETURN`
	masqLine := `-A terway-masq -j MASQUERADE`
	snatLine := func(ip string) string {
		return `-A terway-masq -o cali+ -m comment --comment "terway:eni-only-snat" -j SNAT --to-source ` + ip
	}
	// insertSNATArg is the argv (unquoted comment) the script passes to iptables -I 1.
	insertSNATArg := func(ip string) string {
		return `-t nat -I terway-masq 1 -o cali+ -m comment --comment terway:eni-only-snat -j SNAT --to-source ` + ip
	}
	insertRetArg := `-t nat -I terway-masq 2 -o cali+ -m comment --comment terway:masq-pod-bypass -j RETURN`

	type result struct {
		status int
		rules  []string
		writes []string
	}

	// runModel emulates iptables so we can exercise masq_eni_only's
	// ordering / idempotency logic against the real script.
	runModel := func(t *testing.T, ipt, nodeIP string, chainExists, failDelete, failInsert bool, runs int, initial ...string) result {
		t.Helper()
		flag := func(v bool) string {
			if v {
				return "1"
			}
			return "0"
		}
		model := `
script=$1
ipt=$2
nodeIP=$3
runs=$4
chain_exists=$5
fail_delete=$6
fail_insert=$7
shift 7
chain=("$@")
writes=()
postrouting=1
masq_rule='-A terway-masq -j MASQUERADE'

# Convert an iptables -I argv into the canonical -S rendered form (quoted comment).
build_stored() {
  local a="$1"
  a=${a#-t nat -I terway-masq }
  a=${a#* }
  a=${a// --comment terway:eni-only-snat / --comment \"terway:eni-only-snat\" }
  a=${a// --comment terway:masq-pod-bypass / --comment \"terway:masq-pod-bypass\" }
  printf '%s' "-A terway-masq $a"
}

_model() {
  case "$*" in
    "-t nat -L terway-masq")
      [ "$chain_exists" -eq 1 ] || return 1
      if [ ${#chain[@]} -gt 0 ]; then printf '%s\n' "${chain[@]}"; fi ;;
    "-t nat -N terway-masq") chain_exists=1; writes+=("$*") ;;
    "-t nat -L POSTROUTING") [ "$postrouting" -eq 1 ] && printf '%s\n' terway-masq ;;
    "-t nat -A POSTROUTING "*) postrouting=1; writes+=("$*") ;;
    "-t nat -S terway-masq")
      [ "$chain_exists" -eq 1 ] || return 1
      printf '%s\n' '-N terway-masq'
      if [ ${#chain[@]} -gt 0 ]; then printf '%s\n' "${chain[@]}"; fi ;;
    "-t nat -D terway-masq 1")
      [ "$fail_delete" -eq 0 ] || return 4
      chain=("${chain[@]:1}"); writes+=("$*") ;;
    "-t nat -I terway-masq 1 -o cali+ -m comment --comment terway:eni-only-snat -j SNAT --to-source "*)
      [ "$fail_insert" -eq 0 ] || return 4
      chain=("$(build_stored "$*")" "${chain[@]}"); writes+=("$*") ;;
    "-t nat -I terway-masq 2 -o cali+ -m comment --comment terway:masq-pod-bypass -j RETURN")
      [ "$fail_insert" -eq 0 ] || return 4
      chain=("${chain[@]:0:1}" "$(build_stored "$*")" "${chain[@]:1}"); writes+=("$*") ;;
    "-t nat -A terway-masq -j MASQUERADE") chain+=("$masq_rule"); writes+=("$*") ;;
    *) return 2 ;;
  esac
}
iptables()  { _model "$@"; }
ip6tables() { _model "$@"; }

source "$script"
status=0
for ((i=0;i<runs;i++)); do
  masq_eni_only "$ipt" "$nodeIP" || { status=$?; break; }
done
echo "__STATUS__$status"
for r in "${chain[@]}"; do printf '__RULE__%s\n' "$r"; done
for w in "${writes[@]}"; do printf '__WRITE__%s\n' "$w"; done
`
		args := []string{"-c", model, "bash", script, ipt, nodeIP, strconv.Itoa(runs), flag(chainExists), flag(failDelete), flag(failInsert)}
		args = append(args, initial...)
		out, err := exec.Command("bash", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("model execution failed: %v\n%s", err, out)
		}
		var r result
		for _, line := range strings.Split(string(out), "\n") {
			switch {
			case strings.HasPrefix(line, "__STATUS__"):
				r.status, _ = strconv.Atoi(strings.TrimPrefix(line, "__STATUS__"))
			case strings.HasPrefix(line, "__RULE__"):
				r.rules = append(r.rules, strings.TrimPrefix(line, "__RULE__"))
			case strings.HasPrefix(line, "__WRITE__"):
				r.writes = append(r.writes, strings.TrimPrefix(line, "__WRITE__"))
			}
		}
		return r
	}

	t.Run("fresh install converges to SNAT,RETURN,MASQUERADE", func(t *testing.T) {
		r := runModel(t, "iptables", "10.0.0.1", false, false, false, 1)
		assert.Equal(t, 0, r.status)
		assert.Equal(t, []string{snatLine("10.0.0.1"), retLine, masqLine}, r.rules)
		assert.Equal(t, []string{
			"-t nat -N terway-masq",
			insertSNATArg("10.0.0.1"),
			insertRetArg,
			"-t nat -A terway-masq -j MASQUERADE",
		}, r.writes)
	})

	t.Run("correct chain is untouched across runs (no iptables churn)", func(t *testing.T) {
		r := runModel(t, "iptables", "10.0.0.1", true, false, false, 3, snatLine("10.0.0.1"), retLine, masqLine)
		assert.Equal(t, 0, r.status)
		assert.Equal(t, []string{snatLine("10.0.0.1"), retLine, masqLine}, r.rules)
		assert.Empty(t, r.writes)
	})

	t.Run("legacy RETURN-only chain upgrades to SNAT first", func(t *testing.T) {
		r := runModel(t, "iptables", "10.0.0.1", true, false, false, 1, retLine, masqLine)
		assert.Equal(t, 0, r.status)
		assert.Equal(t, []string{snatLine("10.0.0.1"), retLine, masqLine}, r.rules)
		assert.Equal(t, []string{"-t nat -D terway-masq 1", insertSNATArg("10.0.0.1"), insertRetArg}, r.writes)
	})

	t.Run("node address change self-heals the SNAT rule", func(t *testing.T) {
		r := runModel(t, "iptables", "10.9.9.9", true, false, false, 1, snatLine("10.0.0.1"), retLine, masqLine)
		assert.Equal(t, 0, r.status)
		assert.Equal(t, []string{snatLine("10.9.9.9"), retLine, masqLine}, r.rules)
	})

	t.Run("ipset rule upgrades to stateless SNAT", func(t *testing.T) {
		legacy := strings.Replace(snatLine("10.0.0.1"), "-m comment", "-m set --match-set terway-exclusive-local dst -m comment", 1)
		r := runModel(t, "iptables", "10.0.0.1", true, false, false, 2, legacy, retLine, masqLine)
		assert.Equal(t, 0, r.status)
		assert.Equal(t, []string{snatLine("10.0.0.1"), retLine, masqLine}, r.rules)
		assert.Equal(t, []string{"-t nat -D terway-masq 1", "-t nat -D terway-masq 1", insertSNATArg("10.0.0.1"), insertRetArg}, r.writes)
	})

	t.Run("node address prefix is not an exact match", func(t *testing.T) {
		r := runModel(t, "iptables", "10.0.0.1", true, false, false, 1, snatLine("10.0.0.10"), retLine, masqLine)
		assert.Equal(t, 0, r.status)
		assert.Equal(t, []string{snatLine("10.0.0.1"), retLine, masqLine}, r.rules)
	})

	t.Run("duplicate and misordered rules converge", func(t *testing.T) {
		r := runModel(t, "iptables", "10.0.0.1", true, false, false, 1,
			snatLine("10.0.0.1"), snatLine("10.0.0.1"), retLine, retLine, masqLine)
		assert.Equal(t, 0, r.status)
		assert.Equal(t, []string{snatLine("10.0.0.1"), retLine, masqLine}, r.rules)

		r = runModel(t, "iptables", "10.0.0.1", true, false, false, 1, retLine, snatLine("10.0.0.1"), masqLine)
		assert.Equal(t, 0, r.status)
		assert.Equal(t, []string{snatLine("10.0.0.1"), retLine, masqLine}, r.rules)
	})

	t.Run("ip6tables uses the node IPv6 address", func(t *testing.T) {
		r := runModel(t, "ip6tables", "fd00::1", false, false, false, 1)
		assert.Equal(t, 0, r.status)
		assert.Equal(t, snatLine("fd00::1"), r.rules[0])
	})

	t.Run("delete failure is returned", func(t *testing.T) {
		r := runModel(t, "iptables", "10.0.0.1", true, true, false, 1, retLine, masqLine)
		assert.Equal(t, 1, r.status)
		assert.Equal(t, []string{retLine, masqLine}, r.rules)
	})

	t.Run("insert failure is returned", func(t *testing.T) {
		r := runModel(t, "iptables", "10.0.0.1", true, false, true, 1, retLine, masqLine)
		assert.Equal(t, 1, r.status)
	})

	t.Run("missing node address fails hard before touching rules", func(t *testing.T) {
		r := runModel(t, "iptables", "", false, false, false, 1)
		assert.Equal(t, 1, r.status)
		assert.Empty(t, r.writes)
	})
}

func Test_runExclusiveENI(t *testing.T) {
	t.Run("resolves v4 and installs iptables masq", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()
		patches.ApplyFunc(resolveExclusiveNodeIP, func(ipv4, ipv6 bool) (string, string, error) {
			assert.True(t, ipv4)
			assert.False(t, ipv6)
			return "10.0.0.1", "", nil
		})
		var got []string
		patches.ApplyFunc(configENIOnlyMasq, func(ipt, nodeIP string) error {
			got = append(got, ipt+"="+nodeIP)
			return nil
		})
		patches.ApplyFunc(runHealthCheckServer, func(ctx context.Context, cfg *PolicyConfig) error { return nil })
		// ctrl.SetupSignalHandler is single-shot; stub it so repeated subtests
		// do not hit its second-call "close of closed channel" panic.
		patches.ApplyFunc(ctrl.SetupSignalHandler, func() context.Context { return context.Background() })

		err := runExclusiveENI(&PolicyConfig{ExclusiveENI: true})
		assert.NoError(t, err)
		assert.Equal(t, []string{"iptables=10.0.0.1"}, got)
	})

	t.Run("dual stack installs ip6tables too", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()
		patches.ApplyFunc(resolveExclusiveNodeIP, func(_, _ bool) (string, string, error) {
			return "10.0.0.1", "fd00::1", nil
		})
		var got []string
		patches.ApplyFunc(configENIOnlyMasq, func(ipt, nodeIP string) error {
			got = append(got, ipt+"="+nodeIP)
			return nil
		})
		patches.ApplyFunc(runHealthCheckServer, func(ctx context.Context, cfg *PolicyConfig) error { return nil })
		patches.ApplyFunc(ctrl.SetupSignalHandler, func() context.Context { return context.Background() })

		err := runExclusiveENI(&PolicyConfig{ExclusiveENI: true, IPv6: true})
		assert.NoError(t, err)
		assert.Equal(t, []string{"iptables=10.0.0.1", "ip6tables=fd00::1"}, got)
	})

	t.Run("fails hard when node address resolution errors", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()
		patches.ApplyFunc(resolveExclusiveNodeIP, func(_, _ bool) (string, string, error) {
			return "", "", fmt.Errorf("no route")
		})
		var called bool
		patches.ApplyFunc(configENIOnlyMasq, func(_, _ string) error { called = true; return nil })

		err := runExclusiveENI(&PolicyConfig{ExclusiveENI: true})
		assert.Error(t, err)
		assert.False(t, called, "masq must not run when the node address is unresolved")
	})

	t.Run("fails hard when v6 address is missing under ipv6 config", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()
		patches.ApplyFunc(resolveExclusiveNodeIP, func(_, _ bool) (string, string, error) {
			return "10.0.0.1", "", nil
		})
		var got []string
		patches.ApplyFunc(configENIOnlyMasq, func(ipt, nodeIP string) error { got = append(got, ipt); return nil })

		err := runExclusiveENI(&PolicyConfig{ExclusiveENI: true, IPv6: true})
		assert.Error(t, err)
		assert.Equal(t, []string{"iptables"}, got, "v6 masq is not attempted without a v6 address")
	})
}

func Test_runCilium(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *PolicyConfig
		setupMocks  func() *gomonkey.Patches
		expectError bool
		errorMsg    string
	}{
		{
			name: "successful execution with ipvlan datapath",
			cfg: &PolicyConfig{
				HasCiliumChainer:    true,
				Datapath:            dataPathIPvlan,
				EnableNetworkPolicy: true,
				HealthCheckPort:     "9099",
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				// Mock os.ReadFile for parsePolicyConfig
				patches.ApplyFunc(os.ReadFile, func(name string) ([]byte, error) {
					if name == cniFilePath {
						return []byte(`{
							"cniVersion": "0.4.0",
							"name": "terway-chainer",
							"plugins": [
								{
									"type": "terway"
								}
							]
						}`), nil
					}
					return nil, os.ErrNotExist
				})
				// Mock readFunc for shouldAppend
				patches.ApplyFunc(shouldAppend, func() (bool, error) {
					return false, nil
				})
				// Mock exec.LookPath
				patches.ApplyFunc(exec.LookPath, func(file string) (string, error) {
					if file == "cilium-agent" {
						return "/usr/bin/cilium-agent", nil
					}
					return "", fmt.Errorf("command not found")
				})
				// Mock syscall.Exec
				patches.ApplyFunc(syscall.Exec, func(argv0 string, argv []string, envv []string) error {
					return nil
				})
				return patches
			},
			expectError: false,
		},
		{
			name: "no cilium chainer installed",
			cfg: &PolicyConfig{
				HasCiliumChainer: false,
				HealthCheckPort:  "9099",
			},
			setupMocks: func() *gomonkey.Patches {
				return gomonkey.NewPatches()
			},
			expectError: true,
			errorMsg:    "no cilium chainer is installed",
		},
		{
			name: "cilium-agent not found",
			cfg: &PolicyConfig{
				HasCiliumChainer:    true,
				Datapath:            dataPathIPvlan,
				EnableNetworkPolicy: true,
				HealthCheckPort:     "9099",
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				// Mock os.ReadFile for parsePolicyConfig
				patches.ApplyFunc(os.ReadFile, func(name string) ([]byte, error) {
					if name == cniFilePath {
						return []byte(`{
							"cniVersion": "0.4.0",
							"name": "terway-chainer",
							"plugins": [
								{
									"type": "terway"
								}
							]
						}`), nil
					}
					return nil, os.ErrNotExist
				})
				// Mock readFunc for shouldAppend
				patches.ApplyFunc(shouldAppend, func() (bool, error) {
					return false, nil
				})
				// Mock exec.LookPath to return error
				patches.ApplyFunc(exec.LookPath, func(file string) (string, error) {
					return "", fmt.Errorf("executable file not found in $PATH")
				})
				return patches
			},
			expectError: true,
			errorMsg:    "cilium-agent is not installed",
		},
		{
			name: "parsePolicyConfig error",
			cfg: &PolicyConfig{
				HasCiliumChainer:    true,
				Datapath:            dataPathIPvlan,
				EnableNetworkPolicy: true,
				HealthCheckPort:     "9099",
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				// Mock os.ReadFile to return error
				patches.ApplyFunc(os.ReadFile, func(name string) ([]byte, error) {
					if name == cniFilePath {
						return nil, fmt.Errorf("file not found")
					}
					return nil, os.ErrNotExist
				})
				return patches
			},
			expectError: true,
			errorMsg:    "file not found",
		},
		{
			name: "successful execution with v2 datapath and KPR enabled",
			cfg: &PolicyConfig{
				HasCiliumChainer:     true,
				Datapath:             dataPathV2,
				EnableKPR:            true,
				EnableNetworkPolicy:  false,
				InClusterLoadBalance: true,
				HealthCheckPort:      "9099",
			},
			setupMocks: func() *gomonkey.Patches {
				patches := gomonkey.NewPatches()
				// Mock os.ReadFile for parsePolicyConfig
				patches.ApplyFunc(os.ReadFile, func(name string) ([]byte, error) {
					if name == cniFilePath {
						return []byte(`{
							"cniVersion": "0.4.0",
							"name": "terway-chainer",
							"plugins": [
								{
									"type": "terway"
								}
							]
						}`), nil
					}
					return nil, os.ErrNotExist
				})
				// Mock readFunc for shouldAppend
				patches.ApplyFunc(shouldAppend, func() (bool, error) {
					return false, nil
				})
				// Mock exec.LookPath
				patches.ApplyFunc(exec.LookPath, func(file string) (string, error) {
					if file == "cilium-agent" {
						return "/usr/bin/cilium-agent", nil
					}
					return "", fmt.Errorf("command not found")
				})
				// Mock syscall.Exec
				patches.ApplyFunc(syscall.Exec, func(argv0 string, argv []string, envv []string) error {
					return nil
				})
				return patches
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mocks
			patches := tt.setupMocks()
			defer patches.Reset()

			// Execute the function
			err := runCilium(tt.cfg)

			// Verify results
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func Test_runCilium_ArgumentsAndEnvironment(t *testing.T) {
	// Test that the correct arguments and environment variables are set
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	var capturedArgs []string
	var capturedEnv []string

	// Mock os.ReadFile for parsePolicyConfig
	patches.ApplyFunc(os.ReadFile, func(name string) ([]byte, error) {
		if name == cniFilePath {
			return []byte(`{
				"cniVersion": "0.4.0",
				"name": "terway-chainer",
				"plugins": [
					{
						"type": "terway",
						"cilium_enable_hubble": "true",
						"cilium_hubble_metrics": "drop",
						"cilium_hubble_listen_address": ":4244",
						"cilium_hubble_metrics_server": ":9091",
						"host_stack_cidrs": ["169.254.20.10/32", "169.254.20.11/32"]
					}
				]
			}`), nil
		}
		return nil, os.ErrNotExist
	})

	// Mock readFunc for shouldAppend
	patches.ApplyFunc(shouldAppend, func() (bool, error) {
		return false, nil
	})

	// Mock exec.LookPath
	patches.ApplyFunc(exec.LookPath, func(file string) (string, error) {
		if file == "cilium-agent" {
			return "/usr/bin/cilium-agent", nil
		}
		return "", fmt.Errorf("command not found")
	})

	// Mock syscall.Exec to capture arguments and environment
	patches.ApplyFunc(syscall.Exec, func(argv0 string, argv []string, envv []string) error {
		// Make a copy of the arguments to avoid memory issues
		capturedArgs = make([]string, len(argv))
		copy(capturedArgs, argv)

		// Make a copy of the environment variables
		capturedEnv = make([]string, len(envv))
		copy(capturedEnv, envv)

		return nil
	})

	cfg := &PolicyConfig{
		HasCiliumChainer:     true,
		Datapath:             dataPathV2,
		EnableKPR:            true,
		EnableNetworkPolicy:  true,
		InClusterLoadBalance: true,
		HealthCheckPort:      "9099",
	}

	err := runCilium(cfg)
	assert.NoError(t, err)

	// Verify basic arguments
	assert.Contains(t, capturedArgs, "cilium-agent")
	assert.Contains(t, capturedArgs, "--routing-mode=native")
	assert.Contains(t, capturedArgs, "--cni-chaining-mode=terway-chainer")
	assert.Contains(t, capturedArgs, "--enable-ipv4-masquerade=false")
	assert.Contains(t, capturedArgs, "--enable-ipv6-masquerade=false")
	assert.Contains(t, capturedArgs, "--agent-health-port=9099")

	// Verify datapath-specific arguments
	assert.Contains(t, capturedArgs, "--datapath-mode=veth")

	// Verify KPR arguments
	assert.Contains(t, capturedArgs, "--kube-proxy-replacement=true")
	assert.Contains(t, capturedArgs, "--bpf-lb-sock=true")
	assert.Contains(t, capturedArgs, "--enable-node-port=true")

	// Verify network policy arguments
	assert.Contains(t, capturedArgs, "--enable-policy=default")

	// Verify in-cluster load balance arguments
	assert.Contains(t, capturedArgs, "--enable-in-cluster-loadbalance=true")

	// Verify hubble arguments
	assert.Contains(t, capturedArgs, "--enable-hubble=true")
	assert.Contains(t, capturedArgs, "--hubble-metrics=drop")
	assert.Contains(t, capturedArgs, "--hubble-listen-address=:4244")

	// Verify host stack CIDR arguments
	assert.Contains(t, capturedArgs, "--terway-host-stack-cidr=169.254.20.10/32,169.254.20.11/32")

	// Verify environment variables are passed through
	assert.NotEmpty(t, capturedEnv)
}
func Test_getPolicyConfig(t *testing.T) {
	tests := []struct {
		name           string
		capFileContent string
		envVars        map[string]string
		want           *PolicyConfig
		wantErr        bool
	}{
		{
			name: "basic config with IPv6",
			capFileContent: `cni_ipv6_stack = true
datapath = veth
network_policy_provider = calico
cni_exclusive_eni = false
has_cilium_chainer = false
kube_proxy_replacement = false`,
			envVars: map[string]string{"FELIX_HEALTHPORT": "9099"},
			want: &PolicyConfig{
				Datapath:            "veth",
				EnableNetworkPolicy: false,
				PolicyProvider:      "calico",
				ExclusiveENI:        false,
				HealthCheckPort:     "9099",
				IPv6:                true,
				HasCiliumChainer:    false,
				EnableKPR:           false,
			},
			wantErr: false,
		},
		{
			name: "exclusive eni mode",
			capFileContent: `cni_ipv6_stack = false
datapath = ipvlan
network_policy_provider = ebpf
cni_exclusive_eni = eniOnly
has_cilium_chainer = true
kube_proxy_replacement = true`,
			envVars: map[string]string{"FELIX_HEALTHPORT": "8080"},
			want: &PolicyConfig{
				Datapath:            "ipvlan",
				EnableNetworkPolicy: false,
				PolicyProvider:      "ebpf",
				ExclusiveENI:        true,
				HealthCheckPort:     "8080",
				IPv6:                false,
				HasCiliumChainer:    true,
				EnableKPR:           true,
			},
			wantErr: false,
		},
		{
			name:           "file not exist",
			capFileContent: "",
			want:           nil,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary file for node capabilities
			var capFilePath string
			if tt.capFileContent != "" {
				tempFile, err := os.CreateTemp("", "node_capabilities")
				assert.NoError(t, err)
				defer os.Remove(tempFile.Name())

				_, err = tempFile.Write([]byte(tt.capFileContent))
				assert.NoError(t, err)
				err = tempFile.Close()
				assert.NoError(t, err)

				capFilePath = tempFile.Name()
			} else {
				capFilePath = "/nonexistent/file"
			}

			// Set environment variables
			for k, v := range tt.envVars {
				os.Setenv(k, v)
				defer os.Unsetenv(k)
			}

			// Mock getAllConfig function
			patches := gomonkey.NewPatches()
			defer patches.Reset()
			patches.ApplyFunc(getAllConfig, func(base string) (*TerwayConfig, error) {
				return &TerwayConfig{
					enableNetworkPolicy: tt.want.EnableNetworkPolicy,
					enableInClusterLB:   false,
				}, nil
			})

			got, err := getPolicyConfig(capFilePath)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want.Datapath, got.Datapath)
				assert.Equal(t, tt.want.EnableNetworkPolicy, got.EnableNetworkPolicy)
				assert.Equal(t, tt.want.PolicyProvider, got.PolicyProvider)
				assert.Equal(t, tt.want.ExclusiveENI, got.ExclusiveENI)
				assert.Equal(t, tt.want.HealthCheckPort, got.HealthCheckPort)
				assert.Equal(t, tt.want.IPv6, got.IPv6)
				assert.Equal(t, tt.want.HasCiliumChainer, got.HasCiliumChainer)
				assert.Equal(t, tt.want.EnableKPR, got.EnableKPR)
			}
		})
	}
}
