/*
Copyright 2021 Terway Authors.

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
	"testing"

	"github.com/go-playground/validator/v10"
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
			wantErr: true,
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
