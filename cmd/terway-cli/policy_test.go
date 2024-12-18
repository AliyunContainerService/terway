package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/Jeffail/gabs/v2"
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
				return nil, nil
			},
			checkFunc: func(t *testing.T, strings []string, err error) {
				assert.NoError(t, err)
				assert.NotContains(t, strings, "--disable-per-package-lb=true")
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
