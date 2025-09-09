package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Jeffail/gabs/v2"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
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
