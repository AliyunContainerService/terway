//go:build linux

package smc

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/stretchr/testify/assert"
)

func Test_ensureSMCR(t *testing.T) {
	tests := []struct {
		name           string
		modprobeResult error
		statResult     error
		expected       bool
	}{
		{
			name:           "successful SMC module load and stat check",
			modprobeResult: nil,
			statResult:     nil,
			expected:       true,
		},
		{
			name:           "modprobe fails",
			modprobeResult: fmt.Errorf("modprobe failed"),
			statResult:     nil,
			expected:       false,
		},
		{
			name:           "stat check fails",
			modprobeResult: nil,
			statResult:     fmt.Errorf("stat failed"),
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			// Mock exec.Command for modprobe
			patches.ApplyFunc(exec.Command, func(name string, arg ...string) *exec.Cmd {
				if name == "modprobe" && len(arg) > 0 && arg[0] == "smc" {
					cmd := &exec.Cmd{}
					patches.ApplyMethodReturn(cmd, "CombinedOutput", []byte(""), tt.modprobeResult)
					return cmd
				}
				return exec.Command(name, arg...)
			})

			// Mock os.Stat
			patches.ApplyFunc(os.Stat, func(name string) (os.FileInfo, error) {
				if name == "/proc/sys/net/smc/tcp2smc" {
					return nil, tt.statResult
				}
				return nil, os.ErrNotExist
			})

			result := ensureSMCR()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func Test_pnetID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single underscore",
			input:    "device_1",
			expected: "1",
		},
		{
			name:     "multiple underscores",
			input:    "test_device_name_123",
			expected: "123",
		},
		{
			name:     "no underscores",
			input:    "device",
			expected: "device",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "underscore at end",
			input:    "device_",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pnetID(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func Test_ensureForERDMADev(t *testing.T) {
	tests := []struct {
		name          string
		deviceName    string
		statOutput    []byte
		statError     error
		addOutput     []byte
		addError      error
		expectedError bool
		errorContains string
	}{
		{
			name:          "device already exists",
			deviceName:    "test_device_1",
			statOutput:    []byte("test_device_1 is configured"),
			statError:     nil,
			addOutput:     nil,
			addError:      nil,
			expectedError: false,
		},
		{
			name:          "device not exists, add successfully",
			deviceName:    "test_device_2",
			statOutput:    []byte("other devices"),
			statError:     nil,
			addOutput:     []byte("added successfully"),
			addError:      nil,
			expectedError: false,
		},
		{
			name:          "stat command fails",
			deviceName:    "test_device_3",
			statOutput:    []byte("error"),
			statError:     fmt.Errorf("stat failed"),
			addOutput:     nil,
			addError:      nil,
			expectedError: true,
			errorContains: "failed to get smc-pnet stat",
		},
		{
			name:          "add command fails",
			deviceName:    "test_device_4",
			statOutput:    []byte("other devices"),
			statError:     nil,
			addOutput:     []byte("add failed"),
			addError:      fmt.Errorf("add failed"),
			expectedError: true,
			errorContains: "failed to config smc-pnet rdma device",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			// Mock exec.Command
			patches.ApplyFunc(exec.Command, func(name string, arg ...string) *exec.Cmd {
				cmd := &exec.Cmd{}
				if name == smcPnet {
					if len(arg) > 0 && arg[0] == "-s" {
						// stat command
						patches.ApplyMethodReturn(cmd, "CombinedOutput", tt.statOutput, tt.statError)
					} else if len(arg) > 0 && arg[0] == "-a" {
						// add command
						patches.ApplyMethodReturn(cmd, "CombinedOutput", tt.addOutput, tt.addError)
					}
				}
				return cmd
			})

			err := ensureForERDMADev(tt.deviceName)

			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func Test_ensureForNetDevice(t *testing.T) {
	tests := []struct {
		name          string
		erdmaDev      string
		netDevice     string
		statOutput    []byte
		statError     error
		addOutput     []byte
		addError      error
		expectedError bool
		errorContains string
	}{
		{
			name:          "net device already exists",
			erdmaDev:      "test_erdma_1",
			netDevice:     "eth0",
			statOutput:    []byte("eth0 is configured"),
			statError:     nil,
			addOutput:     nil,
			addError:      nil,
			expectedError: false,
		},
		{
			name:          "net device not exists, add successfully",
			erdmaDev:      "test_erdma_2",
			netDevice:     "eth1",
			statOutput:    []byte("other devices"),
			statError:     nil,
			addOutput:     []byte("added successfully"),
			addError:      nil,
			expectedError: false,
		},
		{
			name:          "stat command fails",
			erdmaDev:      "test_erdma_3",
			netDevice:     "eth2",
			statOutput:    []byte("error"),
			statError:     fmt.Errorf("stat failed"),
			addOutput:     nil,
			addError:      nil,
			expectedError: true,
			errorContains: "failed to get smc-pnet stat for net device",
		},
		{
			name:          "add command fails",
			erdmaDev:      "test_erdma_4",
			netDevice:     "eth3",
			statOutput:    []byte("other devices"),
			statError:     nil,
			addOutput:     []byte("add failed"),
			addError:      fmt.Errorf("add failed"),
			expectedError: true,
			errorContains: "failed to config smc-pnet net device",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			// Mock exec.Command
			patches.ApplyFunc(exec.Command, func(name string, arg ...string) *exec.Cmd {
				cmd := &exec.Cmd{}
				if name == smcPnet {
					if len(arg) > 0 && arg[0] == "-s" {
						// stat command
						patches.ApplyMethodReturn(cmd, "CombinedOutput", tt.statOutput, tt.statError)
					} else if len(arg) > 0 && arg[0] == "-a" {
						// add command
						patches.ApplyMethodReturn(cmd, "CombinedOutput", tt.addOutput, tt.addError)
					}
				}
				return cmd
			})

			err := ensureForNetDevice(tt.erdmaDev, tt.netDevice)

			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func Test_ConfigSMCForDevice(t *testing.T) {
	tests := []struct {
		name              string
		erdmaDev          string
		netDevice         string
		netns             ns.NetNS
		supportSMCRResult bool
		ensureERDMAError  error
		ensureNetError    error
		expectedError     bool
		errorContains     string
	}{
		{
			name:              "SMC not supported",
			erdmaDev:          "test_erdma",
			netDevice:         "eth0",
			netns:             nil,
			supportSMCRResult: false,
			ensureERDMAError:  nil,
			ensureNetError:    nil,
			expectedError:     false,
		},
		{
			name:              "successful config without netns",
			erdmaDev:          "test_erdma",
			netDevice:         "eth0",
			netns:             nil,
			supportSMCRResult: true,
			ensureERDMAError:  nil,
			ensureNetError:    nil,
			expectedError:     false,
		},
		{
			name:              "successful config with netns",
			erdmaDev:          "test_erdma",
			netDevice:         "eth0",
			netns:             &mockNetNS{},
			supportSMCRResult: true,
			ensureERDMAError:  nil,
			ensureNetError:    nil,
			expectedError:     false,
		},
		{
			name:              "ensureERDMA fails",
			erdmaDev:          "test_erdma",
			netDevice:         "eth0",
			netns:             nil,
			supportSMCRResult: true,
			ensureERDMAError:  fmt.Errorf("erdma error"),
			ensureNetError:    nil,
			expectedError:     true,
			errorContains:     "erdma error",
		},
		{
			name:              "ensureNetDevice fails without netns",
			erdmaDev:          "test_erdma",
			netDevice:         "eth0",
			netns:             nil,
			supportSMCRResult: true,
			ensureERDMAError:  nil,
			ensureNetError:    fmt.Errorf("net error"),
			expectedError:     true,
			errorContains:     "net error",
		},
		{
			name:              "ensureNetDevice fails with netns",
			erdmaDev:          "test_erdma",
			netDevice:         "eth0",
			netns:             &mockNetNS{},
			supportSMCRResult: true,
			ensureERDMAError:  nil,
			ensureNetError:    fmt.Errorf("net error"),
			expectedError:     true,
			errorContains:     "net error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			// Mock supportSMCR
			patches.ApplyFunc(supportSMCR, func() bool {
				return tt.supportSMCRResult
			})

			// Mock ensureForERDMADev
			patches.ApplyFunc(ensureForERDMADev, func(name string) error {
				return tt.ensureERDMAError
			})

			// Mock ensureForNetDevice
			patches.ApplyFunc(ensureForNetDevice, func(erdmaDev, netDevice string) error {
				return tt.ensureNetError
			})

			err := ConfigSMCForDevice(tt.erdmaDev, tt.netDevice, tt.netns)

			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// mockNetNS is a mock implementation of ns.NetNS for testing
type mockNetNS struct{}

func (m *mockNetNS) Do(toRun func(ns.NetNS) error) error {
	return toRun(m)
}

func (m *mockNetNS) Close() error {
	return nil
}

func (m *mockNetNS) Fd() uintptr {
	return 0
}

func (m *mockNetNS) Path() string {
	return "/proc/self/ns/net"
}

func (m *mockNetNS) Set() error {
	return nil
}
