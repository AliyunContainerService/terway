/*
Copyright 2021-2022 Terway Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controlplane

import (
	"os"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"
)

func TestParseAndValidateCredential(t *testing.T) {
	tests := []struct {
		name       string
		credential Credential
		wantErr    bool
	}{
		{
			name: "use ak",
			credential: Credential{
				AccessKey:      "foo",
				AccessSecret:   "foo",
				CredentialPath: "",
			},
			wantErr: false,
		},
		{
			name: "use credential",
			credential: Credential{
				AccessKey:      "",
				AccessSecret:   "",
				CredentialPath: "foo",
			},
			wantErr: false,
		},
		{
			name: "miss ak",
			credential: Credential{
				AccessKey:      "foo",
				AccessSecret:   "",
				CredentialPath: "",
			},
			wantErr: true,
		},
		{
			name: "miss ak",
			credential: Credential{
				AccessKey:      "foo",
				AccessSecret:   "",
				CredentialPath: "foo",
			},
			wantErr: true,
		},
		{
			name: "miss all",
			credential: Credential{
				AccessKey:      "",
				AccessSecret:   "",
				CredentialPath: "",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.New().Struct(&tt.credential)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseAndValidateCredential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestIsControllerEnabled(t *testing.T) {
	type args struct {
		name        string
		enable      bool
		controllers []string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "enable all",
			args: args{
				name:        "foo",
				enable:      false,
				controllers: []string{"aa", "bb", "*"},
			},
			want: true,
		}, {
			name: "default disable",
			args: args{
				name:        "foo",
				enable:      false,
				controllers: []string{"aa", "bb"},
			},
			want: false,
		}, {
			name: "default enable",
			args: args{
				name:        "foo",
				enable:      true,
				controllers: []string{"aa", "bb"},
			},
			want: true,
		}, {
			name: "disabled",
			args: args{
				name:        "foo",
				enable:      true,
				controllers: []string{"aa", "-foo"},
			},
			want: false,
		}, {
			name: "enabled",
			args: args{
				name:        "foo",
				enable:      false,
				controllers: []string{"aa", "foo"},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsControllerEnabled(tt.args.name, tt.args.enable, tt.args.controllers); got != tt.want {
				t.Errorf("IsControllerEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseAndValidate(t *testing.T) {
	configFile, err := os.CreateTemp("", "")
	assert.NoError(t, err)
	defer os.Remove(configFile.Name())

	err = os.WriteFile(configFile.Name(), []byte(`disableWebhook: true
regionID: "cn-hangzhou"
leaseLockName: "terway-controller-lock"
leaseLockNamespace: "kube-system"
controllerNamespace: "kube-system"
controllerName: "terway-controlplane"
metricsBindAddress: "127.0.0.1:9999"
healthzBindAddress: "0.0.0.0:8080"
clusterDomain: "cluster.local"
clusterID: foo
vpcID: bar
disableWebhook: true
webhookURLMode: true
leaderElection: true
webhookPort: 4443`), os.ModeType)
	assert.NoError(t, err)

	credentialFilePath, err := os.CreateTemp("", "")
	assert.NoError(t, err)
	defer os.Remove(credentialFilePath.Name())

	err = os.WriteFile(credentialFilePath.Name(), []byte(`accessKey: foo
accessSecret: bar`), os.ModeType)
	assert.NoError(t, err)

	cfg, err := ParseAndValidate(configFile.Name(), credentialFilePath.Name())
	assert.NoError(t, err)

	assert.Equal(t, "cn-hangzhou", cfg.RegionID)
	assert.Equal(t, ptr.To(true), cfg.EnableWebhookInjectResource)
}
