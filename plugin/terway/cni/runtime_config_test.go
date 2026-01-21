package cni

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRuntimeDNS_AsCNIDns(t *testing.T) {
	tests := []struct {
		name     string
		dns      RuntimeDNS
		expected struct {
			Nameservers []string
			Search      []string
			Options     []string
		}
	}{
		{
			name: "empty DNS",
			dns:  RuntimeDNS{},
			expected: struct {
				Nameservers []string
				Search      []string
				Options     []string
			}{
				Nameservers: nil,
				Search:      nil,
				Options:     nil,
			},
		},
		{
			name: "DNS with nameservers",
			dns: RuntimeDNS{
				Nameservers: []string{"8.8.8.8", "8.8.4.4"},
			},
			expected: struct {
				Nameservers []string
				Search      []string
				Options     []string
			}{
				Nameservers: []string{"8.8.8.8", "8.8.4.4"},
				Search:      nil,
				Options:     nil,
			},
		},
		{
			name: "DNS with all fields",
			dns: RuntimeDNS{
				Nameservers: []string{"8.8.8.8"},
				Search:      []string{"example.com", "test.com"},
				Options:     []string{"timeout:2"},
			},
			expected: struct {
				Nameservers []string
				Search      []string
				Options     []string
			}{
				Nameservers: []string{"8.8.8.8"},
				Search:      []string{"example.com", "test.com"},
				Options:     []string{"timeout:2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.dns.AsCNIDns()
			assert.Equal(t, tt.expected.Nameservers, result.Nameservers)
			assert.Equal(t, tt.expected.Search, result.Search)
			assert.Equal(t, tt.expected.Options, result.Options)
		})
	}
}

func TestRuntimePortMapEntry(t *testing.T) {
	// Test RuntimePortMapEntry structure
	entry := RuntimePortMapEntry{
		HostPort:      8080,
		ContainerPort: 80,
		Protocol:      "tcp",
		HostIP:        "0.0.0.0",
	}

	assert.Equal(t, 8080, entry.HostPort)
	assert.Equal(t, 80, entry.ContainerPort)
	assert.Equal(t, "tcp", entry.Protocol)
	assert.Equal(t, "0.0.0.0", entry.HostIP)

	// Test without HostIP
	entry2 := RuntimePortMapEntry{
		HostPort:      9090,
		ContainerPort: 90,
		Protocol:      "udp",
	}
	assert.Equal(t, "", entry2.HostIP)
}

func TestRuntimeBandwidthEntry(t *testing.T) {
	// Test RuntimeBandwidthEntry structure
	entry := RuntimeBandwidthEntry{
		IngressRate:  1000000, // 1Mbps
		IngressBurst: 2000000,
		EgressRate:   2000000, // 2Mbps
		EgressBurst:  4000000,
	}

	assert.Equal(t, 1000000, entry.IngressRate)
	assert.Equal(t, 2000000, entry.IngressBurst)
	assert.Equal(t, 2000000, entry.EgressRate)
	assert.Equal(t, 4000000, entry.EgressBurst)

	// Test zero values
	entry2 := RuntimeBandwidthEntry{}
	assert.Equal(t, 0, entry2.IngressRate)
	assert.Equal(t, 0, entry2.IngressBurst)
	assert.Equal(t, 0, entry2.EgressRate)
	assert.Equal(t, 0, entry2.EgressBurst)
}

func TestRuntimeConfig(t *testing.T) {
	// Test RuntimeConfig structure
	config := RuntimeConfig{
		DNS: RuntimeDNS{
			Nameservers: []string{"8.8.8.8"},
			Search:      []string{"example.com"},
		},
		PortMaps: []RuntimePortMapEntry{
			{
				HostPort:      8080,
				ContainerPort: 80,
				Protocol:      "tcp",
			},
		},
		Bandwidth: RuntimeBandwidthEntry{
			IngressRate: 1000000,
			EgressRate:  2000000,
		},
	}

	assert.Len(t, config.DNS.Nameservers, 1)
	assert.Len(t, config.PortMaps, 1)
	assert.Equal(t, 1000000, config.Bandwidth.IngressRate)

	// Test empty config
	config2 := RuntimeConfig{}
	assert.Empty(t, config2.DNS.Nameservers)
	assert.Nil(t, config2.PortMaps)
	assert.Equal(t, 0, config2.Bandwidth.IngressRate)
}
