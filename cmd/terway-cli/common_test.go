package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReadyENIConfig(t *testing.T) {
	testCases := []struct {
		name           string
		files          map[string]string
		expectedConfig *TerwayConfig
		expectedError  error
	}{
		{
			name: "basic",
			files: map[string]string{
				"10-terway.conf":         "cni_config",
				"eni_conf":               "eni_config",
				"in_cluster_loadbalance": "true",
			},
			expectedConfig: &TerwayConfig{
				enableNetworkPolicy: true,
				enableInClusterLB:   true,
				eniConfig:           []byte("eni_config"),
				cniConfig:           []byte("cni_config"),
			},
		},
		{
			name: "disable network policy",
			files: map[string]string{
				"10-terway.conf":         "cni_config",
				"eni_conf":               "eni_config",
				"disable_network_policy": "true",
			},
			expectedConfig: &TerwayConfig{
				enableNetworkPolicy: false,
				eniConfig:           []byte("eni_config"),
				cniConfig:           []byte("cni_config"),
			},
		},
		{
			name: "enable network policy with empty file",
			files: map[string]string{
				"10-terway.conf":         "cni_config",
				"eni_conf":               "eni_config",
				"disable_network_policy": "",
			},
			expectedConfig: &TerwayConfig{
				enableNetworkPolicy: true,
				eniConfig:           []byte("eni_config"),
				cniConfig:           []byte("cni_config"),
			},
		},

		{
			name: "with conflist",
			files: map[string]string{
				"10-terway.conf":     "cni_config",
				"10-terway.conflist": "cni_config_list",
				"eni_conf":           "eni_config",
			},
			expectedConfig: &TerwayConfig{
				enableNetworkPolicy: true,
				eniConfig:           []byte("eni_config"),
				cniConfig:           []byte("cni_config"),
				cniConfigList:       []byte("cni_config_list"),
			},
		},
		{
			name: "missing 10-terway.conf",
			files: map[string]string{
				"eni_conf": "eni_config",
			},
			expectedError: &fs.PathError{},
		},
		{
			name: "missing eni_conf",
			files: map[string]string{
				"10-terway.conf": "cni_config",
			},
			expectedError: &fs.PathError{},
		},
		{
			name: "error reading disable_network_policy",
			files: map[string]string{
				"10-terway.conf":         "cni_config",
				"eni_conf":               "eni_config",
				"disable_network_policy": "invalid content",
			},
			expectedConfig: &TerwayConfig{
				enableNetworkPolicy: false, // Because any non-false, 0, or empty value disables
				eniConfig:           []byte("eni_config"),
				cniConfig:           []byte("cni_config"),
			},
		},
		{
			name: "error reading in_cluster_loadbalance",
			files: map[string]string{
				"10-terway.conf": "cni_config",
				"eni_conf":       "eni_config",
			},
			expectedConfig: &TerwayConfig{
				enableNetworkPolicy: true,
				enableInClusterLB:   false,
				eniConfig:           []byte("eni_config"),
				cniConfig:           []byte("cni_config"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			for filename, content := range tc.files {
				filePath := filepath.Join(tempDir, filename)
				err := os.WriteFile(filePath, []byte(content), 0644)
				assert.NoError(t, err)
			}

			cfg, err := getAllConfig(tempDir)

			if tc.expectedError != nil {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedConfig, cfg)
			}
		})
	}
}
